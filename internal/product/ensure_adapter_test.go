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

func TestMREnsureUpdateThenOfficialExactReadEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX script")
	}
	for _, test := range []struct {
		name     string
		shape    string
		wantCode int
	}{
		{name: "official diff refs shape", shape: "official", wantCode: 0},
		{name: "prior REST shape", shape: "rest", wantCode: 0},
		{name: "mismatched dual head", shape: "mismatch", wantCode: 6},
		{name: "malformed official head", shape: "malformed", wantCode: 6},
		{name: "duplicate official head", shape: "duplicate", wantCode: 6},
		{name: "missing head identity", shape: "missing", wantCode: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			script, record, applied := fakeEnsureGlab(t)
			var stdout, stderr bytes.Buffer
			deps := Dependencies{
				Runtime: runtimepkg.Dependencies{
					Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: t.TempDir(),
					LookupEnv: func(string) (string, bool) { return "", false },
				},
				GlabPath: script,
				Env: append(os.Environ(),
					"GLAB_AXI_FAKE_ENSURE_RECORD="+record,
					"GLAB_AXI_FAKE_ENSURE_APPLIED="+applied,
					"GLAB_AXI_FAKE_VIEW_SHAPE="+test.shape,
				),
				IsHumanTerminal: func() bool { return false },
			}
			code := Run(context.Background(), ensureArgs(t, "new", "new body"), deps)
			if code != test.wantCode {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, test.wantCode, stdout.String(), stderr.String())
			}
			if test.wantCode == 0 {
				if !strings.Contains(stdout.String(), `"action":"reconciled_update"`) || !strings.Contains(stdout.String(), `"head_sha":"`+mrViewTestHeadForProduct+`"`) {
					t.Fatalf("successful reconciliation output=%s", stdout.String())
				}
			} else {
				var envelope uxv1.Envelope
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error == nil || envelope.Error.Code != uxv1.CodeAmbiguousUpdate {
					t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
				}
			}
			assertEnsureAdapterSequence(t, record)
		})
	}
}

const mrViewTestHeadForProduct = "0123456789012345678901234567890123456789"

func fakeEnsureGlab(t *testing.T) (script, record, applied string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "glab")
	record = filepath.Join(dir, "record")
	applied = filepath.Join(dir, "applied")
	body := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${GLAB_AXI_FAKE_ENSURE_RECORD}"
if [ "${1:-}" = "version" ]; then
  printf 'glab 1.112.0 (816e3a52)\n'
  exit 0
fi
if [ "$*" = "api --method GET --hostname gitlab.com projects/group%2Fproject" ]; then
  printf '%s' '{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}'
  exit 0
fi
case "$*" in
  "api --method GET --hostname gitlab.com projects/group%2Fproject/merge_requests?"*)
    printf '%s' '[{"id":1011,"iid":11,"title":"old","description":"old body","state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/11","source_branch":"feature","target_branch":"main","source_project_id":101,"target_project_id":101,"sha":"0123456789012345678901234567890123456789","detailed_merge_status":"checking"}]'
    exit 0
    ;;
  "api --method PUT --hostname gitlab.com projects/group%2Fproject/merge_requests/11 --input "*)
    input="${8:-}"
    if [ ! -f "$input" ] || ! grep -Fq '"title":"new"' "$input" || ! grep -Fq '"description":"new body"' "$input"; then
      printf 'unexpected private update payload\n' >&2
      exit 93
    fi
    : > "${GLAB_AXI_FAKE_ENSURE_APPLIED}"
    printf 'synthetic ambiguous transport after applied update\n' >&2
    exit 42
    ;;
  "mr view 11 --output json -R group/project")
    if [ ! -f "${GLAB_AXI_FAKE_ENSURE_APPLIED}" ]; then
      printf 'exact read preceded update\n' >&2
      exit 94
    fi
    prefix='{"id":1011,"iid":11,"project_id":101,"title":"new","description":"new body","state":"opened","web_url":"https://gitlab.com/group/project/-/merge_requests/11","source_branch":"feature","target_branch":"main","source_project_id":101,"target_project_id":101'
    suffix=',"detailed_merge_status":"checking"}'
    case "${GLAB_AXI_FAKE_VIEW_SHAPE}" in
      official) printf '%s' "${prefix},\"diff_refs\":{\"head_sha\":\"0123456789012345678901234567890123456789\"}${suffix}" ;;
      rest) printf '%s' "${prefix},\"sha\":\"0123456789012345678901234567890123456789\"${suffix}" ;;
      mismatch) printf '%s' "${prefix},\"sha\":\"0123456789012345678901234567890123456789\",\"diff_refs\":{\"head_sha\":\"1123456789012345678901234567890123456789\"}${suffix}" ;;
      malformed) printf '%s' "${prefix},\"diff_refs\":{\"head_sha\":42}${suffix}" ;;
      duplicate) printf '%s' "${prefix},\"diff_refs\":{\"head_sha\":\"0123456789012345678901234567890123456789\",\"head_sha\":\"0123456789012345678901234567890123456789\"}${suffix}" ;;
      missing) printf '%s' "${prefix},\"diff_refs\":{}${suffix}" ;;
      *) exit 95 ;;
    esac
    exit 0
    ;;
esac
printf 'unexpected argv: %s\n' "$*" >&2
exit 92
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, record, applied
}

func assertEnsureAdapterSequence(t *testing.T, record string) {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	writes, reads, lists := 0, 0, 0
	writeIndex, readIndex := -1, -1
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "api --method PUT "):
			writes++
			writeIndex = index
		case line == "mr view 11 --output json -R group/project":
			reads++
			readIndex = index
		case strings.Contains(line, "/merge_requests?"):
			lists++
		}
	}
	if writes != 1 || reads != 1 || lists != 1 || readIndex != writeIndex+1 {
		t.Fatalf("writes=%d reads=%d lists=%d write_index=%d read_index=%d argv=%q", writes, reads, lists, writeIndex, readIndex, lines)
	}
}
