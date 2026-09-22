package product

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Both executable names must reject implicit delegated administration before
// discovering glab. Native success and redirect regressions live alongside the
// full-operation TLS fixture in repo_admin_native_test.go.
func TestRepoAdminExecutableAliasesRequireNativeOptIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no-child fixture uses POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			marker := filepath.Join(dir, "child-attempt")
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\nprintf child > \"$GL_AXI_CHILD_MARKER\"\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"create", "edit", "fork"} {
				args := adminTestArgs(action)
				args = args[:len(args)-2]
				if action == "edit" {
					args = append(args, "--expected-state-file", adminTestFile(t, adminTestBody(adminTestProject("team/sub/project", 101).adminProject)), "--accept-non-atomic", "--visibility", "private")
				}
				command := exec.Command(binary, args...)
				command.Dir = dir
				command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "GL_AXI_CHILD_MARKER=" + marker}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 2 {
					t.Fatalf("%s exit=%v stdout=%s stderr=%s", action, err, stdout.String(), stderr.String())
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("implicit administration constructed a child")
				}
			}
		})
	}
}
