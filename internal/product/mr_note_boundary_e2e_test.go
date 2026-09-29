package product

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/limits"
)

func TestMRNoteBoundaryExecutableTLS(t *testing.T) {
	t.Parallel()
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := buildMRExecutable(t, program)
			for _, mode := range []string{"preflight-page-bound", "post-page-bound", "pending-ready", "pending-edit", "pending-label", "wrong-configured-url"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					f := newMRNativeFixture(t, "opened")
					args := f.args("comment")
					wantCode, writes, noNetwork := 8, 0, false
					switch mode {
					case "post-page-bound":
						wantCode, writes = 6, 1
					case "pending-ready", "pending-edit":
						args = f.args(strings.TrimPrefix(mode, "pending-"))
						wantCode, noNetwork = 2, true
					case "pending-label":
						args = append(replaceArg(ensureArgs(t, "title", "body"), "gitlab.com", mrWriteTestHost), "--auth-source", "native", "--label", "unapproved")
						wantCode, noNetwork = 2, true
					case "wrong-configured-url":
						args = replaceArg(args, mrNativeWebBase+"/group/project/-/merge_requests/42", "https://other.example.invalid/group/project/-/merge_requests/42")
						wantCode, noNetwork = 9, true
					}
					f.configure(func() {
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							if mode == "preflight-page-bound" && r.URL.EscapedPath() == mrNativeProjectLookupPath || mode == "post-page-bound" && r.Method == http.MethodPost {
								_, _ = io.WriteString(w, `{"padding":"`+strings.Repeat("x", limits.MaxJSONPageBytes)+`"}`)
								return true
							}
							return false
						}
					})
					code, out := runMRExecutable(t, binary, f, args)
					if code != wantCode || len(out) > 64<<10 {
						t.Errorf("exit=%d want=%d output length=%d output=%s", code, wantCode, len(out), out)
					}
					if mode == "post-page-bound" && (!strings.Contains(out, `"outcome":"unknown"`) || strings.Contains(out, `"note_id"`)) {
						t.Errorf("lost note identity was guessed: %s", out)
					}
					requests := f.server.Requests()
					if noNetwork && len(requests) != 0 {
						t.Errorf("rejected request made %d provider calls", len(requests))
					}
					for _, request := range requests {
						if request.Method == http.MethodGet && strings.Contains(request.URL, "/notes") {
							t.Error("lost identity triggered note lookup")
						}
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != writes {
						t.Errorf("writes=%d want=%d", f.writes, writes)
					}
				})
			}
			for _, mode := range []string{"cancel-preflight", "cancel-mutation", "mutation-timeout"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					f := newMRNativeFixture(t, "opened")
					entered, release := make(chan struct{}, 1), make(chan struct{})
					defer close(release)
					f.configure(func() {
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							if mode == "cancel-preflight" && r.URL.EscapedPath() == mrNativeProjectLookupPath || mode != "cancel-preflight" && r.Method == http.MethodPost {
								_, _ = io.Copy(io.Discard, r.Body)
								if mode == "mutation-timeout" {
									// Send headers so this exercises the existing 15-second
									// mutation context, not the transport's 10-second header bound.
									w.WriteHeader(http.StatusOK)
									w.(http.Flusher).Flush()
								}
								select {
								case entered <- struct{}{}:
								default:
								}
								select {
								case <-r.Context().Done():
								case <-release:
								}
								return true
							}
							return false
						}
					})
					ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, f.args("comment")...)
					cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/nonexistent", "GL_AXI_CONFIG=" + f.deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + f.keyring.token, "GOMAXPROCS=2"}
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- cmd.Wait() }()
					select {
					case <-entered:
					case <-ctx.Done():
						t.Fatal("CLI never reached selected phase")
					}
					started := time.Now()
					if mode != "mutation-timeout" {
						if err := cmd.Process.Signal(os.Interrupt); err != nil {
							t.Fatal(err)
						}
					}
					err := <-done
					if ctx.Err() != nil {
						t.Fatal("operation exceeded unchanged outer bound")
					}
					code := 0
					if err != nil {
						if e, ok := err.(*exec.ExitError); ok {
							code = e.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					wantCode, writes := 6, 1
					if mode == "cancel-preflight" {
						wantCode, writes = 130, 0
					}
					out := stdout.String()
					if code != wantCode || strings.Contains(out+stderr.String(), f.keyring.token) || len(out) > 64<<10 {
						t.Errorf("exit=%d want=%d output=%s", code, wantCode, out)
					}
					if writes == 1 && (!strings.Contains(out, `"outcome":"unknown"`) || !strings.Contains(out, `"attempts":1`) || strings.Contains(out, `"note_id"`)) {
						t.Errorf("untruthful ambiguous receipt: %s", out)
					}
					if mode == "mutation-timeout" && time.Since(started) < limits.MergeMutationOperation-time.Second {
						t.Error("mutation deadline was shortened")
					}
					for _, request := range f.server.Requests() {
						if request.Method == http.MethodGet && strings.Contains(request.URL, "/notes") {
							t.Error("lost identity triggered a note search")
						}
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != writes {
						t.Errorf("writes=%d want=%d", f.writes, writes)
					}
				})
			}
		})
	}
}
