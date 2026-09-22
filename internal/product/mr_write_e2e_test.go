package product

import (
	"bytes"
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

// Both public executables, the real parser/adapter, and synthetic TLS protocol
// fixtures are exercised. This fake is deliberately only a protocol relay for
// the exact pinned argv; it has no credential/profile/network fallback.
func TestMRWritesExecutableTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX protocol relay")
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl protocol relay unavailable")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binDir := t.TempDir()
			binary := filepath.Join(binDir, program)
			build := exec.Command("go", "build", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			t.Run("invalid input starts no child", func(t *testing.T) {
				dir := t.TempDir()
				marker := filepath.Join(dir, "child-started")
				script := "#!/bin/sh\nprintf child > \"$MR_WRITE_MARKER\"\nexit 99\n"
				if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				body := filepath.Join(dir, "body")
				if err := os.WriteFile(body, []byte("ordinary prose\n/merge"), 0600); err != nil {
					t.Fatal(err)
				}
				base := mrWriteArgs("close", "opened")
				for _, args := range [][]string{
					replaceArg(base, "42", "042"), replaceArg(base, mrWriteTestURL, mrWriteTestURL+"?wrong=1"),
					removeFlag(base, "--expected-head", true), appendCopy(base, "--expected-state", "closed"),
					append(mrWriteArgs("comment", "opened"), "--body-file", body),
				} {
					command := exec.Command(binary, args...)
					command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "MR_WRITE_MARKER=" + marker}
					if output, err := command.CombinedOutput(); err == nil {
						t.Fatalf("invalid invocation succeeded: %s", output)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatalf("invalid input started a child: %v", err)
					}
				}
			})
			cases := []struct {
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
				{"malformed state reply reconciles", "close", "opened", "malformed", 1, 0, "observed"},
				{"lost state reply reconciles", "reopen", "closed", "lost", 1, 0, "observed"},
				{"unchanged postcondition", "close", "opened", "not-applied", 1, 6, "unknown"},
				{"note lost ID never guessed", "comment", "opened", "lost", 1, 6, "unknown"},
				{"note malformed ID never guessed", "comment", "opened", "malformed", 1, 6, "unknown"},
				{"note exact read ID mismatch", "comment", "opened", "note-id-drift", 1, 6, "unknown"},
				{"note wrong resource", "comment", "opened", "note-resource", 1, 6, "unknown"},
				{"note body drift", "comment", "opened", "note-body", 1, 6, "unknown"},
				{"state rejected", "close", "opened", "rejected", 1, 4, "rejected"},
				{"note rejected", "comment", "opened", "rejected", 1, 4, "rejected"},
				{"preflight drift", "close", "opened", "drift", 0, 6, ""},
				{"wrong project", "comment", "opened", "project", 0, 9, ""},
				{"wrong MR IID", "close", "opened", "iid", 0, 9, ""},
				{"wrong URL", "close", "opened", "url", 0, 9, ""},
				{"wrong head", "close", "opened", "head", 0, 6, ""},
				{"wrong branch", "close", "opened", "branch", 0, 6, ""},
				{"fork denied", "close", "opened", "fork", 0, 6, ""},
				{"merged denied", "reopen", "closed", "merged", 0, 6, ""},
				{"missing base", "close", "opened", "missing-base", 0, 8, ""},
				{"post head drift", "close", "opened", "post-head", 1, 6, "unknown"},
				{"post state unreadable", "close", "opened", "post-malformed", 1, 6, "unknown"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					var mu sync.Mutex
					writes, reads, noteReads := 0, 0, 0
					state := tc.initial
					body := "A synthetic ordinary note.\n"
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						path := r.URL.EscapedPath()
						projectPath := "/api/v4/projects/group%2Fproject"
						if r.Method == "GET" && path == projectPath {
							id := 101
							if tc.mode == "project" {
								id = 102
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "path_with_namespace": "group/project", "web_url": "https://" + mrWriteTestHost + "/group/project"})
							return
						}
						if r.Method == "GET" && path == projectPath+"/merge_requests/42" {
							reads++
							record := mrWriteRecord(state)
							switch tc.mode {
							case "drift":
								if reads == 2 {
									record["updated_at"] = "2026-08-15T12:00:01Z"
								}
							case "iid":
								record["iid"] = 43
							case "url":
								record["web_url"] = mrWriteTestURL + "0"
							case "head":
								record["sha"] = strings.Repeat("c", 40)
							case "branch":
								record["target_branch"] = "other"
							case "fork":
								record["source_project_id"] = 102
							case "merged":
								record["state"] = "merged"
							case "missing-base":
								delete(record, "base_sha")
							case "post-head":
								if writes > 0 {
									record["sha"] = strings.Repeat("c", 40)
								}
							case "post-malformed":
								if writes > 0 {
									_, _ = io.WriteString(w, "{broken")
									return
								}
							}
							_ = json.NewEncoder(w).Encode(record)
							return
						}
						if r.Method == "GET" && path == projectPath+"/merge_requests/42/notes/501" {
							noteReads++
							note := mrWriteNote(body)
							if tc.mode == "note-id-drift" {
								note["id"] = 502
							}
							if tc.mode == "note-body" {
								note["body"] = "unseen edit"
							}
							_ = json.NewEncoder(w).Encode(note)
							return
						}
						if (r.Method == "POST" && path == projectPath+"/merge_requests/42/notes") || (r.Method == "PUT" && path == projectPath+"/merge_requests/42") {
							writes++
							var input map[string]any
							if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 1 {
								t.Errorf("unsafe input: %v %v", input, err)
							}
							if r.Method == "POST" && input["body"] != body {
								t.Errorf("note body=%v", input)
							}
							if r.Method == "PUT" && input["state_event"] != tc.action {
								t.Errorf("state input=%v", input)
							}
							if tc.mode == "rejected" {
								w.WriteHeader(403)
								return
							}
							if tc.mode != "not-applied" && r.Method == "PUT" {
								state = "closed"
								if tc.action == "reopen" {
									state = "opened"
								}
							}
							if tc.mode == "lost" {
								w.WriteHeader(503)
								return
							}
							if tc.mode == "malformed" {
								_, _ = io.WriteString(w, "{broken")
								return
							}
							if r.Method == "POST" {
								note := mrWriteNote(body)
								if tc.mode == "note-resource" {
									note["noteable_id"] = 999
								}
								_ = json.NewEncoder(w).Encode(note)
							} else {
								_ = json.NewEncoder(w).Encode(mrWriteRecord(state))
							}
							return
						}
						t.Errorf("undeclared protocol request %s %s", r.Method, r.URL)
						w.WriteHeader(500)
					}))
					defer server.Close()
					cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
					if err != nil {
						t.Fatal(err)
					}
					ca := filepath.Join(dir, "ca.pem")
					if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
						t.Fatal(err)
					}
					counter := filepath.Join(dir, "argv")
					script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> "$MR_WRITE_ARGV"
