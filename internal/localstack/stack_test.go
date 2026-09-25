package localstack

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func fixture(t *testing.T) (string, Record) {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	testGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "base")
	testGit(t, dir, "remote", "add", "origin", "https://gitlab.example/team/repo.git")
	testGit(t, dir, "checkout", "-qb", "one")
	testGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "one")
	testGit(t, dir, "checkout", "-qb", "two")
	testGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "two")
	return dir, Record{Version: Version, Host: "gitlab.example", Project: "team/repo", Base: "main", Branches: []Branch{{Name: "one"}, {Name: "two"}}}
}
func openFixture(t *testing.T, dir string, mutate bool) *Repo {
	t.Helper()
	r, e := Open(context.Background(), dir, "gitlab.example", "team/repo", "demo", mutate)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(r.Close)
	return r
}
func register(t *testing.T, r *Repo, rec Record) (Snapshot, string) {
	t.Helper()
	s, e := r.Inspect(rec)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(nil, "", rec, s); e != nil {
		t.Fatal(e)
	}
	_, oid, e := r.Load()
	if e != nil {
		t.Fatal(e)
	}
	return s, oid
}
func TestMetadataReplayCASAndPreservation(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	before := testGit(t, dir, "show-ref", "--heads")
	head := testGit(t, dir, "symbolic-ref", "HEAD")
	if e := os.WriteFile(filepath.Join(dir, "untracked"), []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	snap, oid := register(t, r, rec)
	old, readOID, e := r.Load()
	if e != nil || oid != readOID || !reflect.DeepEqual(old, &rec) {
		t.Fatalf("load=%+v %s %v", old, readOID, e)
	}
	if action, e := r.Save(old, oid, rec, snap); e != nil || action != "unchanged" {
		t.Fatalf("replay=%s %v", action, e)
	}
	next := rec
	next.Branches = append([]Branch(nil), rec.Branches...)
	next.Branches[0].MR = 7
	if _, e = r.Save(old, oid, next, snap); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(old, oid, rec, snap); e == nil {
		t.Fatal("stale metadata CAS succeeded")
	}
	if testGit(t, dir, "show-ref", "--heads") != before || testGit(t, dir, "symbolic-ref", "HEAD") != head {
		t.Fatal("registration changed branches")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "untracked")); string(b) != "preserve" {
		t.Fatal("lost untracked work")
	}
}
func TestMetadataDetectsConcurrentBranchChangeAndLocks(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	s, e := r.Inspect(rec)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Open(context.Background(), dir, rec.Host, rec.Project, "another", true); e == nil {
		t.Fatal("concurrent stack writer acquired lock")
	}
	testGit(t, dir, "update-ref", "refs/heads/one", s.Heads["two"])
	if _, e = r.Save(nil, "", rec, s); e == nil {
		t.Fatal("stale branch CAS succeeded")
	}
	old, _, e := r.Load()
	if e != nil || old != nil {
		t.Fatal("partial metadata published")
	}
	// A ref transaction must refuse while another Git writer owns a ref lock.
	s, e = r.Inspect(rec)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, ".git", "refs", "heads", "one.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Save(nil, "", rec, s); e == nil {
		t.Fatal("ignored Git ref lock")
	}
}
func TestMetadataRejectsSymbolicRefRace(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	s, err := r.Inspect(rec)
	if err != nil {
		t.Fatal(err)
	}
	// Alias the same object: OID-only dereferencing must not hide this change.
	testGit(t, dir, "branch", "alias", "one")
	testGit(t, dir, "symbolic-ref", "refs/heads/one", "refs/heads/alias")
	if _, err = r.Save(nil, "", rec, s); err == nil {
		t.Fatal("symbolic ref race accepted")
	}
}

func TestMetadataRejectsSymbolicMetadataReplacement(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	snap, oid := register(t, r, rec)
	testGit(t, dir, "update-ref", "refs/glab-axi/alias", oid)
	testGit(t, dir, "symbolic-ref", r.ref, "refs/glab-axi/alias")
	if _, err := r.Save(&rec, oid, rec, snap); err == nil {
		t.Fatal("symbolic metadata replacement accepted")
	}
	if got := testGit(t, dir, "symbolic-ref", r.ref); got != "refs/glab-axi/alias" {
		t.Fatal("concurrent symbolic ref overwritten")
	}
}

