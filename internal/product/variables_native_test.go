// Feature integration regressions against the landed product-native boundary.
// All authentication and HTTP behavior use the shared production implementation.
package product

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/testgitlab"
)

type nativeVariableTestKeyring struct {
	gets  int
	value string
}

func (k *nativeVariableTestKeyring) Get(context.Context, string, string) (string, error) {
	k.gets++
	if k.value == "" {
		return "", auth.ErrKeyringNotFound
	}
	return k.value, nil
}
func (*nativeVariableTestKeyring) Set(context.Context, string, string, string) error {
	return fmt.Errorf("unexpected credential write")
}
func (*nativeVariableTestKeyring) Delete(context.Context, string, string) error {
	return fmt.Errorf("unexpected credential deletion")
}

type nativeVariableFixture struct {
	server                                                 *testgitlab.Server
	configPath                                             string
	host, web, token, oldValue, newValue, oldFile, newFile string
	mu                                                     sync.Mutex
	state                                                  []map[string]any
	writes                                                 int
	mode                                                   string
	redirectCode                                           int
	redirectURL                                            string
	cancelOnWrite                                          context.CancelFunc
	inventoryReads                                         int
	projectReads                                           int
	driftField                                             string
	driftValue                                             any
}

// Fixture setup uses only the already-shipped native config and TLS test
// server. It supplies no auth resolver, HTTP transport, or selection parser.
func newNativeVariableFixture(t *testing.T, class, mode string, mappedWeb bool) *nativeVariableFixture {
	t.Helper()
	dir := t.TempDir()
	// Native config requires its containing directory to be explicitly private;
	// do not assume testing.TempDir's child-directory mode satisfies that guard.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &nativeVariableFixture{host: "gitlab.com", web: "https://gitlab.com", mode: mode}
	if mappedWeb {
		f.host = "gitlab.private.example"
		f.web = "https://web.private.example/gitlab"
	}
	f.token = strings.Join([]string{"synthetic", "native", "credential"}, "_")
	f.oldValue = strings.Join([]string{"synthetic", "private", "previous"}, "_")
	f.newValue = strings.Join([]string{"synthetic", "private", "desired"}, "_")
	f.oldFile = filepath.Join(dir, "previous")
	f.newFile = filepath.Join(dir, "desired")
	for path, value := range map[string]string{f.oldFile: f.oldValue, f.newFile: f.newValue} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.state = []map[string]any{variableRecord("KEY", "*", "hidden", true, f.oldValue), variableRecord("KEY", "review/*", "masked", false, f.oldValue)}
	if class != "absent" {
		f.state = append([]map[string]any{variableRecord("KEY", "production", class, false, f.oldValue)}, f.state...)
	}
	f.server = testgitlab.New(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	ca, err := f.server.CAFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	if err := cfg.Put(f.host, config.Host{GitHosts: []string{f.host}, APIBase: f.server.HTTP.URL + "/api/v4", WebBase: f.web, CABundle: ca, ProxyDisabled: true}); err != nil {
		t.Fatal(err)
	}
	f.configPath = filepath.Join(dir, "config.json")
	if err := config.Save(f.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *nativeVariableFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("PRIVATE-TOKEN") != f.token {
		http.Error(w, "wrong synthetic credential", 401)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/version":
		version := "17.6.0"
		if f.mode == "old-version" {
			version = "17.5.9"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"version": version})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/group/project":
		f.projectReads++
		id := 101
		web := f.web + "/group/project"
		if f.mode == "wrong-project" || f.mode == "project-drift" && f.projectReads > 1 || f.mode == "post-project-drift" && f.writes > 0 {
			id = 102
		}
		if f.mode == "wrong-host" {
			web = "https://wrong.example/group/project"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "path_with_namespace": "group/project", "web_url": web})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/101/variables":
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 || page > 10 || r.URL.Query().Get("per_page") != "100" {
			http.Error(w, "wrong pagination", 400)
			return
		}
		f.inventoryReads++
		if f.mode == "post-read-failure" && f.writes > 0 {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": f.oldValue})
			return
		}
		if f.mode == "drift" && f.inventoryReads > 1 {
			for _, record := range f.state {
				if record["key"] == "KEY" && record["environment_scope"] == "production" {
					record["value"] = f.newValue
				}
			}
		}
		if f.mode == "metadata-drift" && f.inventoryReads > 1 || f.mode == "post-metadata-drift" && f.writes > 0 {
			for _, record := range f.state {
				if record["key"] == "KEY" && record["environment_scope"] == "production" {
					record[f.driftField] = f.driftValue
				}
			}
		}
		if f.mode == "page-limit" || f.mode == "two-pages" || f.mode == "operation-limit" {
			count := 100
			if f.mode == "two-pages" && page == 2 {
				count = 1
			}
			items := make([]map[string]any, 0, count)
			for i := 0; i < count; i++ {
				item := variableWire(variableRecord("KEY"+strconv.Itoa((page-1)*100+i), "production", "hidden", false, f.oldValue))
				if f.mode == "operation-limit" {
					item["description"] = strings.Repeat("x", 18000)
				}
				items = append(items, item)
			}
			_ = json.NewEncoder(w).Encode(items)
			return
		}
		if f.mode == "oversized" {
			fmt.Fprint(w, strings.Repeat(" ", (2<<20)+1))
			return
		}
		if f.mode == "malformed" {
			fmt.Fprint(w, f.oldValue)
			return
		}
		if f.mode == "missing-hidden" {
			for _, o := range f.state {
				delete(o, "hidden")
			}
		}
		items := make([]map[string]any, 0, len(f.state))
		for _, record := range f.state {
			items = append(items, variableWire(record))
		}
		if f.mode == "duplicate" {
			items = append(items, items[0])
		}
		_ = json.NewEncoder(w).Encode(items)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/101/variables", (r.Method == http.MethodPut || r.Method == http.MethodDelete) && r.URL.Path == "/api/v4/projects/101/variables/KEY":
		f.writes++
		if f.cancelOnWrite != nil {
			f.cancelOnWrite()
			return
		}
		if r.Method != http.MethodPost && r.URL.Query().Get("filter[environment_scope]") != "production" {
			http.Error(w, "wrong exact scope", 400)
			return
		}
		if f.redirectCode != 0 {
			w.Header().Set("Location", f.redirectURL)
			w.WriteHeader(f.redirectCode)
			return
		}
		if f.mode == "rejected" || f.mode == "uncertain" {
			status := 403
			if f.mode == "uncertain" {
				status = 500
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "glpat-" + f.newValue})
			return
		}
		if f.mode == "unapplied-success" {
			status := http.StatusNoContent
			if r.Method == http.MethodPost {
				status = http.StatusCreated
			} else if r.Method == http.MethodPut {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			return
		}
		index := -1
		for i, o := range f.state {
			if o["key"] == "KEY" && o["environment_scope"] == "production" {
				index = i
			}
		}
		if r.Method == http.MethodDelete {
			if index >= 0 {
				f.state = append(f.state[:index], f.state[index+1:]...)
			}
		} else {
			var payload map[string]any
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&payload) != nil || payload["value"] != f.newValue {
				http.Error(w, "invalid private payload", 400)
				return
			}
			if r.Method == http.MethodPost {
				if index >= 0 || payload["key"] != "KEY" || payload["environment_scope"] != "production" {
					http.Error(w, "wrong create identity", 409)
					return
				}
				payload["hidden"] = payload["masked_and_hidden"]
				delete(payload, "masked_and_hidden")
				f.state = append(f.state, payload)
				index = len(f.state) - 1
			} else {
				if index < 0 {
					http.Error(w, "missing exact identity", 404)
					return
				}
				for k, v := range payload {
					f.state[index][k] = v
				}
			}
		}
		if f.mode == "wrong-poststate" && index >= 0 && r.Method != http.MethodDelete {
			f.state[index]["hidden"] = false
		}
		if f.mode == "applied-error" {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": f.newValue})
			return
		}
		if f.mode == "lost-response" {
			panic(http.ErrAbortHandler)
		}
		if f.mode == "unexpected-status" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if f.mode == "truncated-response" {
			w.Header().Set("Content-Length", "100")
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			}
			fmt.Fprint(w, "{")
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(variableWire(f.state[index]))
	default:
		http.Error(w, "unexpected fixture route", 404)
	}
}
func (f *nativeVariableFixture) args(group, action, class, format string) []string {
	if action == "list" {
		return []string{group, action, "-R", "group/project", "--hostname", f.host, "--scope", "production", "--auth-source", "native", "--format", format}
	}
	args := variableArgs(action, class)
	args[0] = group
	for flag, value := range map[string]string{"--hostname": f.host, "--expected-project-url": f.web + "/group/project", "--expected-value-file": f.oldFile, "--value-file": f.newFile, "--format": format} {
		if flag == "--expected-value-file" && (class == "absent" || class == "hidden") || flag == "--value-file" && action != "set" {
			continue
		}
		args = replaceVariableArg(args, flag, value)
	}
	return append(args, "--auth-source", "native")
}
func (f *nativeVariableFixture) deps(t *testing.T, fromKeyring bool) (*bytes.Buffer, *bytes.Buffer, Dependencies, *nativeVariableTestKeyring, *int) {
	t.Helper()
	stdout, stderr, deps := productTestDeps(t, nil)
	keyring := &nativeVariableTestKeyring{}
	if fromKeyring {
		keyring.value = f.token
	}
	selectedLookups := 0
	deps.Runtime.ConfigPath = f.configPath
	deps.Runtime.HTTPClient = f.server.HTTP.Client()
	deps.Runtime.Keyring = keyring
	deps.Runtime.LookupEnv = func(name string) (string, bool) {
		if name == "GL_AXI_TOKEN" && !fromKeyring {
			selectedLookups++
			if selectedLookups == 1 {
				return f.token, true
			}
			return f.token + "_different_account", true
		}
		return "", false
	}
	deps.NewDelegate = func() delegateClient { t.Fatal("native feature constructed an official-glab delegate"); return nil }
	return stdout, stderr, deps, keyring, &selectedLookups
}
func (f *nativeVariableFixture) assertConfidential(t *testing.T, texts ...string) {
	t.Helper()
	for _, text := range texts {
		for _, value := range []string{f.token, f.oldValue, f.newValue} {
			if strings.Contains(text, value) || strings.Contains(text, fmt.Sprintf("%x", sha256.Sum256([]byte(value)))) {
				t.Fatal("synthetic credential or private value escaped native output")
			}
		}
	}
	for _, r := range f.server.Requests() {
		if r.Header.Get("PRIVATE-TOKEN") != f.token || strings.Contains(r.URL, f.token) || strings.Contains(r.URL, f.oldValue) || strings.Contains(r.URL, f.newValue) || bytes.Contains(r.Body, []byte(f.token)) {
			t.Fatal("full operation changed credential or leaked credential/value into a URI")
		}
	}
}

