package product

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// This synthetic glab process speaks only the pinned fixed API argv to a local
// TLS server. It has no official profile or usable credential, and never uses
// the selected public hostname as a transport destination.
func TestRepoAdminGlabProcess(t *testing.T) {
	if os.Getenv("GL_AXI_ADMIN_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	args = args[1:]
	if len(args) == 1 && args[0] == "version" {
		fmt.Print("glab 1.112.0 (816e3a52)\n")
		os.Exit(0)
	}
	if len(args) != 6 && len(args) != 10 {
		os.Exit(2)
	}
	if args[0] != "api" || args[1] != "--method" || args[3] != "--hostname" || args[4] != "gitlab.example.invalid" {
		os.Exit(2)
	}
	var body io.Reader
	if len(args) == 10 {
		if args[6] != "--input" || args[8] != "--header" || args[9] != "Content-Type: application/json" {
			os.Exit(2)
		}
		data, err := os.ReadFile(args[7])
		if err != nil {
			os.Exit(2)
		}
		info, err := os.Stat(args[7])
		if err != nil || info.Mode().Perm() != 0600 {
			os.Exit(2)
		}
		body = bytes.NewReader(data)
	}
	ca, err := os.ReadFile(os.Getenv("GL_AXI_ADMIN_CA"))
	if err != nil {
		os.Exit(2)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		os.Exit(2)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(args[2], os.Getenv("GL_AXI_ADMIN_SERVER")+"/api/v4/"+args[5], body)
	if err != nil {
		os.Exit(2)
	}
	req.Host = args[4]
	req.Header.Set("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		os.Exit(1)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "glab: %d\n", response.StatusCode)
		os.Exit(1)
	}
	_, _ = io.Copy(os.Stdout, response.Body)
	os.Exit(0)
}

func TestRepoAdminExecutableAliasesTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic process launcher uses POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
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
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\nexec \"$GL_AXI_HELPER_BINARY\" -test.run=^TestRepoAdminGlabProcess$ -- \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, scenario := range []string{"create", "edit", "fork", "fork-failed", "wrong-account", "wrong-namespace", "wrong-host", "drift", "lost-write", "wrong-response", "malformed"} {
				t.Run(scenario, func(t *testing.T) {
					action := scenario
					if action != "create" && action != "fork" {
						action = "edit"
					}
					if scenario == "fork-failed" {
						action = "fork"
					}
					before := adminTestProject("team/sub/project", 101)
					after := before
					if action == "create" {
						after.ID = 102
						after.Description = ""
					} else if action == "fork" {
						after = adminTestForkState().after
						after.ImportStatus = "scheduled"
						if scenario == "fork-failed" {
							after.ImportStatus = "failed"
						}
					} else {
						after.Description = "new"
					}
					var mu sync.Mutex
					writes, reads, requests := 0, 0, 0
					secret := strings.Join([]string{"synthetic", "project", "admin", "credential"}, "-")
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						requests++
						if r.Host != "gitlab.example.invalid" || r.Header.Get("PRIVATE-TOKEN") != secret {
							t.Error("wrong authority or synthetic credential")
							w.WriteHeader(403)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if r.Method == "GET" {
							switch r.URL.EscapedPath() {
							case "/api/v4/user":
								user := adminAccount{ID: 7, Username: "tester"}
								if scenario == "wrong-account" {
									user.Username = "another"
								}
								_ = json.NewEncoder(w).Encode(user)
							case "/api/v4/namespaces/21":
								ns := before.Namespace
								if scenario == "wrong-namespace" {
									ns.ID = 22
								}
								_ = json.NewEncoder(w).Encode(ns)
							case "/api/v4/projects/team%2Fsub%2Fproject", "/api/v4/projects/team%2Fsub%2Ffork":
								reads++
								if writes == 0 && (action == "create" || strings.HasSuffix(r.URL.Path, "/fork")) {
									w.WriteHeader(404)
									return
								}
								p := before
								if writes > 0 {
									p = after
								}
								if scenario == "wrong-host" {
									p.URL = "https://other.invalid/team/sub/project"
								}
								if scenario == "drift" && reads == 2 {
									p.Visibility = "public"
								}
								_ = json.NewEncoder(w).Encode(p)
							default:
								t.Errorf("unexpected GET %s", r.URL)
								w.WriteHeader(400)
							}
							return
						}
						writes++
						path, method := "/api/v4/projects", "POST"
						if action == "edit" {
							path, method = "/api/v4/projects/101", "PUT"
						}
						if action == "fork" {
							path = "/api/v4/projects/101/fork"
						}
						if r.URL.Path != path || r.Method != method || writes != 1 {
							t.Errorf("unexpected mutation %s %s writes=%d", r.Method, r.URL, writes)
							w.WriteHeader(400)
							return
						}
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						want := map[string]any{"description": "new"}
						if action != "edit" {
							want = map[string]any{"path": "project", "name": "project", "namespace_id": float64(21), "visibility": "private"}
							if action == "create" {
								want["initialize_with_readme"] = false
							} else {
								want["path"], want["name"] = "fork", "fork"
							}
						}
						if string(adminTestBody(body)) != string(adminTestBody(want)) {
							t.Errorf("payload %v want %v", body, want)
						}
						if scenario == "lost-write" {
							w.WriteHeader(500)
							return
						}
						p := after
						if scenario == "wrong-response" {
							p.ID++
						}
						_ = json.NewEncoder(w).Encode(p)
					}))
					defer server.Close()
					ca := adminTestFile(t, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
					args := adminTestArgs(action)
					if action == "edit" {
						args = adminEditArgs(t, before, "--description-file", adminTestFile(t, []byte("new")))
					}
					if scenario == "malformed" {
						args = append(args, "--clone")
					}
					cmd := exec.Command(binary, args...)
					cmd.Dir = dir
					cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "GL_AXI_ADMIN_HELPER=1", "GL_AXI_HELPER_BINARY=" + helper, "GL_AXI_ADMIN_CA=" + ca, "GL_AXI_ADMIN_SERVER=" + server.URL, "GITLAB_TOKEN=" + secret}
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					err := cmd.Run()
					code := 0
					if err != nil {
						exit, ok := err.(*exec.ExitError)
						if !ok {
							t.Fatal(err)
						}
						code = exit.ExitCode()
					}
					mu.Lock()
					gotWrites, gotRequests := writes, requests
					mu.Unlock()
					wantCode, wantWrites := 0, 1
					switch scenario {
					case "wrong-account", "wrong-namespace", "wrong-host":
						wantCode, wantWrites = 9, 0
					case "drift":
						wantCode, wantWrites = 6, 0
					case "wrong-response":
						wantCode = 6
					case "fork-failed":
						wantCode = 8
					case "malformed":
						wantCode, wantWrites = 2, 0
					}
					if code != wantCode || gotWrites != wantWrites || scenario == "malformed" && gotRequests != 0 {
						t.Fatalf("exit=%d writes=%d requests=%d stdout=%s stderr=%s", code, gotWrites, gotRequests, stdout.String(), stderr.String())
					}
					if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
						t.Fatal("credential exposed")
					}
					var result struct {
						OK   bool        `json:"ok"`
						Data adminOutput `json:"data"`
						Meta struct {
							Complete bool `json:"complete"`
						} `json:"meta"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if scenario == "fork" && (result.Data.Administration.Outcome != "accepted" || result.Meta.Complete) {
						t.Fatalf("fork pretended ready: %s", stdout.String())
					}
					if scenario == "lost-write" && !result.Data.Administration.Reconciled {
						t.Fatalf("lost response was not reconciled: %s", stdout.String())
					}
				})
			}
		})
	}
}
