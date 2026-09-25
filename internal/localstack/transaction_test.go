package localstack

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPreparedMetadataCancellationReleasesGitLocks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deterministic process interleaving")
	}
	dir, rec := fixture(t)
	r := openFixture(t, dir, true)
	snap, err := r.Inspect(rec)
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	marker := filepath.Join(dir, ".git", "prepared-read")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	// A slow read after prepare makes caller cancellation deterministic. The
	// transaction process itself still executes the real installed Git.
	script := fmt.Sprintf("#!/bin/sh\nset -eu\nfor arg do\n if [ \"$arg\" = symbolic-ref ] && [ -e .git/refs/heads/one.lock ]; then\n  : > %s\n  exec sleep 10\n fi\ndone\nexec %s \"$@\"\n", quote(marker), quote(git))
	if err = os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.ctx = ctx
	finished := make(chan error, 1)
	go func() { _, e := r.Save(nil, "", rec, snap); finished <- e }()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	observed := false
	for !observed {
		select {
		case <-deadline.C:
			cancel()
			t.Fatal("prepared read checkpoint not reached")
		case <-tick.C:
			_, e := os.Stat(marker)
			observed = e == nil
		case e := <-finished:
			t.Fatalf("transaction ended before cancellation: %v", e)
		}
	}
	cancel()
	select {
	case e := <-finished:
		if e == nil {
			t.Fatal("cancelled prepared publication succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("prepared transaction did not stop")
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/one", "refs/heads/two", "refs/glab-axi/stacks/demo"} {
		if _, e := os.Stat(filepath.Join(dir, ".git", filepath.FromSlash(ref)+".lock")); !os.IsNotExist(e) {
			t.Fatalf("Git lock retained after graceful abort: %s %v", ref, e)
		}
	}
	if _, e := os.Stat(filepath.Join(dir, ".git", "refs", "glab-axi", "stacks", "demo")); !os.IsNotExist(e) {
		t.Fatal("cancelled transaction published metadata")
	}
	if got := testGit(t, dir, "rev-parse", "one"); got != snap.Heads["one"] {
		t.Fatal("cancelled transaction changed branch")
	}
}
