package product

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This fixture perturbs only disposable repositories through real Git. The
// shim injects a competing Git operation at the final metadata-object write,
// after CLI preflight but before its ref transaction. No product hook is used.
func TestStackExecutableExactRefRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deterministic Git interleaving fixture")
	}
	binary := stackBinary(t, "gl-axi")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	for _, mode := range []string{"ordinary-stable", "ordinary-different-oid", "symbolic-same-oid", "symbolic-different-oid", "symbolic-before-preflight", "symbolic-after-prepare"} {
		t.Run(mode, func(t *testing.T) {
			dir := stackFixture(t, "gitlab.example")
			target := "one"
			if mode == "symbolic-different-oid" {
				target = "two"
			}
			stackGit(t, dir, "branch", "alias", target)
			if mode == "symbolic-before-preflight" {
				stackGit(t, dir, "symbolic-ref", "refs/heads/one", "refs/heads/alias")
			}
			tools := t.TempDir()
			mutation := ""
			switch mode {
			case "ordinary-different-oid":
				mutation = quote(git) + " update-ref refs/heads/one " + stackGit(t, dir, "rev-parse", "two")
			case "symbolic-same-oid", "symbolic-different-oid":
				mutation = quote(git) + " symbolic-ref refs/heads/one refs/heads/alias"
			}
			lockedAttempt := ""
			if mode == "symbolic-after-prepare" {
				lockedAttempt = fmt.Sprintf("if [ -e .git/refs/heads/one.lock ] && [ ! -e .git/race-attempt ]; then\n if %s symbolic-ref refs/heads/one refs/heads/alias 2>.git/race-diagnostic; then echo 0 >.git/race-attempt; else echo $? >.git/race-attempt; fi\nfi\n", quote(git))
			}
			script := fmt.Sprintf("#!/bin/sh\nset -eu\n%sfor arg do\n if [ \"$arg\" = hash-object ]; then\n  %s\n fi\ndone\nexec %s \"$@\"\n", lockedAttempt, mutation+"\n  :", quote(git))
			if err = os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			env := []string{"PATH=" + tools + ":" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
			out := stackExec(t, binary, dir, env, stackArgs("init", "gitlab.example", "one", "two", "--base", "main", "--allow-local-metadata")...)
			wantSuccess := mode == "ordinary-stable" || mode == "symbolic-after-prepare"
			if mode == "symbolic-after-prepare" {
				attempt, err := os.ReadFile(filepath.Join(dir, ".git", "race-attempt"))
				if err != nil || strings.TrimSpace(string(attempt)) != "1" {
					t.Fatalf("prepared Git lock did not refuse competitor: %q %v", attempt, err)
				}
			}
			cmd := exec.Command(git, "show-ref", "--verify", "refs/glab-axi/stacks/demo")
			cmd.Dir = dir
			published := cmd.Run() == nil
			t.Logf("mode=%s CLI_success=%t metadata_published=%t", mode, out.OK, published)
			if out.OK != wantSuccess || published != wantSuccess {
				t.Fatalf("exact ordinary-ref contract violated: %+v", out)
			}
		})
	}
}
