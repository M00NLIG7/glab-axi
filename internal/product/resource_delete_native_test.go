package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	runtimepkg "gl-axi/internal/runtime"
)

// These feature acceptance tests are prepared against the published shared
// native interface. No private credentials or official profile is consulted.
const deletionTestHost = "gitlab.delete.example"
const deletionTestWeb = "https://web.delete.example/gitlab"
const deletionTestTime = "2026-09-01T12:00:00Z"
const deletionTestSHA = "0123456789abcdef0123456789abcdef01234567"

type deletionCase struct {
	name, group, command, selector, route, webPath, confirmation string
	personal                                                     bool
	expected                                                     []string
	body                                                         map[string]any
}

func deletionCases() []deletionCase {
	return []deletionCase{
		{name: "issue", group: "issue", command: "delete", selector: "42", route: "projects/101/issues/42", webPath: "/group/project/-/issues/42", confirmation: "--confirm-delete-issue",
			expected: []string{"--expected-id", "1001", "--expected-state", "opened", "--expected-updated-at", deletionTestTime},
			body:     map[string]any{"id": 1001, "iid": 42, "project_id": 101, "state": "opened", "updated_at": deletionTestTime, "title": "synthetic issue"}},
		{name: "pipeline", group: "pipeline", command: "delete", selector: "88", route: "projects/101/pipelines/88", webPath: "/group/project/-/pipelines/88", confirmation: "--confirm-delete-pipeline",
			expected: []string{"--acknowledge-child-cancellation", deletionTestWeb + "/group/project/-/pipelines/88", "--expected-sha", deletionTestSHA, "--expected-ref", "main", "--expected-status", "success", "--expected-updated-at", deletionTestTime},
			body:     map[string]any{"id": 88, "project_id": 101, "sha": deletionTestSHA, "ref": "main", "status": "success", "updated_at": deletionTestTime}},
		{name: "release", group: "release", command: "delete", selector: "v1.0", route: "projects/101/releases/v1.0", webPath: "/group/project/-/releases/v1.0", confirmation: "--confirm-delete-release",
			expected: []string{"--acknowledge-catalog-unpublication", deletionTestWeb + "/group/project/-/releases/v1.0", "--expected-commit", deletionTestSHA, "--expected-created-at", deletionTestTime},
			body:     map[string]any{"tag_name": "v1.0", "name": "release", "created_at": deletionTestTime, "released_at": deletionTestTime, "commit": map[string]any{"id": deletionTestSHA}, "tag_path": "/gitlab/group/project/-/tags/v1.0"}},
		// Distinct leaves make personal scope explicitly repo-less; project deletion
		// requires -R. Neither scope is inferred from cwd or from a failed lookup.
		{name: "personal-snippet", group: "snippet", command: "delete", selector: "31", route: "snippets/31", webPath: "/-/snippets/31", confirmation: "--confirm-delete-snippet", personal: true,
			expected: []string{"--expected-author-id", "7", "--expected-updated-at", deletionTestTime},
			body:     map[string]any{"id": 31, "project_id": nil, "author": map[string]any{"id": 7}, "updated_at": deletionTestTime, "title": "snippet", "visibility": "private"}},
		{name: "project-snippet", group: "snippet", command: "delete-project", selector: "32", route: "projects/101/snippets/32", webPath: "/group/project/-/snippets/32", confirmation: "--confirm-delete-snippet",
			expected: []string{"--expected-author-id", "7", "--expected-updated-at", deletionTestTime},
			body:     map[string]any{"id": 32, "project_id": 101, "author": map[string]any{"id": 7}, "updated_at": deletionTestTime, "title": "snippet", "visibility": "private"}},
	}
}

func (c deletionCase) args() []string {
	u := deletionTestWeb + c.webPath
	args := []string{c.group, c.command, c.selector, "--hostname", deletionTestHost, "--auth-source", "native", "--expected-url", u, c.confirmation, u, "--format", "json"}
	if !c.personal {
		args = append(args, "--repo", "group/project", "--expected-project-id", "101")
	}
	return append(args, c.expected...)
}

type deletionSpyKeyring struct{ calls atomic.Int32 }

func (k *deletionSpyKeyring) Get(context.Context, string, string) (string, error) {
	k.calls.Add(1)
	return "", auth.ErrKeyringNotFound
}
func (k *deletionSpyKeyring) Set(context.Context, string, string, string) error {
	k.calls.Add(1)
	return fmt.Errorf("unexpected keyring write")
}
func (k *deletionSpyKeyring) Delete(context.Context, string, string) error {
	k.calls.Add(1)
	return fmt.Errorf("unexpected keyring delete")
}

