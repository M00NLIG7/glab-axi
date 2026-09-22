package product

// These feature-integration tests are prepared against the shared product-native
// interface. They intentionally require the native-only MR registry/routing
// integration; they are not evidence that the unlanded boundary is available.

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

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	runtimepkg "gl-axi/internal/runtime"
	"gl-axi/internal/testgitlab"
)

const mrNativeWebBase = "https://review.example.invalid/gitlab"
const mrNativeAPIPath = "/gitlab/api/v4/projects/group%2Fproject"

// Test-only in-memory credentials. No system keyring/profile is ever consulted.
type mrNativeKeyring struct {
	token  string
	err    error
	gets   int
	writes int
}

func (k *mrNativeKeyring) Get(context.Context, string, string) (string, error) {
	k.gets++
	if k.gets > 1 {
		return "", errors.New("credential was resolved more than once")
	}
	return k.token, k.err
}
func (k *mrNativeKeyring) Set(context.Context, string, string, string) error {
	k.writes++
	return errors.New("test keyring is read-only")
}
func (k *mrNativeKeyring) Delete(context.Context, string, string) error {
	k.writes++
	return errors.New("test keyring is read-only")
}

type mrNativeFixture struct {
	t              *testing.T
	server         *testgitlab.Server
	keyring        *mrNativeKeyring
	deps           Dependencies
	stdout, stderr bytes.Buffer
	mu             sync.Mutex
	state          string
	writes         int
	reads          int
	noteBody       string
	mutate         func(http.ResponseWriter, *http.Request) bool
	ensure         bool
	ensureRecord   upstreamMR
}

func newMRNativeFixture(t *testing.T, state string) *mrNativeFixture {
	t.Helper()
	f := &mrNativeFixture{t: t, state: state, noteBody: "A synthetic ordinary note.\n"}
	f.keyring = &mrNativeKeyring{token: strings.Join([]string{"synthetic", "native", "mr", "sentinel"}, "-")}
	f.server = testgitlab.New(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	dir := t.TempDir()
	cfg := config.New()
	if err := cfg.Put(mrWriteTestHost, config.Host{GitHosts: []string{mrWriteTestHost}, APIBase: f.server.HTTP.URL + "/gitlab/api/v4", WebBase: mrNativeWebBase, ProxyDisabled: true}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	f.deps = Dependencies{Runtime: runtimepkg.Dependencies{
		Stdin: strings.NewReader(""), Stdout: &f.stdout, Stderr: &f.stderr, Cwd: dir, ConfigPath: configPath,
		Keyring: f.keyring, LookupEnv: func(string) (string, bool) { return "", false }, HTTPClient: f.server.HTTP.Client(),
	}, NewDelegate: func() delegateClient {
		t.Fatal("native MR operation constructed an official-profile delegate")
		return nil
	}}
	return f
}

func (f *mrNativeFixture) mr() map[string]any {
	record := mrWriteRecord(f.state)
	record["web_url"] = mrNativeWebBase + "/group/project/-/merge_requests/42"
	// Real REST shape: base/head diff references are nested, not fabricated
	// canonical top-level base_sha from a fake official-client response.
	delete(record, "base_sha")
	record["diff_refs"] = map[string]any{"base_sha": strings.Repeat("b", 40), "head_sha": mergeTestHead, "start_sha": strings.Repeat("c", 40)}
	return record
}

func (f *mrNativeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("PRIVATE-TOKEN") != f.keyring.token {
		f.t.Error("native operation changed/missed its selected credential")
	}
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.EscapedPath()
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		f.writes++
	}
	if f.mutate != nil && f.mutate(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && path == mrNativeAPIPath:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 101, "path_with_namespace": "group/project", "web_url": mrNativeWebBase + "/group/project"})
	case r.Method == http.MethodGet && path == mrNativeAPIPath+"/merge_requests/42":
		f.reads++
		_ = json.NewEncoder(w).Encode(f.mr())
	case r.Method == http.MethodPut && path == mrNativeAPIPath+"/merge_requests/42":
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 {
			f.t.Error("invalid native state body")
			w.WriteHeader(400)
			return
		}
		switch payload["state_event"] {
		case "close":
			f.state = "closed"
		case "reopen":
			f.state = "opened"
		default:
			f.t.Error("native state body broadened authority")
		}
		_ = json.NewEncoder(w).Encode(f.mr())
	case r.Method == http.MethodPost && path == mrNativeAPIPath+"/merge_requests/42/notes":
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 || payload["body"] != f.noteBody {
			f.t.Error("invalid native note body")
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(mrWriteNote(f.noteBody))
	case r.Method == http.MethodGet && path == mrNativeAPIPath+"/merge_requests/42/notes/501":
		_ = json.NewEncoder(w).Encode(mrWriteNote(f.noteBody))
	case f.ensure && r.Method == http.MethodGet && path == mrNativeAPIPath+"/merge_requests":
		if r.URL.Query().Get("source_branch") != "feature" || r.URL.Query().Get("target_branch") != "main" || r.URL.Query().Get("state") != "opened" {
			f.t.Error("native ensure lost its exact branch/state selectors")
		}
		_, _ = io.WriteString(w, "[]")
	case f.ensure && r.Method == http.MethodPost && path == mrNativeAPIPath+"/merge_requests":
		_ = json.NewEncoder(w).Encode(f.ensureRecord)
	default:
		f.t.Errorf("unexpected typed native MR request: %s %s", r.Method, r.URL.EscapedPath())
		w.WriteHeader(500)
	}
}

