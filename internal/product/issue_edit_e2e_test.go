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

// Omission of the auth selector preserves the delegated validation lane for
// both executable names. No implicit native credential switch or PUT is allowed.
func TestIssueEditExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process fixture uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	validateCLI := issueEditCLIValidator(t)
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binDir := t.TempDir()
			binary := filepath.Join(binDir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			if err := os.WriteFile(filepath.Join(binDir, "glab"), []byte(issueEditProcessFixture), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"default refusal", "noop", "preview", "wrong target", "stale", "drift", "renamed", "reused", "ambiguous labels"} {
				t.Run(mode, func(t *testing.T) {
					dir := t.TempDir()
					title := filepath.Join(dir, "title")
					value, label := "new title", "triage"
					if mode == "noop" {
						value, label = "old title", "keep"
					}
					if err := os.WriteFile(title, []byte(value+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					args := append(issueEditBaseArgs(), "--title-file", title, "--add-label", label, "--format", "json")
					if mode == "preview" {
						args = append(args, "--dry-run")
					}
					if mode == "wrong target" {
						args = replaceArg(args, issueEditTestURL, "https://gitlab.com/other/project/-/issues/42")
					}
					secret := strings.Join([]string{"synthetic", "executable", "token"}, "-")
					command := exec.Command(binary, args...)
					command.Dir = dir
					command.Env = []string{"PATH=" + binDir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "GL_AXI_E2E_DIR=" + dir, "GL_AXI_E2E_MODE=" + mode, "GITLAB_TOKEN=" + secret}
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					runErr := command.Run()
					validateCLI(t, stdout.Bytes())
					var envelope struct {
						Schema string          `json:"schema"`
						OK     bool            `json:"ok"`
						Data   issueEditOutput `json:"data"`
						Error  struct {
							Code    uxv1.Code       `json:"code"`
							Receipt issueEditOutput `json:"receipt"`
						} `json:"error"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatalf("decode: %v stdout=%s stderr=%s", err, &stdout, &stderr)
					}
					recorded, _ := os.ReadFile(filepath.Join(dir, "record"))
					if envelope.Schema != uxv1.Schema || strings.Contains(string(recorded), "--method PUT") || strings.Contains(string(recorded)+stdout.String()+stderr.String(), secret) {
						t.Fatalf("boundary: record=%s stdout=%s stderr=%s", recorded, &stdout, &stderr)
					}
					if mode == "noop" || mode == "preview" {
						want := "preview"
						if mode == "noop" {
							want = "unchanged"
						}
						if runErr != nil || !envelope.OK || envelope.Data.Edit.Action != want {
							t.Fatalf("err=%v output=%s", runErr, &stdout)
						}
					} else if runErr == nil || envelope.OK {
						t.Fatalf("unsafe success: %s", &stdout)
					}
					if mode == "default refusal" && (envelope.Error.Code != uxv1.CodeSafety || envelope.Error.Receipt.Edit.Action != "refused" || envelope.Error.Receipt.Edit.RefusalReason != "native_auth_required") {
						t.Fatalf("default did not require opt-in: %s", &stdout)
					}
					t.Logf("CLI receipt: %s", stdout.Bytes())
					t.Logf("Delegated argv: %s", recorded)
				})
			}
		})
	}
}

const issueEditProcessFixture = `#!/bin/sh
set -eu
if [ "${1-}" = version ]; then
  printf '%s\n' 'glab 1.112.0 (816e3a52)'
  exit 0
fi
n=0
if [ -f "$GL_AXI_E2E_DIR/count" ]; then n=$(cat "$GL_AXI_E2E_DIR/count"); fi
n=$((n + 1))
printf '%s' "$n" > "$GL_AXI_E2E_DIR/count"
printf '%s\n' "$*" >> "$GL_AXI_E2E_DIR/record"
mode="$GL_AXI_E2E_MODE"
title='old title'
updated='2026-08-15T12:00:00Z'
if [ "$mode" = stale ]; then updated='2026-08-15T12:00:01Z'; fi
if [ "$mode" = drift ] && [ "$n" = 4 ]; then title='concurrent'; fi
issue="{\"id\":1001,\"iid\":42,\"project_id\":101,\"title\":\"$title\",\"description\":\"old body\",\"state\":\"opened\",\"web_url\":\"https://gitlab.com/group/project/-/issues/42\",\"labels\":[\"keep\"],\"updated_at\":\"$updated\"}"
case "$n" in
  1)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject' ]
    printf '%s' '{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}'
    ;;
  2|4)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/42' ]
    printf '%s' "$issue"
    ;;
  3|5)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/labels?include_ancestor_groups=true&page=1&per_page=100' ]
    catalog='[{"id":10,"name":"triage"},{"id":12,"name":"keep"}]'
    if [ "$n" = 5 ]; then
      case "$mode" in
        renamed) catalog='[{"id":10,"name":"renamed"},{"id":12,"name":"keep"}]';;
        reused) catalog='[{"id":99,"name":"triage"},{"id":12,"name":"keep"}]';;
        'ambiguous labels') catalog='[{"id":10,"name":"triage"},{"id":99,"name":"triage"}]';;
      esac
    fi
    printf '%s' "$catalog"
    ;;
  *) exit 1;;
esac
`
