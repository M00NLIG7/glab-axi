package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/testgitlab"
)

func stackBinary(t *testing.T, name string) string {
	t.Helper()
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, "./cmd/"+name)
	cmd.Dir = root
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build %v %s", e, out)
	}
	return path
}
func stackExec(t *testing.T, binary, dir string, env []string, args ...string) stackEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var out stackEnvelope
	if e := json.Unmarshal(stdout.Bytes(), &out); e != nil {
		t.Fatalf("args=%v decode=%v run=%v stdout=%s stderr=%s", args, e, err, stdout.String(), stderr.String())
	}
	if (err == nil) != out.OK || stderr.Len() != 0 {
		t.Fatalf("exit %v output %s stderr %s", err, stdout.String(), stderr.String())
	}
	return out
}
func TestStackExecutableAliases(t *testing.T) {
	for _, name := range []string{"gl-axi", "glab-axi"} {
		t.Run(name, func(t *testing.T) {
			binary := stackBinary(t, name)
			dir := stackFixture(t, "gitlab.example")
			home := t.TempDir()
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
			// Retained local remote is deliberately never selected by the product.
			remote := filepath.Join(t.TempDir(), "remote.git")
			stackGit(t, dir, "init", "--bare", remote)
			stackGit(t, dir, "remote", "add", "archive", remote)
			stackGit(t, dir, "push", "archive", "main", "one")
			refs := stackGit(t, dir, "show-ref", "--heads")
			remoteRefs := stackGit(t, remote, "show-ref")
			run := func(action string, args ...string) stackEnvelope {
				return stackExec(t, binary, dir, env, stackArgs(action, "gitlab.example", args...)...)
			}
			if out := run("init", "one", "two", "--base", "main", "--allow-local-metadata"); !out.OK {
				t.Fatal(out.Error)
			}
			if stackGit(t, dir, "branch", "--show-current") != "two" {
				t.Fatal("init switched current branch")
			}
			if out := run("init", "one", "two", "--base", "main", "--allow-local-metadata"); !out.OK || out.Data.Stack.Action != "unchanged" {
				t.Fatalf("replay %+v", out)
			}
			if out := run("view", "--limit", "1"); !out.OK || out.Meta.Complete || !out.Meta.Truncated {
				t.Fatalf("bounded view %+v", out)
			}
			for _, step := range []struct {
				action, want string
				args         []string
			}{{"down", "one", nil}, {"trunk", "main", nil}, {"up", "two", []string{"2"}}, {"bottom", "one", nil}, {"top", "two", nil}, {"checkout", "main", []string{"main"}}} {
				current := stackGit(t, dir, "branch", "--show-current")
				args := append(append([]string{}, step.args...), "--allow-checkout", "--expected-current", current, "--expected-head", stackGit(t, dir, "rev-parse", current), "--expected-target", stackGit(t, dir, "rev-parse", step.want))
				if out := run(step.action, args...); !out.OK || out.Data.Stack.Current != step.want {
					t.Fatalf("%+v output %+v", step, out)
				}
			}
			bad := []string{"one", "--allow-checkout", "--expected-current", "main", "--expected-head", stackGit(t, dir, "rev-parse", "main"), "--expected-target", stackGit(t, dir, "rev-parse", "one")}
			_ = os.WriteFile(filepath.Join(dir, "unsaved"), []byte("keep local untracked work"), 0600)
			if out := run("checkout", bad...); out.OK {
				t.Fatal("dirty checkout succeeded")
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "unsaved")); string(b) != "keep local untracked work" {
				t.Fatal("lost untracked work")
			}
			stackGit(t, dir, "checkout", "--detach", "-q", "main")
			if out := run("checkout", bad...); out.OK {
				t.Fatal("detached checkout succeeded")
			}
			if out := run("view"); !out.OK || out.Data.Stack.Current != "" {
				t.Fatalf("detached view %+v", out)
			}
			if out := run("init", "one", "two", "--base", "two", "--allow-local-metadata"); out.OK {
				t.Fatal("cycle accepted")
			}
			if out := run("init", "one", "main", "--base", "two", "--allow-local-metadata"); out.OK {
				t.Fatal("registration collision accepted")
			}
			if out := run("init", "one", "--base=main\ncreate refs/heads/injected", "--allow-local-metadata"); out.OK {
				t.Fatal("ref injection accepted")
			}
			wrong := stackArgs("view", "other.example")
			if out := stackExec(t, binary, dir, env, wrong...); out.OK {
				t.Fatal("wrong authority accepted")
			}
			if stackGit(t, dir, "show-ref", "--heads") != refs || stackGit(t, remote, "show-ref") != remoteRefs {
				t.Fatal("local or remote refs were changed")
			}
		})
	}
}

// Like the repository's existing official-glab contract tests, this optional
// fixture is activated only by an explicitly selected checksum-pinned binary.
// It uses a private profile, local TLS server and synthetic token, never login.
func TestStackOfficialGlabTLSExecutable(t *testing.T) {
	upstream := os.Getenv("GL_AXI_OFFICIAL_GLAB_TEST_BINARY")
	if upstream == "" {
		upstream = os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
	}
	if upstream == "" {
		t.Skip("set GL_AXI_OFFICIAL_GLAB_TEST_BINARY to pinned glab for offline TLS fixture")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX official-glab path fixture")
	}
	binary := stackBinary(t, "gl-axi")
	for _, mode := range []string{"valid", "equals-branches", "unicode-branches", "wrong-head", "wrong-target", "fork", "drift", "partial-failure"} {
		t.Run(mode, func(t *testing.T) {
			runStackOfficialGlabTLSExecutable(t, binary, upstream, mode)
		})
	}
}

