package product

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Exercise persisted CLI inputs across invocations, not a reconstructed snapshot
// or an injected product client. Only the isolated TLS provider is synthetic.
func TestRepoAdminNativeExecutableLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persisted native configuration is unproven on Windows; no-child fixture uses POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			check := func(t *testing.T, f *adminNativeFixture, args []string, wantCode, writes int, outcome string) []byte {
				t.Helper()
				stdout, stderr, runErr := f.runExecutable(t, binary, args)
				code := 0
				if runErr != nil {
					exit, ok := runErr.(*exec.ExitError)
					if !ok {
						t.Fatal(runErr)
					}
					code = exit.ExitCode()
				}
				f.verify(t, writes, stdout, stderr)
				if code != wantCode {
					t.Fatalf("exit=%d want=%d stdout=%s", code, wantCode, stdout)
				}
				if outcome != "" {
					var result struct {
						Data  adminOutput `json:"data"`
						Error struct {
							Receipt adminOutput `json:"receipt"`
						} `json:"error"`
					}
					if err := json.Unmarshal([]byte(stdout), &result); err != nil {
						t.Fatal(err)
					}
					receipt := result.Data.Administration
					if code != 0 {
						receipt = result.Error.Receipt.Administration
					}
					if receipt.Outcome != outcome {
						t.Fatalf("outcome=%s want=%s stdout=%s", receipt.Outcome, outcome, stdout)
					}
				}
				return []byte(stdout)
			}
			t.Run("snapshot-noop-edit-stale-prestate", func(t *testing.T) {
				f := newAdminNativeFixture(t, "edit")
				stdout := check(t, f, []string{"repo", "view", "-R", f.before.Path, "--hostname", "gitlab.example.invalid", "--admin-snapshot", "--auth-source", "native", "--format", "json"}, 0, 0, "")
				var snapshot struct {
					Data struct {
						Snapshot json.RawMessage `json:"admin_snapshot"`
					} `json:"data"`
				}
				if err := json.Unmarshal(stdout, &snapshot); err != nil {
					t.Fatal(err)
				}
				prestate := adminTestFile(t, snapshot.Data.Snapshot)
				args := append(adminTestArgs("edit"), "--expected-state-file", prestate, "--accept-non-atomic", "--description-file", adminTestFile(t, []byte("old")))
				check(t, f, args, 0, 0, "unchanged")
				args[len(args)-1] = adminTestFile(t, []byte("new"))
				check(t, f, args, 0, 1, "completed")
				// Reusing the old snapshot must not perform another PUT, even
				// though the desired description now equals provider state.
				check(t, f, args, 6, 1, "")
			})
			t.Run("wrong-account-refused", func(t *testing.T) {
				f := newAdminNativeFixture(t, "create")
				f.wrongAccount = true
				check(t, f, f.args(t), 9, 0, "")
			})
			for _, action := range []string{"create", "edit", "fork"} {
				t.Run(action+"-uncertain-write-no-retry", func(t *testing.T) {
					f := newAdminNativeFixture(t, action)
					f.mutationStatus = 500
					code, outcome := 6, "ambiguous"
					if action == "edit" {
						code, outcome = 0, "completed"
					}
					check(t, f, f.args(t), code, 1, outcome)
				})
			}
			for _, state := range []struct {
				status  string
				outcome string
				code    int
			}{
				{"started", "in_progress", 0},
				{"finished", "completed", 0},
				{"failed", "failed", 8},
				{"invented", "ambiguous", 6},
			} {
				t.Run("fork-"+state.status, func(t *testing.T) {
					f := newAdminNativeFixture(t, "fork")
					f.after.ImportStatus = state.status
					check(t, f, f.args(t), state.code, 1, state.outcome)
				})
			}
		})
	}
}
