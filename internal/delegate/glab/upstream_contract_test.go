package glab

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinnedUpstreamPublicContract executes only version/help against the
// checksum-verified official package. CI supplies the binary; no profile,
// credential, repository, or GitLab API is involved.
func TestPinnedUpstreamPublicContract(t *testing.T) {
	binary := os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = []string{
			"HOME=" + home,
			"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
			"GLAB_CHECK_UPDATE=false",
			"NO_COLOR=1",
			"TERM=dumb",
			"PATH=/usr/bin:/bin",
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("glab %v: %v\nstdout=%s\nstderr=%s", args, err, stdout.String(), stderr.String())
		}
		if stdout.Len()+stderr.Len() > 256<<10 {
			t.Fatalf("glab %v help exceeded bound", args)
		}
		return stdout.String() + stderr.String()
	}
	if got, want := strings.TrimSpace(run("version")), "glab "+SupportedVersion+" ("+SupportedBuild+")"; got != want {
		t.Fatalf("version=%q want=%q", got, want)
	}
	checks := []struct {
		args     []string
		required []string
	}{
		{[]string{"auth", "login", "--help"}, []string{"--hostname", "--insecure-storage", "--device", "--web"}},
		{[]string{"auth", "status", "--help"}, []string{"--hostname", "--show-token"}},
		{[]string{"issue", "list", "--help"}, []string{"--output", "--page", "--per-page", "--repo"}},
		{[]string{"issue", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"mr", "list", "--help"}, []string{"--output", "--source-branch", "--target-branch"}},
		{[]string{"mr", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"mr", "diff", "--help"}, []string{"--color", "--repo"}},
		{[]string{"ci", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"ci", "get", "--help"}, []string{"--merge-request", "--pipeline-id", "--output"}},
		{[]string{"release", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"release", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"repo", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"repo", "view", "--help"}, []string{"--output"}},
		{[]string{"label", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"api", "--help"}, []string{"--method", "--hostname", "--input"}},
	}
	for _, check := range checks {
		output := run(check.args...)
		for _, required := range check.required {
			if !strings.Contains(output, required) {
				t.Fatalf("glab %v no longer advertises %q", check.args, required)
			}
		}
	}
}
