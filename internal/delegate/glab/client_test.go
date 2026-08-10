package glab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"glab-axi/internal/auth"
	"glab-axi/internal/commandctx"
	"glab-axi/internal/contract/uxv1"

	"github.com/creack/pty"
	"golang.org/x/term"
)

type probeKeyring struct {
	mu      sync.Mutex
	entries map[string]string
	setErr  error
}

func (k *probeKeyring) Get(context.Context, string, string) (string, error) {
	return "", auth.ErrKeyringNotFound
}

func (k *probeKeyring) Set(_ context.Context, service, account, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.setErr != nil {
		return k.setErr
	}
	if k.entries == nil {
		k.entries = map[string]string{}
	}
	k.entries[service+"\x00"+account] = value
	return nil
}

func (k *probeKeyring) Delete(_ context.Context, service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.entries, service+"\x00"+account)
	return nil
}

func TestClientPinsVersionSanitizesEnvironmentAndBoundsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	secret := strings.Join([]string{"glpat", "runtime", "client", "sentinel"}, "-")
	client := NewClient(ClientConfig{
		Path: script,
		Env: append(os.Environ(),
			"GLAB_AXI_FAKE_RECORD="+record,
			"GLAB_AXI_FAKE_BODY={\"iid\":7,\"title\":\"safe\"}",
			"GLAB_DEBUG_HTTP=true",
			"GLAB_CHECK_UPDATE=true",
			"GLAB_ENABLE_CI_AUTOLOGIN=true",
			"PAGER=hostile-pager",
			"GITLAB_TOKEN="+secret,
		),
	})
	response, err := client.Do(context.Background(), Request{Operation: OpIssueView, Host: "gitlab.com", Repo: "group/project", IID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if response.UpstreamVersion != SupportedVersion || string(response.Body) != `{"iid":7,"title":"safe"}` {
		t.Fatalf("unexpected response: %#v", response)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "version") || !strings.Contains(string(data), "issue list") && !strings.Contains(string(data), "issue view") {
		t.Fatalf("unexpected child record: %q", data)
	}

	overflow := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=overflow")})
	_, err = overflow.Do(context.Background(), Request{Operation: OpIssueView, Host: "gitlab.com", Repo: "group/project", IID: 7})
	if err == nil || uxv1.AsError(err).Code != uxv1.CodeUpstream {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestClientMapsTimeoutCancellationAndChildErrorsWithoutRawStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	request := Request{Operation: OpIssueView, Host: "gitlab.com", Repo: "group/project", IID: 7}

	timeoutClient := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=sleep")})
	if _, err := timeoutClient.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer timeoutCancel()
	if _, err := timeoutClient.Do(timeoutCtx, request); err == nil || uxv1.AsError(err).Code != uxv1.CodeUpstream {
		t.Fatalf("timeout error=%v", err)
	}

	// Reuse the version-pinned client so this assertion exercises operation
	// cancellation rather than starting another dependency probe under load.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := timeoutClient.Do(cancelCtx, request); err == nil || uxv1.AsError(err).Code != uxv1.CodeCanceled {
		t.Fatalf("cancel error=%v", err)
	}

	childSecret := strings.Join([]string{"glpat", "child", "stderr", "sentinel"}, "-")
	failureClient := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=auth-failure", "GLAB_AXI_FAKE_STDERR_SENTINEL="+childSecret)})
	_, err := failureClient.Do(context.Background(), request)
	if err == nil || uxv1.AsError(err).Code != uxv1.CodeAuthentication || strings.Contains(err.Error(), childSecret) {
		t.Fatalf("child error=%v", err)
	}
}

func TestClientRejectsMissingAndChangedOfficialGlab(t *testing.T) {
	missing := NewClient(ClientConfig{LookPath: func(string) (string, error) { return "", errors.New("missing") }})
	if _, err := missing.Version(context.Background()); err == nil || uxv1.AsError(err).Code != uxv1.CodeDependencyMissing {
		t.Fatalf("missing error=%v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	script, record := fakeGlab(t)
	for _, mode := range []string{"changed-version", "changed-build"} {
		changed := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE="+mode)})
		if _, err := changed.Version(context.Background()); err == nil || uxv1.AsError(err).Code != uxv1.CodeDependencyUnsupported {
			t.Fatalf("%s error=%v", mode, err)
		}
	}
}

func TestLoginChildDescriptorsAreTerminalsEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("outer PTY regression uses a Unix test terminal")
	}
	if os.Getenv("GLAB_AXI_LOGIN_E2E_HELPER") == "1" {
		client := NewClient(ClientConfig{
			Path:   os.Getenv("GLAB_AXI_LOGIN_E2E_GLAB"),
			Env:    os.Environ(),
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			IsTerminal: func() bool {
				return term.IsTerminal(int(os.Stdin.Fd())) &&
					term.IsTerminal(int(os.Stdout.Fd())) &&
					term.IsTerminal(int(os.Stderr.Fd()))
			},
			SecureStoreProbe: func(context.Context) error { return nil },
		})
		if _, err := client.Login(context.Background(), "gitlab.com"); err != nil {
			t.Fatal(err)
		}
		return
	}

	script, record := fakeGlab(t)
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoginChildDescriptorsAreTerminalsEndToEnd$")
	cmd.Env = append(os.Environ(),
		"GLAB_AXI_LOGIN_E2E_HELPER=1",
		"GLAB_AXI_LOGIN_E2E_GLAB="+script,
		"GLAB_AXI_FAKE_RECORD="+record,
		"GLAB_AXI_FAKE_MODE=require-tty",
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		t.Fatal(err)
	}
	_ = slave.Close()
	outputDone := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(master)
		outputDone <- output
	}()
	waitErr := cmd.Wait()
	_ = master.Close()
	output := <-outputDone
	if waitErr != nil {
		t.Fatalf("PTY login helper failed: %v; output=%q", waitErr, output)
	}
	if !bytes.Contains(output, []byte("delegated-child-tty=1/1/1")) {
		t.Fatalf("delegated child did not inherit three terminals: %q", output)
	}
}