func TestAmbiguousMergeBaseRefused(t *testing.T) {
	dir, rec := fixture(t)
	root := testGit(t, dir, "rev-parse", "main")
	tree := testGit(t, dir, "rev-parse", "main^{tree}")
	a := testGit(t, dir, "commit-tree", tree, "-p", root, "-m", "a")
	b := testGit(t, dir, "commit-tree", tree, "-p", root, "-m", "b")
	left := testGit(t, dir, "commit-tree", tree, "-p", a, "-p", b, "-m", "left merge")
	right := testGit(t, dir, "commit-tree", tree, "-p", b, "-p", a, "-m", "right merge")
	testGit(t, dir, "update-ref", "refs/heads/one", left)
	testGit(t, dir, "update-ref", "refs/heads/two", right)
	if bases := testGit(t, dir, "merge-base", "--all", "one", "two"); len(strings.Fields(bases)) != 2 {
		t.Fatal("fixture lacks ambiguous bases")
	}
	r := openFixture(t, dir, true)
	snap, err := r.Inspect(rec)
	if err != nil || snap.Valid || snap.Nodes[1].Status != "diverged" {
		t.Fatalf("ambiguous graph: %+v %v", snap, err)
	}
	if _, err = r.Save(nil, "", rec, snap); err == nil {
		t.Fatal("ambiguous graph registered")
	}
}

