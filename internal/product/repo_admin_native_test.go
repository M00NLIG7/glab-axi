package product

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
)

// These tests specify the public feature integration with the shared explicit
// native selector. They deliberately do not provide a substitute native client,
// auth selector, credential resolver, or official-profile fallback.

type adminNativeKeyring struct {
	gets  int
	value string
	err   error
}

func (k *adminNativeKeyring) Get(context.Context, string, string) (string, error) {
	k.gets++
	return k.value, k.err
}
func (*adminNativeKeyring) Set(context.Context, string, string, string) error {
	panic("project administration must not persist credentials")
}
func (*adminNativeKeyring) Delete(context.Context, string, string) error {
	panic("project administration must not delete credentials")
}

type adminNativeWire struct {
	Method            string
	Path              string
	CredentialMatches bool
	Body              map[string]any
}
type adminNativeFixture struct {
	t                  *testing.T
	mu                 sync.Mutex
	server             *httptest.Server
	foreign            *httptest.Server
	path               string
	token              string
	otherToken         string
	action             string
	before             adminProviderProject
	after              adminProviderProject
	wires              []adminNativeWire
	foreignRequests    int
	unexpectedRequests int
	mutations          int
	applied            bool
	redirectCode       int
	crossOrigin        bool
	redirectPreflight  bool
	mutationStatus     int
	wrongAccount       bool
}

