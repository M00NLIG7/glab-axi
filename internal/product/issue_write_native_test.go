package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	runtimepkg "gl-axi/internal/runtime"
	"gl-axi/internal/testgitlab"
)

// Prepared against the published shared native contract. These are feature
// acceptance tests, not an alternate auth resolver or transport. Enablement
// and execution depend on the shared boundary landing and feature integration.
const issueNativeHost = "gitlab.issue-native-contract.example"
const issueNativeWeb = "https://web.issue-native-contract.example/gitlab"
const issueNativeAPIPath = "/gitlab/api/v4"

type issueNativeKeyring struct {
	mu    sync.Mutex
	token string
	gets  int
}

func (k *issueNativeKeyring) Get(context.Context, string, string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets++
	if k.token == "" {
		return "", auth.ErrKeyringNotFound
	}
	if k.gets > 1 {
		return strings.Join([]string{"different", "second", "credential"}, "-"), nil
	}
	return k.token, nil
}
func (*issueNativeKeyring) Set(context.Context, string, string, string) error {
	return auth.ErrKeyringUnavailable
}
func (*issueNativeKeyring) Delete(context.Context, string, string) error {
	return auth.ErrKeyringUnavailable
}
func (k *issueNativeKeyring) reads() int { k.mu.Lock(); defer k.mu.Unlock(); return k.gets }

// A GitLab fixture only. HTTP/auth behavior always comes from the real product
// via runtime Dependencies, never from a replacement client in these tests.
func issueNativeHandler(t *testing.T, action string, mutate http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	var mu sync.Mutex
	state := "opened"
	if action == "reopen" {
		state = "closed"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.EscapedPath(), issueNativeAPIPath)
		if !strings.HasPrefix(r.URL.EscapedPath(), issueNativeAPIPath+"/") {
			t.Error("request escaped configured API prefix")
			w.WriteHeader(404)
			return
		}
		switch r.Method + " " + path {
		case "GET /projects/group%2Fproject":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 101, "path_with_namespace": "group/project", "web_url": issueNativeWeb + "/group/project"})
		case "GET /projects/101/issues/42":
			_, _ = w.Write([]byte(strings.ReplaceAll(string(issueWriteBody(state)), "https://gitlab.com", issueNativeWeb)))
		case "POST /projects/101/issues", "POST /projects/101/issues/42/notes", "PUT /projects/101/issues/42":
			if mutate != nil {
				mutate(w, r)
				return
			}
			if r.Method == "PUT" {
				if action == "reopen" {
					state = "opened"
				} else {
					state = "closed"
				}
			}
			if r.Method == "POST" {
				w.WriteHeader(http.StatusCreated)
			}
			if strings.HasSuffix(path, "/notes") {
				_, _ = w.Write(issueNoteBody())
				return
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(string(issueWriteBody(state)), "https://gitlab.com", issueNativeWeb)))
		default:
			t.Error("unexpected issue-native request route or method")
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func issueNativeArgs(t *testing.T, action string) []string {
	t.Helper()
	args := issueWriteArgs(t, action)
	args = replaceIssueWriteArg(args, "--hostname", issueNativeHost)
	expected := issueNativeWeb + "/group/project/-/issues/42"
	if action == "create" {
		expected = issueNativeWeb + "/group/project"
	}
	args = replaceIssueWriteArg(args, "--expected-url", expected)
	return args
}

func issueNativeDeps(t *testing.T, server *testgitlab.Server, token string, keyring *issueNativeKeyring) (*bytes.Buffer, *bytes.Buffer, Dependencies) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	ca, err := server.CAFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Hosts[issueNativeHost] = config.Host{GitHosts: []string{issueNativeHost}, APIBase: server.HTTP.URL + issueNativeAPIPath, WebBase: issueNativeWeb, CABundle: ca, ProxyDisabled: true}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{
		Cwd: dir, ConfigPath: path, Stdout: &out, Stderr: &stderr, Keyring: keyring, HTTPClient: server.HTTP.Client(),
		LookupEnv: func(name string) (string, bool) {
			if name == "GL_AXI_TOKEN" && token != "" {
				return token, true
			}
			return "", false
		},
	}, NewDelegate: func() delegateClient {
		t.Error("native issue operation constructed an official-profile delegate")
		return &fakeDelegate{}
	}}
	return &out, &stderr, deps
}

