package product

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"gl-axi/internal/testgitlab"
)

// Both public binaries use the landed native HTTP boundary and isolated
// persisted authority/CA configuration. The official-glab child is a trap,
// not the provider fixture. Windows persisted-config support is not claimed.
func TestIssueWritesNativeContractExecutableAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persisted native config/self-managed mapping on Windows remains unproven")
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
			sentinel := filepath.Join(dir, "child-invoked")
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\nprintf invoked > \"$ISSUE_NATIVE_CHILD_SENTINEL\"\nexit 91\n"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"create", "comment", "note", "close", "reopen"} {
				for _, mode := range []string{"success", "rejected", "lost", "malformed", "wrong-project", "wrong-iid", "invalid-host", "invalid-url", "quick-action", "noop", "missing-native", "redirect"} {
					t.Run(action+"/"+mode, func(t *testing.T) {
						stateOp := action == "close" || action == "reopen"
						if mode == "noop" && !stateOp || mode == "quick-action" && stateOp || mode == "wrong-iid" && action == "create" {
							t.Skip("not applicable")
						}
						token := strings.Join([]string{"synthetic", "native", "executable", action}, "-")
						var mutationCount atomic.Int32
						mutate := http.HandlerFunc(nil)
						switch mode {
						case "rejected":
							mutate = func(w http.ResponseWriter, _ *http.Request) {
								w.WriteHeader(422)
								_, _ = w.Write([]byte(`{"message":"untrusted provider detail"}`))
							}
						case "lost":
							mutate = func(w http.ResponseWriter, _ *http.Request) {
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
							}
						case "malformed":
							mutate = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }
						case "redirect":
							mutate = func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Location", "https://"+r.Host+issueNativeAPIPath+"/projects/999/issues/900")
								w.WriteHeader(307)
							}
						}
						normal := issueNativeHandler(t, action, mutate)
						server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if strings.Contains(r.URL.Path, "/projects/999/") {
								t.Error("native request followed an unapproved redirect")
								w.WriteHeader(500)
								return
							}
							if r.Method == "POST" || r.Method == "PUT" {
								mutationCount.Add(1)
							}
							if mode == "wrong-project" && strings.HasSuffix(r.URL.Path, "/projects/group/project") {
								_, _ = w.Write([]byte(`{"id":999}`))
								return
							}
							if mode == "wrong-iid" && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/42") {
								_, _ = w.Write([]byte(`{"id":1001,"iid":43}`))
								return
							}
							if mode == "noop" && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/issues/42") {
								state := "closed"
								if action == "reopen" {
									state = "opened"
								}
								_, _ = w.Write([]byte(strings.ReplaceAll(string(issueWriteBody(state)), "https://gitlab.com", issueNativeWeb)))
								return
							}
							normal(w, r)
						}))
						defer server.Close()
						_, _, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
						args := issueNativeArgs(t, action)
						wantExit, wantWrites := 0, 1
						switch mode {
						case "rejected":
							wantExit = 2
						case "lost", "malformed", "redirect":
							wantExit = 6
						case "wrong-project", "wrong-iid":
							wantExit, wantWrites = 9, 0
						case "invalid-host":
							args = replaceIssueWriteArg(args, "--hostname", "https://wrong.example")
							wantExit, wantWrites = 2, 0
						case "invalid-url":
							args = replaceIssueWriteArg(args, "--expected-url", "https://wrong.example/group/project/-/issues/42")
							wantExit, wantWrites = 9, 0
						case "quick-action":
							for i, v := range args {
								if v == "--body-file" || v == "--description-file" {
									if err := os.WriteFile(args[i+1], []byte("body\n/close"), 0600); err != nil {
										t.Fatal(err)
									}
								}
							}
							wantExit, wantWrites = 2, 0
						case "noop":
							state := "closed"
							if action == "reopen" {
								state = "opened"
							}
							args = replaceIssueWriteArg(args, "--expected-state", state)
							wantWrites = 0
						case "missing-native":
							args = args[:len(args)-2]
							wantExit, wantWrites = 2, 0
						}
						command := exec.Command(binary, args...)
						command.Dir = root
						command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "GL_AXI_CONFIG=" + deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + token, "ISSUE_NATIVE_CHILD_SENTINEL=" + sentinel}
						var out, stderr bytes.Buffer
						command.Stdout, command.Stderr = &out, &stderr
						runErr := command.Run()
						code := 0
						if runErr != nil {
							exit, ok := runErr.(*exec.ExitError)
							if !ok {
								t.Fatal(runErr)
							}
							code = exit.ExitCode()
						}
						requests := server.Requests()
						assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
						if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
							t.Fatal("issue write invoked official glab")
						}
						if code != wantExit || stderr.Len() != 0 {
							t.Fatalf("exit=%d want=%d output=%s stderr=%s", code, wantExit, &out, &stderr)
						}
						if int(mutationCount.Load()) != wantWrites {
							t.Fatalf("mutations=%d want=%d", mutationCount.Load(), wantWrites)
						}
						if strings.Contains(out.String(), "untrusted provider detail") {
							t.Fatal("raw provider error escaped")
						}
						if mode == "invalid-host" || mode == "invalid-url" || mode == "quick-action" || mode == "missing-native" {
							if len(requests) != 0 {
								t.Fatal("invalid input made a network request")
							}
						}
						if wantWrites == 1 {
							_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
							if r.MutationAttempts != 1 || r.RetrySafe || r.AtomicPrecondition {
								t.Fatalf("receipt=%+v", r)
							}
						}
					})
				}
			}
		})
	}
}
