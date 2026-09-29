package product

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMRNativeEnsureExactUpdateIdentityExecutableTLS(t *testing.T) {
	t.Parallel()
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := buildMRExecutable(t, program)
			for _, mode := range []string{"valid", "head", "global-id", "duplicate-project"} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					f := newMRNativeFixture(t, "opened")
					record := ensureMR(11, "old", "body")
					record.WebURL = mrNativeWebBase + "/group/project/-/merge_requests/11"
					f.configure(func() {
						f.mutate = func(w http.ResponseWriter, r *http.Request) bool {
							path := r.URL.EscapedPath()
							if path == mrNativeProjectLookupPath && mode == "duplicate-project" {
								_, _ = io.WriteString(w, `{"id":202,"id":101,"path_with_namespace":"group/project","web_url":"`+mrNativeWebBase+`/group/project"}`)
								return true
							}
							if path == mrNativeAPIPath+"/merge_requests" && r.Method == http.MethodGet {
								_ = json.NewEncoder(w).Encode([]upstreamMR{record})
								return true
							}
							if path == mrNativeAPIPath+"/merge_requests/11" {
								if r.Method == http.MethodPut {
									var input map[string]string
									if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input) != 2 || input["title"] != "wanted" || input["description"] != "body" {
										t.Error("unexpected ensure update payload")
									}
									record.Title = "wanted"
									if mode == "head" {
										record.SHA = strings.Repeat("d", 40)
									}
									if mode == "global-id" {
										record.ID++
									}
								}
								_ = json.NewEncoder(w).Encode(record)
								return true
							}
							return false
						}
					})
					args := append(replaceArg(ensureArgs(t, "wanted", "body"), "gitlab.com", mrWriteTestHost), "--auth-source", "native")
					code, out := runMRExecutable(t, binary, f, args)
					want, writes := 6, 1
					if mode == "valid" {
						want = 0
					}
					if mode == "duplicate-project" {
						want, writes = 8, 0
					}
					if code != want {
						t.Errorf("exit=%d want=%d output=%s", code, want, out)
					}
					if want == 6 && !strings.Contains(out, `"code":"ambiguous_update"`) {
						t.Errorf("unproved update accepted: %s", out)
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
