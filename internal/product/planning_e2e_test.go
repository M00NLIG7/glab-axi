package product

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/delegate/glab"
	runtimepkg "gl-axi/internal/runtime"
)

func fakePlanningProcess(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "glab")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GL_AXI_PLAN_RECORD"
if [ "${1-}" = version ]; then
  printf 'glab 1.112.0 (816e3a52)\n'
  exit 0
fi
[ "$1" = api ] && [ "$2" = graphql ] && [ "$3" = --method ] && [ "$4" = POST ] && [ "$5" = --hostname ] && [ "$6" = gitlab.com ]
[ "$7" = --raw-field ]
case "$8" in 'query=query PlanningRead('* ) ;; *) exit 91 ;; esac
case "$8" in *mutation*|*__schema*) exit 92 ;; esac
case "${GL_AXI_PLAN_MODE-}" in
 sleep) : > "$GL_AXI_PLAN_READY"; exec sleep 30 ;;
 forbidden) printf 'HTTP 403: fixture denied\n' >&2; exit 1 ;;
 absent) printf 'HTTP 404: fixture absent\n' >&2; exit 1 ;;
esac
case "$*" in *after=next-page*) cat "$GL_AXI_PLAN_FIXTURE.next" ;; *) cat "$GL_AXI_PLAN_FIXTURE" ;; esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPlanningExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if body, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, body)
			}
			fakePlanningProcess(t, dir)
			fixture, record := filepath.Join(dir, "response"), filepath.Join(dir, "record")
			secret := strings.Join([]string{"synthetic", "planning", "credential"}, "-")
			run := func(args []string, doc planJSON, mode string) (planEnvelope, string) {
				t.Helper()
				body, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture, body, 0600); err != nil {
					t.Fatal(err)
				}
				_ = os.Remove(record)
				cmd := exec.Command(binary, args...)
				cmd.Dir = dir
				// An isolated environment makes access to real profiles and credentials
				// impossible in the protocol fixture. No installed glab is invoked.
				cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GITLAB_TOKEN=" + secret, "GL_AXI_PLAN_RECORD=" + record, "GL_AXI_PLAN_FIXTURE=" + fixture, "GL_AXI_PLAN_MODE=" + mode}
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				runErr := cmd.Run()
				var out planEnvelope
				if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
					t.Fatalf("decode: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
				}
				if (runErr == nil) != out.OK || stderr.Len() != 0 {
					t.Fatalf("run=%v stderr=%s out=%s", runErr, stderr.String(), stdout.String())
				}
				recordBytes, _ := os.ReadFile(record)
				if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) || strings.Contains(string(recordBytes), secret) {
					t.Fatal("credential leaked")
				}
				return out, string(recordBytes)
			}
			for _, group := range []bool{false, true} {
				for _, op := range []glab.Operation{glab.OpBoardList, glab.OpBoardView, glab.OpBoardIssues, glab.OpWorkItemFields, glab.OpWorkItemHierarchy} {
					nodes := []any{}
					switch op {
					case glab.OpBoardList:
						nodes = append(nodes, planBoard(group, 7))
					case glab.OpBoardView:
						nodes = append(nodes, planColumn(8))
					case glab.OpBoardIssues:
						nodes = append(nodes, planItem(9, false, false))
					case glab.OpWorkItemHierarchy:
						nodes = append(nodes, planItem(9, true, group))
					}
					doc := planDoc(op, group, nodes...)
					out, recorded := run(planArgs(op, group, 30), doc, "")
					if !out.OK || !out.Meta.Complete || out.Meta.UpstreamVersion != glab.SupportedVersion {
						t.Fatalf("%s group=%t out=%+v err=%+v", op, group, out, out.Error)
					}
					if strings.Count(recorded, "query=query PlanningRead(") != 1 || !strings.HasPrefix(recorded, "version\n") {
						t.Fatalf("argv: %s", recorded)
					}
					if op == glab.OpBoardIssues {
						if !strings.Contains(recorded, "board=gid://gitlab/Board/7") || !strings.Contains(recorded, "list=gid://gitlab/List/8") {
							t.Fatal("lost board/list binding")
						}
						var receipt PlanningOrderingReceipt
						if err := json.Unmarshal(out.Data["ordering"], &receipt); err != nil {
							t.Fatal(err)
						}
						if !receipt.Acknowledged || receipt.Outcome != "may_have_occurred" || receipt.BoardID != 7 || receipt.ListID != 8 {
							t.Fatalf("missing ordering receipt %+v", receipt)
						}
					} else if op == glab.OpBoardList || op == glab.OpBoardView {
						if strings.Contains(recorded, "issues(") {
							t.Fatal("pure metadata fetched issues")
						}
					}
				}
			}
			doc := planDoc(glab.OpBoardList, false, planBoard(false, 1))
			planMore(doc, glab.OpBoardList, "next-page")
			next, _ := json.Marshal(planDoc(glab.OpBoardList, false, planBoard(false, 2)))
			if err := os.WriteFile(fixture+".next", next, 0600); err != nil {
				t.Fatal(err)
			}
			out, recorded := run(planArgs(glab.OpBoardList, false, 30), doc, "")
			if !out.OK || out.Meta.Count != 2 || strings.Count(recorded, "query=query PlanningRead(") != 2 || !strings.Contains(recorded, "after=next-page") {
				t.Fatalf("pagination %+v %s", out, recorded)
			}
			out, _ = run(planArgs(glab.OpBoardList, false, 1), doc, "")
			if !out.OK || out.Meta.Complete || out.Meta.Reason != "display_limit" {
				t.Fatalf("limit %+v", out)
			}
			for _, mode := range []string{"forbidden", "absent"} {
				out, _ := run(planArgs(glab.OpBoardList, false, 30), planDoc(glab.OpBoardList, false), mode)
				if out.OK || out.Error == nil || out.Meta.Complete {
					t.Fatalf("%s %+v", mode, out)
				}
			}
			unavailable := planDoc(glab.OpWorkItemHierarchy, false)
			planRoot(unavailable)["widgets"] = []any{}
			out, _ = run(planArgs(glab.OpWorkItemHierarchy, false, 30), unavailable, "")
			if out.OK || out.Error.Code != "unsupported" {
				t.Fatalf("unavailable %+v", out)
			}
			wrong := planDoc(glab.OpBoardList, false)
			wrong["data"].(planJSON)["scope"].(planJSON)["fullPath"] = "another/project"
			out, _ = run(planArgs(glab.OpBoardList, false, 30), wrong, "")
			if out.OK || out.Error.Code != "safety_violation" {
				t.Fatalf("identity %+v", out)
			}
			for _, args := range [][]string{{"board", "issues", "7", "--list-id", "8", "-R", planPath, "--hostname", "gitlab.com", "--format=json"}, {"work-item", "hierarchy", "0", "--group", "team/nested", "--format=json"}, {"board", "view", "7", "-R", planPath, "--group", "team/nested", "--format=json"}} {
				out, recorded := run(args, nil, "")
				if out.OK || recorded != "" {
					t.Fatalf("malformed input did child work: %+v %s", out, recorded)
				}
			}
		})
	}
}
func TestPlanningRealChildCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	path := fakePlanningProcess(t, dir)
	var stdout, stderr bytes.Buffer
	ready := filepath.Join(dir, "ready")
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Cwd: dir, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, LookupEnv: func(string) (string, bool) { return "", false }}, GlabPath: path, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GL_AXI_PLAN_RECORD=" + filepath.Join(dir, "record"), "GL_AXI_PLAN_READY=" + ready, "GL_AXI_PLAN_MODE=sleep"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- Run(ctx, planArgs(glab.OpBoardList, false, 30), deps) }()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case code := <-done:
			t.Fatalf("exited before API readiness: %d stderr=%s out=%s", code, stderr.String(), stdout.String())
		case <-deadline:
			cancel()
			<-done
			t.Fatal("child did not reach API readiness")
		case <-ticker.C:
			if _, err := os.Stat(ready); err != nil {
				continue
			}
			cancel()
			select {
			case code := <-done:
				if code != 130 {
					t.Fatalf("cancel exit=%d stderr=%s out=%s", code, stderr.String(), stdout.String())
				}
				return
			case <-time.After(5 * time.Second):
				t.Fatal("child did not cancel")
			}
		}
	}
}