func runStackOfficialGlabTLSExecutable(t *testing.T, binary, upstream, mode string) {
	t.Helper()
	home := t.TempDir()
	var mu sync.Mutex
	host := ""
	base, first, last := "main", "one", "two"
	if mode == "equals-branches" {
		base, first, last = "base=a=b", "feature/a=b", "feature/c=d=e="
	} else if mode == "unicode-branches" {
		base, first, last = "main\u2003", "topic", "topic\u00a0"
	}
	heads := map[string]string{}
	count := 0
	server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/v4/projects/team/repo") {
			http.Error(w, "unexpected", 400)
			return
		}
		if r.Header.Get("PRIVATE-TOKEN") != strings.Join([]string{"synthetic", "stack", "token"}, "-") {
			http.Error(w, "no synthetic token", 401)
			return
		}
		count++
		if strings.HasSuffix(r.URL.Path, "/team/repo") {
			_ = json.NewEncoder(w).Encode(mergeProject{ID: 7, PathWithNamespace: "team/repo", WebURL: "https://" + host + "/team/repo"})
			return
		}
		if r.URL.Query().Get("with_merge_status_recheck") != "true" {
			http.Error(w, "wrong typed query", 400)
			return
		}
		iid := int64(11)
		branch, parent := first, base
		if strings.HasSuffix(r.URL.Path, "/12") {
			iid = 12
			branch, parent = last, first
		}
		mr := upstreamMR{ID: 100 + iid, IID: iid, State: "opened", WebURL: canonicalMRURL(host, "team/repo", iid), SourceProjectID: 7, TargetProjectID: 7, SourceBranch: branch, TargetBranch: parent, SHA: heads[branch]}
		switch mode {
		case "wrong-head":
			mr.SHA = strings.Repeat("a", 40)
		case "wrong-target":
			mr.TargetBranch = "another"
		case "fork":
			mr.SourceProjectID = 8
		case "drift":
			if count > 3 {
				mr.ID++
			}
		case "partial-failure":
			if iid == 12 {
				http.Error(w, "synthetic failure", 502)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(mr)
	}))
	defer server.Close()
	mu.Lock()
	host = "gitlab.stack.example"
	mu.Unlock()
	dir := stackFixture(t, host)
	if base != "main" {
		stackGit(t, dir, "branch", "-m", "main", base)
		stackGit(t, dir, "branch", "-m", "one", first)
		stackGit(t, dir, "branch", "-m", "two", last)
	}
	before := stackGit(t, dir, "show-ref", "--heads")
	mu.Lock()
	for _, b := range []string{first, last} {
		heads[b] = stackGit(t, dir, "rev-parse", b)
	}
	mu.Unlock()
	ca, e := server.CAFile(home)
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, "config")
	if e = os.Mkdir(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    api_host: %q\n    api_protocol: https\n    ca_cert: %q\n", host, strings.TrimPrefix(server.HTTP.URL, "https://"), ca)), 0600); e != nil {
		t.Fatal(e)
	}
	tools := filepath.Join(home, "bin")
	if e = os.Mkdir(tools, 0700); e != nil {
		t.Fatal(e)
	}
	upstream, e = filepath.Abs(upstream)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(upstream, filepath.Join(tools, "glab")); e != nil {
		t.Fatal(e)
	}
	env := []string{"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + strings.Join([]string{"synthetic", "stack", "token"}, "-"), "NO_PROXY=*", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	args := stackArgs("link", host, first, last, "--base", base, "--mr", first+"=11", "--mr", last+"=12", "--allow-local-metadata")
	out := stackExec(t, binary, dir, env, args...)
	body, _ := json.Marshal(out)
	t.Logf("CLI stack link %q: %s", mode, body)
	success := mode == "valid" || mode == "equals-branches" || mode == "unicode-branches"
	if out.OK != success {
		t.Fatalf("official TLS %+v requests=%d", out, len(server.Requests()))
	}
	if success {
		if out.Meta.UpstreamVersion != glab.SupportedVersion || out.Data.Stack.MRObservation != "observed" || len(server.Requests()) != 6 {
			t.Fatalf("expected two exact observation passes: %+v requests=%d", out, len(server.Requests()))
		}
		if again := stackExec(t, binary, dir, env, args...); !again.OK || again.Data.Stack.Action != "unchanged" {
			t.Fatalf("replay: %+v", again)
		}
		view := stackExec(t, binary, dir, env, stackArgs("view", host, "--mrs")...)
		body, _ = json.Marshal(view)
		t.Logf("CLI stack view --mrs: %s", body)
		if !view.OK || !view.Meta.Complete || view.Data.Stack.Base != base || view.Data.Stack.Current != last || len(view.Data.Stack.Branches) != 2 {
			t.Fatalf("bound view: %+v", view)
		}
		for i, name := range []string{first, last} {
			node := view.Data.Stack.Branches[i]
			if node.Name != name || node.MR != int64(11+i) || node.Parent != []string{base, first}[i] {
				t.Fatalf("binding identity: %+v", node)
			}
		}
	} else {
		view := stackExec(t, binary, dir, env, stackArgs("view", host)...)
		if view.OK || view.Error == nil || view.Error.Code != "not_found" {
			t.Fatalf("failed binding published metadata: %+v", view)
		}
		t.Logf("after refused link: metadata not_found")
	}
	if stackGit(t, dir, "show-ref", "--heads") != before {
		t.Fatal("binding changed local branch tips")
	}
	for _, req := range server.Requests() {
		t.Logf("provider request: %s %s", req.Method, req.URL)
		if req.Method != "GET" || len(req.Body) != 0 {
			t.Fatal("provider mutation attempted")
		}
	}
}
