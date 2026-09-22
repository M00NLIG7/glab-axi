package product

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/config"
	"gl-axi/internal/safedownload"
)

func downloadTestArchive(t *testing.T, bad bool) []byte {
	t.Helper()
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	name := "bin/app.txt"
	if bad {
		name = "../escape.txt"
	}
	file, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("artifact contents"))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

type downloadCLIResult struct {
	OK   bool `json:"ok"`
	Data struct {
		Download  downloadReceipt  `json:"download"`
		Artifacts artifactMetadata `json:"artifacts"`
	} `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Meta struct {
		Backend string `json:"backend"`
	} `json:"meta"`
}

type downloadCLIFixture struct {
	server                               *httptest.Server
	home, configPath, destination, token string
	archive, asset                       []byte
	archiveSize                          int64
	rawJob                               bool
	rawPath                              string
	mode                                 string
	mu                                   sync.Mutex
	requests                             []string
	transferred                          bool
	started                              chan struct{}
	once                                 sync.Once
}

func newDownloadCLIFixture(t *testing.T, mode string) *downloadCLIFixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &downloadCLIFixture{home: home, destination: filepath.Join(home, "download"), token: strings.Join([]string{"synthetic", "download", "cli", "credential"}, "-"), archive: downloadTestArchive(t, mode == "malicious"), asset: []byte("release contents"), mode: mode, started: make(chan struct{})}
	f.archiveSize = int64(len(f.archive))
	f.rawJob = mode == "raw-job"
	f.rawPath = "bin/app.bin"
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.handle(t, w, r) }))
	f.server.EnableHTTP2 = mode == "http2"
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	ca := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	if err := cfg.Put("download.example", config.Host{GitHosts: []string{"download.example"}, APIBase: f.server.URL + "/api/v4", WebBase: f.server.URL, CABundle: ca, ProxyDisabled: true}); err != nil {
		t.Fatal(err)
	}
	f.configPath = filepath.Join(home, "private", "config.json")
	if err := config.Save(f.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *downloadCLIFixture) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.RequestURI)
	if f.mode == "http2" && r.ProtoMajor != 1 {
		t.Error("native operation negotiated HTTP/2, which can transparently replay refused streams")
		http.Error(w, "unexpected protocol", 500)
		return
	}
	if r.Method != "GET" || r.Header.Get("Private-Token") != f.token {
		t.Error("unexpected method or native identity")
		http.Error(w, "unauthorized fixture", 403)
		return
	}
	sha := strings.Repeat("a", 40)
	web := f.server.URL + "/group/project"
	pipeline := map[string]any{"id": 71, "project_id": 101, "ref": "main", "sha": sha, "web_url": web + "/-/pipelines/71"}
	write := func(value any) {
		w.Header().Set("Content-Type", "application/json")
		if f.mode == "duplicate-json" && r.URL.Path == "/api/v4/projects/group/project" {
			body, err := json.Marshal(value)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = w.Write(bytes.Replace(body, []byte(`"id":`), []byte(`"id":999,"id":`), 1))
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}
	rawPath, err := url.PathUnescape(f.rawPath)
	if err != nil {
		t.Error(err)
		http.Error(w, "invalid fixture path", 500)
		return
	}
	switch r.URL.Path {
	case "/api/v4/projects/group/project":
		project := map[string]any{"id": 101, "path_with_namespace": "group/project", "web_url": web}
		if f.mode == "wrong-project" {
			project["path_with_namespace"] = "other/project"
		}
		if f.mode == "wrong-host" {
			project["web_url"] = "https://wrong.example/group/project"
		}
		write(project)
	case "/api/v4/projects/101/jobs/42":
		if f.mode == "wrong-pipeline" {
			pipeline["id"] = 72
		}
		if f.mode == "wrong-ref" {
			pipeline["ref"] = "other"
		}
		if f.mode == "wrong-sha" {
			pipeline["sha"] = strings.Repeat("b", 40)
		}
		id := 42
		if f.mode == "wrong-job" {
			id = 43
		}
		filename := "artifacts.zip"
		if f.mode == "drift" && f.transferred {
			filename = "changed.zip"
		}
		write(map[string]any{"id": id, "ref": "main", "web_url": web + "/-/jobs/42", "pipeline": pipeline, "commit": map[string]any{"id": sha}, "artifacts_file": map[string]any{"filename": filename, "size": f.archiveSize}})
	case "/api/v4/projects/101/pipelines/71", "/api/v4/projects/101/pipelines/72":
		write(pipeline)
	case "/api/v4/projects/101/releases/v1.0":
		tag := "v1.0"
		if f.mode == "wrong-tag" {
			tag = "v2.0"
		}
		write(map[string]any{"tag_name": tag, "commit": map[string]any{"id": sha}, "_links": map[string]any{"self": web + "/-/releases/v1.0"}})
	case "/api/v4/projects/101/releases/v1.0/assets/links":
		assetURL := f.server.URL + "/api/v4/projects/101/packages/generic/app/v1.0/app.bin"
		if f.rawJob {
			assetURL = web + "/-/jobs/42/artifacts/raw/" + f.rawPath
		}
		if f.mode == "external" {
			assetURL = "https://outside.example/app.bin"
		}
		link := map[string]any{"id": 7, "name": "app.bin", "url": assetURL}
		if f.mode == "paginated" && r.URL.Query().Get("page") == "1" {
			links := make([]map[string]any, 100)
			for i := range links {
				links[i] = map[string]any{"id": 1000 + i, "name": fmt.Sprint("other-", i), "url": assetURL}
			}
			w.Header().Set("X-Next-Page", "2")
			write(links)
			return
		}
		if f.mode == "duplicate-link" {
			write([]any{link, link})
			return
		}
		write([]any{link})
	case "/api/v4/projects/101/packages":
		if r.URL.Query().Get("package_type") != "generic" || r.URL.Query().Get("package_name") != "app" || r.URL.Query().Get("package_version") != "v1.0" || r.URL.Query().Get("status") != "default" {
			t.Error("missing package filter")
		}
		pkg := map[string]any{"id": 201, "name": "app", "version": "v1.0", "package_type": "generic", "status": "default"}
		if f.mode == "ambiguous-package" {
			second := map[string]any{"id": 202, "name": "app", "version": "v1.0", "package_type": "generic", "status": "default"}
			write([]any{pkg, second})
			return
		}
		write([]any{pkg})
	case "/api/v4/projects/101/packages/201/package_files":
		digest := sha256.Sum256(f.asset)
		hash := hex.EncodeToString(digest[:])
		if f.mode == "checksum" {
			hash = strings.Repeat("0", 64)
		}
		write([]any{map[string]any{"id": 901, "package_id": 201, "file_name": "app.bin", "size": len(f.asset), "file_sha256": hash}})
	case "/api/v4/projects/101/jobs/42/artifacts/tree":
		f.transferred = true
		write([]any{map[string]any{"name": "app.bin", "path": "bin/app.bin", "type": "file", "size": len(f.asset), "mode": "100644"}})
	case "/api/v4/projects/101/jobs/42/artifacts", "/api/v4/projects/101/packages/generic/app/v1.0/app.bin", "/api/v4/projects/101/jobs/42/artifacts/" + rawPath:
		f.transferred = true
		if f.mode == "redirect" {
			w.Header().Set("Location", f.server.URL+"/api/v4/projects/999/private")
			w.WriteHeader(302)
			return
		}
		if f.mode == "race-destination" {
			if err := os.Mkdir(f.destination, 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(f.destination, "caller"), []byte("keep"), 0o600); err != nil {
				t.Error(err)
			}
		}
		body := f.asset
		if strings.HasSuffix(r.URL.Path, "/42/artifacts") {
			body = f.archive
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if f.mode == "oversize" {
			size := len(body) + 100
			if f.rawJob {
				size = safedownload.MaxArchiveBytes + 1
			}
			w.Header().Set("Content-Length", fmt.Sprint(size))
			_, _ = w.Write(body)
			return
		}
		if f.mode == "cancel" {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			f.once.Do(func() { close(f.started) })
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return
		}
		_, _ = w.Write(body)
	default:
		t.Errorf("unexpected request: %s", r.RequestURI)
		http.Error(w, "unexpected request", 400)
	}
}
func (f *downloadCLIFixture) command(binary, kind string, extra ...string) *exec.Cmd {
	args := []string{"job", kind, "42", "--pipeline-id", "71", "--expected-ref", "main"}
	if kind == "release" {
		args = []string{"release", "download", "v1.0", "--asset-id", "7", "--asset-name", "app.bin"}
	}
	args = append(args, "--auth-source", "native", "--hostname", "download.example", "--repo", "group/project", "--expected-sha", strings.Repeat("a", 40), "--format", "json")
	if kind != "artifacts" {
		args = append(args, "--destination", f.destination)
	}
	args = append(args, extra...)
	command := exec.Command(binary, args...)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "PATH=" + f.home, "GL_AXI_CONFIG=" + f.configPath, "GL_AXI_TOKEN=" + f.token, "NO_PROXY=*"}
	if runtime.GOOS == "windows" {
		command.Env = append(command.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"), "TEMP="+f.home, "TMP="+f.home)
	}
	return command
}
func assertDownloadCleanup(t *testing.T, f *downloadCLIFixture) {
	t.Helper()
	entries, err := os.ReadDir(f.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gl-axi-download-") {
			t.Fatalf("staging not cleaned: %s", entry.Name())
		}
	}
}

func TestDownloadExecutableAliasesEndToEnd(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, out)
			}
			for _, tc := range []struct {
				kind, mode string
				ok         bool
			}{
				{"artifacts", "ok", true}, {"download", "ok", true}, {"download", "http2", true}, {"release", "ok", true}, {"release", "paginated", true}, {"release", "raw-job", true},
				{"download", "duplicate-json", false}, {"download", "wrong-project", false}, {"download", "wrong-host", false}, {"download", "wrong-pipeline", false}, {"download", "wrong-ref", false}, {"download", "wrong-sha", false}, {"download", "wrong-job", false},
				{"release", "wrong-tag", false}, {"release", "duplicate-link", false}, {"release", "ambiguous-package", false}, {"release", "external", false}, {"release", "checksum", false},
				{"download", "redirect", false}, {"release", "redirect", false}, {"download", "oversize", false}, {"download", "malicious", false}, {"download", "drift", false}, {"download", "race-destination", false},
			} {
				t.Run(tc.kind+"/"+tc.mode, func(t *testing.T) {
					f := newDownloadCLIFixture(t, tc.mode)
					command := f.command(binary, tc.kind)
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					runErr := command.Run()
					var result downloadCLIResult
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatalf("decode: %v output=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if result.OK != tc.ok || (runErr == nil) != tc.ok || result.Meta.Backend != "native" {
						t.Fatalf("result=%+v run=%v stderr=%s", result, runErr, stderr.String())
					}
					if tc.mode == "duplicate-json" {
						f.mu.Lock()
						count, transferred := len(f.requests), f.transferred
						f.mu.Unlock()
						if count != 1 || transferred || result.Error.Code != "upstream_error" {
							t.Fatal("ambiguous project JSON did not stop before further requests")
						}
					}
					if strings.Contains(stdout.String()+stderr.String(), f.token) || stdout.Len() > 16384 {
						t.Fatal("output confidentiality or receipt bound violated")
					}
					if tc.ok && tc.kind != "artifacts" {
						name := "bin/app.txt"
						want := "artifact contents"
						if tc.kind == "release" {
							name = "app.bin"
							want = string(f.asset)
						}
						body, err := os.ReadFile(filepath.Join(f.destination, filepath.FromSlash(name)))
						if err != nil || string(body) != want {
							t.Fatalf("file=%q err=%v", body, err)
						}
						if result.Data.Download.ProjectID != 101 || result.Data.Download.SHA256 == "" || result.Data.Download.Files != 1 {
							t.Fatalf("receipt=%+v", result.Data.Download)
						}
					} else if tc.kind == "artifacts" {
						if result.Data.Artifacts.JobID != 42 || result.Data.Artifacts.PipelineID != 71 {
							t.Fatalf("metadata=%+v", result.Data.Artifacts)
						}
					} else if tc.mode == "race-destination" {
						body, err := os.ReadFile(filepath.Join(f.destination, "caller"))
						if err != nil || string(body) != "keep" {
							t.Fatal("caller file not preserved")
						}
					} else if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
						t.Fatal("failure published destination")
					}
					assertDownloadCleanup(t, f)
				})
			}
			t.Run("raw-route-selection", func(t *testing.T) {
				for _, tc := range []struct {
					path, route string
					ok          bool
				}{
					{"tree", "tree", false},
					{"%74ree", "tree", false},
					{"tr%65e", "tree", false},
					{"app.bin", "app.bin", true},
					{"treehouse", "treehouse", true},
					{"tree/app.bin", "tree/app.bin", true},
					{"bin/tree", "bin/tree", true},
					{"bin/%74ree", "bin/tree", true},
				} {
					t.Run(tc.path, func(t *testing.T) {
						f := newDownloadCLIFixture(t, "raw-job")
						f.rawPath = tc.path
						command := f.command(binary, "release")
						var stdout, stderr bytes.Buffer
						command.Stdout, command.Stderr = &stdout, &stderr
						runErr := command.Run()
						var result downloadCLIResult
						if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
							t.Fatalf("decode: %v output=%s stderr=%s", err, &stdout, &stderr)
						}
						if result.OK != tc.ok || (runErr == nil) != tc.ok || result.Meta.Backend != "native" {
							t.Fatalf("result=%+v run=%v stderr=%s", result, runErr, &stderr)
						}
						f.mu.Lock()
						transferred := f.transferred
						requests := append([]string(nil), f.requests...)
						f.mu.Unlock()
						transfers := 0
						for _, request := range requests {
							if request == "GET /api/v4/projects/101/jobs/42/artifacts/tree" {
								t.Fatal("reserved listing route requested as an asset")
							}
							if request == "GET /api/v4/projects/101/jobs/42/artifacts/"+tc.route {
								transfers++
							}
						}
						if transferred != tc.ok {
							t.Fatalf("transferred=%v want=%v", transferred, tc.ok)
						}
						if tc.ok {
							body, err := os.ReadFile(filepath.Join(f.destination, "app.bin"))
							if err != nil || !bytes.Equal(body, f.asset) || transfers != 1 {
								t.Fatalf("body=%q err=%v transfers=%d", body, err, transfers)
							}
							digest := sha256.Sum256(f.asset)
							receipt := result.Data.Download
							if receipt.JobID != 42 || receipt.PipelineID != 71 || receipt.Bytes != int64(len(f.asset)) || receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.Checksum != "sha256_receipt_only" {
								t.Fatalf("receipt=%+v", receipt)
							}
						} else {
							if command.ProcessState.ExitCode() != 9 || result.Error.Code != "safety_violation" || result.Data.Download != (downloadReceipt{}) {
								t.Fatalf("result=%+v run=%v", result, runErr)
							}
							if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
								t.Fatal("reserved route published destination")
							}
						}
						if strings.Contains(stdout.String()+stderr.String(), f.token) {
							t.Fatal("credential appeared in output")
						}
						assertDownloadCleanup(t, f)
					})
				}
			})
			t.Run("invalid-before-work", func(t *testing.T) {
				for _, extra := range [][]string{{"--auth-source", "native"}, {"--auth-source=official"}, {"--asset-url=https://other.example"}} {
					f := newDownloadCLIFixture(t, "ok")
					command := f.command(binary, "download", extra...)
					if err := command.Run(); err == nil {
						t.Fatal("invalid input accepted")
					}
					f.mu.Lock()
					count := len(f.requests)
					f.mu.Unlock()
					if count != 0 {
						t.Fatal("invalid input reached provider")
					}
					assertDownloadCleanup(t, f)
				}
			})
			if runtime.GOOS != "windows" {
				t.Run("caller-cancel", func(t *testing.T) {
					f := newDownloadCLIFixture(t, "cancel")
					command := f.command(binary, "download")
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					if err := command.Start(); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- command.Wait() }()
					select {
					case <-f.started:
					case err := <-done:
						t.Fatalf("exited before transfer: %v %s", err, stdout.String())
					case <-time.After(10 * time.Second):
						_ = command.Process.Kill()
						t.Fatal("transfer did not start")
					}
					if err := command.Process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-done:
						if err == nil {
							t.Fatal("cancel succeeded")
						}
					case <-time.After(10 * time.Second):
						_ = command.Process.Kill()
						t.Fatal("cancel did not finish")
					}
					var result downloadCLIResult
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Error.Code != "canceled" {
						t.Fatalf("result=%+v err=%v output=%s", result, err, stdout.String())
					}
					assertDownloadCleanup(t, f)
				})
			}
		})
	}
}