func TestLoginInterruptCancelsDelegatedPTYChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal regression uses Unix interrupt semantics")
	}
	if os.Getenv("GLAB_AXI_LOGIN_SIGNAL_HELPER") == "1" {
		client := NewClient(ClientConfig{
			Path:   os.Getenv("GLAB_AXI_LOGIN_E2E_GLAB"),
			Env:    os.Environ(),
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			IsTerminal: func() bool {
				return term.IsTerminal(int(os.Stdin.Fd())) &&
					term.IsTerminal(int(os.Stdout.Fd())) &&
					term.IsTerminal(int(os.Stderr.Fd()))
			},
			SecureStoreProbe: func(context.Context) error { return nil },
		})
		code := commandctx.Run(func(ctx context.Context) int {
			_, err := client.Login(ctx, "gitlab.com")
			if err == nil {
				return 0
			}
			return uxv1.ExitCode(err)
		})
		os.Exit(code)
	}

	script, record := fakeGlab(t)
	ready := filepath.Join(t.TempDir(), "ready")
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoginInterruptCancelsDelegatedPTYChild$")
	cmd.Env = append(os.Environ(),
		"GLAB_AXI_LOGIN_SIGNAL_HELPER=1",
		"GLAB_AXI_LOGIN_E2E_GLAB="+script,
		"GLAB_AXI_FAKE_RECORD="+record,
		"GLAB_AXI_FAKE_MODE=login-wait",
		"GLAB_AXI_FAKE_READY="+ready,
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		t.Fatal(err)
	}
	_ = slave.Close()
	outputDone := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(master)
		outputDone <- output
	}()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("delegated login child did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	_ = master.Close()
	output := <-outputDone
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupt exit=%v output=%q", waitErr, output)
	}
	if !bytes.Contains(output, []byte("synthetic login waiting")) {
		t.Fatalf("interactive output was not relayed before interrupt: %q", output)
	}
}

func TestLoginRequiresHumanTTYAndSecureStoreBeforeChildExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	nonTTY := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record), IsTerminal: func() bool { return false }, Keyring: &probeKeyring{}})
	if _, err := nonTTY.Login(context.Background(), "gitlab.com"); err == nil || uxv1.AsError(err).Code != uxv1.CodeInteractiveRequired {
		t.Fatalf("non-TTY error=%v", err)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-TTY login executed child: %v", err)
	}

	terminals := newLoginTestTerminals(t)
	unavailable := NewClient(ClientConfig{
		Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record),
		Stdin: terminals.stdin.slave, Stdout: terminals.stdout.slave, Stderr: terminals.stderr.slave,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{setErr: errors.New("no secure store")},
	})
	if _, err := unavailable.Login(context.Background(), "gitlab.com"); err == nil || uxv1.AsError(err).Code != uxv1.CodeSafety {
		t.Fatalf("secure-store error=%v", err)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("insecure login executed child: %v", err)
	}
}