type deletionTestReceipt struct {
	Action            string         `json:"action"`
	Resource          string         `json:"resource"`
	Scope             string         `json:"scope"`
	URL               string         `json:"url"`
	Tag               string         `json:"tag"`
	DeleteStatus      int            `json:"delete_status"`
	DeleteAttempted   bool           `json:"delete_attempted"`
	Acknowledged      bool           `json:"acknowledged"`
	Postcondition     string         `json:"postcondition"`
	TagPostcondition  string         `json:"tag_postcondition"`
	Concurrency       string         `json:"concurrency"`
	Expected          map[string]any `json:"expected"`
	IntendedEffects   []string       `json:"intended_effects"`
	ChildCancellation *struct {
		Acknowledged bool   `json:"acknowledged"`
		Outcome      string `json:"outcome"`
	} `json:"child_cancellation"`
	CatalogUnpublication *struct {
		Acknowledged bool   `json:"acknowledged"`
		Outcome      string `json:"outcome"`
	} `json:"catalog_unpublication"`
}

type deletionEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Deletion deletionTestReceipt `json:"deletion"`
	} `json:"data"`
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
		Receipt   struct {
			Deletion deletionTestReceipt `json:"deletion"`
		} `json:"receipt"`
	} `json:"error"`
	Meta struct {
		Backend  string `json:"backend"`
		Upstream string `json:"upstream_version"`
	} `json:"meta"`
}

type deletionFixture struct {
	t                        *testing.T
	item                     deletionCase
	mode                     string
	server                   *httptest.Server
	configPath               string
	token                    string
	keyring                  deletionSpyKeyring
	child                    atomic.Int32
	lookup                   atomic.Int32
	mu                       sync.Mutex
	requests                 []string
	deletes, reads, tagReads int
	childPipelineStatus      string
	catalogState             string
	catalogVersions          int
	forbiddenPaths           int
	wrongCredential          bool
	snippetDeleted           bool
	unrelated                string
	redirect                 string
	cancelHit                chan struct{}
	cancelOnce               sync.Once
	selections               atomic.Int32
}