func TestNativeVariableFullOperationUsesOneSelectedIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, test := range []struct {
		group, action, class, mode string
		want                       int
	}{
		{"secret", "set", "absent", "", 0}, {"secret", "set", "hidden", "", 0}, {"secret", "delete", "hidden", "", 0},
		{"variable", "set", "ordinary", "", 0}, {"variable", "set", "absent", "", 0}, {"variable", "delete", "ordinary", "", 0},
		{"secret", "set", "hidden", "applied-error", 6}, {"secret", "delete", "hidden", "applied-error", 6},
		{"secret", "set", "hidden", "uncertain", 6}, {"secret", "delete", "hidden", "uncertain", 6},
		{"secret", "set", "hidden", "rejected", 4}, {"secret", "delete", "hidden", "rejected", 4},
		{"variable", "set", "ordinary", "lost-response", 6}, {"variable", "delete", "ordinary", "lost-response", 6},
	} {
		for _, keyring := range []bool{false, true} {
			for _, mapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-%s-%s-%s-keyring-%t-mapped-%t", test.group, test.action, test.class, test.mode, keyring, mapped), func(t *testing.T) {
					f := newNativeVariableFixture(t, test.class, test.mode, mapped)
					stdout, stderr, deps, store, lookups := f.deps(t, keyring)
					exit := Run(context.Background(), f.args(test.group, test.action, test.class, "json"), deps)
					f.assertConfidential(t, stdout.String(), stderr.String())
					if exit != test.want {
						t.Fatalf("wrong native outcome: exit=%d want=%d", exit, test.want)
					}
					f.mu.Lock()
					writes := f.writes
					f.mu.Unlock()
					if writes != 1 {
						t.Fatal("native mutation missing or retried")
					}
					if keyring && (store.gets != 1 || *lookups != 0) || !keyring && (store.gets != 0 || *lookups != 1) {
						t.Fatal("full operation resolved more than one credential")
					}
					var envelope struct {
						Meta uxv1.Meta `json:"meta"`
					}
					if json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Meta.Backend != "native" || envelope.Meta.UpstreamVersion != "" || envelope.Meta.Host != f.host {
						t.Fatal("native backend/authority misreported")
					}
					if !strings.Contains(stdout.String(), `"atomic_precondition":false`) {
						t.Fatal("native transport claimed provider CAS")
					}
					// Unselected environments retain their exact old values after all writes.
					f.mu.Lock()
					others := 0
					for _, o := range f.state {
						if o["environment_scope"] != "production" {
							others++
							if o["value"] != f.oldValue {
								t.Error("wrong-scope overwrite")
							}
						}
					}
					f.mu.Unlock()
					if others != 2 {
						t.Fatal("wrong-scope deletion")
					}
				})
			}
		}
	}
}

