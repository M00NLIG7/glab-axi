package product

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

func testReadPaginationExecutable(t *testing.T, binary, dir, fixtureDir string) {
	t.Helper()
	for _, group := range []string{"issue", "mr"} {
		for _, tc := range []struct {
			name, reason       string
			pages, limit, last int
			code               int
			complete           bool
		}{
			{"exact", "", 2, 100, 0, 0, true},
			{"display", "display_limit", 2, 100, 1, 0, false},
			{"hard-pages", "hard_page_limit", 10, 1000, 100, 0, false},
			{"bad-page", "provider_response", 2, 100, 0, 8, false},
			{"page-overflow", "provider_response", 1, 100, 101, 8, false},
		} {
			t.Run(group+"/"+tc.name, func(t *testing.T) {
				pages := t.TempDir()
				record := filepath.Join(t.TempDir(), "calls")
				pattern := group + " list --output json --all --label=triage --label=needs review --page {page} --per-page 100 -R group/project"
				var wantCalls strings.Builder
				wantCalls.WriteString("version\n")
				for page := 1; page <= tc.pages; page++ {
					n := 100
					if page == tc.pages {
						n = tc.last
					}
					items := make([]any, n)
					for i := range items {
						items[i] = readParityObject(group, "body", (page-1)*100+i+1)
					}
					body, err := json.Marshal(items)
					if err != nil {
						t.Fatal(err)
					}
					if tc.name == "bad-page" && page == 2 {
						body = []byte(`{"malformed":`)
					}
					if err := os.WriteFile(filepath.Join(pages, strconv.Itoa(page)+".json"), body, 0600); err != nil {
						t.Fatal(err)
					}
					wantCalls.WriteString(strings.ReplaceAll(pattern, "{page}", strconv.Itoa(page)) + "\n")
				}
				command := exec.Command(binary, readParityArgs([]string{group, "list", "--state=all", "--label=triage", "--label=needs review", "--limit=" + strconv.Itoa(tc.limit)})...)
				command.Dir = dir
				command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record, "GL_AXI_READ_PAGES="+pages, "GL_AXI_READ_EXPECTED="+pattern)
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				_ = command.Run()
				if command.ProcessState == nil || command.ProcessState.ExitCode() != tc.code || stderr.Len() != 0 {
					t.Fatalf("exit=%v stdout=%s stderr=%s", command.ProcessState, &stdout, &stderr)
				}
				var envelope uxv1.Envelope
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.OK != (tc.code == 0) || envelope.Meta.Complete != tc.complete || envelope.Meta.Reason != tc.reason || tc.code != 0 && envelope.Data != nil {
					t.Fatalf("unexpected result: %s", &stdout)
				}
				if tc.code == 0 && envelope.Meta.Count != tc.limit {
					t.Fatalf("count=%d want=%d", envelope.Meta.Count, tc.limit)
				}
				calls, err := os.ReadFile(record)
				if err != nil || string(calls) != wantCalls.String() {
					t.Fatalf("unexpected child work: %s error=%v", calls, err)
				}
			})
		}
	}
}

func testReadBodyTruncationExecutable(t *testing.T, binary, dir, fixtureDir string) {
	t.Helper()
	for _, group := range []string{"issue", "mr"} {
		for _, action := range []string{"list", "view"} {
			t.Run(group+"/"+action, func(t *testing.T) {
				record := filepath.Join(t.TempDir(), "calls")
				response := filepath.Join(t.TempDir(), "response.json")
				body := readParityBody(t, group, action, strings.Repeat("界", limits.MaxDescriptionBytes/3+1))
				if err := os.WriteFile(response, body, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{group, action, "--fields=description"}
				expected := group + " list --output json --page 1 --per-page 31 -R group/project"
				if action == "view" {
					args[2] = "42"
					expected = group + " view 42 --output json -R group/project"
				}
				command := exec.Command(binary, readParityArgs(args)...)
				command.Dir = dir
				command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record, "GL_AXI_READ_RESPONSE="+response, "GL_AXI_READ_EXPECTED="+expected)
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v %s", err, out)
				}
				var envelope struct {
					Data map[string]json.RawMessage `json:"data"`
					Meta uxv1.Meta                  `json:"meta"`
				}
				if err := json.Unmarshal(out, &envelope); err != nil {
					t.Fatal(err)
				}
				var item struct{ Description string }
				if action == "view" {
					err = json.Unmarshal(envelope.Data[group], &item)
				} else {
					key := "issues"
					if group == "mr" {
						key = "mrs"
					}
					var items []struct{ Description string }
					err = json.Unmarshal(envelope.Data[key], &items)
					if len(items) != 1 {
						t.Fatal("expected one resource")
					}
					item = items[0]
				}
				if err != nil || !utf8.ValidString(item.Description) || len(item.Description) > limits.MaxDescriptionBytes || len(item.Description) < limits.MaxDescriptionBytes-3 || !strings.HasSuffix(item.Description, "…[truncated]") || !envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "field_limit" {
					t.Fatalf("invalid body bound: bytes=%d meta=%+v error=%v", len(item.Description), envelope.Meta, err)
				}
			})
		}
	}
}