func newDeletionFixture(t *testing.T, item deletionCase, mode string) *deletionFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("persisted native config/self-managed mapping on Windows remains unproven; this feature does not change that boundary")
	}
	f := &deletionFixture{t: t, item: item, mode: mode, token: strings.Join([]string{"synthetic", "delete", t.Name()}, "-"), cancelHit: make(chan struct{})}
	if item.group == "pipeline" {
		f.childPipelineStatus = "running"
	}
	if item.group == "release" {
		f.catalogState, f.catalogVersions = "published", 1
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	home := t.TempDir()
	f.configPath = filepath.Join(home, "config.json")
	cfg := config.New()
	cfg.Hosts[deletionTestHost] = config.Host{GitHosts: []string{deletionTestHost}, APIBase: f.server.URL + "/gitlab/api/v4", WebBase: deletionTestWeb, ProxyDisabled: true}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	f.unrelated = filepath.Join(home, "unrelated-caller-file")
	if err := os.WriteFile(f.unrelated, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *deletionFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	if r.Header.Get("Private-Token") != f.token {
		f.wrongCredential = true
	}
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/gitlab/api/v4/")
	if r.Method == "GET" && path == "user" {
		if f.mode == "account-drift" && f.deletes > 0 {
			fmt.Fprint(w, `{"id":8,"username":"other","state":"active"}`)
			return
		}
		fmt.Fprint(w, `{"id":7,"username":"fixture","state":"active"}`)
		return
	}
	if r.Method == "GET" && (path == "projects/group%2Fproject" || path == "projects/101") {
		if f.mode == "project-forbidden" {
			w.WriteHeader(403)
			return
		}
		if f.mode == "project-wrong-id" {
			fmt.Fprint(w, `{"id":102,"path_with_namespace":"group/project","web_url":"`+deletionTestWeb+`/group/project"}`)
			return
		}
		fmt.Fprint(w, `{"id":101,"path_with_namespace":"group/project","web_url":"`+deletionTestWeb+`/group/project"}`)
		return
	}
	if r.Method == "GET" && path == "projects/101/repository/tags/"+strings.TrimPrefix(f.item.route, "projects/101/releases/") && f.item.group == "release" {
		f.tagReads++
		sha := deletionTestSHA
		if f.mode == "pre-tag-drift" || f.mode == "tag-drift" && f.deletes > 0 {
			sha = strings.Repeat("b", 40)
		}
		json.NewEncoder(w).Encode(map[string]any{"name": f.item.selector, "commit": map[string]any{"id": sha}})
		return
	}
	if path != f.item.route || r.URL.RawQuery != "" {
		f.forbiddenPaths++
		w.WriteHeader(400)
		return
	}
	if r.Method == "DELETE" {
		f.deletes++
		b, err := io.ReadAll(io.LimitReader(r.Body, 1024))
		if err != nil || len(b) != 0 {
			f.forbiddenPaths++
		}
		if f.item.group == "pipeline" && f.item.body["status"] == "running" {
			switch f.mode {
			case "success", "lost-absence", "child-canceled-parent-present":
				f.childPipelineStatus = "canceled"
			}
		}
		if f.item.group == "release" {
			switch f.mode {
			case "success", "lost-absence", "malformed-delete", "wrong-delete-identity", "tag-drift":
				f.catalogVersions--
				if f.catalogVersions == 0 {
					f.catalogState = "unpublished"
				}
			}
		}
		switch f.mode {
		case "snippet-400-absent", "snippet-400-present", "snippet-400-unverified":
			// Repository removal can fail after the snippet row is committed away.
			f.snippetDeleted = f.mode != "snippet-400-present"
			w.WriteHeader(400)
			fmt.Fprint(w, `{"message":"Failed to remove snippet."}`)
			return
		case "delete-400":
			w.WriteHeader(400)
			return
		case "delete-401":
			w.WriteHeader(401)
			return
		case "delete-403":
			w.WriteHeader(403)
			return
		case "delete-404":
			w.WriteHeader(404)
			return
		case "delete-412":
			w.WriteHeader(412)
			return
		case "delete-429":
			w.WriteHeader(429)
			return
		case "delete-500", "lost-absence", "child-canceled-parent-present":
			w.WriteHeader(500)
			return
		case "disconnect":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		case "delete-cancel":
			f.cancelOnce.Do(func() { close(f.cancelHit) })
			<-r.Context().Done()
			return
		case "redirect-307", "redirect-308", "redirect-302", "cross-origin":
			location := f.redirect
			if location == "" {
				location = f.server.URL + "/gitlab/api/v4/projects/999/issues/43"
			}
			w.Header().Set("Location", location)
			status := 307
			if f.mode == "redirect-308" {
				status = 308
			}
			if f.mode == "redirect-302" {
				status = 302
			}
			w.WriteHeader(status)
			return
		case "unexpected-202":
			w.WriteHeader(202)
			return
		case "unexpected-200":
			w.WriteHeader(200)
			fmt.Fprint(w, `{}`)
			return
		case "oversize-delete":
			w.WriteHeader(200)
			fmt.Fprint(w, strings.Repeat("x", (2<<20)+1))
			return
		}
		if f.item.group == "release" {
			w.WriteHeader(200)
			if f.mode == "malformed-delete" {
				fmt.Fprint(w, `{"tag_name":`)
				return
			}
			body := f.resource()
			if f.mode == "wrong-delete-identity" {
				body["tag_name"] = "sibling"
			}
			json.NewEncoder(w).Encode(body)
		} else {
			w.WriteHeader(204)
		}
		return
	}
	if r.Method != "GET" {
		f.forbiddenPaths++
		w.WriteHeader(400)
		return
	}
	f.reads++
	if f.deletes > 0 {
		switch f.mode {
		case "post-forbidden", "snippet-400-unverified":
			w.WriteHeader(403)
			return
		case "snippet-400-absent", "snippet-400-present":
			if f.snippetDeleted {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(f.resource())
			}
			return
		case "post-malformed":
			fmt.Fprint(w, `{"id":`)
			return
		case "post-present", "redirect-307", "redirect-308", "redirect-302", "cross-origin", "child-canceled-parent-present":
			json.NewEncoder(w).Encode(f.resource())
			return
		default:
			w.WriteHeader(404)
			return
		}
	}
	switch f.mode {
	case "pre-404":
		w.WriteHeader(404)
		return
	case "pre-401":
		w.WriteHeader(401)
		return
	case "pre-403":
		w.WriteHeader(403)
		return
	case "pre-malformed":
		fmt.Fprint(w, `{"id":`)
		return
	case "pre-oversize":
		fmt.Fprint(w, strings.Repeat("x", (2<<20)+1))
		return
	case "pre-cancel":
		f.cancelOnce.Do(func() { close(f.cancelHit) })
		<-r.Context().Done()
		return
	}
	body := f.resource()
	if f.mode == "wrong-id" {
		body["id"] = 999
		body["tag_name"] = "sibling"
	}
	if f.mode == "wrong-project" {
		body["project_id"] = 102
	}
	if f.mode == "wrong-url" {
		body["web_url"] = "https://other.example/sibling"
		body["_links"] = map[string]any{"self": "https://other.example/sibling"}
	}
	if f.mode == "drift" && f.reads > 1 {
		body["updated_at"] = "2026-09-01T12:00:01Z"
		body["created_at"] = "2026-09-01T12:00:01Z"
	}
	if f.mode == "wrong-sha" {
		body["sha"] = strings.Repeat("a", 40)
		body["commit"] = map[string]any{"id": strings.Repeat("a", 40)}
	}
	if f.mode == "wrong-author" {
		body["author"] = map[string]any{"id": 8}
	}
	json.NewEncoder(w).Encode(body)
}

func (f *deletionFixture) resource() map[string]any {
	out := map[string]any{}
	for k, v := range f.item.body {
		out[k] = v
	}
	if f.item.group == "release" {
		out["_links"] = map[string]any{"self": deletionTestWeb + f.item.webPath}
	} else {
		out["web_url"] = deletionTestWeb + f.item.webPath
	}
	return out
}

func (f *deletionFixture) run(ctx context.Context, args []string, hasCredential bool) (int, deletionEnvelope) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Cwd: filepath.Dir(f.configPath), ConfigPath: f.configPath, Keyring: &f.keyring, HTTPClient: f.server.Client(), Stdout: &stdout, Stderr: &stderr,
		LookupEnv: func(name string) (string, bool) {
			f.lookup.Add(1)
			if hasCredential && name == "GL_AXI_TOKEN" {
				if f.selections.Add(1) > 1 {
					return f.token + "-rotated", true
				}
				return f.token, true
			}
			return "", false
		},
	}, NewDelegate: func() delegateClient { f.child.Add(1); panic("deletion must never delegate or fall back") }}
	code := Run(ctx, args, deps)
	var envelope deletionEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		f.t.Fatalf("output is not a JSON envelope: %v", err)
	}
	if bytes.Contains(stdout.Bytes(), []byte(f.token)) || bytes.Contains(stderr.Bytes(), []byte(f.token)) {
		f.t.Fatal("credential reached product output")
	}
	if f.child.Load() != 0 {
		f.t.Fatal("official-glab child was used")
	}
	preserved, err := os.ReadFile(f.unrelated)
	if err != nil || string(preserved) != "preserve" {
		f.t.Fatal("unrelated local content changed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wrongCredential || f.forbiddenPaths != 0 {
		f.t.Fatalf("unsafe credential or route: credential=%v unexpected=%d requests=%q", f.wrongCredential, f.forbiddenPaths, f.requests)
	}
	if len(f.requests) > 12 {
		f.t.Fatalf("unbounded fixed-resource operation: %d requests", len(f.requests))
	}
	return code, envelope
}

