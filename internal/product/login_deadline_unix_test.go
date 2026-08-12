//go:build !windows

package product

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"glab-axi/internal/delegate/glab"
	"glab-axi/internal/limits"
	runtimepkg "glab-axi/internal/runtime"

	"github.com/creack/pty"
)

// TestProductLoginPromptOutlivesShortOperation compares the product wrapper
// with the same credential-free prompt executed directly. Neither prompt may
// inherit the ordinary noninteractive operation deadline.
func TestProductLoginPromptOutlivesShortOperation(t *testing.T) {
	script, record := promptWaitingFakeGlab(t)
	directReady := filepath.Join(t.TempDir(), "direct-ready")
	wrappedReady := filepath.Join(t.TempDir(), "wrapped-ready")

	directMaster, directSlave, directOutput := openPromptPTY(t)
	direct := exec.Command(script, "auth", "login", "--hostname", "gitlab.example.invalid")
	direct.Env = append(os.Environ(), "GLAB_AXI_FAKE_READY="+directReady, "GLAB_AXI_FAKE_RECORD="+record)
	direct.Stdin, direct.Stdout, direct.Stderr = directSlave, directSlave, directSlave
	if err := direct.Start(); err != nil {
		t.Fatal(err)
	}
	_ = directSlave.Close()
	t.Cleanup(func() {
		_ = direct.Process.Kill()
		_, _ = direct.Process.Wait()
		_ = directMaster.Close()
	})

	wrappedMaster, wrappedSlave, wrappedOutput := openPromptPTY(t)
	deps := Dependencies{
		Runtime: runtimepkg.Dependencies{
			Stdin: wrappedSlave, Stdout: wrappedSlave, Stderr: wrappedSlave, Cwd: t.TempDir(),
			LookupEnv: func(string) (string, bool) { return "", false },
		},
		Env:             append(os.Environ(), "GLAB_AXI_FAKE_READY="+wrappedReady, "GLAB_AXI_FAKE_RECORD="+record),
		IsHumanTerminal: func() bool { return true },
	}
	deps.NewDelegate = func() delegateClient {
		return glab.NewClient(glab.ClientConfig{
			Path: script, Dir: deps.Runtime.Cwd, Env: deps.Env,
			Stdin: wrappedSlave, Stdout: wrappedSlave, Stderr: wrappedSlave,
			IsTerminal:       deps.IsHumanTerminal,
			SecureStoreProbe: func(context.Context) error { return nil },
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*limits.ShortOperation)
	defer cancel()
	wrappedDone := make(chan int, 1)
	go func() {
		wrappedDone <- Run(ctx, []string{"auth", "login", "--hostname", "gitlab.example.invalid"}, deps)
	}()
	waitForPromptReady(t, directReady, 5*time.Second)
	waitForPromptReady(t, wrappedReady, 5*time.Second)

	timer := time.NewTimer(limits.ShortOperation + 250*time.Millisecond)
	defer timer.Stop()
	select {
	case code := <-wrappedDone:
		t.Fatalf("wrapped prompt stopped at the short-operation deadline: exit=%d", code)
	case <-timer.C:
	}
	if err := direct.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("direct prompt did not remain alive beyond ShortOperation: %v", err)
	}
	if _, err := directMaster.Write([]byte("continue\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := wrappedMaster.Write([]byte("continue\n")); err != nil {
		t.Fatal(err)
	}
	if err := direct.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-wrappedDone:
		if code != 0 {
			t.Fatalf("wrapped prompt failed after human input: exit=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wrapped prompt did not complete after human input")
	}

	_ = directMaster.Close()
	directText := <-directOutput
	if !bytes.Contains(directText, []byte("synthetic human prompt")) || !bytes.Contains(directText, []byte("child-tty=1/1/1")) {
		t.Fatalf("direct prompt output=%q", directText)
	}
	_ = wrappedSlave.Close()
	_ = wrappedMaster.Close()
	wrappedText := <-wrappedOutput
	if !bytes.Contains(wrappedText, []byte("synthetic human prompt")) || !bytes.Contains(wrappedText, []byte("child-tty=1/1/1")) || bytes.Contains(wrappedText, []byte("timed out")) {
		t.Fatalf("wrapped output=%q", wrappedText)
	}
	argv, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(argv), "auth login --hostname gitlab.example.invalid\n") != 2 || strings.Count(string(argv), "version\n") != 1 {
		t.Fatalf("unexpected direct/wrapped argv record: %q", argv)
	}
}

func openPromptPTY(t *testing.T) (*os.File, *os.File, <-chan []byte) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = slave.Close()
		_ = master.Close()
	})
	output := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(master)
		output <- data
	}()
	return master, slave, output
}

func waitForPromptReady(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("prompt did not become ready: %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func promptWaitingFakeGlab(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "glab")
	record := filepath.Join(dir, "argv")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${GLAB_AXI_FAKE_RECORD}"
if [ "${1:-}" = "version" ]; then
  printf 'glab 1.112.0 (816e3a52)\n'
  exit 0
fi
if [ "$#" -ne 4 ] || [ "${1:-}" != "auth" ] || [ "${2:-}" != "login" ] || [ "${3:-}" != "--hostname" ]; then
  exit 93
fi
stdin_tty=0; stdout_tty=0; stderr_tty=0
[ -t 0 ] && stdin_tty=1
[ -t 1 ] && stdout_tty=1
[ -t 2 ] && stderr_tty=1
printf 'child-tty=%s/%s/%s\n' "$stdin_tty" "$stdout_tty" "$stderr_tty" >&2
[ "$stdin_tty/$stdout_tty/$stderr_tty" = "1/1/1" ] || exit 92
printf 'synthetic human prompt\n' >&2
: > "${GLAB_AXI_FAKE_READY}"
IFS= read -r answer
[ "$answer" = "continue" ]
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, record
}
