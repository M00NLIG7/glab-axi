//go:build windows

package glab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsLoginTerminalModesAreRestored(t *testing.T) {
	const helper = "GLAB_AXI_WINDOWS_MODE_RESTORE_HELPER"
	if os.Getenv(helper) == "1" {
		testWindowsLoginTerminalModesAreRestored(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLoginTerminalModesAreRestored$", "-test.v")
	cmd.Env = append(os.Environ(), helper+"=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terminal-mode helper failed: %v\n%s", err, output)
	}
}

func testWindowsLoginTerminalModesAreRestored(t *testing.T) {
	detach, err := attachWindowsTestConsole()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()

	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	var originalInput, originalOutput uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &originalInput); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &originalOutput); err != nil {
		t.Fatal(err)
	}

	restoreOutput, err := prepareLoginOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	preparedInput, err := prepareLoginInput(input)
	if err != nil {
		_ = restoreOutput()
		t.Fatal(err)
	}
	defer preparedInput.file.Close()

	var activeInput, activeOutput uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &activeInput); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &activeOutput); err != nil {
		t.Fatal(err)
	}
	wantInput := originalInput &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_OUTPUT)
	wantInput |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	wantOutput := originalOutput | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if activeInput != wantInput || activeOutput != wantOutput {
		t.Fatalf("console modes not prepared: input=%#x want=%#x, output=%#x want=%#x", activeInput, wantInput, activeOutput, wantOutput)
	}

	if err := preparedInput.restore(); err != nil {
		t.Fatal(err)
	}
	if err := preparedInput.restore(); err != nil {
		t.Fatalf("second input restore failed: %v", err)
	}
	if err := restoreOutput(); err != nil {
		t.Fatal(err)
	}
	if err := restoreOutput(); err != nil {
		t.Fatalf("second output restore failed: %v", err)
	}

	var restoredInput, restoredOutput uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &restoredInput); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &restoredOutput); err != nil {
		t.Fatal(err)
	}
	if restoredInput != originalInput || restoredOutput != originalOutput {
		t.Fatalf("console modes not restored: input %#x -> %#x, output %#x -> %#x", originalInput, restoredInput, originalOutput, restoredOutput)
	}
}

func attachWindowsTestConsole() (func(), error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	// CI shells may leave the process attached to a degenerate, non-interactive
	// console inherited from the runner's own process tree. AllocConsole then
	// fails with ERROR_ACCESS_DENIED and, if tolerated as-is, ConPTY children
	// spawned against that ambient console never see a working terminal.
	// Detach first so AllocConsole always creates a fresh, functional console.
	_, _, _ = kernel32.NewProc("FreeConsole").Call()
	allocated, _, callErr := kernel32.NewProc("AllocConsole").Call()
	if allocated == 0 {
		return nil, fmt.Errorf("AllocConsole failed: %w", callErr)
	}
	return func() {
		_, _, _ = kernel32.NewProc("FreeConsole").Call()
	}, nil
}