func (f *mrNativeFixture) args(action string) []string {
	f.t.Helper()
	args := replaceArg(mrWriteArgs(action, f.state), mrWriteTestURL, mrNativeWebBase+"/group/project/-/merge_requests/42")
	args = append(args, "--auth-source", "native")
	if action == "note" || action == "comment" {
		path := filepath.Join(f.t.TempDir(), "body")
		if err := os.WriteFile(path, []byte(f.noteBody), 0600); err != nil {
			f.t.Fatal(err)
		}
		args = append(args, "--body-file", path)
	}
	return args
}

func (f *mrNativeFixture) checkNoCredentialOutput(t *testing.T) {
	t.Helper()
	if strings.Contains(f.stdout.String(), f.keyring.token) || strings.Contains(f.stderr.String(), f.keyring.token) {
		t.Fatal("native credential escaped into output")
	}
	if f.keyring.gets != 1 || f.keyring.writes != 0 {
		t.Fatalf("keyring reads=%d mutations=%d, want one read only", f.keyring.gets, f.keyring.writes)
	}
	if strings.Contains(f.stdout.String(), `"backend":"official-glab"`) || strings.Contains(f.stdout.String(), "upstream_version") {
		t.Fatal("native operation claimed an official profile/version")
	}
}

func TestMRNativeFullSequenceIdentityAndConfiguredAuthority(t *testing.T) {
	for _, tc := range []struct {
		action, state, outcome string
		writes                 int
	}{
		{"comment", "opened", "created", 1}, {"note", "closed", "created", 1},
		{"close", "opened", "observed", 1}, {"reopen", "closed", "observed", 1},
		{"close", "closed", "unchanged", 0}, {"reopen", "opened", "unchanged", 0},
	} {
		t.Run(tc.action+"-"+tc.state, func(t *testing.T) {
			f := newMRNativeFixture(t, tc.state)
			if code := Run(context.Background(), f.args(tc.action), f.deps); code != 0 {
				t.Fatalf("exit=%d output=%s", code, f.stdout.String())
			}
			if !strings.Contains(f.stdout.String(), `"backend":"native"`) || !strings.Contains(f.stdout.String(), `"outcome":"`+tc.outcome+`"`) {
				t.Fatalf("untruthful native receipt: %s", f.stdout.String())
			}
			f.checkNoCredentialOutput(t)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != tc.writes {
				t.Fatalf("mutations=%d want=%d", f.writes, tc.writes)
			}
		})
	}
}

func TestMRNativeRedirectsNeverTransmitASecondTarget(t *testing.T) {
	for _, tc := range []struct {
		name, action, phase string
		status              int
		crossOrigin         bool
		writes              int
	}{
		{"project read", "close", "project", 302, true, 0},
		{"note write cross authority", "comment", "write", 302, true, 1},
		{"note write cross path", "comment", "write", 307, false, 1},
		{"state write cross authority", "close", "write", 303, true, 1},
		{"state write cross path", "close", "write", 308, false, 1},
		{"note readback", "comment", "note", 302, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMRNativeFixture(t, "opened")
			other := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = io.WriteString(w, `{}`) }))
			defer other.Close()
			location := f.server.HTTP.URL + "/gitlab/api/v4/unauthorized"
			if tc.crossOrigin {
				location = other.HTTP.URL + "/unauthorized"
			}
			f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
				match := tc.phase == "project" && r.URL.EscapedPath() == mrNativeAPIPath || tc.phase == "write" && r.Method != http.MethodGet || tc.phase == "note" && strings.HasSuffix(r.URL.Path, "/notes/501")
				if !match {
					return false
				}
				w.Header().Set("Location", location)
				w.WriteHeader(tc.status)
				return true
			}
			if code := Run(context.Background(), f.args(tc.action), f.deps); code == 0 {
				t.Fatalf("redirect produced false success: %s", f.stdout.String())
			}
			f.checkNoCredentialOutput(t)
			if len(other.Requests()) != 0 {
				t.Fatal("cross-origin redirect transmitted a second request")
			}
			for _, request := range f.server.Requests() {
				if strings.Contains(request.URL, "unauthorized") {
					t.Fatal("same-origin cross-path redirect was followed")
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != tc.writes {
				t.Fatalf("mutation attempts=%d want=%d", f.writes, tc.writes)
			}
			if strings.Contains(f.stdout.String(), location) {
				t.Fatal("unsafe Location escaped into bounded output")
			}
		})
	}
}

