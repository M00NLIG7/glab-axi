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

// Both public executables cross the real process adapter with private input.
// The fake glab exercises success, drift, and transport ambiguity without any
// live account or provider. The separate official-package TLS test pins HTTP.
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
			binDir := t.TempDir()
			binary := filepath.Join(binDir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			fakeGlab := filepath.Join(binDir, "glab")
			if err := os.WriteFile(fakeGlab, []byte(issueEditProcessFixture), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"success", "noop", "preview", "wrong target", "stale", "drift", "renamed", "reused", "ambiguous labels", "post label drift", "lost", "malformed", "wrong response", "unapplied", "read failure", "concurrent labels"} {
				t.Run(mode, func(t *testing.T) {
					dir := t.TempDir()
					title := filepath.Join(dir, "title")
					value := "new title"
					label := "triage"
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
					puts := strings.Count(string(recorded), "--method PUT")
					wantPuts, wantAction := 1, ""
					switch mode {
					case "success":
						wantAction = "updated"
					case "noop":
						wantPuts, wantAction = 0, "unchanged"
					case "preview":
						wantPuts, wantAction = 0, "preview"
					case "lost", "malformed":
						wantAction = "reconciled_update"
					case "wrong target", "stale", "drift", "renamed", "reused", "ambiguous labels":
						wantPuts = 0
					}
					if puts != wantPuts || envelope.Schema != uxv1.Schema || strings.Contains(string(recorded)+stdout.String()+stderr.String(), secret) || strings.Contains(string(recorded), "new title") {
						t.Fatalf("boundary: puts=%d record=%s stdout=%s stderr=%s", puts, recorded, &stdout, &stderr)
					}
					if wantAction != "" {
						if runErr != nil || !envelope.OK || envelope.Data.Edit.Action != wantAction || envelope.Data.Edit.Warning != issueEditRaceWarning {
							t.Fatalf("action=%s err=%v stdout=%s stderr=%s", wantAction, runErr, &stdout, &stderr)
						}
					} else {
						if runErr == nil || envelope.OK {
							t.Fatalf("unsafe success: %s", &stdout)
						}
						if wantPuts == 1 {
							assertIssueEditAmbiguous(t, 1, stdout.Bytes())
						}
					}
					if puts == 1 {
						payload, err := os.ReadFile(filepath.Join(dir, "payload"))
						if err != nil || strings.TrimSpace(string(payload)) != `{"title":"new title","add_labels":"triage"}` {
							t.Fatalf("payload=%s err=%v", payload, err)
						}
					}
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
id=1001
labels='["keep"]'
if [ "$mode" = stale ]; then updated='2026-08-15T12:00:01Z'; fi
if [ "$mode" = drift ] && [ "$n" = 4 ]; then title='concurrent'; fi
if [ "$n" -ge 6 ]; then
  title='new title'
  updated='2026-08-15T12:00:01Z'
  labels='["keep","triage"]'
  if [ "$mode" = unapplied ]; then title='old title'; labels='["keep"]'; fi
  if [ "$mode" = 'concurrent labels' ]; then labels='["keep","triage","unseen"]'; fi
fi
if [ "$mode" = 'wrong response' ] && [ "$n" = 6 ]; then id=2002; fi
issue="{\"id\":$id,\"iid\":42,\"project_id\":101,\"title\":\"$title\",\"description\":\"old body\",\"state\":\"opened\",\"web_url\":\"https://gitlab.com/group/project/-/issues/42\",\"labels\":$labels,\"updated_at\":\"$updated\"}"
case "$n" in
  1)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject' ]
    printf '%s' '{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}'
    ;;
  2|4|7)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/42' ]
    if [ "$mode" = 'read failure' ] && [ "$n" = 7 ]; then exit 1; fi
    printf '%s' "$issue"
    ;;
  3|5|8)
    [ "$*" = 'api --method GET --hostname gitlab.com projects/group%2Fproject/labels?include_ancestor_groups=true&page=1&per_page=100' ]
    catalog='[{"id":10,"name":"triage"},{"id":12,"name":"keep"}]'
    if [ "$n" = 5 ]; then
      case "$mode" in
        renamed) catalog='[{"id":10,"name":"renamed"},{"id":12,"name":"keep"}]';;
        reused) catalog='[{"id":99,"name":"triage"},{"id":12,"name":"keep"}]';;
        'ambiguous labels') catalog='[{"id":10,"name":"triage"},{"id":99,"name":"triage"}]';;
      esac
    fi
    if [ "$n" = 8 ] && [ "$mode" = 'post label drift' ]; then catalog='[{"id":99,"name":"triage"},{"id":12,"name":"keep"}]'; fi
    printf '%s' "$catalog"
    ;;
  6)
    [ "$1 $2 $3 $4 $5 $6 $7 $9" = 'api --method PUT --hostname gitlab.com projects/101/issues/42 --input --header' ]
    cat "$8" > "$GL_AXI_E2E_DIR/payload"
    if [ "$mode" = lost ]; then exit 1; fi
    if [ "$mode" = malformed ]; then printf '{'; else printf '%s' "$issue"; fi
    ;;
  *) exit 1;;
esac
`
