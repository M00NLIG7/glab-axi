package product

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/testgitlab"
)

const mrWriteTestHost = "gitlab.example.invalid"
const mrWriteTestURL = "https://gitlab.example.invalid/group/project/-/merge_requests/42"

func mrWriteArgs(action, state string) []string {
	return []string{"mr", action, "42", "--repo", "group/project", "--hostname", mrWriteTestHost,
		"--expected-url", mrWriteTestURL, "--expected-source", "feature", "--expected-target", "main",
		"--expected-head", mergeTestHead, "--expected-state", state, "--format", "json"}
}

func mrWriteRecord(state string) map[string]any {
	return map[string]any{"id": 1042, "iid": 42, "project_id": 101, "source_project_id": 101, "target_project_id": 101,
		"web_url": mrWriteTestURL, "source_branch": "feature", "target_branch": "main", "sha": mergeTestHead,
		"base_sha": strings.Repeat("b", 40), "updated_at": "2026-08-15T12:00:00Z", "state": state}
}

func mrWriteNote(body string) map[string]any {
	return map[string]any{"id": 501, "body": body, "author": map[string]any{"id": 7, "username": "synthetic-author"},
		"created_at": "2026-08-15T12:00:01Z", "updated_at": "2026-08-15T12:00:01Z", "system": false,
		"noteable_id": 1042, "noteable_iid": 42, "noteable_type": "MergeRequest", "project_id": 101, "resolvable": false}
}