func TestLoginRejectsMockedTerminalPredicateForPipeDescriptors(t *testing.T) {
	stdin, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutReader, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer stdinWriter.Close()
	defer stdout.Close()
	defer stdoutReader.Close()
	defer stderr.Close()
	defer stderrReader.Close()

	probeCalled := false
	client := NewClient(ClientConfig{
		Path: "unused", Stdin: stdin, Stdout: stdout, Stderr: stderr,
		IsTerminal:       func() bool { return true },
		SecureStoreProbe: func(context.Context) error { probeCalled = true; return nil },
	})
	_, loginErr := client.Login(context.Background(), "gitlab.com")
	if loginErr == nil || uxv1.AsError(loginErr).Code != uxv1.CodeInteractiveRequired || probeCalled {
		t.Fatalf("pipe login error=%v probe_called=%t", loginErr, probeCalled)
	}
}

func TestLoginStripsAmbientCredentialsWhileReadsRetainApprovedTokens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	secret := strings.Join([]string{"glpat", "login", "environment", "sentinel"}, "-")
	base := append(os.Environ(),
		"GLAB_AXI_FAKE_RECORD="+record,
		"GLAB_AXI_FAKE_REQUIRE_LOGIN_ENV_CLEAN=1",
		"GLAB_AXI_FAKE_MODE=login-failure",
		"GITLAB_TOKEN="+secret,
		"GITLAB_ACCESS_TOKEN="+secret,
		"OAUTH_TOKEN="+secret,
		"CI_JOB_TOKEN="+secret,
		"CI=true",
		"GITLAB_CI=true",
	)
	terminals := newLoginTestTerminals(t)
	client := NewClient(ClientConfig{
		Path: script, Env: base,
		Stdin: terminals.stdin.slave, Stdout: terminals.stdout.slave, Stderr: terminals.stderr.slave,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
	})
	_, loginErr := client.Login(context.Background(), "gitlab.com")
	_, terminalOutput := terminals.close()
	if loginErr == nil || uxv1.AsError(loginErr).Code != uxv1.CodeAuthentication {
		t.Fatal("synthetic login failure was not mapped to a controlled authentication error")
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || bytes.Contains(terminalOutput, []byte(secret)) || strings.Contains(loginErrString(loginErr), secret) {
		t.Fatal("login credential reached child argv, terminal output, or wrapper error")
	}

	readEnv := sanitizedEnv([]string{"GITLAB_TOKEN=" + secret, "glab_debug_http=true"}, "gitlab.com", false)
	loginEnv := sanitizedEnv([]string{"GITLAB_TOKEN=" + secret, "glab_debug_http=true"}, "gitlab.com", true)
	versionEnv := sanitizedEnv([]string{"GITLAB_TOKEN=" + secret, "glab_debug_http=true"}, "", false)
	if !envContains(readEnv, "GITLAB_TOKEN", secret) || envContains(loginEnv, "GITLAB_TOKEN", secret) || envContains(versionEnv, "GITLAB_TOKEN", secret) {
		t.Fatal("credential environment lane separation changed")
	}
	for _, item := range append(append(readEnv, loginEnv...), versionEnv...) {
		if strings.EqualFold(strings.SplitN(item, "=", 2)[0], "GLAB_DEBUG_HTTP") && item != "GLAB_DEBUG_HTTP=false" {
			t.Fatalf("case-variant unsafe environment survived: %q", item)
		}
	}
}

func TestLoginRelaysInteractiveOutputAwayFromStructuredStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	terminals := newLoginTestTerminals(t)
	client := NewClient(ClientConfig{
		Path:  script,
		Env:   append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=login-output"),
		Stdin: terminals.stdin.slave, Stdout: terminals.stdout.slave, Stderr: terminals.stderr.slave,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
	})
	_, loginErr := client.Login(context.Background(), "gitlab.com")
	structured, terminalOutput := terminals.close()
	if loginErr != nil {
		t.Fatal(loginErr)
	}
	if len(structured) != 0 || !bytes.Contains(terminalOutput, []byte("interactive login output")) {
		t.Fatalf("structured=%q terminal=%q", structured, terminalOutput)
	}
}

