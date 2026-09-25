package product

import (
	"os"
	"testing"
)

func TestStackExecutableExactRefIdentity(t *testing.T) {
	binary := stackBinary(t, "gl-axi")
	home := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	for _, storage := range []string{"loose", "packed"} {
		for _, ref := range []string{"refs/heads/main", "refs/heads/one", "refs/heads/two", "refs/glab-axi/stacks/demo"} {
			t.Run(storage+"/"+ref, func(t *testing.T) {
				dir := stackFixture(t, "gitlab.example")
				run := func(action string, args ...string) stackEnvelope {
					return stackExec(t, binary, dir, env, stackArgs(action, "gitlab.example", args...)...)
				}
				initArgs := []string{"one", "two", "--base", "main", "--allow-local-metadata"}
				if out := run("init", initArgs...); !out.OK {
					t.Fatal(out.Error)
				}
				metadata := stackGit(t, dir, "show-ref", "--verify", "--hash", "refs/glab-axi/stacks/demo")
				oid := stackGit(t, dir, "show-ref", "--verify", "--hash", ref)
				stackGit(t, dir, "update-ref", "refs/tags/"+ref, oid)
				if storage == "packed" {
					stackGit(t, dir, "pack-refs", "--all", "--prune")
				}
				if out := run("view"); !out.OK || !out.Meta.Complete {
					t.Fatalf("existing exact ref: %+v", out)
				}
				stackGit(t, dir, "update-ref", "-d", ref)
				out := run("view")
				if ref == "refs/glab-axi/stacks/demo" {
					if out.OK || out.Error == nil || out.Error.Code != "not_found" {
						t.Fatalf("tag supplied missing metadata: %+v", out)
					}
					if out = run("init", initArgs...); !out.OK || out.Data.Stack.Action != "registered" {
						t.Fatalf("tag blocked fresh prepared registration: %+v", out)
					}
				} else {
					if !out.OK || out.Meta.Complete || out.Meta.Reason != "local_graph_stale" || len(out.Data.Stack.Branches) != 2 {
						t.Fatalf("missing exact branch: %+v", out)
					}
					s := out.Data.Stack
					switch ref {
					case "refs/heads/main":
						if s.BaseHead != "" || s.Branches[0].Status != "missing_parent" {
							t.Fatalf("tag supplied base: %+v", s)
						}
					case "refs/heads/one":
						if s.Branches[0].Head != "" || s.Branches[0].Status != "missing" || s.Branches[1].Status != "missing_parent" {
							t.Fatalf("tag supplied member/parent: %+v", s)
						}
					case "refs/heads/two":
						if s.Branches[1].Head != "" || s.Branches[1].Status != "missing" {
							t.Fatalf("tag supplied tip: %+v", s)
						}
					}
					if out = run("init", initArgs...); out.OK {
						t.Fatal("registered missing exact branch")
					}
					if got := stackGit(t, dir, "show-ref", "--verify", "--hash", "refs/glab-axi/stacks/demo"); got != metadata {
						t.Fatal("failed registration changed metadata")
					}
				}
				if got := stackGit(t, dir, "show-ref", "--verify", "--hash", "refs/tags/"+ref); got != oid {
					t.Fatal("tag was changed")
				}
			})
		}
	}
}

func TestStackExecutableUnicodeBranchIdentity(t *testing.T) {
	binary := stackBinary(t, "gl-axi")
	dir := stackFixture(t, "gitlab.example")
	home := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	base, first, last := "main\u2003", "topic", "topic\u00a0"
	for _, branch := range []string{base, first, last} {
		stackGit(t, dir, "branch", branch, "main")
	}
	stackGit(t, dir, "switch", "-q", last)
	run := func(action string, args ...string) stackEnvelope {
		return stackExec(t, binary, dir, env, stackArgs(action, "gitlab.example", args...)...)
	}
	if out := run("init", first, last, "--base", base, "--allow-local-metadata"); !out.OK {
		t.Fatal(out.Error)
	}
	assertCurrent := func(out stackEnvelope, want string) {
		t.Helper()
		if !out.OK || !out.Meta.Complete || out.Data.Stack.Current != want || len(out.Data.Stack.Branches) != 2 {
			t.Fatalf("expected current %q: %+v", want, out)
		}
		for _, node := range out.Data.Stack.Branches {
			if node.Current != (node.Name == want) {
				t.Fatalf("incorrect current node: %+v", node)
			}
		}
		if got := stackGit(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/"+want {
			t.Fatalf("HEAD = %q, want %q", got, want)
		}
	}
	assertCurrent(run("view"), last)
	head := stackGit(t, dir, "rev-parse", "HEAD")
	if out := run("checkout", first, "--allow-checkout", "--expected-current", first, "--expected-head", head, "--expected-target", head); out.OK {
		t.Fatalf("accepted trimmed expectation: %+v", out)
	}
	assertCurrent(run("view"), last)
	refs := stackGit(t, dir, "show-ref")
	for _, step := range []struct {
		action, want string
		args         []string
	}{
		{"checkout", first, []string{first}},
		{"checkout", last, []string{last}},
		{"checkout", last, []string{last}},
		{"down", first, nil},
		{"up", last, nil},
		{"bottom", first, nil},
		{"top", last, nil},
		{"trunk", base, nil},
		{"up", last, []string{"2"}},
		{"down", base, []string{"2"}},
	} {
		current := run("view").Data.Stack.Current
		args := append(append([]string{}, step.args...), "--allow-checkout", "--expected-current", current, "--expected-head", head, "--expected-target", head)
		out := run(step.action, args...)
		assertCurrent(out, step.want)
		wantAction := "checked_out"
		if current == step.want {
			wantAction = "unchanged"
		}
		if out.Data.Stack.Action != wantAction {
			t.Fatalf("%s: action = %s, want %s", step.action, out.Data.Stack.Action, wantAction)
		}
		assertCurrent(run("view"), step.want)
	}
	if stackGit(t, dir, "show-ref") != refs {
		t.Fatal("navigation changed refs")
	}
}
