package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"glab-axi/internal/auth"
	runtimepkg "glab-axi/internal/runtime"
	"glab-axi/internal/testgitlab"
)

type memoryKeyring struct {
	mu      sync.Mutex
	service string
	account string
	secret  string
}

func (m *memoryKeyring) Get(context.Context, string, string) (string, error) {
	return "", auth.ErrKeyringNotFound
}

func (m *memoryKeyring) Set(_ context.Context, service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.service, m.account, m.secret = service, account, secret
	return nil
}

func TestTokenImportUsesOnlyStdinAndKeyring(t *testing.T) {
	secret := generatedToken()
	var sawHeader bool
	server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" || r.Method != http.MethodGet {
			t.Error("token validation used an unexpected endpoint")
		}
		if r.Header.Get("PRIVATE-TOKEN") == secret {
			sawHeader = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": "import-bot"})
	}))
	defer server.Close()

	temp := t.TempDir()
	if err := os.Chmod(temp, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temp, "config.json")
	host := strings.TrimPrefix(server.HTTP.URL, "https://")
	keyring := &memoryKeyring{}
	var stdout, stderr bytes.Buffer
	deps := runtimepkg.Dependencies{
		Stdin:      strings.NewReader(secret + "\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		Cwd:        temp,
		ConfigPath: configPath,
		Keyring:    keyring,
		HTTPClient: server.HTTP.Client(),
		IsTerminal: func() bool { return false },
	}
	args := []string{"auth", "import", "--host", host, "--api-base", server.HTTP.URL + "/api/v4", "--web-base", server.HTTP.URL, "--token-stdin", "--format", "json"}
	if code := RunNative(context.Background(), args, deps); code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !sawHeader || keyring.secret != secret || keyring.service == "" || keyring.account != host {
		t.Fatal("validated credential was not stored through the keyring abstraction")
	}
	if bytes.Contains(stdout.Bytes(), []byte(secret)) || bytes.Contains(stderr.Bytes(), []byte(secret)) {
		t.Fatal("credential sentinel reached command output")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("credential sentinel reached config")
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode=%v", info.Mode().Perm())
	}
	for _, arg := range args {
		if arg == secret {
			t.Fatal("credential sentinel reached argv")
		}
	}
}

func TestNativeAuthStatusEmitsVersionedJSONAndTOON(t *testing.T) {
	secret := generatedToken()
	server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": "bot"})
		case "/api/v4/projects/group/project":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "path_with_namespace": "group/project", "web_url": serverURLFromRequest(r) + "/group/project"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
		}
	}))
	defer server.Close()
	temp := t.TempDir()
	host := strings.TrimPrefix(server.HTTP.URL, "https://")
	configPath := filepath.Join(temp, "config.json")
	configData, _ := json.Marshal(map[string]any{
		"schema": "glab-axi/config-v1",
		"hosts": map[string]any{host: map[string]any{
			"git_hosts": []string{host}, "api_base": server.HTTP.URL + "/api/v4", "web_base": server.HTTP.URL, "proxy_disabled": true,
		}},
	})
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "toon"} {
		var stdout, stderr bytes.Buffer
		deps := runtimepkg.Dependencies{
			Stdout: &stdout, Stderr: &stderr, Cwd: temp, ConfigPath: configPath,
			LookupEnv: func(name string) (string, bool) {
				if name == "GLAB_AXI_TOKEN" {
					return secret, true
				}
				return "", false
			},
			HTTPClient: server.HTTP.Client(),
		}
		if code := RunNative(context.Background(), []string{"auth", "status", "--host", host, "--repo", "group/project", "--format", format}, deps); code != 0 {
			t.Fatalf("format=%s exit=%d stderr=%s stdout=%s", format, code, stderr.String(), stdout.String())
		}
		if bytes.Contains(stdout.Bytes(), []byte(secret)) || bytes.Contains(stderr.Bytes(), []byte(secret)) || !bytes.Contains(stdout.Bytes(), []byte("glab-axi/v1")) {
			t.Fatalf("format=%s violated structured output boundary", format)
		}
	}
}

func TestTokenImportRefusesTerminalBeforeNetwork(t *testing.T) {
	server := testgitlab.New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("terminal token import made a network request")
	}))
	defer server.Close()
	temp := t.TempDir()
	host := strings.TrimPrefix(server.HTTP.URL, "https://")
	var stdout, stderr bytes.Buffer
	deps := runtimepkg.Dependencies{
		Stdin:      strings.NewReader(generatedToken()),
		Stdout:     &stdout,
		Stderr:     &stderr,
		Cwd:        temp,
		ConfigPath: filepath.Join(temp, "config.json"),
		Keyring:    &memoryKeyring{},
		HTTPClient: server.HTTP.Client(),
		IsTerminal: func() bool { return true },
	}
	code := RunNative(context.Background(), []string{"auth", "import", "--host", host, "--api-base", server.HTTP.URL + "/api/v4", "--web-base", server.HTTP.URL, "--token-stdin", "--format", "json"}, deps)
	if code != 9 || len(server.Requests()) != 0 {
		t.Fatalf("exit=%d requests=%d", code, len(server.Requests()))
	}
}

func TestPrivateInputFilesRejectSymlinksAndBroadPermissions(t *testing.T) {
	temp := t.TempDir()
	private := filepath.Join(temp, "private")
	if err := os.WriteFile(private, []byte("title\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readPrivateFile(private, 32, true)
	if err != nil || value != "title" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	broad := filepath.Join(temp, "broad")
	if err := os.WriteFile(broad, []byte("title"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(broad, 32, true); err == nil {
		t.Fatal("broad input permissions were accepted")
	}
	link := filepath.Join(temp, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(link, 32, true); err == nil {
		t.Fatal("symlink input was accepted")
	}
}

func TestNativeForbiddenCommandDoesNotResolveCredentials(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lookup := func(string) (string, bool) {
		t.Fatal("forbidden command attempted credential resolution")
		return "", false
	}
	deps := runtimepkg.Dependencies{Stdout: &stdout, Stderr: &stderr, LookupEnv: lookup}
	if code := RunNative(context.Background(), []string{"mr", "merge", "1", "--format", "json"}, deps); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope["ok"] != false {
		t.Fatalf("invalid structured error: %v", err)
	}
}

func serverURLFromRequest(r *http.Request) string {
	return "https://" + r.Host
}

func generatedToken() string {
	return strings.Join([]string{"glpat", "runtime", "stdin", "sentinel"}, "-")
}