func TestLoginAbortsPinnedPlaintextFallbackWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	for _, mode := range []string{"plaintext-fallback-zero-exit", "plaintext-fallback-wait"} {
		t.Run(mode, func(t *testing.T) {
			script, record := fakeGlab(t)
			terminals := newLoginTestTerminals(t)
			client := NewClient(ClientConfig{
				Path:  script,
				Env:   append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE="+mode),
				Stdin: terminals.stdin.slave, Stdout: terminals.stdout.slave, Stderr: terminals.stderr.slave,
				IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, loginErr := client.Login(ctx, "gitlab.com")
			_, terminalOutput := terminals.close()
			if loginErr == nil || uxv1.AsError(loginErr).Code != uxv1.CodeSafety {
				t.Fatalf("fallback error=%v", loginErr)
			}
			if !bytes.Contains(terminalOutput, []byte(plaintextFallbackWarning)) {
				t.Fatalf("human did not receive upstream warning: %q", terminalOutput)
			}
		})
	}
}

func TestLoginMapsChildFailureAndCancellationWithoutRawOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)

	failureTerminals := newLoginTestTerminals(t)
	failureClient := NewClient(ClientConfig{
		Path:  script,
		Env:   append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=login-failure"),
		Stdin: failureTerminals.stdin.slave, Stdout: failureTerminals.stdout.slave, Stderr: failureTerminals.stderr.slave,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
	})
	_, failureErr := failureClient.Login(context.Background(), "gitlab.com")
	_, failureOutput := failureTerminals.close()
	if failureErr == nil || uxv1.AsError(failureErr).Code != uxv1.CodeAuthentication || strings.Contains(failureErr.Error(), "synthetic login child failure") {
		t.Fatalf("child failure=%v", failureErr)
	}
	if !bytes.Contains(failureOutput, []byte("synthetic login child failure")) {
		t.Fatal("human did not receive child failure text")
	}

	cancelTerminals := newLoginTestTerminals(t)
	ready := filepath.Join(t.TempDir(), "ready")
	cancelClient := NewClient(ClientConfig{
		Path: script,
		Env: append(os.Environ(),
			"GLAB_AXI_FAKE_RECORD="+record,
			"GLAB_AXI_FAKE_MODE=login-wait",
			"GLAB_AXI_FAKE_READY="+ready,
		),
		Stdin: cancelTerminals.stdin.slave, Stdout: cancelTerminals.stdout.slave, Stderr: cancelTerminals.stderr.slave,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
	})
	ctx, cancel := context.WithCancel(context.Background())
	loginDone := make(chan error, 1)
	go func() {
		_, err := cancelClient.Login(ctx, "gitlab.com")
		loginDone <- err
	}()
	waitForFakeGlabReady(t, ready, 5*time.Second)
	started := time.Now()
	cancel()
	var cancelErr error
	select {
	case cancelErr = <-loginDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not terminate the delegated PTY child promptly")
	}
	cancelTerminals.close()
	if cancelErr == nil || uxv1.AsError(cancelErr).Code != uxv1.CodeCanceled {
		t.Fatalf("cancellation error=%v", cancelErr)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestLoginOutputFailureTerminatesDelegatedPTYChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	for _, test := range []struct {
		mode    string
		message string
	}{
		{mode: "login-overflow", message: "interactive output exceeded the safety limit"},
		{mode: "login-malformed", message: "malformed interactive output"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			script, record := fakeGlab(t)
			ready := filepath.Join(t.TempDir(), "ready")
			terminals := newLoginTestTerminals(t)
			client := NewClient(ClientConfig{
				Path: script,
				Env: append(os.Environ(),
					"GLAB_AXI_FAKE_RECORD="+record,
					"GLAB_AXI_FAKE_MODE="+test.mode,
					"GLAB_AXI_FAKE_READY="+ready,
				),
				Stdin: terminals.stdin.slave, Stdout: terminals.stdout.slave, Stderr: terminals.stderr.slave,
				IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			started := time.Now()
			_, loginErr := client.Login(ctx, "gitlab.com")
			terminals.close()
			if loginErr == nil || uxv1.AsError(loginErr).Code != uxv1.CodeUpstream || !strings.Contains(loginErr.Error(), test.message) {
				t.Fatalf("login error=%v", loginErr)
			}
			if ctx.Err() != nil {
				t.Fatalf("output monitor failed to stop the child before the test deadline: %v", ctx.Err())
			}
			if _, err := os.Stat(ready); err != nil {
				t.Fatalf("fake child did not reach its blocking prompt: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatalf("output failure took %s to terminate the child", elapsed)
			}
		})
	}
}

func TestLoginMonitorRejectsMalformedAndOversizedOutput(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		max   int
		want  error
	}{
		{name: "invalid UTF-8", input: []byte{'o', 'k', 0xff}, max: 64, want: errLoginOutputMalformed},
		{name: "NUL", input: []byte{'o', 'k', 0}, max: 64, want: errLoginOutputMalformed},
		{name: "overflow", input: bytes.Repeat([]byte{'x'}, 65), max: 64, want: errLoginOutputOverflow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &loginOutputState{}
			canceled := false
			var output bytes.Buffer
			relayLoginOutput(bytes.NewReader(test.input), &output, test.max, func() { canceled = true }, state)
			warning, gotErr := state.snapshot()
			if warning || !errors.Is(gotErr, test.want) || !canceled || output.Len() > test.max {
				t.Fatalf("warning=%t error=%v canceled=%t output_bytes=%d", warning, gotErr, canceled, output.Len())
			}
		})
	}
}

