//go:build windows

package glab

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

type windowsLoginTerminal struct {
	console windows.Handle
	input   *os.File
	output  *os.File
	process *os.Process

	mu        sync.RWMutex
	afterOnce sync.Once
	closeOnce sync.Once
	closeErr  error
}

func startLoginTerminal(path string, args []string, dir string, environment []string, _ *os.File, width, height int) (loginTerminalSession, error) {
	if err := windowsConPTYAvailable(); err != nil {
		return nil, err
	}
	consoleInput, input, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	output, consoleOutput, err := os.Pipe()
	if err != nil {
		_ = consoleInput.Close()
		_ = input.Close()
		return nil, err
	}
	cleanupPipes := func() {
		_ = consoleInput.Close()
		_ = input.Close()
		_ = output.Close()
		_ = consoleOutput.Close()
	}

	var console windows.Handle
	if err := windows.CreatePseudoConsole(
		windows.Coord{X: int16(width), Y: int16(height)},
		windows.Handle(consoleInput.Fd()),
		windows.Handle(consoleOutput.Fd()),
		0,
		&console,
	); err != nil {
		cleanupPipes()
		return nil, err
	}
	_ = consoleInput.Close()
	_ = consoleOutput.Close()

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	defer attributes.Delete()
	if err := updatePseudoConsoleAttribute(attributes, console); err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}

	application, err := windows.UTF16PtrFromString(path)
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{path}, args...)))
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	var directory *uint16
	if dir != "" {
		directory, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			windows.ClosePseudoConsole(console)
			_ = input.Close()
			_ = output.Close()
			return nil, err
		}
	}
	environmentBlock, err := windowsLoginEnvironmentBlock(environment)
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}

	startup := windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attributes.List(),
	}
	var processInfo windows.ProcessInformation
	if err := windows.CreateProcess(
		application,
		commandLine,
		nil,
		nil,
		false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		&environmentBlock[0],
		directory,
		&startup.StartupInfo,
		&processInfo,
	); err != nil {
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	_ = windows.CloseHandle(processInfo.Thread)
	process, err := os.FindProcess(int(processInfo.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_ = windows.CloseHandle(processInfo.Process)
		windows.ClosePseudoConsole(console)
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	_ = windows.CloseHandle(processInfo.Process)
	return &windowsLoginTerminal{console: console, input: input, output: output, process: process}, nil
}

func (s *windowsLoginTerminal) Read(data []byte) (int, error) {
	return s.output.Read(data)
}

func (s *windowsLoginTerminal) Wait() error {
	state, err := s.process.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return &exec.ExitError{ProcessState: state}
	}
	return nil
}

func (s *windowsLoginTerminal) Kill() error {
	return s.process.Kill()
}

func (s *windowsLoginTerminal) Resize(width, height int) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(s.console, windows.Coord{X: int16(width), Y: int16(height)})
}

func (s *windowsLoginTerminal) InputWriter() io.Writer { return s.input }

func (s *windowsLoginTerminal) AfterWait() error {
	s.afterOnce.Do(func() {
		_ = s.input.Close()
		s.mu.Lock()
		if s.console != 0 {
			windows.ClosePseudoConsole(s.console)
			s.console = 0
		}
		s.mu.Unlock()
	})
	return nil
}

func (s *windowsLoginTerminal) Close() error {
	s.closeOnce.Do(func() {
		_ = s.AfterWait()
		s.closeErr = s.output.Close()
	})
	return s.closeErr
}

var procUpdateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

func updatePseudoConsoleAttribute(attributes *windows.ProcThreadAttributeListContainer, console windows.Handle) error {
	ret, _, callErr := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(attributes.List())),
		0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(console),
		unsafe.Sizeof(console),
		0,
		0,
	)
	if ret == 0 {
		return callErr
	}
	return nil
}

func windowsConPTYAvailable() error {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	for _, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole"} {
		if err := kernel32.NewProc(name).Find(); err != nil {
			return errors.New("windows ConPTY is unavailable")
		}
	}
	return nil
}

func windowsLoginEnvironmentBlock(environment []string) ([]uint16, error) {
	values := make(map[string]string, len(environment)+1)
	for _, item := range environment {
		if !utf8.ValidString(item) || strings.ContainsRune(item, 0) {
			return nil, errors.New("environment contains invalid text")
		}
		name, ok := windowsLoginEnvironmentName(item)
		if !ok {
			return nil, errors.New("environment entry has no name")
		}
		values[strings.ToUpper(name)] = item
	}
	if _, ok := values["SYSTEMROOT"]; !ok {
		values["SYSTEMROOT"] = "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")
	}
	ordered := make([]string, 0, len(values))
	for _, item := range values {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return strings.ToUpper(ordered[left]) < strings.ToUpper(ordered[right])
	})
	joined := strings.Join(ordered, "\x00") + "\x00\x00"
	return utf16.Encode([]rune(joined)), nil
}

func windowsLoginEnvironmentName(item string) (string, bool) {
	if strings.HasPrefix(item, "=") {
		separator := strings.Index(item[1:], "=")
		if separator < 1 {
			return "", false
		}
		separator++
		return item[:separator], true
	}
	separator := strings.IndexByte(item, '=')
	if separator < 1 {
		return "", false
	}
	return item[:separator], true
}

func prepareLoginOutput(file *os.File) (func() error, error) {
	var mode uint32
	handle := windows.Handle(file.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	updated := mode | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(handle, updated); err != nil {
		return nil, err
	}
	var restoreOnce sync.Once
	var restoreErr error
	return func() error {
		restoreOnce.Do(func() { restoreErr = windows.SetConsoleMode(handle, mode) })
		return restoreErr
	}, nil
}

func duplicateLoginFile(file *os.File) (*os.File, error) {
	var duplicate windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(
		process,
		windows.Handle(file.Fd()),
		process,
		&duplicate,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(duplicate), file.Name()+"-login-relay"), nil
}

func prepareLoginInput(file *os.File) (*preparedLoginInput, error) {
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	duplicate, err := duplicateLoginFile(file)
	if err != nil {
		_ = term.Restore(int(file.Fd()), state)
		return nil, err
	}
	var restoreOnce sync.Once
	var restoreErr error
	return &preparedLoginInput{
		file: duplicate,
		restore: func() error {
			restoreOnce.Do(func() { restoreErr = term.Restore(int(file.Fd()), state) })
			return restoreErr
		},
	}, nil
}

func stopLoginInput(file *os.File) {
	_ = windows.CancelIoEx(windows.Handle(file.Fd()), nil)
}

func isLoginTerminalEOF(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, windows.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) ||
		errors.Is(err, windows.ERROR_OPERATION_ABORTED)
}
