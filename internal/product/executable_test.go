package product

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Executables are immutable fixture inputs, not test-owned mutable state. Keep
// each real alias/build mode for this test process only; homes, repos, provider
// responses and transcripts continue to belong to individual tests.
var productExecutables struct {
	sync.Mutex
	dir    string
	paths  map[string]string
	errors map[string]error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if productExecutables.dir != "" {
		if err := os.RemoveAll(productExecutables.dir); err != nil {
			fmt.Fprintln(os.Stderr, "remove executable fixtures:", err)
			code = 1
		}
	}
	os.Exit(code)
}

func productTestExecutable(t *testing.T, program string, trimpath bool) string {
	t.Helper()
	if program != "gl-axi" && program != "glab-axi" {
		t.Fatalf("unsupported product executable %q", program)
	}
	productExecutables.Lock()
	defer productExecutables.Unlock()
	key := fmt.Sprintf("%s-trimpath-%t", program, trimpath)
	if path := productExecutables.paths[key]; path != "" {
		return path
	}
	if err := productExecutables.errors[key]; err != nil {
		t.Fatal(err)
	}
	if productExecutables.dir == "" {
		dir, err := os.MkdirTemp("", "gl-axi-executable-tests-")
		if err != nil {
			t.Fatal(err)
		}
		productExecutables.dir = dir
		productExecutables.paths = map[string]string{}
		productExecutables.errors = map[string]error{}
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(productExecutables.dir, key)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, program)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	args := []string{"build", "-p", "1"}
	if trimpath {
		args = append(args, "-trimpath")
	}
	args = append(args, "-o", path, "./cmd/"+program)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		failure := fmt.Errorf("build %s: %w: %s", program, err, output)
		productExecutables.errors[key] = failure
		t.Fatal(failure)
	}
	productExecutables.paths[key] = path
	return path
}

func TestProductExecutableSurvivesFixtureOwners(t *testing.T) {
	for _, program := range []string{"gl-axi", "glab-axi"} {
		for _, trimpath := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", program, trimpath), func(t *testing.T) {
				var first string
				t.Run("first-owner", func(t *testing.T) {
					first = productTestExecutable(t, program, trimpath)
				})
				t.Run("next-owner", func(t *testing.T) {
					second := productTestExecutable(t, program, trimpath)
					if first != second {
						t.Fatalf("rebuilt identical executable: %s != %s", first, second)
					}
					out, err := exec.Command(second, "--version").CombinedOutput()
					if err != nil || string(out) != program+" 0.2.0 (contract glab-axi/v1)\n" {
						t.Fatalf("fixture lifetime/alias: %v %s", err, out)
					}
				})
			})
		}
	}
}