func TestLoginMonitorMatchesOnlyExactWarningAcrossReads(t *testing.T) {
	warning := []byte(plaintextFallbackWarning)
	state := &loginOutputState{}
	var output bytes.Buffer
	relayLoginOutput(&chunkReader{chunks: [][]byte{warning[:31], warning[31:]}}, &output, 4096, func() {}, state)
	found, monitorErr := state.snapshot()
	if !found || monitorErr != nil || !bytes.Contains(output.Bytes(), warning) {
		t.Fatalf("exact warning found=%t error=%v output=%q", found, monitorErr, output.Bytes())
	}

	nearState := &loginOutputState{}
	near := append(append([]byte(nil), warning[:len(warning)-1]...), '!')
	relayLoginOutput(bytes.NewReader(near), io.Discard, 4096, func() {}, nearState)
	found, monitorErr = nearState.snapshot()
	if found || monitorErr != nil {
		t.Fatalf("near warning found=%t error=%v", found, monitorErr)
	}
}

type capturedTestTerminal struct {
	master *os.File
	slave  *os.File
	done   chan []byte
	once   sync.Once
	output []byte
}

func newCapturedTestTerminal(t *testing.T) *capturedTestTerminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	terminal := &capturedTestTerminal{master: master, slave: slave, done: make(chan []byte, 1)}
	go func() {
		const captureLimit = 1 << 20
		var output bytes.Buffer
		buffer := make([]byte, 4<<10)
		for {
			n, readErr := master.Read(buffer)
			if n > 0 && output.Len() < captureLimit {
				keep := n
				if keep > captureLimit-output.Len() {
					keep = captureLimit - output.Len()
				}
				_, _ = output.Write(buffer[:keep])
			}
			if readErr != nil {
				terminal.done <- output.Bytes()
				return
			}
		}
	}()
	return terminal
}

func (t *capturedTestTerminal) close() []byte {
	t.once.Do(func() {
		_ = t.slave.Close()
		select {
		case t.output = <-t.done:
		case <-time.After(2 * time.Second):
			_ = t.master.Close()
			t.output = <-t.done
		}
		_ = t.master.Close()
	})
	return append([]byte(nil), t.output...)
}

type loginTestTerminals struct {
	stdin  *capturedTestTerminal
	stdout *capturedTestTerminal
	stderr *capturedTestTerminal
	once   sync.Once
	out    []byte
	err    []byte
}

func newLoginTestTerminals(t *testing.T) *loginTestTerminals {
	t.Helper()
	terminals := &loginTestTerminals{
		stdin:  newCapturedTestTerminal(t),
		stdout: newCapturedTestTerminal(t),
		stderr: newCapturedTestTerminal(t),
	}
	t.Cleanup(func() { terminals.close() })
	return terminals
}

