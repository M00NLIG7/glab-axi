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
// The Go fixture has no network or credential access and runs on Windows too.
func TestReadParityExecutableAliasesEndToEnd(t *testing.T) {
	contract := loadReadParityContract(t)
	fixtureDir := t.TempDir()
	fixture := exec.Command("go", "build", "-p", "1", "-trimpath", "-o", filepath.Join(fixtureDir, readExecutableName("glab")), "./testdata/read-glab")
	if output, err := fixture.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v: %s", err, output)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := productTestExecutable(t, program, true)
			for _, group := range []string{"issue", "mr"} {
				for _, action := range []string{"list", "view"} {
					t.Run("help "+group+" "+action, func(t *testing.T) {
						command := exec.Command(binary, group, action, "--help")
						command.Dir = dir
						command.Env = readProcessEnv(dir, fixtureDir)
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
					command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record, "GL_AXI_READ_RESPONSE="+responseFile, "GL_AXI_READ_EXPECTED="+test.expected)
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
						command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record, "GL_AXI_READ_RESPONSE="+responseFile, "GL_AXI_READ_EXPECTED="+expected)
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
			for _, row := range contract.Capabilities {
				t.Run("matrix/"+row.Name, func(t *testing.T) {
					record := filepath.Join(t.TempDir(), "calls")
					response := filepath.Join(t.TempDir(), "response")
					body := readParityBody(t, row.Argv[0], row.Argv[1], "matrix body")
					if err := os.WriteFile(response, body, 0600); err != nil {
						t.Fatal(err)
					}
					var previous []byte
					for attempt := 0; attempt < 2; attempt++ {
						command := exec.Command(binary, readParityArgs(row.Argv)...)
						command.Dir = dir
						command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record, "GL_AXI_READ_RESPONSE="+response, "GL_AXI_READ_EXPECTED="+row.Upstream)
						var stdout, stderr bytes.Buffer
						command.Stdout, command.Stderr = &stdout, &stderr
						_ = command.Run()
						if command.ProcessState == nil || command.ProcessState.ExitCode() != row.Exit || stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
							t.Fatalf("%s: exit=%v stdout=%s stderr=%s", row.Reference, command.ProcessState, &stdout, &stderr)
						}
						if attempt > 0 && !bytes.Equal(previous, stdout.Bytes()) {
							t.Fatal("nondeterministic matrix response")
						}
						previous = append([]byte(nil), stdout.Bytes()...)
					}
					calls, err := os.ReadFile(record)
					if row.Exit == 0 {
						if err != nil || string(calls) != strings.Repeat("version\n"+row.Upstream+"\n", 2) {
							t.Fatalf("unexpected matrix child work: %s error=%v", calls, err)
						}
					} else if !os.IsNotExist(err) {
						t.Fatal("parity gap started a provider child")
					}
				})
			}
			t.Run("output bounds", func(t *testing.T) {
				testReadListOutputBoundsExecutable(t, binary, dir, fixtureDir)
			})
			t.Run("pagination", func(t *testing.T) {
				testReadPaginationExecutable(t, binary, dir, fixtureDir)
			})
			t.Run("body truncation", func(t *testing.T) {
				testReadBodyTruncationExecutable(t, binary, dir, fixtureDir)
			})
			for _, args := range contract.Rejected {
				t.Run("reject "+strings.Join(args, " "), func(t *testing.T) {
					record := filepath.Join(t.TempDir(), "calls")
					command := exec.Command(binary, readParityArgs(args)...)
					command.Dir = dir
					command.Env = readProcessEnv(dir, fixtureDir, "GL_AXI_READ_RECORD="+record)
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

func readExecutableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func readProcessEnv(home, fixtureDir string, extra ...string) []string {
	env := []string{"PATH=" + fixtureDir, "HOME=" + home, "USERPROFILE=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config")}
	if runtime.GOOS == "windows" {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"), "PATHEXT=.EXE")
	}
	return append(env, extra...)
}