func assertIssueNativePrivate(t *testing.T, out, stderr []byte, requests []testgitlab.Request, token string) {
	t.Helper()
	if bytes.Contains(out, []byte(token)) || bytes.Contains(stderr, []byte(token)) {
		t.Fatal("synthetic native credential reached output")
	}
	for _, r := range requests {
		if strings.Contains(r.URL, token) || bytes.Contains(r.Body, []byte(token)) {
			t.Fatal("synthetic native credential reached URI or body")
		}
		if r.Header.Get("Private-Token") != token {
			t.Fatal("operation changed native credential between requests")
		}
	}
}

func TestIssueWritesNativeContractFullSequence(t *testing.T) {
	for _, action := range []string{"create", "comment", "note", "close", "reopen"} {
		for _, source := range []string{"environment", "keyring"} {
			t.Run(action+"/"+source, func(t *testing.T) {
				token := strings.Join([]string{"synthetic", "native", "issue", "sequence"}, "-")
				server := testgitlab.New(issueNativeHandler(t, action, nil))
				defer server.Close()
				keyring := &issueNativeKeyring{}
				envToken := token
				if source == "keyring" {
					envToken = ""
					keyring.token = token
				}
				out, stderr, deps := issueNativeDeps(t, server, envToken, keyring)
				args := issueNativeArgs(t, action)
				if action == "note" {
					args = args[:len(args)-2]
					args = append(args, "--auth-source=native")
				}
				code := Run(context.Background(), args, deps)
				requests := server.Requests()
				assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
				wantExit, wantAttempts := 0, 1
				if action == "close" || action == "reopen" {
					wantExit, wantAttempts = 2, 0
				}
				if code != wantExit {
					t.Fatalf("native sequence exit=%d output=%s", code, out)
				}
				ok, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
				if ok != (wantExit == 0) || receipt.MutationAttempts != wantAttempts || receipt.Identity.Host != issueNativeHost || receipt.Identity.ProjectWebURL != issueNativeWeb+"/group/project" || receipt.Identity.WebURL != issueNativeWeb+"/group/project/-/issues/42" {
					t.Fatalf("receipt=%+v", receipt)
				}
				if wantExit == 2 && (errCode != uxv1.CodeUnsupported || receipt.Outcome != "refused" || receipt.MutationResponse != "not_attempted" || receipt.Postcondition != "preflight") {
					t.Fatalf("refusal receipt=%+v code=%s", receipt, errCode)
				}
				var envelope struct {
					Meta uxv1.Meta `json:"meta"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Meta.Backend != "native" || envelope.Meta.UpstreamVersion != "" || envelope.Meta.Host != issueNativeHost {
					t.Fatalf("native backend metadata=%+v", envelope.Meta)
				}
				wantReads := 0
				if source == "keyring" {
					wantReads = 1
				}
				if keyring.reads() != wantReads {
					t.Fatalf("native credential resolutions=%d want=%d", keyring.reads(), wantReads)
				}
				mutations := 0
				for _, r := range requests {
					if r.Method == http.MethodPost || r.Method == http.MethodPut {
						mutations++
						var payload map[string]string
						if err := json.Unmarshal(r.Body, &payload); err != nil {
							t.Fatal(err)
						}
						switch action {
						case "create":
							if len(payload) != 3 || payload["title"] != "new title" || payload["description"] != "new body" || payload["issue_type"] != "issue" {
								t.Fatal("native create payload changed")
							}
						case "comment", "note":
							if len(payload) != 1 || payload["body"] != "new body" {
								t.Fatal("native note payload changed")
							}
						}
					}
				}
				expectedRequests := 5
				if action == "create" {
					expectedRequests = 3
				}
				if action == "close" || action == "reopen" {
					expectedRequests = 4
				}
				if mutations != wantAttempts || len(requests) != expectedRequests {
					t.Fatalf("requests=%d mutations=%d", len(requests), mutations)
				}
			})
		}
	}
}

func TestIssueWritesNativeContractInvalidSelectorAndInputHaveNoEffects(t *testing.T) {
	for _, invalid := range []string{"omitted", "official", "case", "duplicate", "hostname", "repo", "body"} {
		t.Run(invalid, func(t *testing.T) {
			server := testgitlab.New(nil)
			defer server.Close()
			keyring := &issueNativeKeyring{token: strings.Join([]string{"synthetic", "unused", "keyring"}, "-")}
			out, _, deps := issueNativeDeps(t, server, "", keyring)
			args := issueNativeArgs(t, "comment")
			switch invalid {
			case "omitted":
				args = args[:len(args)-2]
			case "official":
				args = replaceIssueWriteArg(args, "--auth-source", "official")
			case "case":
				args = replaceIssueWriteArg(args, "--auth-source", "Native")
			case "duplicate":
				args = append(args, "--auth-source=native")
			case "hostname", "repo":
				flag := "--" + invalid
				for i, v := range args {
					if v == flag {
						args = append(args[:i], args[i+2:]...)
						break
					}
				}
				deps.Runtime.LookupEnv = func(name string) (string, bool) {
					if name == "GITLAB_HOST" {
						return issueNativeHost, true
					}
					return "", false
				}
			case "body":
				for i, v := range args {
					if v == "--body-file" {
						if err := os.WriteFile(args[i+1], []byte("hello\n/close"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if code := Run(context.Background(), args, deps); code == 0 {
				t.Fatal("invalid native request succeeded")
			}
			if len(server.Requests()) != 0 || keyring.reads() != 0 {
				t.Fatalf("invalid input performed dependency work: requests=%d keyring=%d", len(server.Requests()), keyring.reads())
			}
			if !strings.Contains(out.String(), `"ok":false`) {
				t.Fatal("missing controlled failure envelope")
			}
		})
	}
}

func TestIssueWritesNativeContractUnavailableDoesNotFallback(t *testing.T) {
	for _, failure := range []string{"credential", "configuration", "environment disagreement"} {
		t.Run(failure, func(t *testing.T) {
			server := testgitlab.New(nil)
			defer server.Close()
			keyring := &issueNativeKeyring{}
			out, _, deps := issueNativeDeps(t, server, "", keyring)
			switch failure {
			case "configuration":
				if err := os.Remove(deps.Runtime.ConfigPath); err != nil {
					t.Fatal(err)
				}
			case "environment disagreement":
				deps.Runtime.LookupEnv = func(name string) (string, bool) {
					switch name {
					case "GL_AXI_TOKEN":
						return strings.Join([]string{"synthetic", "first", "account"}, "-"), true
					case "GITLAB_TOKEN":
						return strings.Join([]string{"synthetic", "other", "account"}, "-"), true
					}
					return "", false
				}
			}
			if code := Run(context.Background(), issueNativeArgs(t, "comment"), deps); code == 0 {
				t.Fatal("unavailable native selection succeeded")
			}
			if len(server.Requests()) != 0 {
				t.Fatal("native failure fell back to a network path")
			}
			var e struct {
				Error struct {
					Code uxv1.Code `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.Error.Code == uxv1.CodeUnsupported {
				t.Fatal("native selector was not integrated")
			}
		})
	}
}

func TestIssueWritesNativeContractRedirectsNeverLeaveSelectedRoute(t *testing.T) {
	for _, action := range []string{"create", "comment"} {
		for _, sameOrigin := range []bool{false, true} {
			for _, status := range []int{301, 302, 303, 307, 308} {
				t.Run(fmt.Sprintf("%s/same-origin-%t/%d", action, sameOrigin, status), func(t *testing.T) {
					token := strings.Join([]string{"synthetic", "native", "issue", "redirect"}, "-")
					other := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
					defer other.Close()
					server := testgitlab.New(issueNativeHandler(t, action, func(w http.ResponseWriter, r *http.Request) {
						destination := other.HTTP.URL + "/unapproved-path"
						if sameOrigin {
							destination = "https://" + r.Host + issueNativeAPIPath + "/projects/999/issues/900/notes"
						}
						w.Header().Set("Location", destination)
						w.WriteHeader(status)
					}))
					defer server.Close()
					keyring := &issueNativeKeyring{token: token}
					out, stderr, deps := issueNativeDeps(t, server, "", keyring)
					code := Run(context.Background(), issueNativeArgs(t, action), deps)
					requests := server.Requests()
					assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
					if len(other.Requests()) != 0 {
						t.Fatal("redirect sent a request to another origin")
					}
					mutations := 0
					for _, r := range requests {
						if strings.Contains(r.URL, "projects/999") {
							t.Fatal("same-origin redirect escaped selected resource")
						}
						if r.Method == "POST" || r.Method == "PUT" {
							mutations++
						}
					}
					if mutations != 1 || keyring.reads() != 1 {
						t.Fatalf("mutation attempts=%d credential selections=%d", mutations, keyring.reads())
					}
					_, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
					if code != 6 || receipt.Outcome != "ambiguous" || receipt.MutationAttempts != 1 || errCode != uxv1.CodeAmbiguousCreate {
						t.Fatalf("redirect was not truthfully ambiguous: exit=%d receipt=%+v", code, receipt)
					}
				})
			}
		}
	}
}

func TestIssueWritesNativeContractProviderFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "issue-writes", "provider-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema  string `json:"schema"`
		Backend string `json:"backend"`
		Reads   []struct {
			Method   string `json:"method"`
			Endpoint string `json:"endpoint"`
		} `json:"reads"`
		Operations []struct {
			Name     string            `json:"name"`
			Method   string            `json:"method"`
			Endpoint string            `json:"endpoint"`
			Payload  map[string]string `json:"payload"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/issue-write-provider-contract/v1" || fixture.Backend != "native" || len(fixture.Operations) != 2 || len(fixture.Reads) != 2 {
		t.Fatal("incomplete native issue provider fixture")
	}
	for _, operation := range fixture.Operations {
		action := "create"
		if operation.Name == "issue-note-create" {
			action = "comment"
		}
		t.Run(action, func(t *testing.T) {
			server := testgitlab.New(issueNativeHandler(t, action, nil))
			defer server.Close()
			token := strings.Join([]string{"synthetic", "native", "provider", "fixture"}, "-")
			out, stderr, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
			code := Run(context.Background(), issueNativeArgs(t, action), deps)
			requests := server.Requests()
			assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
			if code != 0 {
				t.Fatalf("native provider fixture exit=%d output=%s", code, out)
			}
			mutations := 0
			for _, request := range requests {
				if request.Method == "GET" {
					declared := false
					for _, read := range fixture.Reads {
						if request.Method == read.Method && request.URL == issueNativeAPIPath+"/"+read.Endpoint {
							declared = true
						}
					}
					if !declared {
						t.Fatal("native issue read is outside provider fixture")
					}
					continue
				}
				mutations++
				var payload map[string]string
				if err := json.Unmarshal(request.Body, &payload); err != nil {
					t.Fatal(err)
				}
				if request.Method != operation.Method || request.URL != issueNativeAPIPath+"/"+operation.Endpoint || !reflect.DeepEqual(payload, operation.Payload) {
					t.Fatal("native issue mutation differs from pinned provider fields or route")
				}
			}
			if mutations != 1 {
				t.Fatalf("mutations=%d", mutations)
			}
		})
	}
}