func TestNativeResourceDeletionSuccess(t *testing.T) {
	for _, item := range deletionCases() {
		t.Run(item.name, func(t *testing.T) {
			f := newDeletionFixture(t, item, "success")
			code, out := f.run(context.Background(), item.args(), true)
			if code != 0 || !out.OK {
				t.Fatalf("exit=%d error=%s", code, out.Error.Code)
			}
			if f.deletes != 1 || f.reads != 3 || f.selections.Load() != 1 || f.keyring.calls.Load() != 0 {
				t.Fatalf("DELETE count=%d resource reads=%d native selections=%d keyring=%d", f.deletes, f.reads, f.selections.Load(), f.keyring.calls.Load())
			}
			if out.Meta.Backend != "native" || out.Meta.Upstream != "" {
				t.Fatalf("wrong backend attribution: %+v", out.Meta)
			}
			result := out.Data.Deletion
			if !result.Acknowledged || !result.DeleteAttempted || result.Concurrency != "best_effort_non_atomic" {
				t.Fatalf("incomplete receipt: %+v", result)
			}
			if item.group == "pipeline" {
				assertPipelineChildCancellationReceipt(t, result, "unverified")
			} else if result.ChildCancellation != nil {
				t.Fatal("child cancellation acknowledgment escaped pipeline deletion")
			}
			if item.group == "release" {
				assertReleaseCatalogUnpublicationReceipt(t, result, "unverified")
				if result.Expected["created_at"] != deletionTestTime || result.Expected["sha"] != deletionTestSHA || result.TagPostcondition != "unchanged" {
					t.Fatalf("lost release expectation: %+v", result)
				}
			} else if result.Expected["updated_at"] != deletionTestTime {
				t.Fatalf("lost reviewed revision: %+v", result)
			}
			if item.group != "release" && result.CatalogUnpublication != nil {
				t.Fatal("catalog unpublication acknowledgment escaped release deletion")
			}
			scope := "project"
			if item.personal {
				scope = "personal"
			}
			status := 204
			if item.group == "release" {
				status = 200
				if f.tagReads < 2 {
					t.Fatal("release deletion did not verify retained tag")
				}
			}
			if result.Action != "deleted" || result.Resource != item.group || result.Scope != scope || result.URL != deletionTestWeb+item.webPath || result.DeleteStatus != status || result.Postcondition != "not_found" {
				t.Fatalf("receipt=%+v", result)
			}
		})
	}
}