func (t *loginTestTerminals) close() ([]byte, []byte) {
	t.once.Do(func() {
		_ = t.stdin.close()
		t.out = t.stdout.close()
		t.err = t.stderr.close()
	})
	return append([]byte(nil), t.out...), append([]byte(nil), t.err...)
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(destination []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(destination, r.chunks[0])
	if n == len(r.chunks[0]) {
		r.chunks = r.chunks[1:]
	} else {
		r.chunks[0] = r.chunks[0][n:]
	}
	return n, nil
}

func loginErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func waitForFakeGlabReady(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake glab did not become ready: %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func fakeGlab(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "glab")
	record := filepath.Join(dir, "record")
	script := `#!/bin/sh
set -eu
if [ "${GLAB_CHECK_UPDATE:-}" != "false" ] || [ "${GLAB_DEBUG_HTTP:-}" != "false" ] || [ "${GLAB_ENABLE_CI_AUTOLOGIN:-}" != "false" ]; then
  echo unsafe-environment >&2
  exit 90
fi
printf '%s\n' "$*" >> "${GLAB_AXI_FAKE_RECORD}"
if [ "${GLAB_AXI_FAKE_REQUIRE_LOGIN_ENV_CLEAN:-}" = "1" ] && [ "${1:-}" = "auth" ] && [ "${2:-}" = "login" ]; then
  if [ -n "${GITLAB_TOKEN:-}" ] || [ -n "${GITLAB_ACCESS_TOKEN:-}" ] || [ -n "${OAUTH_TOKEN:-}" ] || [ -n "${CI_JOB_TOKEN:-}" ]; then
    printf 'ambient credential reached login dependency child: %s%s%s%s\n' "${GITLAB_TOKEN:-}" "${GITLAB_ACCESS_TOKEN:-}" "${OAUTH_TOKEN:-}" "${CI_JOB_TOKEN:-}" >&2
    exit 91
  fi
  if [ "${CI:-}" != "false" ] || [ "${GITLAB_CI:-}" != "false" ]; then
    printf 'ambient CI storage mode reached login dependency child\n' >&2
    exit 91
  fi
fi
if [ "${1:-}" = "version" ]; then
  case "${GLAB_AXI_FAKE_MODE:-}" in
    changed-version) printf 'glab 1.113.0 (changed)\n' ;;
    changed-build) printf 'glab 1.112.0 (different)\n' ;;
    *) printf 'glab 1.112.0 (816e3a52)\n' ;;
  esac
  exit 0
fi
if [ "${1:-}" = "auth" ] && [ "${2:-}" = "login" ]; then
  if [ "$#" -ne 4 ] || [ "${3:-}" != "--hostname" ]; then
    printf 'unexpected login argv\n' >&2
    exit 93
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "require-tty" ]; then
    stdin_tty=0; stdout_tty=0; stderr_tty=0
    [ -t 0 ] && stdin_tty=1
    [ -t 1 ] && stdout_tty=1
    [ -t 2 ] && stderr_tty=1
    printf 'delegated-child-tty=%s/%s/%s\n' "$stdin_tty" "$stdout_tty" "$stderr_tty" >&2
    [ "$stdin_tty/$stdout_tty/$stderr_tty" = "1/1/1" ] || exit 92
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "plaintext-fallback-zero-exit" ]; then
    printf 'WARNING: The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file.\n' >&2
    exit 0
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "plaintext-fallback-wait" ]; then
    printf 'WARNING: The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file.\n' >&2
    IFS= read -r _ignored
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "login-output" ]; then
    printf 'interactive login output\n'
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "login-failure" ]; then
    printf 'synthetic login child failure\n' >&2
    exit 42
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "login-wait" ]; then
    printf 'synthetic login waiting\n' >&2
    if [ -n "${GLAB_AXI_FAKE_READY:-}" ]; then
      : > "${GLAB_AXI_FAKE_READY}"
    fi
    IFS= read -r _ignored
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "login-overflow" ]; then
    if [ -n "${GLAB_AXI_FAKE_READY:-}" ]; then
      : > "${GLAB_AXI_FAKE_READY}"
    fi
    dd if=/dev/zero bs=1048576 count=9 2>/dev/null | tr '\000' x
    IFS= read -r _ignored
  fi
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "login-malformed" ]; then
    if [ -n "${GLAB_AXI_FAKE_READY:-}" ]; then
      : > "${GLAB_AXI_FAKE_READY}"
    fi
    printf '\377'
    IFS= read -r _ignored
  fi
  exit 0
fi
if [ "${GLAB_AXI_FAKE_MODE:-}" = "overflow" ]; then
  dd if=/dev/zero bs=1048576 count=3 2>/dev/null | tr '\000' x
  exit 0
fi
if [ "${GLAB_AXI_FAKE_MODE:-}" = "sleep" ]; then
  sleep 10
  exit 0
fi
if [ "${GLAB_AXI_FAKE_MODE:-}" = "auth-failure" ]; then
  printf '401 token %s\n' "${GLAB_AXI_FAKE_STDERR_SENTINEL:-synthetic}" >&2
  exit 1
fi
if [ -n "${GLAB_AXI_FAKE_BODY:-}" ]; then
  printf '%s' "${GLAB_AXI_FAKE_BODY}"
else
  printf '{}'
fi
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, record
}

func envContains(environment []string, name, value string) bool {
	want := name + "=" + value
	for _, item := range environment {
		if item == want {
			return true
		}
	}
	return false
}
