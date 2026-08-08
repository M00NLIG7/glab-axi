package v1cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	mu        sync.Mutex
	service   string
	account   string
	secret    string
	setErr    error
	deleteErr error
}

func (m *memoryKeyring) Get(context.Context, string, string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.secret == "" {
		return "", auth.ErrKeyringNotFound
	}
	return m.secret, nil
}

func (m *memoryKeyring) Set(_ context.Context, service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	m.service, m.account, m.secret = service, account, secret
	return nil
}

func (m *memoryKeyring) Delete(context.Context, string, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.secret = ""
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

func TestTokenImportIsTransactionalAcrossKeyringAndConfig(t *testing.T) {
	secret := generatedToken()
	server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": "import-bot"})
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.HTTP.URL, "https://")
	args := []string{"auth", "import", "--host", host, "--api-base", server.HTTP.URL + "/api/v4", "--web-base", server.HTTP.URL, "--token-stdin", "--format", "json"}

	t.Run("keyring failure leaves no config", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		keyring := &memoryKeyring{setErr: errors.New("synthetic keyring failure")}
		var stdout bytes.Buffer
		deps := runtimepkg.Dependencies{
			Stdin: strings.NewReader(secret + "\n"), Stdout: &stdout, Stderr: io.Discard,
			Cwd: dir, ConfigPath: configPath, Keyring: keyring,
			HTTPClient: server.HTTP.Client(), IsTerminal: func() bool { return false },
		}
		if code := RunNative(context.Background(), args, deps); code != 3 {
			t.Fatalf("exit=%d output=%s", code, stdout.String())
		}
		if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("config exists after keyring failure: %v", err)
		}
	})

	t.Run("config failure restores previous keyring value", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(dir, "config.json")
		keyring := &memoryKeyring{secret: "previous-secure-value"}
		var stdout bytes.Buffer
		deps := runtimepkg.Dependencies{
			Stdin: strings.NewReader(secret + "\n"), Stdout: &stdout, Stderr: io.Discard,
			Cwd: dir, ConfigPath: configPath, Keyring: keyring,
			HTTPClient: server.HTTP.Client(), IsTerminal: func() bool { return false },
		}
		if code := RunNative(context.Background(), args, deps); code != 9 {
			t.Fatalf("exit=%d output=%s", code, stdout.String())
		}
		if keyring.secret != "previous-secure-value" {
			t.Fatal("previous keyring value was not restored")
		}
		if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("config exists after save failure: %v", err)
		}
	})
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

func TestFrozenV1DataSchemasAreClosed(t *testing.T) {
	for _, name := range []string{"auth-import", "auth-status", "mr-ensure", "mr-view", "ci-status", "ci-jobs", "ci-trace"} {
		path := filepath.Join("..", "..", "..", "schema", "v1", name+".schema.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var schema struct {
			ID                   string         `json:"$id"`
			Type                 string         `json:"type"`
			AdditionalProperties bool           `json:"additionalProperties"`
			Required             []string       `json:"required"`
			Properties           map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if schema.ID == "" || schema.Type != "object" || schema.AdditionalProperties || len(schema.Required) == 0 || len(schema.Properties) == 0 {
			t.Fatalf("%s is not a closed command data schema: %#v", name, schema)
		}
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
