package glab

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

// Observe the environment inside a real child process. Neither inherited
// legacy switches nor wrapper-injected deprecated switches may add warnings
// to the exact status framing; the supported switch must still prevent prompts.
func TestHeadlessChildPromptEnvironmentPreservesHTTPFraming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child fixture")
	}
	home := t.TempDir()
	path := filepath.Join(home, "glab")
	script := `#!/bin/sh
set -eu
if [ "${GLAB_NO_PROMPT-}" != 1 ] || [ "${NO_PROMPT+x}" = x ] || [ "${PROMPT_DISABLED+x}" = x ]; then
  printf 'unexpected prompt environment\n' >&2
  exit 91
fi
case "$*" in
  version) printf 'glab 1.112.0 (816e3a52)\n' ;;
  'api --method GET --hostname gitlab.example projects/group%2Fproject/merge_requests/7/approvals')
    printf '{"message":"denied"}'
    printf 'glab: denied (HTTP 403)\n' >&2
    exit 1
    ;;
  *) exit 92 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		if legacy {
			env = append(env, "NO_PROMPT=1", "PROMPT_DISABLED=1", "GLAB_NO_PROMPT=false")
		}
		client := NewClient(ClientConfig{Path: path, Env: env})
		_, err := client.Do(context.Background(), Request{Operation: OpMRApprovals, Host: "gitlab.example", Repo: "group/project", IID: 7})
		failure := uxv1.AsError(err)
		if failure == nil || failure.StatusCode != 403 || failure.Code != uxv1.CodeForbidden {
			t.Fatalf("inherited legacy=%t: definite child rejection lost: %#v", legacy, failure)
		}
	}
}
