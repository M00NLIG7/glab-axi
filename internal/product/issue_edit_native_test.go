package product

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	runtimepkg "gl-axi/internal/runtime"
)

// These integration tests use the explicit product-native selector. All
// credentials, authority mappings, and TLS servers are isolated synthetic data.
// They intentionally exercise the product interface, not a replacement resolver
// or feature-local HTTP transport.
func TestIssueEditNativeFullSequenceUsesOneCredentialAndAuthority(t *testing.T) {
	for _, selector := range [][]string{{"--auth-source", "native"}, {"--auth-source=native"}} {
		t.Run(strings.Join(selector, "="), func(t *testing.T) {
			f := newIssueEditNativeFixture(t)
			stdout, stderr, deps := f.dependencies(t)
			args := append(f.args(t), selector...)
			if code := Run(context.Background(), args, deps); code != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			var envelope struct {
				Data issueEditOutput `json:"data"`
				Meta uxv1.Meta       `json:"meta"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Data.Edit.Action != "updated" || envelope.Data.Edit.Outcome != "observed_applied" || envelope.Data.Edit.Warning != issueEditRaceWarning || envelope.Data.Edit.Identity.Host != f.host || envelope.Data.Edit.Identity.WebURL != f.before.WebURL || envelope.Meta.Backend != "native" || envelope.Meta.UpstreamVersion != "" || envelope.Meta.Host != f.host {
				t.Fatalf("unexpected native receipt: %s", stdout)
			}
			f.assertRequests(t, 8, 1)
			if f.keyring.reads != 1 {
				t.Fatalf("credential resolutions=%d", f.keyring.reads)
			}
			f.assertConfidential(t, stdout.String()+stderr.String())
		})
	}
}

func TestIssueEditNativeSelectorAndInputRefuseBeforeCredentials(t *testing.T) {
	f := newIssueEditNativeFixture(t)
	base := append(f.args(t), "--auth-source", "native")
	for name, args := range map[string][]string{
		"unsupported source": replaceArg(base, "native", "official"),
		"duplicate source":   appendCopy(base, "--auth-source=native"),
		"missing hostname":   removeFlag(base, "--hostname", true),
		"missing project":    removeFlag(base, "--repo", true),
		"wrong IID URL":      replaceArg(base, f.before.WebURL, f.webBase+"/group/project/-/issues/43"),
		"stale input shape":  replaceArg(base, issueEditTestTimestamp, "yesterday"),
		"missing title file": replaceArg(base, base[indexOfIssueEditFlag(base, "--title-file")+1], filepath.Join(t.TempDir(), "missing")),
	} {
		t.Run(name, func(t *testing.T) {
			stdout, _, deps := f.dependencies(t)
			deps.Runtime.Keyring = &issueEditNativeKeyring{failRead: true, t: t}
			deps.Runtime.LookupEnv = func(name string) (string, bool) {
				if strings.Contains(name, "TOKEN") {
					t.Fatalf("invalid input accessed %s", name)
				}
				return "", false
			}
			if code := Run(context.Background(), args, deps); code == 0 {
				t.Fatalf("unsafe success: %s", stdout)
			}
			f.assertRequests(t, 0, 0)
		})
	}
}

func TestIssueEditNativeUnavailableNeverFallsBack(t *testing.T) {
	for _, mode := range []string{"credential unavailable", "host unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			f := newIssueEditNativeFixture(t)
			stdout, _, deps := f.dependencies(t)
			if mode == "credential unavailable" {
				f.keyring.err = auth.ErrKeyringUnavailable
			} else {
				deps.Runtime.ConfigPath = filepath.Join(t.TempDir(), "absent-config")
			}
			if code := Run(context.Background(), append(f.args(t), "--auth-source", "native"), deps); code == 0 {
				t.Fatalf("unavailable native succeeded: %s", stdout)
			}
			f.assertRequests(t, 0, 0)
		})
	}
}

func TestIssueEditNativeDefaultRemainsNonMutating(t *testing.T) {
	// Without the selector this remains the original delegated preview/no-op
	// lane; an actual edit must ask for deliberate native selection, not switch
	// credentials automatically or issue the unsafe delegated PUT.
	before := issueEditFixture()
	delegate := issueEditDelegate(before, before, nil)
	stdout, _, deps := productTestDeps(t, delegate)
	deps.Runtime.Keyring = &issueEditNativeKeyring{t: t, failRead: true}
	if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code == 0 {
		t.Fatalf("unselected native write succeeded: %s", stdout)
	}
	assertIssueEditNoMutation(t, delegate)
}

func TestIssueEditNativePrestateAndNoOp(t *testing.T) {
	for _, mode := range []string{"preview", "noop", "wrong issue", "stale", "drift", "label reused"} {
		t.Run(mode, func(t *testing.T) {
			f := newIssueEditNativeFixture(t)
			f.mode = mode
			args := append(f.args(t), "--auth-source", "native")
			if mode == "preview" {
				args = append(args, "--dry-run")
			}
			if mode == "noop" {
				args = replaceArg(args, "triage", "keep")
				if err := os.WriteFile(args[indexOfIssueEditFlag(args, "--title-file")+1], []byte(f.before.Title), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stdout, _, deps := f.dependencies(t)
			code := Run(context.Background(), args, deps)
			if (mode == "preview" || mode == "noop") != (code == 0) {
				t.Fatalf("exit=%d output=%s", code, stdout)
			}
			for _, record := range f.snapshot() {
				if record.method != "GET" {
					t.Fatalf("prestate/preview mutated: %#v", record)
				}
			}
			f.assertConfidential(t, stdout.String())
		})
	}
}

func TestIssueEditNativeAmbiguousTransportReconcilesWithoutRetry(t *testing.T) {
	for _, mode := range []string{"lost", "server failure", "malformed", "unapplied", "wrong response", "read failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newIssueEditNativeFixture(t)
			f.mode = mode
			stdout, _, deps := f.dependencies(t)
			code := Run(context.Background(), append(f.args(t), "--auth-source", "native"), deps)
			if mode == "lost" || mode == "server failure" || mode == "malformed" {
				if code != 0 || !strings.Contains(stdout.String(), `"action":"reconciled_update"`) {
					t.Fatalf("exit=%d output=%s", code, stdout)
				}
			} else {
				assertIssueEditAmbiguous(t, code, stdout.Bytes())
			}
			f.assertRequests(t, -1, 1)
			if f.keyring.reads != 1 {
				t.Fatalf("credentials re-resolved: %d", f.keyring.reads)
			}
			f.assertConfidential(t, stdout.String())
		})
	}
}

func TestIssueEditNativeRedirectNeverSendsSecondRequest(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, crossOrigin := range []bool{false, true} {
			t.Run(http.StatusText(status)+map[bool]string{false: "/same-origin", true: "/cross-origin"}[crossOrigin], func(t *testing.T) {
				f := newIssueEditNativeFixture(t)
				f.mode, f.redirectStatus = "redirect", status
				var otherRequests atomic.Int32
				other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					otherRequests.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				}))
				defer other.Close()
				f.redirectURL = f.server.URL + "/gitlab/api/v4/unapproved-path"
				if crossOrigin {
					f.redirectURL = other.URL + "/unapproved-path"
				}
				stdout, _, deps := f.dependencies(t)
				// Trust both synthetic peers so a follow-up cannot be hidden by a
				// TLS trust failure. No InsecureSkipVerify or real CA is involved.
				transport := deps.Runtime.HTTPClient.Transport.(*http.Transport).Clone()
				transport.TLSClientConfig = transport.TLSClientConfig.Clone()
				transport.TLSClientConfig.RootCAs = transport.TLSClientConfig.RootCAs.Clone()
				transport.TLSClientConfig.RootCAs.AddCert(other.Certificate())
				deps.Runtime.HTTPClient = &http.Client{Transport: transport}
				defer transport.CloseIdleConnections()
				code := Run(context.Background(), append(f.args(t), "--auth-source", "native"), deps)
				assertIssueEditAmbiguous(t, code, stdout.Bytes())
				f.assertRequests(t, -1, 1)
				for _, record := range f.snapshot() {
					if strings.Contains(record.path, "unapproved") {
						t.Fatalf("followed redirect: %#v", record)
					}
				}
				if otherRequests.Load() != 0 {
					t.Fatal("cross-origin request escaped selected authority")
				}
				f.assertConfidential(t, stdout.String())
			})
		}
	}
}

type issueEditNativeKeyring struct {
	t                       *testing.T
	service, account, token string
	reads                   int
	failRead                bool
	err                     error
}

func (k *issueEditNativeKeyring) Get(_ context.Context, service, account string) (string, error) {
	k.reads++
	if k.failRead {
		k.t.Fatal("unexpected keyring access")
	}
	if service != k.service || account != k.account {
		return "", errors.New("wrong synthetic keyring identity")
	}
	if k.reads > 1 {
		return "", errors.New("credential resolved more than once")
	}
	return k.token, k.err
}
func (k *issueEditNativeKeyring) Set(context.Context, string, string, string) error {
	k.t.Fatal("unexpected keyring write")
	return nil
}
func (k *issueEditNativeKeyring) Delete(context.Context, string, string) error {
	k.t.Fatal("unexpected keyring deletion")
	return nil
}

type issueEditNativeRequest struct {
	method, path  string
	authenticated bool
	body          []byte
}
type issueEditNativeFixture struct {
	server                                              *httptest.Server
	host, webBase, configPath, token, mode, redirectURL string
	redirectStatus                                      int
	keyring                                             *issueEditNativeKeyring
	before, after                                       upstreamIssue
	mu                                                  sync.Mutex
	records                                             []issueEditNativeRequest
	issueReads, labelReads                              int
	applied                                             bool
}

func newIssueEditNativeFixture(t *testing.T) *issueEditNativeFixture {
	t.Helper()
	f := &issueEditNativeFixture{host: "git.issue-edit.example", webBase: "https://web.issue-edit.example/gitlab", token: strings.Join([]string{"synthetic", "issue", "native", "credential"}, "-")}
	f.before = issueEditFixture()
	f.before.WebURL = f.webBase + "/group/project/-/issues/42"
	f.after = f.before
	f.after.Title = "new title"
	f.after.Labels = []string{"bug", "keep", "triage"}
	f.after.UpdatedAt = timePointer(issueEditNextTime)
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	cfg := config.New()
	cfg.Hosts[f.host] = config.Host{GitHosts: []string{f.host}, APIBase: f.server.URL + "/gitlab/api/v4", WebBase: f.webBase, ProxyDisabled: true}
	f.configPath = filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(f.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve(f.host)
	if err != nil {
		t.Fatal(err)
	}
	f.keyring = &issueEditNativeKeyring{t: t, service: auth.ServiceName(resolved), account: f.host, token: f.token}
	return f
}
func (f *issueEditNativeFixture) args(t *testing.T) []string {
	t.Helper()
	args := issueEditArgs(t, stringPointer(f.after.Title), nil, []string{"triage"}, nil, false, "json")
	args = replaceArg(args, "gitlab.com", f.host)
	return replaceArg(args, issueEditTestURL, f.before.WebURL)
}
func (f *issueEditNativeFixture) dependencies(t *testing.T) (*bytes.Buffer, *bytes.Buffer, Dependencies) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return &stdout, &stderr, Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""), Cwd: t.TempDir(), ConfigPath: f.configPath, Keyring: f.keyring, LookupEnv: func(string) (string, bool) { return "", false }, HTTPClient: f.server.Client()}, NewDelegate: func() delegateClient { t.Fatal("native operation constructed official-glab delegate"); return nil }}
}
func (f *issueEditNativeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.records = append(f.records, issueEditNativeRequest{method: r.Method, path: r.URL.EscapedPath(), authenticated: r.Header.Get("PRIVATE-TOKEN") == f.token && r.Header.Get("Authorization") == "", body: body})
	w.Header().Set("Content-Type", "application/json")
	write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	switch r.Method + " " + r.URL.EscapedPath() {
	case "GET /gitlab/api/v4/projects/group%2Fproject":
		write(issueEditProject{ID: 101, PathWithNamespace: "group/project", WebURL: f.webBase + "/group/project"})
	case "GET /gitlab/api/v4/projects/group%2Fproject/issues/42":
		f.issueReads++
		if f.mode == "read failure" && f.issueReads == 3 {
			w.WriteHeader(503)
			return
		}
		issue := f.before
		if f.applied {
			issue = f.after
		}
		if f.mode == "wrong issue" {
			issue.ID++
		}
		if f.mode == "stale" {
			issue.UpdatedAt = timePointer(issueEditNextTime)
		}
		if f.mode == "drift" && f.issueReads == 2 {
			issue.Description = "unseen change"
		}
		write(issue)
	case "GET /gitlab/api/v4/projects/group%2Fproject/labels":
		f.labelReads++
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("include_ancestor_groups") != "true" {
			w.WriteHeader(400)
			return
		}
		labels := issueEditCatalog()
		if f.mode == "label reused" && f.labelReads == 2 {
			labels[0].ID = 99
		}
		write(labels)
	case "PUT /gitlab/api/v4/projects/101/issues/42":
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil || !reflect.DeepEqual(payload, map[string]any{"title": "new title", "add_labels": "triage"}) {
			w.WriteHeader(400)
			return
		}
		f.applied = f.mode != "unapplied" && f.mode != "redirect"
		switch f.mode {
		case "redirect":
			w.Header().Set("Location", f.redirectURL)
			w.WriteHeader(f.redirectStatus)
		case "lost":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		case "server failure":
			w.WriteHeader(500)
			write(map[string]string{"message": f.token})
		case "malformed":
			_, _ = w.Write([]byte(`{"id":`))
		case "wrong response":
			issue := f.after
			issue.ID++
			write(issue)
		case "unapplied":
			w.WriteHeader(500)
		default:
			write(f.after)
		}
	default:
		w.WriteHeader(404)
	}
}
func (f *issueEditNativeFixture) snapshot() []issueEditNativeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]issueEditNativeRequest(nil), f.records...)
}
func (f *issueEditNativeFixture) assertRequests(t *testing.T, wantTotal, wantPuts int) {
	t.Helper()
	records := f.snapshot()
	if wantTotal >= 0 && len(records) != wantTotal {
		t.Fatalf("requests=%d want=%d", len(records), wantTotal)
	}
	puts := 0
	for _, record := range records {
		if !record.authenticated {
			t.Fatal("operation did not retain selected credential")
		}
		if record.method == "PUT" {
			puts++
		} else if record.method != "GET" {
			t.Fatalf("unexpected method %s", record.method)
		}
		f.assertConfidential(t, record.path+string(record.body))
	}
	if puts != wantPuts {
		t.Fatalf("PUT count=%d want=%d", puts, wantPuts)
	}
}
func (f *issueEditNativeFixture) assertConfidential(t *testing.T, value string) {
	t.Helper()
	if strings.Contains(value, f.token) {
		t.Fatal("synthetic credential escaped its header")
	}
}
func indexOfIssueEditFlag(args []string, flag string) int {
	for i, value := range args {
		if value == flag {
			return i
		}
	}
	panic("missing test flag")
}