func TestNativeResourceDeletionPreflightRefusals(t *testing.T) {
	modes := []string{"pre-404", "pre-401", "pre-403", "pre-malformed", "pre-oversize", "wrong-id", "wrong-url", "drift"}
	for _, item := range deletionCases() {
		for _, mode := range modes {
			t.Run(item.name+"/"+mode, func(t *testing.T) {
				f := newDeletionFixture(t, item, mode)
				code, out := f.run(context.Background(), item.args(), true)
				if code == 0 || out.OK || f.deletes != 0 {
					t.Fatalf("exit=%d ok=%v writes=%d", code, out.OK, f.deletes)
				}
			})
		}
		if !item.personal {
			for _, mode := range []string{"project-forbidden", "project-wrong-id"} {
				t.Run(item.name+"/"+mode, func(t *testing.T) {
					f := newDeletionFixture(t, item, mode)
					code, out := f.run(context.Background(), item.args(), true)
					if code == 0 || out.OK || f.deletes != 0 {
						t.Fatalf("exit=%d ok=%v writes=%d", code, out.OK, f.deletes)
					}
				})
			}
		}
		if item.group != "release" {
			t.Run(item.name+"/wrong-project", func(t *testing.T) {
				f := newDeletionFixture(t, item, "wrong-project")
				code, out := f.run(context.Background(), item.args(), true)
				if code == 0 || out.OK || f.deletes != 0 {
					t.Fatalf("exit=%d ok=%v writes=%d", code, out.OK, f.deletes)
				}
			})
		}
	}
}

func TestNativeResourceDeletionNeverReplaysOrInfersSuccessFromAbsence(t *testing.T) {
	modes := []string{"delete-401", "delete-403", "delete-404", "delete-412", "delete-429", "delete-500", "lost-absence", "disconnect", "unexpected-200", "unexpected-202", "post-forbidden", "post-malformed", "post-present", "account-drift", "redirect-302", "redirect-307", "redirect-308"}
	for _, item := range deletionCases() {
		current := append([]string(nil), modes...)
		if item.group == "release" {
			current = append(current, "malformed-delete", "wrong-delete-identity", "oversize-delete", "tag-drift")
		}
		for _, mode := range current {
			t.Run(item.name+"/"+mode, func(t *testing.T) {
				f := newDeletionFixture(t, item, mode)
				code, out := f.run(context.Background(), item.args(), true)
				if code == 0 || out.OK || f.deletes != 1 || out.Error.Retryable {
					t.Fatalf("exit=%d ok=%v writes=%d error=%s retryable=%v", code, out.OK, f.deletes, out.Error.Code, out.Error.Retryable)
				}
				r := out.Error.Receipt.Deletion
				if !r.DeleteAttempted || r.Action == "deleted" || r.Concurrency != "best_effort_non_atomic" {
					t.Fatalf("misleading failure receipt: %+v", r)
				}
				if item.group == "pipeline" {
					assertPipelineChildCancellationReceipt(t, r, "unverified")
				}
				if item.group == "release" {
					assertReleaseCatalogUnpublicationReceipt(t, r, "unverified")
				}
				if strings.HasPrefix(mode, "delete-") && mode != "delete-500" {
					if r.Action != "rejected" || r.Acknowledged {
						t.Fatalf("rejection receipt: %+v", r)
					}
				} else if out.Error.Code != "ambiguous_delete" || r.Action != "ambiguous" {
					t.Fatalf("unknown outcome lost: %+v", out.Error)
				}
			})
		}
	}
}

