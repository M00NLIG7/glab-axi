package glab

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"unicode/utf8"
)

var (
	errLoginOutputMalformed = errors.New("login terminal output is malformed")
	errLoginOutputOverflow  = errors.New("login terminal output exceeded the safety limit")
	errLoginOutputRelay     = errors.New("login terminal output relay failed")
)

type loginOutputState struct {
	mu      sync.Mutex
	warning bool
	err     error
}

func (s *loginOutputState) markWarning() {
	s.mu.Lock()
	s.warning = true
	s.mu.Unlock()
}

func (s *loginOutputState) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *loginOutputState) snapshot() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warning, s.err
}

// relayLoginOutput forwards a bounded, valid UTF-8 stream while retaining only
// enough history to recognize the exact pinned plaintext-storage warning.
// After any terminal failure it keeps draining without forwarding so process
// teardown cannot deadlock on a full PTY.
func relayLoginOutput(source io.Reader, destination io.Writer, maxBytes int, cancel func(), state *loginOutputState) {
	const readSize = 4 << 10
	warning := []byte(plaintextFallbackWarning)
	window := make([]byte, 0, len(warning)-1)
	incomplete := make([]byte, 0, utf8.UTFMax-1)
	buffer := make([]byte, readSize)
	total := 0
	failed := false
	noProgress := 0

	for {
		n, readErr := source.Read(buffer)
		if n == 0 && readErr == nil {
			noProgress++
			if noProgress >= 100 {
				if !failed {
					state.fail(errLoginOutputRelay)
					cancel()
				}
				return
			}
		} else {
			noProgress = 0
		}
		if n > 0 && !failed {
			chunk := buffer[:n]
			remaining := maxBytes - total
			if remaining <= 0 {
				state.fail(errLoginOutputOverflow)
				cancel()
				failed = true
			} else {
				if len(chunk) > remaining {
					chunk = chunk[:remaining]
				}
				total += len(chunk)

				combined := make([]byte, 0, len(incomplete)+len(chunk))
				combined = append(combined, incomplete...)
				combined = append(combined, chunk...)
				incomplete = incomplete[:0]

				cutoff, found := warningCutoff(window, combined, warning)
				candidate := combined
				if found {
					state.markWarning()
					candidate = candidate[:cutoff]
				}
				complete, tail, valid := completeUTF8Prefix(candidate)
				if !valid {
					state.fail(errLoginOutputMalformed)
					cancel()
					failed = true
				} else {
					incomplete = append(incomplete, tail...)
					if len(complete) > 0 {
						if err := writeFull(destination, complete); err != nil {
							state.fail(errLoginOutputRelay)
							cancel()
							failed = true
						}
					}
					if found {
						cancel()
						failed = true
					} else if !failed {
						window = warningWindow(window, complete, len(warning)-1)
					}
				}

				if n > len(chunk) && !failed {
					state.fail(errLoginOutputOverflow)
					cancel()
					failed = true
				}
			}
		}

		if readErr != nil {
			if !isLoginTerminalEOF(readErr) && !failed {
				state.fail(errLoginOutputRelay)
				cancel()
			}
			if !failed && len(incomplete) != 0 {
				state.fail(errLoginOutputMalformed)
				cancel()
			}
			return
		}
	}
}

func completeUTF8Prefix(input []byte) (complete, incomplete []byte, valid bool) {
	for index := 0; index < len(input); {
		if input[index] == 0 {
			return nil, nil, false
		}
		if !utf8.FullRune(input[index:]) {
			return input[:index], input[index:], true
		}
		r, size := utf8.DecodeRune(input[index:])
		if r == utf8.RuneError && size == 1 {
			return nil, nil, false
		}
		index += size
	}
	return input, nil, true
}

func warningCutoff(window, chunk, warning []byte) (int, bool) {
	combined := make([]byte, 0, len(window)+len(chunk))
	combined = append(combined, window...)
	combined = append(combined, chunk...)
	index := bytes.Index(combined, warning)
	if index < 0 {
		return len(chunk), false
	}
	cutoff := index + len(warning) - len(window)
	if cutoff < 0 {
		cutoff = 0
	}
	if cutoff > len(chunk) {
		cutoff = len(chunk)
	}
	return cutoff, true
}

func warningWindow(previous, chunk []byte, keep int) []byte {
	combined := make([]byte, 0, len(previous)+len(chunk))
	combined = append(combined, previous...)
	combined = append(combined, chunk...)
	if len(combined) > keep {
		combined = combined[len(combined)-keep:]
	}
	return append(previous[:0], combined...)
}

func writeFull(destination io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := destination.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