func TestNativeVariableInvalidSelectionAndUnavailableNeverDelegate(t *testing.T) {
	f := newNativeVariableFixture(t, "hidden", "", false)
	base := f.args("secret", "delete", "hidden", "json")
	tests := map[string][]string{
		"omitted":            base[:len(base)-2],
		"official-alias":     replaceVariableArg(base, "--auth-source", "official"),
		"duplicate":          append(append([]string(nil), base...), "--auth-source=native"),
		"missing-host":       replaceVariableArg(base, "--hostname", ""),
		"invalid-prestate":   replaceVariableArg(base, "--expected-type", "unknown"),
		"hidden-value-guard": replaceVariableArg(base, "--expected-value-file", f.oldFile),
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, deps, store, lookups := f.deps(t, false)
			if Run(context.Background(), args, deps) == 0 || store.gets != 0 || *lookups != 0 || len(f.server.Requests()) != 0 {
				t.Fatal("invalid native selector/prestate reached child, credential, or network work")
			}
			f.assertConfidential(t, stdout.String(), stderr.String())
		})
	}
	t.Run("unavailable", func(t *testing.T) {
		stdout, stderr, deps, store, _ := f.deps(t, true)
		store.value = ""
		if Run(context.Background(), base, deps) == 0 || len(f.server.Requests()) != 0 {
			t.Fatal("native credential unavailable fell back to official profile")
		}
		f.assertConfidential(t, stdout.String(), stderr.String())
	})
}

