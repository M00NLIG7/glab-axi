package glab

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"glab-axi/internal/auth"
	"glab-axi/internal/contract/uxv1"
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
	secret := "glpat-runtime-client-sentinel"
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

func TestClientRejectsMissingAndChangedOfficialGlab(t *testing.T) {
	missing := NewClient(ClientConfig{LookPath: func(string) (string, error) { return "", errors.New("missing") }})
	if _, err := missing.Version(context.Background()); err == nil || uxv1.AsError(err).Code != uxv1.CodeDependencyMissing {
		t.Fatalf("missing error=%v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	script, record := fakeGlab(t)
	changed := NewClient(ClientConfig{Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=changed-version")})
	if _, err := changed.Version(context.Background()); err == nil || uxv1.AsError(err).Code != uxv1.CodeDependencyUnsupported {
		t.Fatalf("changed-version error=%v", err)
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

	unavailable := NewClient(ClientConfig{
		Path: script, Env: append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record),
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{setErr: errors.New("no secure store")},
	})
	if _, err := unavailable.Login(context.Background(), "gitlab.com"); err == nil || uxv1.AsError(err).Code != uxv1.CodeSafety {
		t.Fatalf("secure-store error=%v", err)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("insecure login executed child: %v", err)
	}
}

func TestLoginAbortsPinnedPlaintextFallbackWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeGlab(t)
	var stderr bytes.Buffer
	client := NewClient(ClientConfig{
		Path:  script,
		Env:   append(os.Environ(), "GLAB_AXI_FAKE_RECORD="+record, "GLAB_AXI_FAKE_MODE=plaintext-fallback"),
		Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &stderr,
		IsTerminal: func() bool { return true }, Keyring: &probeKeyring{},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.Login(ctx, "gitlab.com"); err == nil || uxv1.AsError(err).Code != uxv1.CodeSafety {
		t.Fatalf("fallback error=%v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), plaintextFallbackWarning) {
		t.Fatalf("human did not receive upstream warning: %q", stderr.String())
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
if [ "${1:-}" = "version" ]; then
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "changed-version" ]; then
    printf 'glab 1.113.0 (changed)\n'
  else
    printf 'glab 1.112.0 (816e3a52)\n'
  fi
  exit 0
fi
if [ "${1:-}" = "auth" ] && [ "${2:-}" = "login" ]; then
  if [ "${GLAB_AXI_FAKE_MODE:-}" = "plaintext-fallback" ]; then
    printf 'WARNING: The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file.\n' >&2
    sleep 10
  fi
  exit 0
fi
if [ "${GLAB_AXI_FAKE_MODE:-}" = "overflow" ]; then
  dd if=/dev/zero bs=1048576 count=3 2>/dev/null | tr '\000' x
  exit 0
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
