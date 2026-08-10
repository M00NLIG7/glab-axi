//go:build !windows

package glab

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type unixLoginTerminal struct {
	master    *os.File
	command   *exec.Cmd
	closeOnce sync.Once
	closeErr  error
}

func startLoginTerminal(path string, args []string, dir string, environment []string, stdin *os.File, width, height int) (loginTerminalSession, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = slave.Close()
		_ = master.Close()
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}); err != nil {
		cleanup()
		return nil, err
	}

	command := exec.Command(path, args...)
	command.Dir = dir
	command.Env = environment
	command.Stdin = stdin
	command.Stdout = slave
	command.Stderr = slave
	if err := command.Start(); err != nil {
		cleanup()
		return nil, err
	}
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = master.Close()
		return nil, err
	}
	return &unixLoginTerminal{master: master, command: command}, nil
}

func (s *unixLoginTerminal) Read(data []byte) (int, error) {
	return s.master.Read(data)
}

func (s *unixLoginTerminal) Wait() error {
	return s.command.Wait()
}

func (s *unixLoginTerminal) Kill() error {
	if s.command.Process == nil {
		return os.ErrProcessDone
	}
	return s.command.Process.Kill()
}

func (s *unixLoginTerminal) Resize(width, height int) error {
	return pty.Setsize(s.master, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
}

func (*unixLoginTerminal) InputWriter() io.Writer { return nil }
func (*unixLoginTerminal) AfterWait() error       { return nil }

func (s *unixLoginTerminal) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.master.Close() })
	return s.closeErr
}

func duplicateLoginFile(file *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), file.Name()+"-login-relay"), nil
}

func prepareLoginOutput(*os.File) (func() error, error) {
	return func() error { return nil }, nil
}

func prepareLoginInput(*os.File) (*preparedLoginInput, error) { return nil, nil }
func stopLoginInput(*os.File)                                 {}

func isLoginTerminalEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
}
