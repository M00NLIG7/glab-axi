package product

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func runNativeMRBinary(t *testing.T, binary string, args []string, f *mrNativeFixture) ([]byte, int) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "child")
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\nprintf child > \"$MR_WRITE_MARKER\"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, args...)
	command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "GL_AXI_CONFIG=" + f.deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + f.keyring.token, "MR_WRITE_MARKER=" + marker, "GOMAXPROCS=2"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	exit := 0
	if err := command.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			exit = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	var envelope struct {
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta.Backend != "native" || envelope.Meta.UpstreamVersion != "" || strings.Contains(stdout.String(), f.keyring.token) || strings.Contains(stderr.String(), f.keyring.token) {
		t.Fatal("native invocation changed backend or exposed credentials")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("native invocation started official glab")
	}
	return stdout.Bytes(), exit
}

func TestMRNativeTitleRefusalsBeforeDependencies(t *testing.T) {
	for _, alias := range []string{"ensure", "create-or-update"} {
		for _, tc := range []struct {
			name, title string
			draft       bool
		}{
			{"empty", " \t\n", false},
			{"empty draft", " \t\n", true},
			{"oversized whitespace", "title" + strings.Repeat(" ", limits.MaxTitleBytes), false},
			{"draft prefix overflow", strings.Repeat("a", limits.MaxTitleBytes-6), true},
			{"invalid UTF-8", "title\xff", false},
			{"NUL", "title\x00", false},
		} {
			t.Run(alias+"/"+tc.name, func(t *testing.T) {
				f := newMRNativeFixture(t, "opened")
				args := replaceArg(ensureArgs(t, tc.title, "body"), "gitlab.com", mrWriteTestHost)
				args = replaceArg(args, "ensure", alias)
				args = append(args, "--auth-source", "native")
				if tc.draft {
					args = append(args, "--draft")
				}
				if code := Run(context.Background(), args, f.deps); code == 0 {
					t.Fatalf("invalid title accepted: %s", f.stdout.String())
				}
				if len(f.server.Requests()) != 0 || f.keyring.gets != 0 || f.keyring.writes != 0 {
					t.Fatal("invalid title consulted credentials or provider")
				}
			})
		}
	}
}

func TestMRReviewBoundariesExecutableTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows persisted-native-config and self-managed mapping remain unproven")
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("mr-write.json", strings.NewReader(MRWriteSchemas()["mr-write"])); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("mr-write.json")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-p", "1", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for _, action := range []string{"close", "reopen"} {
				for _, state := range []string{"opened", "closed"} {
					for _, content := range []string{"stored quick action", "concurrent quick action", "ordinary fenced content"} {
						t.Run(action+"/"+state+"/"+content, func(t *testing.T) {
							f := newMRNativeFixture(t, state)
							description := "keep\n/label ~bug"
							if content == "concurrent quick action" {
								description = "keep"
							} else if content == "ordinary fenced content" {
								description = "keep\n```\n/usr/bin/env\n```"
							}
							original := description
							environmentActive, pagesActive := true, true
							f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
								if r.URL.EscapedPath() != mrNativeAPIPath+"/merge_requests/42" {
									return false
								}
								if r.Method == http.MethodPut {
									if content == "concurrent quick action" {
										description = "keep\n/label ~bug"
									}
									description = strings.ReplaceAll(description, "\n/label ~bug", "")
									if action == "close" {
										environmentActive, pagesActive = false, false
									}
									return false
								}
								record := f.mr()
								record["description"] = description
								_ = json.NewEncoder(w).Encode(record)
								return true
							}
							output, exit := runNativeMRBinary(t, binary, f.args(action), f)
							var envelope struct {
								Data struct {
									Write mrWriteReceipt `json:"write"`
								} `json:"data"`
								Error *uxv1.Error `json:"error"`
							}
							if err := json.Unmarshal(output, &envelope); err != nil {
								t.Fatal(err)
							}
							desired := "closed"
							if action == "reopen" {
								desired = "opened"
							}
							receipt := envelope.Data.Write
							if state == desired {
								if exit != 0 || envelope.Error != nil || receipt.Outcome != "unchanged" {
									t.Errorf("no-op exit=%d output=%s", exit, output)
								}
							} else if exit != 2 || envelope.Error == nil || envelope.Error.Code != uxv1.CodeUnsupported {
								t.Errorf("transition not refused: exit=%d output=%s", exit, output)
							} else {
								encoded, err := json.Marshal(envelope.Error.Receipt)
								if err != nil {
									t.Fatal(err)
								}
								var refusal struct {
									Write mrWriteReceipt `json:"write"`
								}
								if err := json.Unmarshal(encoded, &refusal); err != nil {
									t.Fatal(err)
								}
								receipt = refusal.Write
								if receipt.Outcome != "refused" {
									t.Errorf("missing refusal receipt: %s", output)
								}
							}
							if receipt.Action != action || receipt.Attempts != 0 || receipt.ObservedState != state || receipt.ProviderRevisionEnforced {
								t.Errorf("invalid read-only receipt: %+v", receipt)
							}
							encoded, err := json.Marshal(map[string]any{"write": receipt})
							if err != nil {
								t.Fatal(err)
							}
							var data any
							if err := json.Unmarshal(encoded, &data); err != nil {
								t.Fatal(err)
							}
							if err := schema.Validate(data); err != nil {
								t.Errorf("receipt schema: %v", err)
							}
							f.mu.Lock()
							defer f.mu.Unlock()
							if f.writes != 0 || f.state != state || description != original || !environmentActive || !pagesActive {
								t.Errorf("collateral mutation: writes=%d state=%s description=%q environment=%v pages=%v", f.writes, f.state, description, environmentActive, pagesActive)
							}
						})
					}
				}
			}
			for _, alias := range []string{"ensure", "create-or-update"} {
				for _, tc := range []struct {
					name, title, want, mode string
					draft                   bool
				}{
					{"draft trailing", "title \t\n", "Draft: title", "", true},
					{"draft surrounding", " \ttitle \t\n", "Draft:  \ttitle", "", true},
					{"plain surrounding", " \ttitle \t\n", "title", "", false},
					{"interior and Unicode", "\t title  part\u00a0\u2003 \n", "title  part\u00a0\u2003", "", false},
					{"reconciled create", "title \t\n", "Draft: title", "malformed", true},
					{"different title", "title \t\n", "Draft: title", "different", true},
					{"update", " title \t\n", "title", "update", false},
					{"reconciled update", " title \t\n", "title", "update-malformed", false},
				} {
					t.Run(alias+"/"+tc.name, func(t *testing.T) {
						f := newMRNativeFixture(t, "opened")
						f.ensure = true
						exists := strings.HasPrefix(tc.mode, "update")
						f.ensureRecord = ensureMR(11, "old title", "body")
						f.ensureRecord.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
						f.ensureRecord.Draft = tc.draft
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							path := r.URL.EscapedPath()
							if r.Method == http.MethodGet && path == mrNativeAPIPath+"/merge_requests" && exists {
								_ = json.NewEncoder(w).Encode([]upstreamMR{f.ensureRecord})
								return true
							}
							if r.Method == http.MethodGet && path == mrNativeAPIPath+"/merge_requests/11" {
								_ = json.NewEncoder(w).Encode(f.ensureRecord)
								return true
							}
							if r.Method != http.MethodPost && r.Method != http.MethodPut {
								return false
							}
							var input map[string]string
							if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
								t.Error(err)
							}
							if input["title"] != tc.want {
								t.Errorf("payload title=%q want=%q", input["title"], tc.want)
							}
							f.ensureRecord.Title = strings.Trim(input["title"], "\x00\t\n\v\f\r ")
							if tc.mode == "different" {
								f.ensureRecord.Title = "different title"
							}
							exists = true
							if strings.Contains(tc.mode, "malformed") {
								_, _ = io.WriteString(w, "{broken")
							} else {
								_ = json.NewEncoder(w).Encode(f.ensureRecord)
							}
							return true
						}
						args := replaceArg(ensureArgs(t, strings.TrimSuffix(tc.title, "\n"), "body"), "gitlab.com", mrWriteTestHost)
						args = replaceArg(args, "ensure", alias)
						args = append(args, "--auth-source", "native")
						if tc.draft {
							args = append(args, "--draft")
						}
						for attempt := 0; attempt < 2; attempt++ {
							output, exit := runNativeMRBinary(t, binary, args, f)
							var envelope struct {
								Data  ensureResult `json:"data"`
								Error *uxv1.Error  `json:"error"`
							}
							if err := json.Unmarshal(output, &envelope); err != nil {
								t.Fatal(err)
							}
							if tc.mode == "different" {
								code := uxv1.CodeAmbiguousCreate
								if attempt == 1 {
									code = uxv1.CodeConflict
								}
								if exit != 6 || envelope.Error == nil || envelope.Error.Code != code {
									t.Errorf("exit=%d output=%s", exit, output)
								}
							} else {
								action := "unchanged"
								if attempt == 0 {
									action = "created"
									if strings.HasPrefix(tc.mode, "update") {
										action = "updated"
									}
									if strings.Contains(tc.mode, "malformed") {
										action = "reconciled_" + strings.TrimSuffix(action, "d")
									}
								}
								if exit != 0 || envelope.Error != nil || envelope.Data.Action != action || envelope.Data.MR.Title != tc.want {
									t.Errorf("exit=%d want=%s output=%s", exit, action, output)
								}
							}
							f.mu.Lock()
							writes := f.writes
							f.mu.Unlock()
							if writes != 1 {
								t.Errorf("mutations=%d want=1 total", writes)
							}
						}
					})
				}
			}
		})
	}
}
