package glab

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"

	"golang.org/x/term"
)

const (
	loginDrainTimeout = 2 * time.Second
	loginResizePeriod = 250 * time.Millisecond
)

var errLoginInterrupted = errors.New("login terminal was interrupted")

type loginTerminalSession interface {
	io.Reader
	Wait() error
	Kill() error
	Resize(width, height int) error
	InputWriter() io.Writer
	AfterWait() error
	Close() error
}

type preparedLoginInput struct {
	file      *os.File
	restore   func() error
	mu        sync.Mutex
	err       error
	interrupt bool
	stopping  bool
}

func (r *preparedLoginInput) setResult(err error, interrupted bool) {
	r.mu.Lock()
	if !r.stopping || interrupted {
		if r.err == nil {
			r.err = err
		}
		if interrupted {
			r.interrupt = true
		}
	}
	r.mu.Unlock()
}

func (r *preparedLoginInput) stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
}

func (r *preparedLoginInput) result() (error, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err, r.interrupt
}

func (c *Client) runLogin(ctx context.Context, args []string, host string) error {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()

	stdin, _, stderr, err := c.loginTerminalFiles()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return contextFailure(ctx, "official glab login")
	}

	restoreOutput, err := prepareLoginOutput(stderr)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "cannot establish a monitored human terminal", err)
	}
	defer restoreOutput()
	terminalOutput, err := duplicateLoginFile(stderr)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "cannot establish a monitored human terminal", err)
	}
	defer terminalOutput.Close()

	preparedInput, err := prepareLoginInput(stdin)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "cannot establish a monitored human terminal", err)
	}
	if preparedInput != nil {
		defer preparedInput.restore()
		defer preparedInput.file.Close()
	}

	width, height := loginTerminalSize(stderr)
	session, err := startLoginTerminal(path, args, c.config.Dir, sanitizedEnv(c.config.Env, host, true), stdin, width, height)
	if err != nil {
		if ctx.Err() != nil {
			return contextFailure(ctx, "official glab login")
		}
		if errors.Is(err, os.ErrNotExist) {
			return uxv1.NewError(uxv1.CodeDependencyMissing, "official glab executable disappeared before execution")
		}
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot start official glab login", err)
	}
	defer session.Close()

	childCtx, cancelChild := context.WithCancel(ctx)
	defer cancelChild()
	processDone := make(chan struct{})
	killDone := make(chan struct{})
	go func() {
		select {
		case <-childCtx.Done():
			_ = session.Kill()
		case <-killDone:
		}
		close(processDone)
	}()

	outputState := &loginOutputState{}
	outputDone := make(chan struct{})
	go func() {
		relayLoginOutput(session, terminalOutput, limits.MaxOperationBytes, cancelChild, outputState)
		close(outputDone)
	}()

	inputDone := make(chan struct{})
	if preparedInput != nil {
		go func() {
			relayLoginInput(preparedInput, session.InputWriter(), cancelChild)
			close(inputDone)
		}()
	} else {
		close(inputDone)
	}

	stopResize := watchLoginTerminalSize(stderr, session)
	waitErr := session.Wait()
	close(killDone)
	<-processDone
	stopResize()

	if preparedInput != nil {
		preparedInput.stop()
		stopLoginInput(preparedInput.file)
		_ = preparedInput.file.Close()
		_ = preparedInput.restore()
	}
	afterWaitErr := session.AfterWait()
	inputStopped := waitForLoginRelay(inputDone, loginDrainTimeout)

	outputStopped := waitForLoginRelay(outputDone, loginDrainTimeout)
	if !outputStopped {
		_ = session.Close()
		_ = terminalOutput.Close()
		outputStopped = waitForLoginRelay(outputDone, loginDrainTimeout)
	}

	warning, monitorErr := outputState.snapshot()
	inputErr, interrupted := preparedInput.result()
	switch {
	case warning:
		return uxv1.NewError(uxv1.CodeSafety, "official glab reported insecure credential storage; login was aborted")
	case interrupted:
		return uxv1.Wrap(uxv1.CodeCanceled, "official glab login was canceled", errLoginInterrupted)
	case ctx.Err() != nil:
		return contextFailure(ctx, "official glab login")
	case monitorErr != nil:
		if errors.Is(monitorErr, errLoginOutputOverflow) {
			return uxv1.NewError(uxv1.CodeUpstream, "official glab interactive output exceeded the safety limit")
		}
		if errors.Is(monitorErr, errLoginOutputMalformed) {
			return uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed interactive output")
		}
		return uxv1.NewError(uxv1.CodeUpstream, "official glab interactive output could not be relayed")
	case !inputStopped || !outputStopped:
		return uxv1.NewError(uxv1.CodeUpstream, "official glab terminal relay did not stop")
	case inputErr != nil:
		return uxv1.NewError(uxv1.CodeUpstream, "official glab terminal input could not be relayed")
	case afterWaitErr != nil:
		return uxv1.NewError(uxv1.CodeUpstream, "official glab terminal could not be closed safely")
	case waitErr != nil:
		return uxv1.Wrap(uxv1.CodeAuthentication, "official glab login did not complete", waitErr)
	default:
		return nil
	}
}

func relayLoginInput(relay *preparedLoginInput, destination io.Writer, cancel func()) {
	if destination == nil {
		relay.setResult(errors.New("login terminal input relay is unavailable"), false)
		cancel()
		return
	}
	buffer := make([]byte, 4<<10)
	defer func() {
		for index := range buffer {
			buffer[index] = 0
		}
	}()
	for {
		n, err := relay.file.Read(buffer)
		if n > 0 {
			interrupted := false
			for _, value := range buffer[:n] {
				if value == 3 {
					interrupted = true
					break
				}
			}
			if interrupted {
				for index := 0; index < n; index++ {
					buffer[index] = 0
				}
				relay.setResult(errLoginInterrupted, true)
				cancel()
				return
			}
			if writeErr := writeFull(destination, buffer[:n]); writeErr != nil {
				for index := 0; index < n; index++ {
					buffer[index] = 0
				}
				relay.setResult(errLoginOutputRelay, false)
				cancel()
				return
			}
			for index := 0; index < n; index++ {
				buffer[index] = 0
			}
		}
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				relay.setResult(errLoginOutputRelay, false)
				cancel()
			}
			return
		}
	}
}

func watchLoginTerminalSize(source *os.File, session loginTerminalSession) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(loginResizePeriod)
		defer ticker.Stop()
		lastWidth, lastHeight := loginTerminalSize(source)
		for {
			select {
			case <-ticker.C:
				width, height := loginTerminalSize(source)
				if width != lastWidth || height != lastHeight {
					_ = session.Resize(width, height)
					lastWidth, lastHeight = width, height
				}
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func loginTerminalSize(file *os.File) (int, int) {
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil || width < 1 || height < 1 {
		return 80, 24
	}
	if width > 4096 {
		width = 4096
	}
	if height > 4096 {
		height = 4096
	}
	return width, height
}

func waitForLoginRelay(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
