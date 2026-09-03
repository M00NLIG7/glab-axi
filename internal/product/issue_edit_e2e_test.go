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
// and drives their issue-edit validation and live-refusal path through a
// process-level fake of the pinned official-glab boundary. It is the closest
// local user path without a credential or live GitLab service.
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
case "$n" in
  1)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject' ]
    printf '%s' "$project"
    ;;
  2|3)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/42' ]
    printf '%s' "$before"
    ;;
  *)
    printf '%s\n' 'unexpected mutation or extra read' >&2
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
			runErr := command.Run()
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok || exitErr.ExitCode() != 9 {
				t.Fatalf("run error=%v stderr=%s stdout=%s", runErr, stderr.String(), stdout.String())
			}
			var envelope struct {
				Schema string `json:"schema"`
				OK     bool   `json:"ok"`
				Error  struct {
					Code    uxv1.Code `json:"code"`
					Message string    `json:"message"`
					Receipt struct {
						Edit struct {
							Action        string            `json:"action"`
							Outcome       string            `json:"outcome"`
							DryRun        bool              `json:"dry_run"`
							RefusalReason string            `json:"refusal_reason"`
							Identity      issueEditIdentity `json:"identity"`
							Expected      issueEditExpected `json:"expected"`
							ChangedFields []string          `json:"changed_fields"`
						} `json:"edit"`
					} `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("decode: %v output=%s", err, stdout.String())
			}
			edit := envelope.Error.Receipt.Edit
			if envelope.Schema != uxv1.Schema || envelope.OK || envelope.Error.Code != uxv1.CodeSafety || envelope.Error.Message != "GitLab issue edit refused before mutation: the provider cannot enforce the expected issue revision or requested numeric label identities; use --dry-run for a validated preview" || edit.Action != "refused" || edit.Outcome != "not_applied" || edit.DryRun || edit.RefusalReason != "provider_precondition_unavailable" || edit.Identity.IssueID != 1001 || edit.Expected.UpdatedAt != issueEditTestTimestamp || len(edit.ChangedFields) != 1 || edit.ChangedFields[0] != "title" {
				t.Fatalf("unexpected envelope: %#v", envelope)
			}
			recorded, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if lines := strings.Count(strings.TrimSpace(string(recorded)), "\n") + 1; lines != 3 || strings.Contains(string(recorded), "--method PUT") || strings.Contains(string(recorded), secret) || strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
				t.Fatalf("record/output violated boundary: lines=%d record=%q stdout=%q stderr=%q", lines, recorded, stdout.String(), stderr.String())
			}
		})
	}
}
