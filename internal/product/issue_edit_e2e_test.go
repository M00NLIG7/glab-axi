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

	"gl-axi/internal/contract/uxv1"
)

// TestIssueEditExecutableAliasesEndToEnd builds both public executable names
// and drives their guarded issue-edit command through a process-level fake of
// the pinned official-glab boundary. It is the closest local user path without
// a credential or live GitLab service.
func TestIssueEditExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process fixture uses a POSIX shell")
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
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}

			counter := filepath.Join(dir, "count")
			record := filepath.Join(dir, "record")
			fakeGlab := filepath.Join(dir, "glab")
			script := `#!/bin/sh
set -eu
if [ "${1-}" = version ]; then
  printf '%s\n' 'glab 1.112.0 (816e3a52)'
  exit 0
fi
n=0
if [ -f "$GL_AXI_E2E_COUNT" ]; then n=$(cat "$GL_AXI_E2E_COUNT"); fi
n=$((n + 1))
printf '%s' "$n" > "$GL_AXI_E2E_COUNT"
printf '%s\n' "$*" >> "$GL_AXI_E2E_RECORD"
project='{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}'
before='{"id":1001,"iid":42,"project_id":101,"title":"old title","description":"old body","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/42","labels":["keep"],"updated_at":"2026-08-15T12:00:00Z"}'
after='{"id":1001,"iid":42,"project_id":101,"title":"new title","description":"old body","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/42","labels":["keep"],"updated_at":"2026-08-15T12:00:01Z"}'
case "$n" in
  1)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject' ]
    printf '%s' "$project"
    ;;
  2|3)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/42' ]
    printf '%s' "$before"
    ;;
  4)
    [ "$1" = api ] && [ "$2" = --method ] && [ "$3" = PUT ] &&
      [ "$4" = --hostname ] && [ "$5" = gitlab.com ] &&
      [ "$6" = 'projects/group%2Fproject/issues/42' ] && [ "$7" = --input ] &&
      [ "$9" = --header ] && [ "${10}" = 'Content-Type: application/json' ]
    grep -Fxq '{"title":"new title"}' "$8"
    printf '%s' "$after"
    ;;
  5)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/42' ]
    printf '%s' "$after"
    ;;
  *)
    printf '%s\n' 'unexpected invocation' >&2
    exit 1
    ;;
esac
`
			if err := os.WriteFile(fakeGlab, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			title := filepath.Join(dir, "title")
			if err := os.WriteFile(title, []byte("new title\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			secret := strings.Join([]string{"synthetic", "executable", "token"}, "-")
			command := exec.Command(binary,
				"issue", "edit", "42", "--repo", "group/project", "--hostname", "gitlab.com",
				"--expected-url", issueEditTestURL, "--expected-state", "opened",
				"--expected-updated-at", issueEditTestTimestamp, "--title-file", title, "--format", "json",
			)
			command.Dir = root
			command.Env = append(os.Environ(),
				"PATH="+dir+":/usr/bin:/bin",
				"GL_AXI_E2E_COUNT="+counter,
				"GL_AXI_E2E_RECORD="+record,
				"GITLAB_TOKEN="+secret,
			)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("run: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
			}
			var envelope struct {
				Schema string          `json:"schema"`
				OK     bool            `json:"ok"`
				Data   issueEditOutput `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("decode: %v output=%s", err, stdout.String())
			}
			if envelope.Schema != uxv1.Schema || !envelope.OK || envelope.Data.Edit.Action != "updated" || envelope.Data.Edit.Outcome != "applied" {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
			recorded, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if lines := strings.Count(strings.TrimSpace(string(recorded)), "\n") + 1; lines != 5 || strings.Contains(string(recorded), secret) || strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
				t.Fatalf("record/output violated boundary: lines=%d record=%q stdout=%q stderr=%q", lines, recorded, stdout.String(), stderr.String())
			}
		})
	}
}
