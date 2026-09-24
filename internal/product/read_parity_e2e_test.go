package product

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This is the end-user path: build each supported executable, select a fake
// pinned glab on an isolated PATH, and assert output, exits and child argv.
// The fake has no network or credential access and refuses unexpected argv.
func TestReadParityExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process fixture uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contract := loadReadParityContract(t)
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GL_AXI_READ_RECORD"
if [ "$*" = version ]; then
  printf '%s\n' 'glab 1.112.0 (816e3a52)'
  exit 0
fi
[ "$*" = "$GL_AXI_READ_EXPECTED" ] || exit 1
/bin/cat "$GL_AXI_READ_RESPONSE"
`
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			for _, group := range []string{"issue", "mr"} {
				for _, action := range []string{"list", "view"} {
					t.Run("help "+group+" "+action, func(t *testing.T) {
						command := exec.Command(binary, group, action, "--help")
						command.Dir = dir
						command.Env = []string{"PATH=" + dir, "HOME=" + dir}
						output, err := command.CombinedOutput()
						if err != nil {
							t.Fatalf("help: %v: %s", err, output)
						}
						help := string(output)
						if strings.Contains(help, "--fields") != (action == "list") || strings.Contains(help, "--milestone") != (group == "issue" && action == "list") || strings.Contains(help, "open|opened") || strings.Contains(help, "--body-limit") || strings.Contains(help, "--not-draft") {
							t.Fatalf("unexpected help: %s", help)
						}
					})
				}
			}
			for _, test := range []struct {
				name            string
				args            []string
				expected        string
				wantDescription bool
			}{
				{"filtered issues", []string{"issue", "list", "--state=closed", "--label=triage", "--label=needs review", "--author=alice", "--assignee=bob", "--milestone=release 2", "--sort=updated", "--fields=description,labels"}, "issue list --output json --closed --label=triage --label=needs review --author=alice --assignee=bob --milestone=release 2 --order=updated_at --sort=desc --page 1 --per-page 31 -R group/project", true},
				{"filtered MRs", []string{"mr", "list", "--state=merged", "--source-branch=feature/topic", "--target-branch=main", "--fields=description"}, "mr list --output json --merged --source-branch=feature/topic --target-branch=main --page 1 --per-page 31 -R group/project", true},
				{"issue defaults", []string{"issue", "view", "42"}, "issue view 42 --output json -R group/project", true},
				{"MR defaults", []string{"mr", "view", "42"}, "mr view 42 --output json -R group/project", true},
				{"draft MRs", []string{"mr", "list", "--draft"}, "mr list --output json --draft --page 1 --per-page 31 -R group/project", false},
				{"open issues", []string{"issue", "list", "--state=open", "--fields=author"}, "issue list --output json --page 1 --per-page 31 -R group/project", false},
				{"open MRs", []string{"mr", "list", "--state=open", "--fields=author"}, "mr list --output json --page 1 --per-page 31 -R group/project", false},
			} {
				t.Run(test.name, func(t *testing.T) {
					record := filepath.Join(t.TempDir(), "calls")
					responseFile := filepath.Join(t.TempDir(), "response")
					body := readParityBody(t, test.args[0], test.args[1], strings.Repeat("界", 30))
					if err := os.WriteFile(responseFile, body, 0600); err != nil {
						t.Fatal(err)
					}
					command := exec.Command(binary, readParityArgs(test.args)...)
					command.Dir = dir
					command.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "GL_AXI_READ_RECORD=" + record, "GL_AXI_READ_RESPONSE=" + responseFile, "GL_AXI_READ_EXPECTED=" + test.expected}
					var stdout, stderr bytes.Buffer
					command.Stdout = &stdout
					command.Stderr = &stderr
					if err := command.Run(); err != nil {
						t.Fatalf("run: %v stdout=%s stderr=%s", err, &stdout, &stderr)
					}
					if stderr.Len() != 0 || !json.Valid(stdout.Bytes()) || strings.Contains(stdout.String(), `"description":`) != test.wantDescription {
						t.Fatalf("stdout=%s stderr=%s", &stdout, &stderr)
					}
					var envelope struct {
						Data struct {
							Issue  Issue          `json:"issue"`
							Issues []Issue        `json:"issues"`
							MR     MergeRequest   `json:"mr"`
							MRs    []MergeRequest `json:"mrs"`
						} `json:"data"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if test.args[0] == "issue" {
						item := envelope.Data.Issue
						if test.args[1] == "list" {
							if len(envelope.Data.Issues) != 1 {
								t.Fatalf("issues=%#v", envelope.Data.Issues)
							}
							item = envelope.Data.Issues[0]
						}
						if item.Author != "alice" || len(item.Labels) != 2 || item.CreatedAt == nil || item.UpdatedAt == nil || item.State != "opened" {
							t.Fatalf("default fields lost: %#v", item)
						}
					} else {
						item := envelope.Data.MR
						if test.args[1] == "list" {
							if len(envelope.Data.MRs) != 1 {
								t.Fatalf("mrs=%#v", envelope.Data.MRs)
							}
							item = envelope.Data.MRs[0]
						}
						if item.Author != "alice" || len(item.Labels) != 2 || item.CreatedAt == nil || item.UpdatedAt == nil || item.State != "opened" || item.BaseSHA == "" || item.HeadSHA == "" {
							t.Fatalf("default fields lost: %#v", item)
						}
					}
					calls, err := os.ReadFile(record)
					if err != nil {
						t.Fatal(err)
					}
					if string(calls) != "version\n"+test.expected+"\n" {
						t.Fatalf("unexpected child work: %s", calls)
					}
				})
			}
			for _, test := range readParityURLCases() {
				for _, action := range []string{"list", "view"} {
					t.Run("URL "+test.name+"/"+action, func(t *testing.T) {
						record := filepath.Join(t.TempDir(), "calls")
						responseFile := filepath.Join(t.TempDir(), "response")
						item := readParityObject(test.group, "body", 42)
						item["web_url"] = test.web
						var source any = item
						args := []string{test.group, action}
						expected := test.group + " list --output json --page 1 --per-page 31 -R group/project"
						if action == "list" {
							source = []any{item}
						} else {
							args = append(args, "42")
							expected = test.group + " view 42 --output json -R group/project"
						}
						body, err := json.Marshal(source)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(responseFile, body, 0600); err != nil {
							t.Fatal(err)
						}
						command := exec.Command(binary, readParityArgs(args)...)
						command.Dir = dir
						command.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GL_AXI_READ_RECORD=" + record, "GL_AXI_READ_RESPONSE=" + responseFile, "GL_AXI_READ_EXPECTED=" + expected}
						var stdout, stderr bytes.Buffer
						command.Stdout, command.Stderr = &stdout, &stderr
						err = command.Run()
						code := 0
						if err != nil {
							exit, ok := err.(*exec.ExitError)
							if !ok {
								t.Fatal(err)
							}
							code = exit.ExitCode()
						}
						if code != test.wantCode || stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
							t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, test.wantCode, &stdout, &stderr)
						}
						calls, err := os.ReadFile(record)
						if err != nil || string(calls) != "version\n"+expected+"\n" {
							t.Fatalf("unexpected child work: %s error=%v", calls, err)
						}
					})
				}
			}
			for _, args := range contract.Rejected {
				t.Run("reject "+strings.Join(args, " "), func(t *testing.T) {
					record := filepath.Join(t.TempDir(), "calls")
					command := exec.Command(binary, readParityArgs(args)...)
					command.Dir = dir
					command.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GL_AXI_READ_RECORD=" + record}
					var stdout, stderr bytes.Buffer
					command.Stdout = &stdout
					command.Stderr = &stderr
					err := command.Run()
					exit, ok := err.(*exec.ExitError)
					if !ok || exit.ExitCode() != 2 || stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
						t.Fatalf("error=%v stdout=%s stderr=%s", err, &stdout, &stderr)
					}
					if _, err := os.Stat(record); !os.IsNotExist(err) {
						t.Fatal("invalid input started a child")
					}
				})
			}
		})
	}
}
