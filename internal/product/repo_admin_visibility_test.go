package product

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func TestRepoAdminForkVisibilityDriftAndProviderRestriction(t *testing.T) {
	for _, scenario := range []string{"source-drift", "namespace-restriction"} {
		t.Run(scenario, func(t *testing.T) {
			state := adminTestForkState()
			state.before.Visibility = "public"
			state.after.Visibility = "private"
			delegate := state.delegate()
			base := delegate.doFunc
			sourceReads := 0
			delegate.doFunc = func(ctx context.Context, request glab.Request) (glab.Response, error, bool) {
				response, err, handled := base(ctx, request)
				if request.Operation == adminTestOpProject && request.Repo == state.before.Path {
					sourceReads++
					if scenario == "source-drift" && sourceReads == 2 {
						changed := state.before
						changed.Visibility = "private"
						response.Body = adminTestBody(changed)
					}
				}
				return response, err, handled
			}
			args := adminTestArgs("fork")
			for i, arg := range args {
				if arg == "--visibility" {
					args[i+1] = "internal"
				}
			}
			stdout, _, deps, closeFixture := adminTestNativeDeps(t, delegate)
			code := adminTestRun(context.Background(), args, deps, closeFixture)
			var result struct {
				Error struct {
					Code      uxv1.Code   `json:"code"`
					Retryable bool        `json:"retryable"`
					Receipt   adminOutput `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if code != 6 || sourceReads != 2 || result.Error.Retryable {
				t.Fatalf("exit=%d sourceReads=%d output=%s", code, sourceReads, stdout)
			}
			if scenario == "source-drift" {
				if result.Error.Code != uxv1.CodeConflict || state.writes != 0 || len(delegate.inputBodies) != 0 {
					t.Fatalf("source drift was not refused before POST: writes=%d %s", state.writes, stdout)
				}
				return
			}
			r := result.Error.Receipt.Administration
			if result.Error.Code != uxv1.CodeAmbiguousCreate || state.writes != 1 || !r.MutationAttempted || r.Outcome != "ambiguous" || r.Project == nil || r.Project.Visibility != "private" {
				t.Fatalf("provider restriction lost single-write ambiguity: writes=%d %s", state.writes, stdout)
			}
		})
	}
}

// Exercise both shipped entry points against a provider which clamps fork
// visibility, rather than pretending an impossible broader fork can succeed.
func TestRepoAdminForkVisibilityExecutableTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persisted native configuration is unproven on Windows; no-child fixture uses POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	levels := []string{"private", "internal", "public"}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for sourceIndex, source := range levels {
				for requestedIndex, requested := range levels {
					t.Run(source+"-to-"+requested, func(t *testing.T) {
						f := newAdminNativeFixture(t, "fork")
						f.before.Visibility = source
						// Pinned GitLab v19.0.0 fork service chooses the minimum
						// source/namespace/requested level. This namespace imposes
						// no additional restriction; other policy remains a residual.
						f.after.Visibility = levels[min(sourceIndex, requestedIndex)]
						args := f.args(t)
						for i, arg := range args {
							if arg == "--visibility" {
								args[i+1] = requested
							}
						}
						stdout, stderr, runErr := f.runExecutable(t, binary, args)
						var result struct {
							Data  adminOutput `json:"data"`
							Error uxv1.Error  `json:"error"`
							Meta  uxv1.Meta   `json:"meta"`
						}
						if err := json.Unmarshal([]byte(stdout), &result); err != nil {
							t.Fatal(err)
						}
						if requestedIndex > sourceIndex {
							exit, ok := runErr.(*exec.ExitError)
							if !ok || exit.ExitCode() != 9 || result.Error.Code != uxv1.CodeSafety || result.Error.Retryable {
								t.Fatalf("broader visibility was not refused before creation: run=%v stdout=%s stderr=%s", runErr, stdout, stderr)
							}
							wires := f.verify(t, 0, stdout, stderr)
							if len(wires) == 0 || f.applied {
								t.Fatal("expected source preflight without a created destination")
							}
							return
						}
						if runErr != nil {
							t.Fatalf("allowed fork failed: %v stdout=%s stderr=%s", runErr, stdout, stderr)
						}
						wires := f.verify(t, 1, stdout, stderr)
						for _, wire := range wires {
							if wire.Method == "POST" && wire.Body["visibility"] != requested {
								t.Fatalf("requested visibility changed in payload: %v", wire.Body)
							}
						}
						r := result.Data.Administration
						if result.Meta.Backend != "native" || result.Meta.Complete || !r.MutationAttempted || r.Outcome != "accepted" || r.ImportStatus != "scheduled" || r.SourceID != f.before.ID || r.Project == nil || *r.Project != f.after.adminProject {
							t.Fatalf("allowed fork lost its exact asynchronous receipt: %s", stdout)
						}
					})
				}
			}
		})
	}
}
