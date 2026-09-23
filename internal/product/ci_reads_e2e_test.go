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

// Both public executables cross the real child boundary. The local fixture
// permits only these exact pinned argv, never a provider write or live host.
func TestCIReadExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process fixture uses POSIX shell")
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
				t.Fatalf("build: %v %s", err, output)
			}
			record := filepath.Join(dir, "record")
			fake := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$CI_READ_RECORD"
if [ "$*" != version ]; then
  [ "$GITLAB_HOST" = "$CI_READ_HOST" ] || exit 93
  [ "$GITLAB_TOKEN" = "$CI_READ_CREDENTIAL" ] || exit 94
fi
case "$*" in
 version) printf 'glab 1.112.0 (816e3a52)\n' ;;
 'ci list --output json --ref main --status failed --source push --username alice --sha 1123456789012345678901234567890123456789 --page 1 --per-page 31 -R group/project')
   cat "$CI_READ_FIXTURES/pipelines" ;;
 "api --method GET --hostname $CI_READ_HOST projects/group%2Fproject/pipelines/55")
   cat "$CI_READ_FIXTURES/pipeline" ;;
 "api --method GET --hostname $CI_READ_HOST projects/group%2Fproject/pipelines/55/jobs?page=1&per_page=31&scope%5B%5D=failed")
   cat "$CI_READ_FIXTURES/jobs" ;;
 "api --method GET --hostname $CI_READ_HOST projects/group%2Fproject/jobs/9")
   cat "$CI_READ_FIXTURES/job" ;;
 "api --method GET --hostname $CI_READ_HOST projects/group%2Fproject/jobs/9/trace")
   printf 'last failure\n' ;;
 *) printf 'unexpected or write-capable argv\n' >&2; exit 92 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			writeFixture := func(name string, v any) {
				t.Helper()
				body, _ := json.Marshal(v)
				if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeFixture("pipeline", ciPipeline(55, "success"))
			writeFixture("pipelines", []upstreamPipeline{ciPipeline(55, "failed")})
			writeFixture("job", ciJob(9, "failed"))
			writeFixture("jobs", []upstreamJob{ciJob(9, "failed")})
			secret := strings.Join([]string{"synthetic", "ci", "executable", "token"}, "-")
			selectedHost := "gitlab.com"
			run := func(args []string) (int, string, string) {
				t.Helper()
				argv := append(append([]string{}, args...), "-R", "group/project", "--hostname", selectedHost, "--format", "json")
				cmd := exec.Command(binary, argv...)
				cmd.Dir = dir
				cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "CI_READ_RECORD=" + record, "CI_READ_FIXTURES=" + dir, "GITLAB_TOKEN=" + secret, "CI_READ_CREDENTIAL=" + secret, "CI_READ_HOST=" + selectedHost}
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				code := 0
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						code = exit.ExitCode()
					} else {
						t.Fatal(err)
					}
				}
				if strings.Contains(stdout.String()+stderr.String(), secret) {
					t.Fatal("credential leaked")
				}
				return code, stdout.String(), stderr.String()
			}
			for _, args := range [][]string{
				{"pipeline", "list", "--ref", "main", "--status", "failed", "--source", "push", "--user", "alice", "--sha", ciTestSHA, "--fields", "iid"},
				{"pipeline", "view", "55", "--jobs", "--job-status", "failed", "--ref", "main", "--sha", ciTestSHA},
				{"pipeline", "view", "55", "--job-id", "9", "--trace"},
				{"pipeline", "view", "55", "--trace-failed"},
				{"pipeline", "watch", "55", "--timeout", "30", "--interval", "1", "--ref", "main", "--sha", ciTestSHA},
				{"job", "list", "--pipeline-id", "55", "--status", "failed"},
				{"job", "list", "--pipeline-id", "55", "--job-id", "9", "--status", "failed"},
				{"job", "view", "9", "--pipeline-id", "55"},
				{"job", "trace", "9", "--pipeline-id", "55"},
			} {
				code, stdout, stderr := run(args)
				if code != 0 || stderr != "" || !ciEnvelope(t, stdout).OK {
					t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
				}
			}
			for _, binding := range []struct {
				host, base, returnedBase string
			}{
				{"GITLAB.COM", "", "https://gitlab.com"},
				{"gitlab.com", "https://GITLAB.COM/gitlab", "https://gitlab.com/gitlab"},
			} {
				selectedHost = binding.host
				pipeline := ciPipeline(55, "success")
				pipeline.WebURL = binding.returnedBase + "/group/project/-/pipelines/55"
				job := ciJob(9, "failed")
				job.WebURL = binding.returnedBase + "/group/project/-/jobs/9"
				job.Pipeline = &pipeline
				writeFixture("pipeline", pipeline)
				writeFixture("job", job)
				writeFixture("jobs", []upstreamJob{job})
				for _, args := range [][]string{
					{"pipeline", "view", "55", "--jobs", "--job-status", "failed"},
					{"pipeline", "watch", "55", "--timeout", "30", "--interval", "1"},
					{"job", "list", "--pipeline-id", "55", "--status", "failed"},
					{"job", "trace", "9", "--pipeline-id", "55"},
				} {
					if binding.base != "" {
						args = append(args, "--web-base", binding.base)
					}
					if code, stdout, stderr := run(args); code != 0 || stderr != "" || !ciEnvelope(t, stdout).OK {
						t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
					}
				}
				pipeline.WebURL = "https://gitlab.com/evil/gitlab/group/project/-/pipelines/55"
				job.WebURL = "https://gitlab.com/evil/gitlab/group/project/-/jobs/9"
				writeFixture("pipeline", pipeline)
				writeFixture("job", job)
				for _, args := range [][]string{{"pipeline", "view", "55"}, {"job", "trace", "9", "--pipeline-id", "55"}} {
					if binding.base != "" {
						args = append(args, "--web-base", binding.base)
					}
					if code, stdout, _ := run(args); code != 9 {
						t.Fatalf("hostile prefix %v: code=%d stdout=%s", args, code, stdout)
					}
				}
			}
			selectedHost = "gitlab.com"
			writeFixture("pipeline", ciPipeline(55, "success"))
			writeFixture("job", ciJob(9, "failed"))
			writeFixture("jobs", []upstreamJob{ciJob(9, "failed")})
			before, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(before), secret) || strings.Contains(string(before), "--method PUT") {
				t.Fatal("argv authority escaped")
			}
			for _, args := range [][]string{
				{"pipeline", "list", "--status", "failed", "--status", "success"},
				{"pipeline", "list", "--source", "workflow"}, {"pipeline", "list", "--user", "--help-me"},
				{"pipeline", "list", "--sha", "deadbeef"}, {"pipeline", "list", "--fields", "iid,iid"},
				{"pipeline", "list", "--fields", "sha"}, {"pipeline", "list", "--fields", "web_url"},
				{"pipeline", "list", "--fields", "updated_at"}, {"pipeline", "list", "--fields", "iid,sha"},
				{"pipeline", "view", "55", "--web-base", "https://evil.invalid/gitlab"},
				{"job", "trace", "9", "--web-base", "https://gitlab.com/other/../gitlab"},
				{"pipeline", "view", "55", "--trace"}, {"pipeline", "watch", "55", "--timeout", "301"},
				{"pipeline", "watch", "55", "--timeout", "1", "--interval", "2"},
				{"job", "list", "--pipeline-id", "55", "--status", "failure"},
				{"job", "view", "09"}, {"job", "trace", "9", "--pipeline-id", "wrong"},
				{"pipeline", "retry", "55"},
			} {
				code, stdout, _ := run(args)
				if code != 2 {
					t.Fatalf("%v: %d %s", args, code, stdout)
				}
			}
			after, _ := os.ReadFile(record)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid argv reached child")
			}
			bad := ciPipeline(55, "success")
			bad.Ref = "wrong"
			writeFixture("pipeline", bad)
			if code, stdout, _ := run([]string{"pipeline", "watch", "55", "--ref", "main"}); code != 9 || ciEnvelope(t, stdout).OK {
				t.Fatalf("wrong ref: %d %s", code, stdout)
			}
			bad = ciPipeline(55, "success")
			bad.WebURL = "https://wrong.example/group/project/-/pipelines/55"
			writeFixture("pipeline", bad)
			if code, stdout, _ := run([]string{"pipeline", "view", "55"}); code != 9 {
				t.Fatalf("wrong authority: %d %s", code, stdout)
			}
			job := ciJob(9, "failed")
			job.Pipeline.ID = 56
			writeFixture("job", job)
			if code, stdout, _ := run([]string{"job", "trace", "9", "--pipeline-id", "55"}); code != 9 {
				t.Fatalf("wrong pipeline: %d %s", code, stdout)
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.Contains(entry.Name(), "log") {
					t.Fatal("trace created a full-log escape")
				}
			}
		})
	}
}
