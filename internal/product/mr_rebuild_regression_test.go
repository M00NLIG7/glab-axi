package product

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildMRExecutable(t *testing.T, program string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), program)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-p", "1", "-o", binary, "./cmd/"+program)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	return binary
}

func runMRExecutable(t *testing.T, binary string, f *mrNativeFixture, args []string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/nonexistent", "GL_AXI_CONFIG=" + f.deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + f.keyring.token, "GOMAXPROCS=2"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("CLI exceeded operation bound: %v", ctx.Err())
	}
	if strings.Contains(stdout.String()+stderr.String(), f.keyring.token) {
		t.Fatal("synthetic credential escaped")
	}
	return code, stdout.String()
}

// These end-user regressions exercise the accepted lifecycle exception and the
// pinned provider's title normalization, not a recovered pipeline candidate.
func TestMRRebuildLifecycleAndTitleExecutableTLS(t *testing.T) {
	t.Parallel()
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := buildMRExecutable(t, program)
			for _, tc := range []struct {
				action, state string
				transition    bool
			}{
				{"close", "opened", true}, {"reopen", "closed", true},
				{"close", "closed", false}, {"reopen", "opened", false},
			} {
				t.Run(tc.action+"/"+tc.state, func(t *testing.T) {
					f := newMRNativeFixture(t, tc.state)
					// A stored quick action must never be reprocessed by a state PUT.
					f.configure(func() {
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge_requests/42") {
								record := f.mr()
								record["description"] = "Imported instructions\n/label dangerous"
								_ = json.NewEncoder(w).Encode(record)
								return true
							}
							return false
						}
					})
					code, out := runMRExecutable(t, binary, f, f.args(tc.action))
					wantCode, outcome := 0, "unchanged"
					if tc.transition {
						wantCode, outcome = 2, "refused"
					}
					if code != wantCode || !strings.Contains(out, `"outcome":"`+outcome+`"`) || !strings.Contains(out, `"attempts":0`) {
						t.Errorf("exit=%d want=%d output=%s", code, wantCode, out)
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != 0 || f.state != tc.state {
						t.Errorf("lifecycle caused mutation: writes=%d state=%s", f.writes, f.state)
					}
				})
			}
			for _, draft := range []bool{false, true} {
				title := "  title \t "
				name, want := "plain-title", "title"
				if draft {
					name, want = "draft-title", "Draft:   title"
				}
				t.Run(name, func(t *testing.T) {
					f := newMRNativeFixture(t, "opened")
					created := false
					f.configure(func() {
						f.ensure = true
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							if r.URL.EscapedPath() != mrNativeAPIPath+"/merge_requests" {
								return false
							}
							if r.Method == http.MethodGet && created {
								_ = json.NewEncoder(w).Encode([]upstreamMR{f.ensureRecord})
								return true
							}
							if r.Method == http.MethodPost {
								var input map[string]any
								if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
									t.Error(err)
									w.WriteHeader(400)
									return true
								}
								gotTitle, _ := input["title"].(string)
								f.ensureRecord = ensureMR(11, strings.Trim(gotTitle, "\x00\t\n\v\f\r "), "body")
								f.ensureRecord.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
								f.ensureRecord.Draft = draft
								created = true
							}
							return false
						}
					})
					args := append(replaceArg(ensureArgs(t, title, "body"), "gitlab.com", mrWriteTestHost), "--auth-source", "native")
					if draft {
						args = append(args, "--draft")
					}
					for _, action := range []string{"created", "unchanged"} {
						code, out := runMRExecutable(t, binary, f, args)
						if code != 0 || !strings.Contains(out, `"action":"`+action+`"`) || !strings.Contains(out, `"title":"`+want+`"`) {
							t.Errorf("want=%s exit=%d output=%s", action, code, out)
						}
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != 1 {
						t.Errorf("title caused repeated mutations: %d", f.writes)
					}
				})
			}
		})
	}
}
