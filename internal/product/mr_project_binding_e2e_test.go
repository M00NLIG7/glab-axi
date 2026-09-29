package product

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Model a project path being reassigned after preflight: a mutation by path now
// addresses another project, while the previously resolved numeric ID does not.
// This is synthetic route/identity evidence, not live GitLab acceptance proof.
func TestMRNativeImmutableProjectRouteExecutableTLS(t *testing.T) {
	t.Parallel()
	const lookup = "/gitlab/api/v4/projects/group%2Fproject"
	const bound = "/gitlab/api/v4/projects/101"
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := buildMRExecutable(t, program)
			for _, action := range []string{"comment", "ensure"} {
				t.Run(action, func(t *testing.T) {
					t.Parallel()
					f := newMRNativeFixture(t, "opened")
					wrongProjectWrites := 0
					f.configure(func() {
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							path := r.URL.EscapedPath()
							if path == lookup {
								return false
							}
							byPath := strings.HasPrefix(path, lookup+"/")
							if !byPath && !strings.HasPrefix(path, bound+"/") {
								return false
							}
							suffix := strings.TrimPrefix(path, bound)
							if byPath {
								suffix = strings.TrimPrefix(path, lookup)
							}
							if r.Method == http.MethodPost && byPath {
								wrongProjectWrites++
							}
							switch {
							case r.Method == http.MethodGet && suffix == "/merge_requests":
								_, _ = io.WriteString(w, "[]")
							case r.Method == http.MethodGet && suffix == "/merge_requests/42":
								record := f.mr()
								if byPath && wrongProjectWrites != 0 {
									record["id"], record["project_id"] = 2042, 202
									record["source_project_id"], record["target_project_id"] = 202, 202
								}
								_ = json.NewEncoder(w).Encode(record)
							case r.Method == http.MethodPost && suffix == "/merge_requests/42/notes":
								var input map[string]string
								if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
									t.Error(err)
								}
								f.storedNoteBody = input["body"]
								note := mrWriteNote(f.storedNoteBody)
								if byPath {
									note["project_id"], note["noteable_id"] = 202, 2042
								}
								_ = json.NewEncoder(w).Encode(note)
							case r.Method == http.MethodGet && suffix == "/merge_requests/42/notes/501":
								_ = json.NewEncoder(w).Encode(mrWriteNote(f.storedNoteBody))
							case r.Method == http.MethodPost && suffix == "/merge_requests":
								record := ensureMR(11, "Draft: title", "body")
								record.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
								record.Draft = true
								if byPath {
									record.SourceProjectID, record.TargetProjectID = 202, 202
								}
								_ = json.NewEncoder(w).Encode(record)
							default:
								t.Errorf("unexpected route: %s %s", r.Method, path)
								w.WriteHeader(http.StatusNotFound)
							}
							return true
						}
					})
					args := f.args("comment")
					if action == "ensure" {
						args = append(replaceArg(ensureArgs(t, "title", "body"), "gitlab.com", mrWriteTestHost), "--auth-source", "native", "--draft")
					}
					code, out := runMRExecutable(t, binary, f, args)
					if code != 0 {
						t.Errorf("bound target failed: exit=%d output=%s", code, out)
					}
					requests := f.server.Requests()
					if len(requests) < 2 || requests[0].URL != lookup {
						t.Fatalf("missing exact initial project lookup: %+v", requests)
					}
					for _, request := range requests[1:] {
						if !strings.HasPrefix(request.URL, bound+"/") {
							t.Errorf("request was not bound to resolved project ID: %s %s", request.Method, request.URL)
						}
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.writes != 1 || wrongProjectWrites != 0 {
						t.Errorf("writes=%d wrong-project writes=%d", f.writes, wrongProjectWrites)
					}
				})
			}
		})
	}
}
