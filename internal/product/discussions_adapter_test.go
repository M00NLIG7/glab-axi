package product

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	runtimepkg "gl-axi/internal/runtime"
)

// TestMRDiscussionsOfficialAdapterEndToEnd exercises the public command through
// an actual child process. The synthetic child accepts only the pinned MR view
// and fixed discussion GET argv, so any write-capable or generic path fails.
func TestMRDiscussionsOfficialAdapterEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	script, record := fakeDiscussionGlab(t)
	secret := strings.Join([]string{"synthetic", "discussion", "token", "sentinel"}, "-")
	var stdout, stderr bytes.Buffer
	deps := Dependencies{
		Runtime: runtimepkg.Dependencies{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: t.TempDir(),
			LookupEnv: func(string) (string, bool) { return "", false },
		},
		GlabPath: script,
		Env: append(os.Environ(),
			"GLAB_AXI_FAKE_DISCUSSION_RECORD="+record,
			"GITLAB_TOKEN="+secret,
		),
		IsHumanTerminal: func() bool { return false },
	}
	if code := Run(context.Background(), discussionCommand("json", 1), deps); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			MR          MRDiscussionIdentity `json:"mr"`
			Discussions []MRDiscussion       `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Data.MR.IID != discussionTestIID || len(envelope.Data.Discussions) != 1 || envelope.Data.Discussions[0].Notes[0].Body != "adapter note" || !envelope.Meta.Complete || envelope.Meta.Count != 1 {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("child credential leaked through normalized output")
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"version",
		"mr view 7 --output json -R group/project",
		"api --method GET --hostname gitlab.com projects/group%2Fproject/merge_requests/7/discussions?page=1&per_page=2",
	}
	if len(lines) != len(want) {
		t.Fatalf("child argv=%q", lines)
	}
	for index := range want {
		if lines[index] != want[index] || strings.Contains(lines[index], secret) || strings.Contains(lines[index], "POST") || strings.Contains(lines[index], "PUT") || strings.Contains(lines[index], "PATCH") || strings.Contains(lines[index], "DELETE") {
			t.Fatalf("child argv[%d]=%q want=%q", index, lines[index], want[index])
		}
	}
}

func fakeDiscussionGlab(t *testing.T) (script, record string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "glab")
	record = filepath.Join(dir, "record")
	body := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${GLAB_AXI_FAKE_DISCUSSION_RECORD}"
case "$*" in
  "version")
    printf 'glab 1.112.0 (816e3a52)\n'
    ;;
  "mr view 7 --output json -R group/project")
    printf '%s' '{"id":7007,"iid":7,"project_id":99,"target_project_id":99,"web_url":"https://gitlab.com/group/project/-/merge_requests/7"}'
    ;;
  "api --method GET --hostname gitlab.com projects/group%2Fproject/merge_requests/7/discussions?page=1&per_page=2")
    printf '%s' '[{"id":"adapter-thread","individual_note":true,"notes":[{"id":501,"type":null,"body":"adapter note","author":{"id":17,"username":"reviewer","name":"Reviewer"},"created_at":"2024-01-02T03:04:05Z","updated_at":"2024-01-02T03:04:06Z","system":false,"noteable_id":7007,"noteable_type":"MergeRequest","project_id":99,"noteable_iid":null,"resolvable":false,"resolved":false,"resolved_by":null,"resolved_at":null}]}]'
    ;;
  *)
    printf 'unexpected or write-capable argv\n' >&2
    exit 92
    ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, record
}