func TestWindowsConPTYLoginLifecycleAndSafety(t *testing.T) {
	// CreatePseudoConsole must run from a process attached to a real Win32
	// console: CI shells (e.g. GitHub Actions' bash steps) run with fully
	// pipe-redirected stdio and no console anywhere in the process tree,
	// which otherwise leaves the ConPTY-spawned child without a console
	// GetConsoleMode recognizes. TestWindowsLoginTerminalModesAreRestored
	// already relies on this same console for its own assertions.
	detach, err := attachWindowsTestConsole()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()

	fakeGlab := buildWindowsLoginFake(t)

	t.Run("child terminals and waiting prompt", func(t *testing.T) {
		ready := filepath.Join(t.TempDir(), "ready")
		session := startWindowsLoginTestSession(t, fakeGlab, "prompt", ready)
		var output bytes.Buffer
		state := &loginOutputState{}
		outputDone := make(chan struct{})
		go func() {
			relayLoginOutput(session, &output, 4096, func() { _ = session.Kill() }, state)
			close(outputDone)
		}()
		waitForWindowsLoginFake(t, ready, 5*time.Second)

		waitDone := make(chan error, 1)
		go func() { waitDone <- session.Wait() }()
		select {
		case err := <-waitDone:
			t.Fatalf("ConPTY prompt exited before human input: %v", err)
		case <-time.After(250 * time.Millisecond):
		}
		if _, err := session.InputWriter().Write([]byte("continue\r\n")); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-waitDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("ConPTY prompt did not complete after human input")
		}
		finishWindowsLoginSession(t, session, outputDone)
		warning, monitorErr := state.snapshot()
		if warning || monitorErr != nil || !bytes.Contains(output.Bytes(), []byte("child-tty=1/1/1")) || !bytes.Contains(output.Bytes(), []byte("synthetic human prompt")) {
			t.Fatalf("warning=%t monitor=%v output=%q", warning, monitorErr, output.Bytes())
		}
	})

	t.Run("cancellation terminates child promptly", func(t *testing.T) {
		ready := filepath.Join(t.TempDir(), "ready")
		session := startWindowsLoginTestSession(t, fakeGlab, "wait", ready)
		state := &loginOutputState{}
		outputDone := make(chan struct{})
		go func() {
			relayLoginOutput(session, &bytes.Buffer{}, 4096, func() { _ = session.Kill() }, state)
			close(outputDone)
		}()
		waitForWindowsLoginFake(t, ready, 5*time.Second)
		started := time.Now()
		if err := session.Kill(); err != nil {
			t.Fatal(err)
		}
		waitDone := make(chan error, 1)
		go func() { waitDone <- session.Wait() }()
		select {
		case err := <-waitDone:
			if err == nil {
				t.Fatal("canceled ConPTY child reported success")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation did not terminate the ConPTY child promptly")
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("ConPTY cancellation took %s", elapsed)
		}
		finishWindowsLoginSession(t, session, outputDone)
	})

	for _, test := range []struct {
		mode string
		max  int
		want error
	}{
		{mode: "overflow", max: 64, want: errLoginOutputOverflow},
		{mode: "malformed", max: 4096, want: errLoginOutputMalformed},
	} {
		t.Run(test.mode+" output stops child", func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			session := startWindowsLoginTestSession(t, fakeGlab, test.mode, ready)
			state := &loginOutputState{}
			outputDone := make(chan struct{})
			go func() {
				relayLoginOutput(session, &bytes.Buffer{}, test.max, func() { _ = session.Kill() }, state)
				close(outputDone)
			}()
			waitForWindowsLoginFake(t, ready, 5*time.Second)
			waitDone := make(chan error, 1)
			go func() { waitDone <- session.Wait() }()
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s output did not stop the ConPTY child", test.mode)
			}
			finishWindowsLoginSession(t, session, outputDone)
			warning, monitorErr := state.snapshot()
			if warning || !errors.Is(monitorErr, test.want) {
				t.Fatalf("warning=%t monitor=%v want=%v", warning, monitorErr, test.want)
			}
		})
	}
}

func startWindowsLoginTestSession(t *testing.T, path, mode, ready string) loginTerminalSession {
	t.Helper()
	environment := append(os.Environ(), "GLAB_AXI_WINDOWS_FAKE_MODE="+mode, "GLAB_AXI_WINDOWS_FAKE_READY="+ready)
	session, err := startLoginTerminal(path, nil, "", environment, nil, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Kill()
		_ = session.Close()
	})
	return session
}

func finishWindowsLoginSession(t *testing.T, session loginTerminalSession, outputDone <-chan struct{}) {
	t.Helper()
	if err := session.AfterWait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-outputDone:
	case <-time.After(2 * time.Second):
		_ = session.Close()
		select {
		case <-outputDone:
		case <-time.After(2 * time.Second):
			t.Fatal("ConPTY output relay did not stop")
		}
	}
	if err := session.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func waitForWindowsLoginFake(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows fake login did not become ready: %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func buildWindowsLoginFake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	executable := filepath.Join(dir, "fake-glab.exe")
	program := `package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func main() {
	mode := os.Getenv("GLAB_AXI_WINDOWS_FAKE_MODE")
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprintln(os.Stderr, "child-tty=0")
		os.Exit(92)
	}
	fmt.Fprintln(os.Stderr, "child-tty=1/1/1")
	fmt.Fprintln(os.Stderr, "synthetic human prompt")
	if err := os.WriteFile(os.Getenv("GLAB_AXI_WINDOWS_FAKE_READY"), []byte("ready"), 0600); err != nil {
		panic(err)
	}
	switch mode {
	case "prompt":
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(answer) != "continue" {
			os.Exit(44)
		}
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, 65))
		for { time.Sleep(time.Hour) }
	case "malformed":
		_, _ = os.Stdout.Write([]byte{0})
		for { time.Sleep(time.Hour) }
	case "wait":
		for { time.Sleep(time.Hour) }
	default:
		os.Exit(93)
	}
}
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", executable, source)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build Windows fake glab: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	return executable
}
