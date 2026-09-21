package product

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	runtimepkg "gl-axi/internal/runtime"
)

// Both real public entrypoints run against a process fixture, not an injected
// product delegate. The fixture rejects every argv except these exact GETs.
func TestCollaborationExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child fixture")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, out)
			}
			for _, test := range []struct {
				name, group, leaf, file, body string
				code, limit                   int
				contains                      string
				args                          []string
				noChild                       bool
			}{
				{name: "issue notes", group: "issue", leaf: "discussions", contains: `"issue_id":7007`},
				{name: "exact discussion limit", group: "issue", leaf: "discussions", limit: 1, contains: `"complete":true`},
				{name: "discussion limit probe", group: "issue", leaf: "discussions", limit: 1, file: "discussions-1.json", body: string(collaborationIssuePage(t, 0, 2)), contains: `"reason":"display_limit"`},
				{name: "discussion pagination", group: "issue", leaf: "discussions", limit: 101, file: "discussions-1.json", body: string(collaborationIssuePage(t, 0, 100)), contains: `"count":100`},
				{name: "empty comments", group: "issue", leaf: "discussions", file: "discussions-1.json", body: "[]", contains: `"discussions":[]`},
				{name: "denied comments", group: "issue", leaf: "discussions", file: "discussions.error", body: "HTTP 403 provider-secret", code: 4, contains: `"code":"forbidden"`},
				{name: "absent comments endpoint", group: "issue", leaf: "discussions", file: "discussions.error", body: "HTTP 404 provider-secret", code: 5, contains: `"complete":false`},
				{name: "malformed comments", group: "issue", leaf: "discussions", file: "discussions-1.json", body: `{"error":"provider-secret"}`, code: 8, contains: `"code":"upstream_error"`},
				{name: "wrong note target", group: "issue", leaf: "discussions", file: "discussions-1.json", body: strings.Replace(string(collaborationIssuePage(t, 0, 1)), `"noteable_id":7007`, `"noteable_id":7008`, 1), code: 9, contains: `"code":"safety_violation"`},
				{name: "wrong issue host", group: "issue", leaf: "discussions", file: "issue.json", body: strings.Replace(string(collaborationIssueBody()), "gitlab.com", "evil.invalid", 1), code: 9, contains: `"code":"safety_violation"`},
				{name: "changed issue", group: "issue", leaf: "discussions", file: "issue.final.json", body: strings.Replace(string(collaborationIssueBody()), `"id":7007`, `"id":7008`, 1), code: 6, contains: `"code":"conflict"`},
				{name: "approvals", group: "mr", leaf: "approvals", contains: `"state":"approved"`},
				{name: "empty approvals", group: "mr", leaf: "approvals", file: "approvals.json", body: `{"id":7007,"iid":7,"project_id":99,"approved":false,"approved_by":[]}`, contains: `"state":"not_approved"`},
				{name: "unknown approval state", group: "mr", leaf: "approvals", file: "approvals.json", body: `{"id":7007,"iid":7,"project_id":99,"approved_by":[]}`, contains: `"reason":"state_unknown"`},
				{name: "denied approval endpoint", group: "mr", leaf: "approvals", file: "approvals.error", body: "HTTP 403 provider-secret", contains: `"reason":"access_denied"`},
				{name: "unavailable approval endpoint", group: "mr", leaf: "approvals", file: "approvals.error", body: "HTTP 404 provider-secret", contains: `"reason":"not_found_or_unsupported"`},
				{name: "unframed denial is not unavailable", group: "mr", leaf: "approvals", file: "approvals.error", body: "403 provider-secret", code: 4, contains: `"code":"forbidden"`},
				{name: "authentication error", group: "mr", leaf: "approvals", file: "approvals.error", body: "HTTP 401 provider-secret", code: 3, contains: `"code":"authentication_error"`},
				{name: "malformed approval state", group: "mr", leaf: "approvals", file: "approvals.json", body: `{"id":7007,"iid":7,"project_id":99,"approved_by":[],"approved":"yes"}`, code: 8, contains: `"code":"upstream_error"`},
				{name: "wrong approval IID", group: "mr", leaf: "approvals", file: "approvals.json", body: strings.Replace(string(collaborationApprovalBody()), `"iid":7`, `"iid":8`, 1), code: 9, contains: `"code":"safety_violation"`},
				{name: "wrong head", group: "mr", leaf: "approvals", code: 6, contains: `"code":"conflict"`, args: []string{"--expected-head", discussionTestBaseSHA}},
				{name: "exact head", group: "mr", leaf: "approvals", contains: `"state":"approved"`, args: []string{"--expected-head", discussionTestHeadSHA}},
				{name: "changed head", group: "mr", leaf: "approvals", file: "mr.final.json", body: strings.Replace(string(collaborationMRBody(t)), discussionTestHeadSHA, discussionTestBaseSHA, 1), code: 6, contains: `"code":"conflict"`},
				{name: "invalid head", group: "mr", leaf: "approvals", code: 2, contains: `"code":"validation_error"`, args: []string{"--expected-head", "bad"}, noChild: true},
				{name: "invalid host", group: "issue", leaf: "discussions", code: 2, contains: `"code":"validation_error"`, noChild: true},
				{name: "invalid project", group: "mr", leaf: "approvals", code: 2, contains: `"code":"validation_error"`, noChild: true},
				{name: "invalid IID", group: "issue", leaf: "discussions", code: 2, contains: `"code":"validation_error"`, noChild: true},
				{name: "invalid limit", group: "issue", leaf: "discussions", code: 2, contains: `"code":"validation_error"`, limit: -1, noChild: true},
				{name: "mutation refused", group: "mr", leaf: "approvals", code: 2, contains: `"ok":false`, args: []string{"--approve"}, noChild: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					dir, env := collaborationProcessFixture(t)
					if test.file != "" {
						if err := os.WriteFile(filepath.Join(dir, test.file), []byte(test.body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					limit := test.limit
					if limit == 0 {
						limit = 30
					}
					args := collaborationCommand(test.group, test.leaf, limit)
					switch test.name {
					case "invalid host":
						args[6] = "bad/path"
					case "invalid project":
						args[4] = "group/../project"
					case "invalid IID":
						args[2] = "0"
					}
					args = append(args, test.args...)
					command := exec.Command(binary, args...)
					command.Dir = dir
					command.Env = env
					var stdout, stderr bytes.Buffer
					command.Stdout = &stdout
					command.Stderr = &stderr
					err := command.Run()
					code := 0
					if err != nil {
						if e, ok := err.(*exec.ExitError); ok {
							code = e.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					if code != test.code || stderr.Len() != 0 || !strings.Contains(stdout.String(), test.contains) || strings.Contains(stdout.String(), "provider-secret") || strings.Contains(stdout.String(), "synthetic-collaboration-token") {
						t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
					}
					record, _ := os.ReadFile(filepath.Join(dir, "record"))
					if test.noChild && len(record) != 0 {
						t.Fatalf("invalid input spawned child: %s", record)
					}
					for _, denied := range []string{"POST", "PUT", "PATCH", "DELETE", "synthetic-collaboration-token"} {
						if strings.Contains(string(record), denied) {
							t.Fatalf("child boundary: %s", record)
						}
					}
					if test.name == "discussion pagination" && !strings.Contains(string(record), "discussions?page=2&per_page=100") {
						t.Fatalf("missing stable second page: %s", record)
					}
					if test.name == "wrong head" && strings.Contains(string(record), "/approvals") {
						t.Fatal("wrong head performed approval read")
					}
				})
			}
		})
	}
}

func collaborationProcessFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		"project.json":       discussionProjectBody(99, "group/project"),
		"issue.json":         collaborationIssueBody(),
		"mr.json":            collaborationMRBody(t),
		"approvals.json":     collaborationApprovalBody(),
		"discussions-1.json": collaborationIssuePage(t, 0, 1),
		"discussions-2.json": []byte(`[]`),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
set -eu
cd "$COLLAB_FIXTURE"
printf '%s\n' "$*" >> record
case "$*" in
  'version') printf 'glab 1.112.0 (816e3a52)\n'; exit 0 ;;
  'api --method GET --hostname gitlab.com projects/group%2Fproject') file=project ;;
  'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/7') file=issue ;;
  'mr view 7 --output json -R group/project') file=mr ;;
  'api --method GET --hostname gitlab.com projects/group%2Fproject/merge_requests/7/approvals') file=approvals ;;
  'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/7/discussions?page=1&per_page=31'|'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/7/discussions?page=1&per_page=2'|'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/7/discussions?page=1&per_page=100') file=discussions; page=1 ;;
  'api --method GET --hostname gitlab.com projects/group%2Fproject/issues/7/discussions?page=2&per_page=100') file=discussions; page=2 ;;
  *) printf 'unexpected argv\n' >&2; exit 92 ;;
esac
if [ -f "$file.wait" ]; then touch waiting; exec sleep 30; fi
if [ -f "$file.error" ]; then cat "$file.error" >&2; exit 1; fi
if [ "$file" = discussions ]; then cat "discussions-$page.json"; exit 0; fi
if [ -f "$file.seen" ] && [ -f "$file.final.json" ]; then cat "$file.final.json"; else cat "$file.json"; fi
touch "$file.seen"
`
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "GITLAB_TOKEN=synthetic-collaboration-token", "COLLAB_FIXTURE=" + dir}
}

func TestCollaborationCancellationKillsOfficialChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child fixture")
	}
	for _, issue := range []bool{false, true} {
		dir, env := collaborationProcessFixture(t)
		group, leaf, file := "mr", "approvals", "approvals"
		if issue {
			group, leaf, file = "issue", "discussions", "discussions"
		}
		if err := os.WriteFile(filepath.Join(dir, file+".wait"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var stdout, stderr bytes.Buffer
		deps := Dependencies{Runtime: runtimepkg.Dependencies{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: dir, LookupEnv: func(string) (string, bool) { return "", false }}, GlabPath: filepath.Join(dir, "glab"), Env: env}
		done := make(chan int, 1)
		go func() { done <- Run(ctx, collaborationCommand(group, leaf, 30), deps) }()
		deadline := time.After(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, "waiting")); err == nil {
				break
			}
			select {
			case <-deadline:
				t.Fatal("child did not start")
			case <-time.After(10 * time.Millisecond):
			}
		}
		cancel()
		select {
		case code := <-done:
			if code != 130 {
				t.Fatalf("exit=%d %s", code, stdout.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("canceled child hung")
		}
		var out struct {
			Error *uxv1.Error `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out.Error == nil || out.Error.Code != uxv1.CodeCanceled {
			t.Fatalf("%s", stdout.String())
		}
		record, _ := os.ReadFile(filepath.Join(dir, "record"))
		if n := strings.Count(string(record), "\n"); n != 4 {
			t.Fatalf("extra child work after cancel: %s (%s)", record, strconv.Itoa(n))
		}
	}
}