func TestCheckoutGuardedAndPreservesUnlandedRefs(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	snap, oid := register(t, r, rec)
	refs := testGit(t, dir, "show-ref", "--heads")
	if _, e := r.Checkout(rec, oid, snap, "one", "two", snap.Heads["two"], snap.Heads["one"]); e != nil {
		t.Fatal(e)
	}
	if testGit(t, dir, "branch", "--show-current") != "one" || testGit(t, dir, "show-ref", "--heads") != refs {
		t.Fatal("navigation lost unlanded work")
	}
	snap, e := r.Inspect(rec)
	if e != nil {
		t.Fatal(e)
	}
	if a, e := r.Checkout(rec, oid, snap, "one", "one", snap.Heads["one"], snap.Heads["one"]); e != nil || a != "unchanged" {
		t.Fatalf("no-op %s %v", a, e)
	}
	if _, e := r.Checkout(rec, oid, snap, "two", "one", snap.Heads["main"], snap.Heads["two"]); e == nil {
		t.Fatal("wrong expectation accepted")
	}
}
func TestCheckoutRefusesUnsafeLocalStates(t *testing.T) {
	for _, mode := range []string{"untracked", "ignored", "dirty", "staged", "detached", "rebase", "index-lock", "filter", "sparse", "assume-unchanged", "skip-worktree", "other-worktree", "diverged"} {
		t.Run(mode, func(t *testing.T) {
			dir, rec := fixture(t)
			if e := os.WriteFile(filepath.Join(dir, "tracked"), []byte("base"), 0600); e != nil {
				t.Fatal(e)
			}
			testGit(t, dir, "add", "tracked")
			testGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-qm", "tracked")
			r := openFixture(t, dir, true)
			_, oid := register(t, r, rec)
			switch mode {
			case "untracked":
				_ = os.WriteFile(filepath.Join(dir, "new"), []byte("local"), 0600)
			case "ignored":
				_ = os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("ignored\n"), 0600)
				_ = os.WriteFile(filepath.Join(dir, "ignored"), []byte("local"), 0600)
			case "dirty", "staged":
				_ = os.WriteFile(filepath.Join(dir, "tracked"), []byte("local"), 0600)
				if mode == "staged" {
					testGit(t, dir, "add", "tracked")
				}
			case "detached":
				testGit(t, dir, "checkout", "--detach", "-q")
			case "rebase":
				_ = os.Mkdir(filepath.Join(dir, ".git", "rebase-apply"), 0700)
			case "index-lock":
				_ = os.WriteFile(filepath.Join(dir, ".git", "index.lock"), nil, 0600)
			case "filter":
				testGit(t, dir, "config", "filter.danger.clean", "touch never")
			case "sparse":
				testGit(t, dir, "config", "core.sparseCheckout", "true")
			case "assume-unchanged":
				testGit(t, dir, "update-index", "--assume-unchanged", "tracked")
				_ = os.WriteFile(filepath.Join(dir, "tracked"), []byte("local"), 0600)
			case "skip-worktree":
				testGit(t, dir, "update-index", "--skip-worktree", "tracked")
				_ = os.WriteFile(filepath.Join(dir, "tracked"), []byte("local"), 0600)
			case "other-worktree":
				testGit(t, dir, "worktree", "add", filepath.Join(t.TempDir(), "other"), "one")
			case "diverged":
				testGit(t, dir, "update-ref", "refs/heads/one", testGit(t, dir, "rev-parse", "two"))
			}
			before := testGit(t, dir, "show-ref", "--heads")
			head := testGit(t, dir, "rev-parse", "HEAD")
			content, _ := os.ReadFile(filepath.Join(dir, "tracked"))
			s, e := r.Inspect(rec)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "diverged" { // base is still an ancestor, but the source was changed after the snapshot below.
				s.Heads["one"] = s.Heads["main"]
			}
			if _, e = r.Checkout(rec, oid, s, "one", "two", s.Heads["two"], s.Heads["one"]); e == nil {
				t.Fatal("unsafe checkout succeeded")
			}
			if testGit(t, dir, "rev-parse", "HEAD") != head || testGit(t, dir, "show-ref", "--heads") != before {
				t.Fatal("ref changed on refusal")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "tracked"))
			if string(got) != string(content) {
				t.Fatal("local work was changed")
			}
		})
	}
}
func TestGraphCorruptionBoundsAndCancellation(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, false)
	tests := []Record{rec, rec, rec, rec}
	tests[0].Base = "two"
	tests[1].Branches = []Branch{{Name: "one"}, {Name: "one"}}
	tests[2].Branches = []Branch{{Name: "--bad"}}
	tests[3].Branches = make([]Branch, 33)
	for _, bad := range tests {
		if Validate(bad) == nil {
			t.Fatal("invalid graph accepted")
		}
	}
	testGit(t, dir, "update-ref", "-d", "refs/heads/one")
	snap, e := r.Inspect(rec)
	if e != nil || snap.Valid || snap.Nodes[0].Status != "missing" || snap.Nodes[1].Status != "missing_parent" {
		t.Fatalf("missing evidence: %+v %v", snap, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Open(ctx, dir, rec.Host, rec.Project, "demo", true); e == nil {
		t.Fatal("cancelled operation proceeded")
	}
	for _, bad := range []string{"../escape", "HEAD", "refs/heads/x", "a\nb", "x.lock", "-bad", "x@{1}", strings.Repeat("a", 1025)} {
		if ValidBranch(bad) {
			t.Fatalf("invalid branch accepted: %q", bad)
		}
	}
}
func TestIdentityAndSymbolicRefs(t *testing.T) {
	dir, rec := fixture(t)
	if _, e := Open(context.Background(), dir, rec.Host, "other/repo", "demo", true); e == nil {
		t.Fatal("wrong repo accepted")
	}
	// Ambient Git selectors cannot redirect the chosen repository.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	r := openFixture(t, dir, false)
	testGit(t, dir, "symbolic-ref", "refs/heads/one", "refs/heads/two")
	if _, e := r.Inspect(rec); e == nil {
		t.Fatal("symbolic branch accepted")
	}
	testGit(t, dir, "config", "--add", "remote.origin.url", "https://other.example/team/repo.git")
	if _, e := Open(context.Background(), dir, rec.Host, rec.Project, "demo", false); e == nil {
		t.Fatal("ambiguous origin accepted")
	}
}
func TestDivergedGraphAndCorruptMetadata(t *testing.T) {
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	testGit(t, dir, "checkout", "-q", "main")
	testGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "advanced trunk")
	snap, e := r.Inspect(rec)
	if e != nil || snap.Valid || snap.Nodes[0].Status != "diverged" {
		t.Fatalf("divergence: %+v %v", snap, e)
	}
	if _, e = r.Save(nil, "", rec, snap); e == nil {
		t.Fatal("diverged graph registered")
	}
	for _, body := range []string{`{}`, `{"version":"unknown"}`, strings.Repeat("x", MaxBytes+1)} {
		obj, e := r.git([]byte(body), "hash-object", "-w", "--stdin")
		if e != nil {
			t.Fatal(e)
		}
		testGit(t, dir, "update-ref", r.ref, strings.TrimSpace(obj))
		if _, _, e = r.Load(); e == nil {
			t.Fatal("corrupt metadata accepted")
		}
	}
	copy := rec
	copy.Project = "other/repo"
	b, _ := json.Marshal(copy)
	obj, e := r.git(b, "hash-object", "-w", "--stdin")
	if e != nil {
		t.Fatal(e)
	}
	testGit(t, dir, "update-ref", r.ref, strings.TrimSpace(obj))
	if _, _, e = r.Load(); e == nil {
		t.Fatal("wrong metadata project accepted")
	}
}
