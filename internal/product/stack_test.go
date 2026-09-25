package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/localstack"
	runtimepkg "gl-axi/internal/runtime"
	"gl-axi/internal/testgitlab"
)

func stackGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func stackFixture(t *testing.T, host string) string {
	t.Helper()
	dir := t.TempDir()
	stackGit(t, dir, "init", "-q", "-b", "main")
	stackGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "base")
	stackGit(t, dir, "remote", "add", "origin", "https://"+host+"/team/repo.git")
	for _, b := range []string{"one", "two"} {
		stackGit(t, dir, "checkout", "-qb", b)
		stackGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", b)
	}
	return dir
}
func stackArgs(action, host string, args ...string) []string {
	return append(append([]string{"stack", action}, args...), "--stack", "demo", "--hostname", host, "-R", "team/repo", "--format", "json")
}

type stackEnvelope struct {
	OK    bool        `json:"ok"`
	Data  stackData   `json:"data"`
	Meta  uxv1.Meta   `json:"meta"`
	Error *uxv1.Error `json:"error"`
}

func runStackTest(t *testing.T, ctx context.Context, dir string, d delegateClient, args ...string) stackEnvelope {
	t.Helper()
	var out, stderr bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Cwd: dir, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &stderr}, NewDelegate: func() delegateClient {
		if d == nil {
			t.Fatal("local operation attempted provider access")
		}
		return d
	}}
	code := Run(ctx, args, deps)
	var result stackEnvelope
	if e := json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatalf("%v: %s stderr %s", e, out.String(), stderr.String())
	}
	if (code == 0) != result.OK || stderr.Len() != 0 {
		t.Fatalf("code %d: %s %s", code, out.String(), stderr.String())
	}
	return result
}
func TestStackGrammarContract(t *testing.T) {
	for _, action := range []string{"view", "init", "link", "checkout", "up", "down", "top", "bottom", "trunk"} {
		if _, ok := lookupDefinition([]string{"stack", action}); !ok {
			t.Fatal(action)
		}
	}
	for _, action := range []string{"add", "submit", "push", "sync", "rebase", "unstack", "merge"} {
		if _, e := Parse([]string{"stack", action}); e == nil {
			t.Fatal("unimplemented follow-on accepted: " + action)
		}
	}
	for _, args := range [][]string{
		stackArgs("init", "gitlab.example", "one", "--base", "main"),
		stackArgs("init", "gitlab.example", "one", "one", "--base", "main", "--allow-local-metadata"),
		stackArgs("init", "gitlab.example", "main", "--base", "main", "--allow-local-metadata"),
		stackArgs("init", "gitlab.example", "refs/heads/one", "--base", "main", "--allow-local-metadata"),
		stackArgs("link", "gitlab.example", "one", "two", "--base", "main", "--allow-local-metadata", "--mr", "one=1", "--mr", "two=1"),
		stackArgs("link", "gitlab.example", "one", "two", "--base", "main", "--allow-local-metadata", "--mr", "one=1", "--mr", "unknown=2"),
		stackArgs("view", "gitlab.example", "--auto"),
		stackArgs("view", "gitlab.example", "--yes"),
		stackArgs("checkout", "gitlab.example", "one"),
	} {
		if _, e := Parse(args); e == nil {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
	valid := stackArgs("init", "gitlab.example", "one", "two", "--base", "main", "--allow-local-metadata")
	if _, e := Parse(valid); e != nil {
		t.Fatal(e)
	}
	args := []string{"stack", "view", "--stack", "demo"}
	if _, e := Parse(args); e == nil {
		t.Fatal("implicit target accepted")
	}
	for _, n := range []string{"0", "-1", "01", "33", "99999999999999999999"} {
		args = stackArgs("up", "gitlab.example", n, "--allow-checkout", "--expected-current", "one", "--expected-head", strings.Repeat("a", 40), "--expected-target", strings.Repeat("b", 40))
		if _, e := Parse(args); e == nil {
			t.Fatal("invalid distance " + n)
		}
	}
}
func TestStackLocalPartialViewAndNavigation(t *testing.T) {
	dir := stackFixture(t, "gitlab.example")
	out := runStackTest(t, context.Background(), dir, nil, stackArgs("init", "gitlab.example", "one", "two", "--base", "main", "--allow-local-metadata")...)
	if !out.OK {
		t.Fatal(out.Error)
	}
	out = runStackTest(t, context.Background(), dir, nil, stackArgs("view", "gitlab.example", "--limit", "1")...)
	if !out.OK || out.Meta.Complete || !out.Meta.Truncated || out.Data.Stack.Total != 2 || len(out.Data.Stack.Branches) != 1 {
		t.Fatalf("partial: %+v", out)
	}
	rec := localstack.Record{Base: "main", Branches: []localstack.Branch{{Name: "one"}, {Name: "two"}}}
	for _, c := range []struct {
		action, current, want string
		args                  []string
	}{{"down", "two", "one", nil}, {"up", "main", "two", []string{"2"}}, {"top", "main", "two", nil}, {"bottom", "two", "one", nil}, {"trunk", "one", "main", nil}, {"checkout", "one", "two", []string{"two"}}} {
		got, e := stackDestination(c.action, c.args, rec, c.current)
		if e != nil || got != c.want {
			t.Fatalf("%+v -> %s %v", c, got, e)
		}
	}
	for _, c := range []struct {
		action, current string
		args            []string
	}{{"up", "two", nil}, {"down", "main", nil}, {"checkout", "one", []string{"other"}}, {"top", "", nil}, {"bottom", "other", nil}} {
		if _, e := stackDestination(c.action, c.args, rec, c.current); e == nil {
			t.Fatalf("invalid destination %+v", c)
		}
	}
	stackGit(t, dir, "update-ref", "-d", "refs/heads/one")
	out = runStackTest(t, context.Background(), dir, nil, stackArgs("view", "gitlab.example")...)
	if !out.OK || out.Meta.Complete || out.Data.Stack.Branches[0].Status != "missing" {
		t.Fatalf("missing: %+v", out)
	}
}

// TLS-backed synthetic provider used by the product orchestrator tests. The
// executable/official-glab fixture below separately proves real adapter argv.
type stackTLSDelegate struct {
	fakeDelegate
	server *testgitlab.Server
}

func (d *stackTLSDelegate) Do(ctx context.Context, r glab.Request) (glab.Response, error) {
	path := "/api/v4/projects/" + url.PathEscape(r.Repo)
	switch r.Operation {
	case glab.OpEnsureProject:
	case glab.OpMergeMRView:
		path += fmt.Sprintf("/merge_requests/%d?with_merge_status_recheck=true", r.IID)
	default:
		return glab.Response{}, fmt.Errorf("unexpected operation %s", r.Operation)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", d.server.HTTP.URL+path, nil)
	if e != nil {
		return glab.Response{}, e
	}
	req.Header.Set("PRIVATE-TOKEN", strings.Join([]string{"synthetic", "stack", "token"}, "-"))
	response, e := d.server.HTTP.Client().Do(req)
	if e != nil {
		return glab.Response{}, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		return glab.Response{}, fmt.Errorf("synthetic provider failure")
	}
	return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}, e
}
func TestStackBindingTLSIdentityAndReplay(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-project", "wrong-iid", "wrong-url", "fork", "wrong-source", "wrong-base", "wrong-head", "closed", "merged", "partial-failure", "drift", "local-race", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := stackFixture(t, "gitlab.example")
			heads := map[string]string{"one": stackGit(t, dir, "rev-parse", "one"), "two": stackGit(t, dir, "rev-parse", "two")}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			count := 0
			server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				count++
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					t.Error("provider mutation attempted")
					http.Error(w, "refused", 400)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/team/repo") {
					id := int64(7)
					project := "team/repo"
					if mode == "wrong-project" {
						project = "other/repo"
					}
					_ = json.NewEncoder(w).Encode(mergeProject{ID: id, PathWithNamespace: project, WebURL: "https://gitlab.example/team/repo"})
					return
				}
				iid := int64(11)
				branch, parent := "one", "main"
				if strings.HasSuffix(r.URL.Path, "/12") {
					iid = 12
					branch, parent = "two", "one"
				}
				m := upstreamMR{ID: 100 + iid, IID: iid, State: "opened", WebURL: canonicalMRURL("gitlab.example", "team/repo", iid), SourceProjectID: 7, TargetProjectID: 7, SourceBranch: branch, TargetBranch: parent, SHA: heads[branch]}
				switch mode {
				case "wrong-iid":
					m.IID++
				case "wrong-url":
					m.WebURL = "https://wrong.example/team/repo/-/merge_requests/11"
				case "fork":
					m.SourceProjectID = 8
				case "wrong-source":
					m.SourceBranch = "another"
				case "wrong-base":
					m.TargetBranch = "other"
				case "wrong-head":
					m.SHA = strings.Repeat("a", 40)
				case "closed", "merged":
					m.State = mode
				case "partial-failure":
					if iid == 12 {
						http.Error(w, "partial", 502)
						return
					}
				case "drift":
					if count > 3 {
						m.ID++
					}
				case "local-race":
					if count == 6 {
						stackGit(t, dir, "update-ref", "refs/heads/one", heads["two"])
					}
				case "cancel":
					cancel()
				}
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer server.Close()
			d := &stackTLSDelegate{server: server}
			args := stackArgs("link", "gitlab.example", "one", "two", "--base", "main", "--mr", "one=11", "--mr", "two=12", "--allow-local-metadata")
			before := stackGit(t, dir, "show-ref", "--heads")
			out := runStackTest(t, ctx, dir, d, args...)
			if out.OK != (mode == "valid") {
				t.Fatalf("mode %s output %+v", mode, out)
			}
			if mode == "valid" {
				if out.Data.Stack.MRObservation != "observed" || out.Meta.UpstreamVersion != glab.SupportedVersion {
					t.Fatalf("missing evidence %+v", out)
				}
				again := runStackTest(t, ctx, dir, d, args...)
				if !again.OK || again.Data.Stack.Action != "unchanged" {
					t.Fatalf("replay %+v", again)
				}
				view := runStackTest(t, ctx, dir, d, stackArgs("view", "gitlab.example", "--mrs")...)
				if !view.OK || !view.Meta.Complete {
					t.Fatalf("view %+v", view)
				}
			} else {
				r, e := localstack.Open(context.Background(), dir, "gitlab.example", "team/repo", "demo", false)
				if e != nil {
					t.Fatal(e)
				}
				old, _, e := r.Load()
				r.Close()
				if e != nil || old != nil {
					t.Fatalf("partial metadata published: %+v %v", old, e)
				}
			}
			if mode != "local-race" && stackGit(t, dir, "show-ref", "--heads") != before {
				t.Fatal("branch changed")
			}
			for _, req := range server.Requests() {
				if req.Method != "GET" || string(req.Body) != "" {
					t.Fatal("provider write")
				}
			}
		})
	}
}