func TestNativeResourceDeletionRejectsCrossOriginBeforeTransmission(t *testing.T) {
	for _, item := range deletionCases() {
		t.Run(item.name, func(t *testing.T) {
			var crossed atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { crossed.Add(1); w.WriteHeader(204) }))
			defer target.Close()
			f := newDeletionFixture(t, item, "cross-origin")
			f.redirect = target.URL + "/must-not-receive-request"
			code, out := f.run(context.Background(), item.args(), true)
			if code == 0 || out.OK || f.deletes != 1 || crossed.Load() != 0 {
				t.Fatalf("exit=%d ok=%v writes=%d crossed=%d", code, out.OK, f.deletes, crossed.Load())
			}
		})
	}
}

func TestNativeResourceDeletionMissingCredentialNeverFallsBack(t *testing.T) {
	for _, item := range deletionCases() {
		t.Run(item.name, func(t *testing.T) {
			f := newDeletionFixture(t, item, "success")
			code, out := f.run(context.Background(), item.args(), false)
			if code == 0 || out.OK || len(f.requests) != 0 || f.keyring.calls.Load() != 1 {
				t.Fatalf("exit=%d ok=%v requests=%d keyring=%d", code, out.OK, len(f.requests), f.keyring.calls.Load())
			}
		})
	}
}

func deletionReplaceFlag(args []string, flag, value string, remove bool) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == flag {
			if remove {
				return append(out[:i], out[i+2:]...)
			}
			out[i+1] = value
			return out
		}
	}
	return out
}

func TestNativeResourceDeletionRejectsInvalidSelectorsWithoutDependencyWork(t *testing.T) {
	for _, item := range deletionCases() {
		base := item.args()
		inputs := map[string][]string{
			"missing-auth":         deletionReplaceFlag(base, "--auth-source", "", true),
			"wrong-auth":           deletionReplaceFlag(base, "--auth-source", "official", false),
			"duplicate-auth":       append(append([]string(nil), base...), "--auth-source", "native"),
			"missing-host":         deletionReplaceFlag(base, "--hostname", "", true),
			"missing-confirmation": deletionReplaceFlag(base, item.confirmation, "", true),
			"wrong-confirmation":   deletionReplaceFlag(base, item.confirmation, "https://wrong.example/resource", false),
			"broad-yes":            append(deletionReplaceFlag(base, item.confirmation, "", true), "--yes"),
			"list-limit":           append(append([]string(nil), base...), "--limit", "1000"),
		}
		if !item.personal {
			inputs["missing-project"] = deletionReplaceFlag(base, "--repo", "", true)
		} else {
			inputs["personal-with-project"] = append(append([]string(nil), base...), "--repo", "group/project")
		}
		for name, args := range inputs {
			t.Run(item.name+"/"+name, func(t *testing.T) {
				f := newDeletionFixture(t, item, "success")
				code, out := f.run(context.Background(), args, true)
				if code == 0 || out.OK || len(f.requests) != 0 || f.lookup.Load() != 0 || f.keyring.calls.Load() != 0 {
					t.Fatalf("exit=%d ok=%v requests=%d lookups=%d keyring=%d", code, out.OK, len(f.requests), f.lookup.Load(), f.keyring.calls.Load())
				}
			})
		}
	}
}

func TestNativeResourceDeletionCancellationBounds(t *testing.T) {
	for _, item := range deletionCases() {
		for _, mode := range []string{"pre-cancel", "delete-cancel"} {
			t.Run(item.name+"/"+mode, func(t *testing.T) {
				f := newDeletionFixture(t, item, mode)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				go func() {
					select {
					case <-f.cancelHit:
						cancel()
					case <-ctx.Done():
					}
				}()
				start := time.Now()
				code, out := f.run(ctx, item.args(), true)
				want := 0
				if mode == "delete-cancel" {
					want = 1
				}
				if code == 0 || out.OK || f.deletes != want || time.Since(start) > 5*time.Second {
					t.Fatalf("exit=%d ok=%v writes=%d duration=%v", code, out.OK, f.deletes, time.Since(start))
				}
			})
		}
	}
}