func TestNativeVariableRedirectsNeverReachAnotherTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, action := range []string{"create", "update", "delete"} {
		for _, code := range []int{301, 302, 303, 307, 308} {
			for _, cross := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-%d-cross-%t", action, code, cross), func(t *testing.T) {
					class, command := "hidden", "set"
					if action == "create" {
						class = "absent"
					}
					if action == "delete" {
						command = "delete"
					}
					f := newNativeVariableFixture(t, class, "", false)
					trap := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
					defer trap.Close()
					f.redirectCode = code
					f.redirectURL = f.server.HTTP.URL + "/api/v4/projects/999/variables"
					if cross {
						f.redirectURL = trap.HTTP.URL + "/api/v4/projects/999/variables"
					}
					stdout, stderr, deps, _, _ := f.deps(t, false)
					if Run(context.Background(), f.args("secret", command, class, "json"), deps) == 0 {
						t.Fatal("redirect became a successful variable mutation")
					}
					f.assertConfidential(t, stdout.String(), stderr.String())
					if len(trap.Requests()) != 0 {
						t.Fatal("cross-origin redirect transmitted a second request")
					}
					for _, request := range f.server.Requests() {
						if strings.Contains(request.URL, "/projects/999/") {
							t.Fatal("same-origin cross-path redirect replayed")
						}
					}
					f.mu.Lock()
					writes := f.writes
					f.mu.Unlock()
					if writes != 1 {
						t.Fatal("mutation was retried")
					}
				})
			}
		}
	}
}