// Actual binaries use the landed native transport, persisted synthetic config,
// TLS CA and runtime synthetic environment token. A glab trap proves no fallback.
func TestMRWritesExecutableTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows persisted-native-config and self-managed mapping remain unproven")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for _, tc := range []struct {
				name, action, initial, mode string
				writes, exit                int
				outcome                     string
			}{
				{"close", "close", "opened", "", 1, 0, "observed"},
				{"reopen", "reopen", "closed", "", 1, 0, "observed"},
				{"already closed", "close", "closed", "", 0, 0, "unchanged"},
				{"already open", "reopen", "opened", "", 0, 0, "unchanged"},
				{"note", "comment", "opened", "", 1, 0, "created"},
				{"note alias on closed", "note", "closed", "", 1, 0, "created"},
				{"malformed state reply", "close", "opened", "malformed", 1, 0, "observed"},
				{"lost state reply", "reopen", "closed", "lost", 1, 0, "observed"},
				{"unchanged postcondition", "close", "opened", "not-applied", 1, 6, "unknown"},
				{"lost note ID", "comment", "opened", "lost", 1, 6, "unknown"},
				{"malformed note ID", "comment", "opened", "malformed", 1, 6, "unknown"},
				{"note ID drift", "comment", "opened", "note-id", 1, 6, "unknown"},
				{"note body drift", "comment", "opened", "note-body", 1, 6, "unknown"},
				{"note whitespace drift", "comment", "opened", "note-whitespace", 1, 6, "unknown"},
				{"wrong note resource", "comment", "opened", "note-resource", 1, 6, "unknown"},
				{"state rejection", "close", "opened", "rejected", 1, 4, "rejected"},
				{"note rejection", "comment", "opened", "rejected", 1, 4, "rejected"},
				{"preflight drift", "close", "opened", "drift", 0, 6, ""},
				{"wrong project", "comment", "opened", "project", 0, 9, ""},
				{"wrong IID", "close", "opened", "iid", 0, 9, ""},
				{"wrong URL", "close", "opened", "url", 0, 9, ""},
				{"wrong head", "close", "opened", "head", 0, 6, ""},
				{"wrong branch", "close", "opened", "branch", 0, 6, ""},
				{"fork denied", "close", "opened", "fork", 0, 6, ""},
				{"merged denied", "reopen", "closed", "merged", 0, 6, ""},
				{"missing base", "close", "opened", "missing-base", 0, 8, ""},
				{"post head drift", "close", "opened", "post-head", 1, 6, "unknown"},
				{"post malformed", "close", "opened", "post-malformed", 1, 6, "unknown"},
				{"invalid selector", "close", "opened", "invalid-selector", 0, 2, ""},
				{"missing opt-in", "close", "opened", "missing-opt-in", 0, 2, ""},
				{"quick action", "comment", "opened", "quick-action", 0, 2, ""},
				{"note redirect cross origin", "comment", "opened", "redirect-cross", 1, 6, "unknown"},
				{"state redirect cross path", "close", "opened", "redirect-path", 1, 6, "unknown"},
				{"creation metadata", "ensure", "opened", "", 1, 0, ""},
				{"creation selection drift", "ensure", "opened", "ensure-drift", 0, 6, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					f := newMRNativeFixture(t, tc.initial)
					other := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
					defer other.Close()
					if tc.action == "ensure" {
						f.ensure = true
						f.ensureRecord = ensureMR(11, "Draft: title", "body")
						f.ensureRecord.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
						f.ensureRecord.Draft = true
						f.ensureRecord.Assignees = []mrMetadataIdentity{{ID: 7}}
						f.ensureRecord.Reviewers = []mrMetadataIdentity{{ID: 8}}
						f.ensureRecord.Milestone = &mrMetadataIdentity{ID: 9}
					}
					f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
						path := r.URL.EscapedPath()
						if tc.mode == "ensure-drift" && r.Method == "GET" && path == mrNativeAPIPath+"/merge_requests" {
							record := f.ensureRecord
							record.Reviewers = []mrMetadataIdentity{{ID: 88}}
							_ = json.NewEncoder(w).Encode([]upstreamMR{record})
							return true
						}
						if tc.mode == "project" && path == mrNativeAPIPath {
							_ = json.NewEncoder(w).Encode(map[string]any{"id": 102, "path_with_namespace": "group/project", "web_url": mrNativeWebBase + "/group/project"})
							return true
						}
						if r.Method == "GET" && path == mrNativeAPIPath+"/merge_requests/42" {
							f.reads++
							record := f.mr()
							switch tc.mode {
							case "drift":
								if f.reads == 2 {
									record["updated_at"] = "2026-08-15T12:00:01Z"
								}
							case "iid":
								record["iid"] = 43
							case "url":
								record["web_url"] = mrWriteTestURL + "0"
							case "head":
								record["sha"] = strings.Repeat("c", 40)
								record["diff_refs"].(map[string]any)["head_sha"] = record["sha"]
							case "branch":
								record["target_branch"] = "other"
							case "fork":
								record["source_project_id"] = 102
							case "merged":
								record["state"] = "merged"
							case "missing-base":
								delete(record["diff_refs"].(map[string]any), "base_sha")
							case "post-head":
								if f.writes > 0 {
									record["sha"] = strings.Repeat("c", 40)
									record["diff_refs"].(map[string]any)["head_sha"] = record["sha"]
								}
							case "post-malformed":
								if f.writes > 0 {
									_, _ = io.WriteString(w, "{broken")
									return true
								}
							}
							_ = json.NewEncoder(w).Encode(record)
							return true
						}
						if r.Method == "GET" && strings.HasSuffix(path, "/notes/501") {
							note := mrWriteNote(f.storedNoteBody)
							if tc.mode == "note-id" {
								note["id"] = 502
							}
							if tc.mode == "note-body" {
								note["body"] = "unseen edit"
							}
							if tc.mode == "note-whitespace" {
								note["body"] = f.storedNoteBody + " "
							}
							_ = json.NewEncoder(w).Encode(note)
							return true
						}
						if r.Method != "GET" {
							switch tc.mode {
							case "redirect-cross":
								w.Header().Set("Location", other.HTTP.URL+"/unauthorized")
								w.WriteHeader(302)
								return true
							case "redirect-path":
								w.Header().Set("Location", f.server.HTTP.URL+"/gitlab/api/v4/unauthorized")
								w.WriteHeader(307)
								return true
							case "rejected":
								w.WriteHeader(403)
								return true
							case "not-applied":
								_ = json.NewEncoder(w).Encode(f.mr())
								return true
							case "malformed", "lost":
								if r.Method == "PUT" {
									f.state = "closed"
									if tc.action == "reopen" {
										f.state = "opened"
									}
								}
								if tc.mode == "malformed" {
									_, _ = io.WriteString(w, "{broken")
								} else {
									c, _, err := w.(http.Hijacker).Hijack()
									if err != nil {
										t.Error(err)
									} else {
										_ = c.Close()
									}
								}
								return true
							case "note-resource":
								note := mrWriteNote(strings.TrimRight(f.noteBody, "\x00\t\n\v\f\r "))
								note["noteable_id"] = 999
								_ = json.NewEncoder(w).Encode(note)
								return true
							}
						}
						return false
					}
					if tc.mode == "quick-action" {
						f.noteBody = "ordinary prose\n/merge"
					}
					var args []string
					if tc.action == "ensure" {
						args = replaceArg(ensureArgs(t, "title", "body"), "gitlab.com", mrWriteTestHost)
						args = append(args, "--auth-source", "native", "--assignee-id", "7", "--reviewer-id", "8", "--milestone-id", "9", "--draft")
					} else {
						args = f.args(tc.action)
					}
					if tc.mode == "invalid-selector" {
						args = replaceArg(args, "native", "official")
					}
					if tc.mode == "missing-opt-in" {
						args = removeFlag(args, "--auth-source", true)
					}
					dir := t.TempDir()
					marker := filepath.Join(dir, "child")
					if err := os.WriteFile(filepath.Join(dir, "glab"), []byte("#!/bin/sh\nprintf child > \"$MR_WRITE_MARKER\"\nexit 99\n"), 0700); err != nil {
						t.Fatal(err)
					}
					command := exec.Command(binary, args...)
					command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "GL_AXI_CONFIG=" + f.deps.Runtime.ConfigPath, "GL_AXI_TOKEN=" + f.keyring.token, "MR_WRITE_MARKER=" + marker}
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
					if exit != tc.exit || tc.outcome != "" && !strings.Contains(stdout.String(), `"outcome":"`+tc.outcome+`"`) {
						t.Fatalf("exit=%d want=%d output=%s stderr=%s", exit, tc.exit, stdout.String(), stderr.String())
					}
					if strings.Contains(stdout.String(), f.keyring.token) || strings.Contains(stderr.String(), f.keyring.token) {
						t.Fatal("synthetic credential escaped")
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("native invocation started official glab")
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != tc.writes {
						t.Fatalf("writes=%d want=%d", f.writes, tc.writes)
					}
					if len(other.Requests()) != 0 {
						t.Fatal("native MR command followed a cross-origin redirect")
					}
					for _, request := range f.server.Requests() {
						if strings.Contains(request.URL, "unauthorized") {
							t.Fatal("native MR command followed a cross-path redirect")
						}
					}
					if tc.exit == 2 && len(f.server.Requests()) != 0 {
						t.Fatal("invalid input performed network work")
					}
				})
			}
		})
	}
}