func TestMRNativeLostMutationResponseNeverBlindRetries(t *testing.T) {
	for _, action := range []string{"comment", "close"} {
		t.Run(action, func(t *testing.T) {
			f := newMRNativeFixture(t, "opened")
			f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodGet {
					return false
				}
				if action == "close" {
					f.state = "closed"
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return true
				}
				_ = connection.Close()
				return true
			}
			code := Run(context.Background(), f.args(action), f.deps)
			if action == "comment" && (code != 6 || !strings.Contains(f.stdout.String(), string(uxv1.CodeAmbiguousCreate))) {
				t.Fatalf("lost note ID was guessed: exit=%d output=%s", code, f.stdout.String())
			}
			if action == "close" && (code != 0 || !strings.Contains(f.stdout.String(), `"outcome":"observed"`)) {
				t.Fatalf("exact state readback failed: exit=%d output=%s", code, f.stdout.String())
			}
			f.checkNoCredentialOutput(t)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 1 {
				t.Fatalf("mutation attempts=%d want=1", f.writes)
			}
		})
	}
}

func TestMRNativeUnavailableNeverFallsBack(t *testing.T) {
	f := newMRNativeFixture(t, "opened")
	f.keyring.err = auth.ErrKeyringUnavailable
	if code := Run(context.Background(), f.args("close"), f.deps); code != 3 {
		t.Fatalf("exit=%d output=%s", code, f.stdout.String())
	}
	if len(f.server.Requests()) != 0 {
		t.Fatal("missing native credential performed provider work")
	}
	f.checkNoCredentialOutput(t)
}

func TestMRNativeCreationMetadataUsesOneNativeIdentity(t *testing.T) {
	f := newMRNativeFixture(t, "opened")
	f.ensure = true
	f.ensureRecord = ensureMR(11, "Draft: title", "body")
	f.ensureRecord.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
	f.ensureRecord.Draft = true
	f.ensureRecord.Assignees = []mrMetadataIdentity{{ID: 7}}
	f.ensureRecord.Reviewers = []mrMetadataIdentity{{ID: 8}}
	f.ensureRecord.Milestone = &mrMetadataIdentity{ID: 9}
	f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 7 || input["title"] != "Draft: title" || input["milestone_id"] != float64(9) {
			t.Error("native creation changed its typed metadata payload")
		}
		for _, field := range []struct {
			key string
			id  float64
		}{{"assignee_ids", 7}, {"reviewer_ids", 8}} {
			ids, ok := input[field.key].([]any)
			if !ok || len(ids) != 1 || ids[0] != field.id {
				t.Errorf("native creation did not preserve %s", field.key)
			}
		}
		return false
	}
	args := replaceArg(ensureArgs(t, "title", "body"), "gitlab.com", mrWriteTestHost)
	args = append(args, "--auth-source=native", "--assignee-id", "7", "--reviewer-id", "8", "--milestone-id", "9", "--draft")
	if code := Run(context.Background(), args, f.deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, f.stdout.String())
	}
	f.checkNoCredentialOutput(t)
	if !strings.Contains(f.stdout.String(), `"backend":"native"`) || !strings.Contains(f.stdout.String(), `"action":"created"`) {
		t.Fatalf("native ensure receipt=%s", f.stdout.String())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writes != 1 {
		t.Fatalf("native creation attempts=%d", f.writes)
	}
}

func TestMRNativeSelectorsRefuseBeforeAnyDependency(t *testing.T) {
	base := append(mrWriteArgs("close", "opened"), "--auth-source", "native")
	for _, args := range [][]string{
		removeFlag(base, "--auth-source", true), replaceArg(base, "native", "official"), appendCopy(base, "--auth-source", "native"),
		removeFlag(base, "--hostname", true), removeFlag(base, "--repo", true),
		{"mr", "merge", "42", "--auth-source", "native"}, {"mr", "view", "42", "--auth-source", "native"},
	} {
		var stdout bytes.Buffer
		deps := Dependencies{Runtime: productRuntimeNoDiscovery(t, &stdout), NewDelegate: func() delegateClient { t.Fatal("invalid native selector constructed a delegate"); return nil }}
		deps.Runtime.LookupEnv = func(string) (string, bool) {
			t.Fatal("invalid native selector consulted environment credentials")
			return "", false
		}
		keyring := &mrNativeKeyring{}
		deps.Runtime.Keyring = keyring
		if code := Run(context.Background(), args, deps); code == 0 {
			t.Fatalf("invalid native selector succeeded: %v", args)
		}
		if keyring.gets != 0 || keyring.writes != 0 {
			t.Fatal("invalid native selector consulted keyring")
		}
	}
}