func newAdminNativeFixture(t *testing.T, action string) *adminNativeFixture {
	t.Helper()
	f := &adminNativeFixture{t: t, action: action, token: strings.Join([]string{"synthetic", "native", "admin", "one"}, "-"), otherToken: strings.Join([]string{"synthetic", "native", "admin", "two"}, "-"), path: "/gitlab/api/v4"}
	f.before = adminTestProject("team/sub/project", 101)
	if action == "fork" {
		f.after = adminTestForkState().after
		f.after.ImportStatus = "scheduled"
	} else {
		f.after = f.before
		if action == "create" {
			f.after.ID = 102
			f.after.Description = ""
		} else {
			f.after.Description = "new"
		}
	}
	// Native authority mapping is intentionally different from the logical host
	// and includes a web prefix. No request should ever go to this web origin.
	for _, p := range []*adminProviderProject{&f.before, &f.after} {
		p.URL = "https://web.gitlab.example.invalid/root/" + p.Path
	}
	if f.after.ForkedFrom != nil {
		f.after.ForkedFrom.URL = f.before.URL
	}
	f.foreign = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.foreignRequests++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":999}`)
	}))
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	// Both synthetic origins must be trusted: refusal must be the redirect
	// policy, not an accidental certificate failure at the second origin.
	if !bytes.Equal(f.server.Certificate().Raw, f.foreign.Certificate().Raw) {
		t.Fatal("fixture origins do not share the httptest CA")
	}
	t.Cleanup(f.server.Close)
	t.Cleanup(f.foreign.Close)
	return f
}
func (f *adminNativeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wire := adminNativeWire{Method: r.Method, Path: r.URL.EscapedPath(), CredentialMatches: r.Header.Get("PRIVATE-TOKEN") == f.token && r.Header.Get("Authorization") == ""}
	if r.Method != "GET" {
		defer r.Body.Close()
		decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
		if err := decoder.Decode(&wire.Body); err != nil {
			f.t.Errorf("mutation JSON: %v", err)
		}
	}
	f.wires = append(f.wires, wire)
	w.Header().Set("Content-Type", "application/json")
	if !wire.CredentialMatches {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	redirect := func() {
		base := f.server.URL
		if f.crossOrigin {
			base = f.foreign.URL
		}
		w.Header().Set("Location", base+f.path+"/projects/999")
		w.WriteHeader(f.redirectCode)
	}
	if r.Method == "GET" {
		switch wire.Path {
		case f.path + "/user":
			if f.redirectPreflight {
				redirect()
				return
			}
			user := adminAccount{ID: 7, Username: "tester"}
			if f.wrongAccount {
				user.ID = 8
			}
			_ = json.NewEncoder(w).Encode(user)
		case f.path + "/namespaces/21":
			_ = json.NewEncoder(w).Encode(f.before.Namespace)
		case f.path + "/projects/" + url.PathEscape(f.before.Path), f.path + "/projects/team%2Fsub%2Ffork":
			if f.applied {
				_, _ = w.Write(adminTestBody(f.after))
				return
			}
			if f.action == "create" || strings.HasSuffix(wire.Path, "%2Ffork") {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(adminTestBody(f.before))
		default:
			f.unexpectedRequests++
			w.WriteHeader(400)
		}
		return
	}
	method, path := "POST", f.path+"/projects"
	if f.action == "edit" {
		method, path = "PUT", f.path+"/projects/101"
	}
	if f.action == "fork" {
		path = f.path + "/projects/101/fork"
	}
	if wire.Method != method || wire.Path != path {
		f.unexpectedRequests++
		w.WriteHeader(400)
		return
	}
	f.mutations++
	if f.redirectCode != 0 {
		redirect()
		return
	}
	f.applied = true
	if f.mutationStatus != 0 {
		w.WriteHeader(f.mutationStatus)
		return
	}
	_, _ = w.Write(adminTestBody(f.after))
}
func (f *adminNativeFixture) args(t *testing.T) []string {
	t.Helper()
	args := adminTestArgs(f.action)
	if f.action == "edit" {
		args = adminEditArgs(t, f.before, "--description-file", adminTestFile(t, []byte("new")))
	}
	return args
}
func (f *adminNativeFixture) deps(t *testing.T) (*bytes.Buffer, *bytes.Buffer, Dependencies) {
	t.Helper()
	stdout, stderr, deps := productTestDeps(t, nil)
	deps.NewDelegate = func() delegateClient { t.Fatal("native operation constructed an official-glab delegate"); return nil }
	cfg := config.New()
	cfg.Hosts["gitlab.example.invalid"] = config.Host{GitHosts: []string{"gitlab.example.invalid"}, APIBase: f.server.URL + f.path, WebBase: "https://web.gitlab.example.invalid/root", ProxyDisabled: true}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	deps.Runtime.ConfigPath = path
	deps.Runtime.HTTPClient = f.server.Client()
	deps.Runtime.LookupEnv = func(name string) (string, bool) {
		if name != "GL_AXI_TOKEN" {
			return "", false
		}
		return f.token, true
	}
	deps.Runtime.Keyring = &adminNativeKeyring{err: auth.ErrKeyringUnavailable}
	return stdout, stderr, deps
}
func (f *adminNativeFixture) verify(t *testing.T, writes int, stdout, stderr string) []adminNativeWire {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mutations != writes || f.foreignRequests != 0 || f.unexpectedRequests != 0 {
		t.Fatalf("mutations=%d want=%d foreign=%d unapproved_path=%d", f.mutations, writes, f.foreignRequests, f.unexpectedRequests)
	}
	for _, wire := range f.wires {
		if !wire.CredentialMatches {
			t.Fatal("a request changed credential during the full operation")
		}
	}
	if strings.Contains(stdout, f.token) || strings.Contains(stderr, f.token) || strings.Contains(stdout, f.otherToken) || strings.Contains(stderr, f.otherToken) {
		t.Fatal("synthetic credential appeared in output")
	}
	return append([]adminNativeWire(nil), f.wires...)
}

func TestRepoAdminNativeFullOperationUsesOneCredentialAndMappedAuthority(t *testing.T) {
	for _, action := range []string{"create", "edit", "fork"} {
		for _, source := range []string{"env", "keyring"} {
			t.Run(action+"/"+source, func(t *testing.T) {
				f := newAdminNativeFixture(t, action)
				stdout, stderr, deps := f.deps(t)
				keyring := &adminNativeKeyring{value: f.token}
				deps.Runtime.Keyring = keyring
				deps.Runtime.LookupEnv = func(name string) (string, bool) {
					if source != "env" || name != "GL_AXI_TOKEN" {
						return "", false
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if len(f.wires) > 0 {
						return f.otherToken, true
					}
					return f.token, true
				}
				code := Run(context.Background(), f.args(t), deps)
				if code != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				wires := f.verify(t, 1, stdout.String(), stderr.String())
				users, namespaces, postReads := 0, 0, 0
				written := false
				for _, wire := range wires {
					switch wire.Path {
					case f.path + "/user":
						users++
					case f.path + "/namespaces/21":
						namespaces++
					}
					if wire.Method != "GET" {
						written = true
					} else if written {
						postReads++
					}
				}
				if users != 2 || namespaces != 2 || postReads != 1 {
					t.Fatalf("incomplete native sequence: users=%d namespaces=%d postReads=%d", users, namespaces, postReads)
				}
				wantGets := 0
				if source == "keyring" {
					wantGets = 1
				}
				if keyring.gets != wantGets {
					t.Fatalf("credential resolved %d times want %d", keyring.gets, wantGets)
				}
				var envelope struct {
					Data adminOutput `json:"data"`
					Meta struct {
						Backend, Host   string
						UpstreamVersion string `json:"upstream_version"`
						Complete        bool   `json:"complete"`
					} `json:"meta"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Meta.Backend != "native" || envelope.Meta.Host != "gitlab.example.invalid" || envelope.Meta.UpstreamVersion != "" || envelope.Data.Administration.Project == nil || envelope.Data.Administration.Project.URL != f.after.URL {
					t.Fatalf("wrong authority/backend: %s", stdout.String())
				}
				if action == "fork" && (envelope.Meta.Complete || envelope.Data.Administration.Outcome != "accepted") {
					t.Fatal("native fork acceptance was treated as ready")
				}
			})
		}
	}
}

func TestRepoAdminNativeSnapshotUsesConfiguredWebBase(t *testing.T) {
	f := newAdminNativeFixture(t, "edit")
	stdout, stderr, deps := f.deps(t)
	args := []string{"repo", "view", "-R", "team/sub/project", "--hostname", "gitlab.example.invalid", "--admin-snapshot", "--auth-source", "native", "--format", "json"}
	code := Run(context.Background(), args, deps)
	if code != 0 {
		t.Fatalf("snapshot exit=%d %s", code, stdout.String())
	}
	wires := f.verify(t, 0, stdout.String(), stderr.String())
	if len(wires) != 1 || wires[0].Path != f.path+"/projects/team%2Fsub%2Fproject" {
		t.Fatalf("snapshot requests=%v", wires)
	}
	var result struct {
		Data struct {
			Snapshot json.RawMessage `json:"admin_snapshot"`
		} `json:"data"`
		Meta struct {
			Backend string `json:"backend"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	snapshot, err := decodeAdminProject(result.Data.Snapshot, true)
	if err != nil || snapshot.adminProject != f.before.adminProject || result.Meta.Backend != "native" {
		t.Fatalf("snapshot lost native identity: error=%v %s", err, stdout.String())
	}
}

func TestRepoAdminNativeRedirectsPreventEveryUnapprovedSecondRequest(t *testing.T) {
	for _, action := range []string{"create", "edit", "fork"} {
		for _, status := range []int{301, 302, 307, 308} {
			for _, crossOrigin := range []bool{false, true} {
				name := action + "/" + http.StatusText(status)
				if crossOrigin {
					name += "/foreign"
				} else {
					name += "/other-project"
				}
				t.Run(name, func(t *testing.T) {
					f := newAdminNativeFixture(t, action)
					f.redirectCode = status
					f.crossOrigin = crossOrigin
					stdout, stderr, deps := f.deps(t)
					code := Run(context.Background(), f.args(t), deps)
					if code == 0 {
						t.Fatalf("redirect mutation claimed success: %s", stdout.String())
					}
					wires := f.verify(t, 1, stdout.String(), stderr.String())
					if len(wires) < 7 {
						t.Fatalf("native full operation was never attempted: exit=%d stdout=%s", code, stdout.String())
					}
				})
			}
		}
	}
	t.Run("preflight", func(t *testing.T) {
		f := newAdminNativeFixture(t, "create")
		f.redirectCode = 302
		f.crossOrigin = true
		f.redirectPreflight = true
		stdout, stderr, deps := f.deps(t)
		code := Run(context.Background(), f.args(t), deps)
		wires := f.verify(t, 0, stdout.String(), stderr.String())
		if code == 0 || len(wires) != 1 {
			t.Fatalf("preflight redirect: exit=%d requests=%d %s", code, len(wires), stdout.String())
		}
	})
}

func TestRepoAdminNativeUncertainWritesReconcileWithoutRetry(t *testing.T) {
	for _, action := range []string{"create", "edit", "fork"} {
		t.Run(action, func(t *testing.T) {
			f := newAdminNativeFixture(t, action)
			f.mutationStatus = 500
			stdout, stderr, deps := f.deps(t)
			code := Run(context.Background(), f.args(t), deps)
			f.verify(t, 1, stdout.String(), stderr.String())
			var envelope struct {
				Data  adminOutput `json:"data"`
				Error struct {
					Receipt adminOutput `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if action == "edit" {
				if code != 0 || !envelope.Data.Administration.Reconciled {
					t.Fatalf("edit not reconciled: %d %s", code, stdout.String())
				}
			} else if code != 6 || envelope.Error.Receipt.Administration.Outcome != "ambiguous" {
				t.Fatalf("uncertain creation misattributed: %d %s", code, stdout.String())
			}
		})
	}
}

func TestRepoAdminNativeSelectorAndUnavailableCredentialsNeverFallback(t *testing.T) {
	for _, scenario := range []string{"missing-selector", "unknown-selector", "official-alias", "duplicate-selector", "missing-host", "missing-repo", "native-unavailable", "env-ambiguity", "wrong-account"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAdminNativeFixture(t, "create")
			stdout, stderr, deps := f.deps(t)
			args := f.args(t)
			keyring := &adminNativeKeyring{err: auth.ErrKeyringUnavailable}
			deps.Runtime.Keyring = keyring
			switch scenario {
			case "missing-selector":
				args = args[:len(args)-2]
			case "unknown-selector":
				args[len(args)-1] = "auto"
			case "official-alias":
				args[len(args)-1] = "official"
			case "duplicate-selector":
				args = append(args, "--auth-source=native")
			case "missing-host", "missing-repo":
				flag := "--hostname"
				if scenario == "missing-repo" {
					flag = "-R"
				}
				for i, arg := range args {
					if arg == flag {
						args = append(args[:i], args[i+2:]...)
						break
					}
				}
			case "native-unavailable":
				deps.Runtime.LookupEnv = func(string) (string, bool) { return "", false }
			case "env-ambiguity":
				deps.Runtime.LookupEnv = func(name string) (string, bool) {
					if name == "GL_AXI_TOKEN" {
						return f.token, true
					}
					if name == "GITLAB_TOKEN" {
						return f.otherToken, true
					}
					return "", false
				}
			case "wrong-account":
				f.wrongAccount = true
			}
			code := Run(context.Background(), args, deps)
			if code == 0 {
				t.Fatal("unsafe selector/credential accepted")
			}
			wires := f.verify(t, 0, stdout.String(), stderr.String())
			wantRequests := 0
			if scenario == "wrong-account" {
				wantRequests = 1
			}
			if len(wires) != wantRequests {
				t.Fatalf("exit=%d requests=%d want=%d %s", code, len(wires), wantRequests, stdout.String())
			}
			wantGets := 0
			if scenario == "native-unavailable" {
				wantGets = 1
			}
			if keyring.gets != wantGets {
				t.Fatalf("keyring gets=%d want=%d stdout=%s", keyring.gets, wantGets, stdout.String())
			}
			if scenario == "native-unavailable" || scenario == "env-ambiguity" {
				if code != 3 {
					t.Fatalf("credential error=%d want=3 %s", code, stdout.String())
				}
			}
		})
	}
}

func TestRepoAdminNativeExecutableAliasesTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no-child fixture uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for _, scenario := range []struct {
				action   string
				redirect int
			}{{"create", 0}, {"edit", 0}, {"fork", 0}, {"create", 302}, {"edit", 301}, {"fork", 307}} {
				action, name := scenario.action, scenario.action
				if scenario.redirect != 0 {
					name += "/redirect"
				}
				t.Run(name, func(t *testing.T) {
					f := newAdminNativeFixture(t, action)
					f.redirectCode, f.crossOrigin = scenario.redirect, action != "edit"
					_, _, deps := f.deps(t)
					cfg, err := config.Load(deps.Runtime.ConfigPath)
					if err != nil {
						t.Fatal(err)
					}
					host := cfg.Hosts["gitlab.example.invalid"]
					host.CABundle = adminTestFile(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}))
					cfg.Hosts["gitlab.example.invalid"] = host
					if err := config.Save(deps.Runtime.ConfigPath, cfg); err != nil {
						t.Fatal(err)
					}
					home := t.TempDir()
					marker := filepath.Join(home, "child-attempt")
					if err := os.WriteFile(filepath.Join(home, "glab"), []byte("#!/bin/sh\nprintf child > \"$GL_AXI_NATIVE_CHILD_MARKER\"\nexit 1\n"), 0700); err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(binary, f.args(t)...)
					cmd.Dir = home
					cmd.Env = []string{"PATH=" + home + ":/usr/bin:/bin", "HOME=" + home, "GL_AXI_CONFIG=" + deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + f.token, "GL_AXI_NATIVE_CHILD_MARKER=" + marker}
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					runErr := cmd.Run()
					f.verify(t, 1, stdout.String(), stderr.String())
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("native CLI attempted an official-glab child")
					}
					if scenario.redirect != 0 {
						exit, ok := runErr.(*exec.ExitError)
						if !ok || (exit.ExitCode() != 6 && exit.ExitCode() != 9) {
							t.Fatalf("redirect run=%v stdout=%s", runErr, stdout.String())
						}
						return
					}
					if runErr != nil {
						t.Fatalf("run: %v stdout=%s stderr=%s", runErr, stdout.String(), stderr.String())
					}
					var result struct {
						Data adminOutput `json:"data"`
						Meta struct {
							Backend  string `json:"backend"`
							Complete bool   `json:"complete"`
						} `json:"meta"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Meta.Backend != "native" || result.Data.Administration.Project == nil || result.Data.Administration.Project.URL != f.after.URL {
						t.Fatalf("native authority receipt: %s", stdout.String())
					}
					if action == "fork" && (result.Meta.Complete || result.Data.Administration.Outcome != "accepted") {
						t.Fatal("asynchronous native fork claimed readiness")
					}
				})
			}
		})
	}
}