func TestNativeVariableListClassAndBoundaries(t *testing.T) {
	for _, group := range []string{"secret", "variable"} {
		for _, mode := range []string{"", "duplicate", "page-limit", "oversized", "malformed", "missing-hidden"} {
			t.Run(group+"-"+mode, func(t *testing.T) {
				f := newNativeVariableFixture(t, "hidden", mode, false)
				f.state = append(f.state, variableRecord("ORDINARY", "production", "ordinary", false, f.oldValue), variableRecord("MASKED", "production", "masked", false, f.oldValue), variableRecord("PROTECTED", "production", "protected", true, f.oldValue))
				stdout, stderr, deps, store, lookups := f.deps(t, false)
				exit := Run(context.Background(), f.args(group, "list", "hidden", "json"), deps)
				f.assertConfidential(t, stdout.String(), stderr.String())
				if *lookups != 1 || store.gets != 0 {
					t.Fatal("list changed native identity")
				}
				if mode != "" {
					if exit == 0 {
						t.Fatal("incomplete/malformed sensitive inventory succeeded")
					}
					return
				}
				var result struct {
					Data struct {
						Variables       []struct{ Key, Class string }
						ValuesDisclosed bool `json:"values_disclosed"`
					} `json:"data"`
					Meta uxv1.Meta `json:"meta"`
				}
				if exit != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Data.ValuesDisclosed || result.Meta.Backend != "native" {
					t.Fatal("invalid native metadata list")
				}
				want := 3
				if group == "variable" {
					want = 1
				}
				if len(result.Data.Variables) != want {
					t.Fatal("wrong native class count")
				}
				for _, o := range result.Data.Variables {
					if (group == "variable") != (o.Class == "ordinary") {
						t.Fatal("secret class exposed through ordinary alias")
					}
				}
			})
		}
	}
}

func TestNativeVariableCompiledAliasesTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gl-axi", "glab-axi"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, name)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+name)
			build.Dir = root
			if body, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, body)
			}
			// No official executable is on PATH. Native config contains only authority
			// and the synthetic fixture CA; the credential is a runtime sentinel.
			for _, tc := range []struct{ group, action, class string }{{"secret", "list", "hidden"}, {"variable", "list", "ordinary"}, {"secret", "set", "absent"}, {"secret", "set", "hidden"}, {"variable", "set", "ordinary"}, {"secret", "delete", "hidden"}, {"variable", "delete", "ordinary"}} {
				for _, format := range []string{"json", "toon"} {
					t.Run(tc.group+"-"+tc.action+"-"+tc.class+"-"+format, func(t *testing.T) {
						f := newNativeVariableFixture(t, tc.class, "", true)
						args := f.args(tc.group, tc.action, tc.class, format)
						// Drive both descriptor-validated file and explicitly piped value input.
						if tc.action == "set" && format == "toon" {
							args = replaceVariableArg(args, "--value-file", "-")
						}
						command := exec.Command(binary, args...)
						if tc.action == "set" && format == "toon" {
							command.Stdin = strings.NewReader(f.newValue)
						}
						command.Dir = dir
						command.Env = []string{"HOME=" + dir, "PATH=" + dir, "GL_AXI_CONFIG=" + f.configPath, "GL_AXI_TOKEN=" + f.token}
						var stdout, stderr bytes.Buffer
						command.Stdout = &stdout
						command.Stderr = &stderr
						err := command.Run()
						f.assertConfidential(t, stdout.String(), stderr.String())
						if err != nil {
							t.Fatalf("compiled native CLI failed: %v", err)
						}
						if !strings.Contains(stdout.String(), "native") || strings.Contains(stdout.String(), "upstream_version") {
							t.Fatal("compiled native CLI reported delegated backend")
						}
						if tc.group == "secret" && tc.action != "list" {
							evidence := "metadata_observed"
							if tc.action == "delete" {
								evidence = "absence_observed"
							}
							if !strings.Contains(stdout.String(), "unavailable_hidden") || !strings.Contains(stdout.String(), evidence) || !strings.Contains(stdout.String(), "provider_acknowledged") {
								t.Fatal("compiled receipt omitted hidden verification limits")
							}
						}
						f.mu.Lock()
						writes := f.writes
						f.mu.Unlock()
						want := 1
						if tc.action == "list" {
							want = 0
						}
						if writes != want {
							t.Fatal("compiled native CLI performed the wrong number of mutations")
						}
					})
				}
			}
		})
	}
}
