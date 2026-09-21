package product

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The public executable, real parser, private-file reader, pinned child argv
// and receipt serializer are exercised together. This fake never uses a token
// store or network. Actual official-glab HTTP transport is covered separately
// by TestPinnedOfficialGlabIssueWritesTLS against a synthetic TLS service.
func TestIssueWritesExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, out)
			}
			fake := filepath.Join(dir, "glab")
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GL_AXI_TEST_RECORD"
if [ "${1-}" = version ]; then printf '%s\n' 'glab 1.112.0 (816e3a52)'; exit 0; fi
[ "$1 $2 $4 $5" = 'api --method --hostname gitlab.com' ]
method=$3
endpoint=$6
project='{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}'
state=opened
if [ "$GL_AXI_TEST_ACTION" = reopen ]; then state=closed; fi
if [ "$GL_AXI_TEST_MODE" = noop ]; then
  if [ "$GL_AXI_TEST_ACTION" = close ]; then state=closed; else state=opened; fi
fi
if [ -f "$GL_AXI_TEST_WRITTEN" ]; then
  if [ "$GL_AXI_TEST_ACTION" = close ]; then state=closed; else state=opened; fi
fi
issue="{\"id\":1001,\"iid\":42,\"project_id\":101,\"title\":\"new title\",\"description\":\"new body\",\"issue_type\":\"issue\",\"state\":\"$state\",\"web_url\":\"https://gitlab.com/group/project/-/issues/42\",\"updated_at\":\"2026-08-15T12:00:00Z\"}"
if [ "$method" = GET ]; then
  case "$endpoint" in
    projects/group%2Fproject)
      if [ "$GL_AXI_TEST_MODE" = wrong-project ]; then printf '%s' '{"id":102}'; else printf '%s' "$project"; fi;;
    projects/101/issues/42)
      if [ "$GL_AXI_TEST_MODE" = wrong-iid ]; then printf '%s' '{"id":1001,"iid":43}'; else printf '%s' "$issue"; fi;;
    *) exit 1;;
  esac
  exit 0
fi
[ "$7" = --input ]
[ "$9" = --header ]
[ "${10}" = 'Content-Type: application/json' ]
# A second mutation within one invocation is an unconditional fixture failure.
[ ! -e "$GL_AXI_TEST_WRITTEN" ]
printf '%s' "$method $endpoint" > "$GL_AXI_TEST_WRITTEN"
cp "$8" "$GL_AXI_TEST_PAYLOAD"
case "$GL_AXI_TEST_MODE" in
  rejected) printf '%s\n' 'glab: HTTP 422 private-provider-detail' >&2; exit 1;;
  lost) printf '%s\n' 'transport timeout private-provider-detail' >&2; exit 1;;
  malformed) printf '%s' '{'; exit 0;;
esac
case "$method $endpoint" in
  'POST projects/101/issues') printf '%s' "$issue";;
  'POST projects/101/issues/42/notes') printf '%s' '{"id":3001,"project_id":101,"noteable_id":1001,"noteable_iid":42,"noteable_type":"Issue","body":"new body","system":false,"internal":false}';;
  'PUT projects/101/issues/42')
    if [ "$GL_AXI_TEST_ACTION" = close ]; then printf '%s' "$issue" | tr '\n' ' ' | /usr/bin/sed 's/"state":"opened"/"state":"closed"/'; else printf '%s' "$issue" | /usr/bin/sed 's/"state":"closed"/"state":"opened"/'; fi;;
  *) exit 1;;
esac
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"create", "comment", "note", "close", "reopen"} {
				for _, mode := range []string{"success", "rejected", "lost", "malformed", "wrong-project", "wrong-iid", "invalid-host", "invalid-url", "quick-action", "noop"} {
					t.Run(action+"/"+mode, func(t *testing.T) {
						stateOp := action == "close" || action == "reopen"
						if mode == "noop" && !stateOp || mode == "quick-action" && stateOp || mode == "wrong-iid" && action == "create" {
							t.Skip("not applicable")
						}
						runDir := t.TempDir()
						record := filepath.Join(runDir, "record")
						written := filepath.Join(runDir, "written")
						args := issueWriteArgs(t, action)
						wantExit, wantWrites := 0, 1
						switch mode {
						case "rejected":
							wantExit = 2
						case "lost", "malformed":
							wantExit = 6
						case "wrong-project", "wrong-iid":
							wantExit, wantWrites = 9, 0
						case "invalid-host":
							args = replaceIssueWriteArg(args, "--hostname", "https://gitlab.com")
							wantExit, wantWrites = 2, 0
						case "invalid-url":
							args = replaceIssueWriteArg(args, "--expected-url", "https://wrong.example/group/project/-/issues/42")
							wantExit, wantWrites = 9, 0
						case "quick-action":
							for i, v := range args {
								if v == "--body-file" || v == "--description-file" {
									if err := os.WriteFile(args[i+1], []byte("body\n/close"), 0600); err != nil {
										t.Fatal(err)
									}
								}
							}
							wantExit, wantWrites = 2, 0
						case "noop":
							state := "closed"
							if action == "reopen" {
								state = "opened"
							}
							args = replaceIssueWriteArg(args, "--expected-state", state)
							wantWrites = 0
						}
						command := exec.Command(binary, args...)
						command.Dir = root
						// Only a synthetic credential in an isolated home, never caller config.
						token := strings.Join([]string{"synthetic", "issue-write", "e2e"}, "-")
						command.Env = []string{"HOME=" + runDir, "PATH=" + dir + ":/usr/bin:/bin", "GLAB_CONFIG_DIR=" + runDir, "GITLAB_TOKEN=" + token, "GL_AXI_TEST_ACTION=" + action, "GL_AXI_TEST_MODE=" + mode, "GL_AXI_TEST_RECORD=" + record, "GL_AXI_TEST_WRITTEN=" + written, "GL_AXI_TEST_PAYLOAD=" + filepath.Join(runDir, "payload")}
						var stdout, stderr bytes.Buffer
						command.Stdout, command.Stderr = &stdout, &stderr
						err := command.Run()
						code := 0
						if err != nil {
							exit, ok := err.(*exec.ExitError)
							if !ok {
								t.Fatal(err)
							}
							code = exit.ExitCode()
						}
						if code != wantExit || stderr.Len() != 0 {
							t.Fatalf("exit=%d want=%d %s %s", code, wantExit, &stdout, &stderr)
						}
						log, _ := os.ReadFile(record)
						mutations := strings.Count(string(log), "--method POST") + strings.Count(string(log), "--method PUT")
						if mutations != wantWrites || strings.Contains(string(log), token) || strings.Contains(stdout.String(), token) || strings.Contains(stdout.String(), "private-provider-detail") || strings.Contains(string(log), "--page") || strings.Contains(string(log), "search") {
							t.Fatalf("log=%s stdout=%s", log, &stdout)
						}
						if mode == "invalid-host" || mode == "invalid-url" || mode == "quick-action" {
							if len(log) != 0 {
								t.Fatalf("invalid input spawned child: %s", log)
							}
						}
						if wantWrites == 1 {
							_, _, r := decodeIssueWriteEnvelope(t, stdout.Bytes())
							if r.MutationAttempts != 1 || r.RetrySafe || r.AtomicPrecondition {
								t.Fatalf("receipt=%+v", r)
							}
						}
					})
				}
			}
		})
	}
}