if [ "$*" = version ]; then printf 'glab 1.112.0 (816e3a52)\n'; exit 0; fi
method=GET
input=
case "$*" in
  'mr view 42 --output json -R group/project') endpoint='projects/group%%2Fproject/merge_requests/42' ;;
  'api --method GET --hostname gitlab.example.invalid projects/group%%2Fproject'|'api --method GET --hostname gitlab.example.invalid projects/group%%2Fproject/merge_requests/42/notes/501') endpoint="$6" ;;
  'api --method PUT --hostname gitlab.example.invalid projects/group%%2Fproject/merge_requests/42 --input '*|'api --method POST --hostname gitlab.example.invalid projects/group%%2Fproject/merge_requests/42/notes --input '*)
    method="$3"; endpoint="$6"; input="$8"
    [ "$9" = --header ]; [ "${10}" = 'Content-Type: application/json' ]; [ "$#" -eq 10 ] ;;
  *) exit 91 ;;
esac
if [ -n "$input" ]; then
  code=$(%q --silent --show-error --max-time 5 --cacert "$MR_WRITE_CA" --request "$method" --header 'Content-Type: application/json' --data-binary "@$input" --output "$MR_WRITE_RESPONSE" --write-out '%%{http_code}' "$MR_WRITE_SERVER/api/v4/$endpoint")
else
  code=$(%q --silent --show-error --max-time 5 --cacert "$MR_WRITE_CA" --output "$MR_WRITE_RESPONSE" --write-out '%%{http_code}' "$MR_WRITE_SERVER/api/v4/$endpoint")
fi
if [ "$code" != 200 ]; then printf 'HTTP %%s\n' "$code" >&2; exit 1; fi
/bin/cat "$MR_WRITE_RESPONSE"
`, curl, curl)
					if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
						t.Fatal(err)
					}
					args := mrWriteArgs(tc.action, tc.initial)
					if tc.action == "comment" || tc.action == "note" {
						input := filepath.Join(dir, "body")
						if err := os.WriteFile(input, []byte(body), 0600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--body-file", input)
					}
					command := exec.Command(binary, args...)
					command.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "MR_WRITE_ARGV=" + counter, "MR_WRITE_CA=" + ca, "MR_WRITE_SERVER=" + server.URL, "MR_WRITE_RESPONSE=" + filepath.Join(dir, "response")}
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					runErr := command.Run()
					exit := 0
					if runErr != nil {
						if e, ok := runErr.(*exec.ExitError); ok {
							exit = e.ExitCode()
						} else {
							t.Fatal(runErr)
						}
					}
					if exit != tc.exit || tc.outcome != "" && !strings.Contains(stdout.String(), `"outcome":"`+tc.outcome+`"`) {
						t.Fatalf("exit=%d want=%d output=%s stderr=%s", exit, tc.exit, stdout.String(), stderr.String())
					}
					mu.Lock()
					defer mu.Unlock()
					if writes != tc.writes {
						t.Fatalf("writes=%d want=%d", writes, tc.writes)
					}
					if tc.outcome == "created" && noteReads != 1 {
						t.Fatalf("note readbacks=%d", noteReads)
					}
					argv, err := os.ReadFile(counter)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(argv), body) || strings.Contains(stdout.String(), body) {
						t.Fatal("body leaked into argv or receipt")
					}
					if tc.writes == 1 && reads != 3 {
						t.Fatalf("MR reads=%d want=3", reads)
					}
				})
			}
		})
	}
}
