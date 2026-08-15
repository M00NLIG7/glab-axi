package product

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"glab-axi/internal/contract/uxv1"
)

func TestEveryDeclaredHelpPathSucceedsWithoutCommandExecution(t *testing.T) {
	for _, definition := range Definitions() {
		args := append([]string{}, definition.Path...)
		args = append(args, "--help")
		result, err := Parse(args)
		if err != nil || result.Help == "" || result.Command != nil {
			t.Fatalf("help %v: result=%#v err=%v", definition.Path, result, err)
		}
	}
	parents := map[string]bool{}
	for _, definition := range Definitions() {
		if len(definition.Path) > 1 {
			parents[definition.Path[0]] = true
		}
	}
	for parent := range parents {
		result, err := Parse([]string{parent, "--help"})
		if err != nil || result.Help == "" {
			t.Fatalf("parent help %q: %v", parent, err)
		}
	}
}

func TestParserAcceptsCommandFirstGlobalEqualsForms(t *testing.T) {
	result, err := Parse([]string{"issue", "view", "12", "-R=group/project", "--hostname=gitlab.example.com", "--limit=7", "--format=json"})
	if err != nil {
		t.Fatal(err)
	}
	parsed := result.Command
	if parsed.Values["--repo"] != "group/project" || parsed.Values["--hostname"] != "gitlab.example.com" || parsed.Limit != 7 || string(parsed.Format) != "json" || parsed.Positionals[0] != "12" {
		t.Fatalf("unexpected parse: %#v", parsed)
	}
	if _, err := Parse([]string{"issue", "view", "12", "-R", "a/b", "--repo=c/d"}); err == nil {
		t.Fatal("alias duplicate was accepted")
	}
}

func TestParserClassifiesPermanentSecurityBoundaries(t *testing.T) {
	for _, args := range [][]string{
		{"api", "projects"}, {"auth", "token"}, {"auth", "show-token"},
		{"auth", "status", "--show-token"},
		{"auth", "login", "--token", "sentinel"},
		{"auth", "login", "--job-token", "sentinel"},
		{"auth", "login", "--stdin"},
		{"auth", "login", "--insecure-storage"},
		{"auth", "login", "--web"},
		{"auth", "login", "--device"},
		{"mr", "approve", "1"}, {"mr", "comment", "1"}, {"issue", "close", "1"},
		{"pipeline", "retry", "2"}, {"repo", "create"}, {"release", "upload"},
		{"label", "delete"}, {"secret", "list"}, {"variable", "list"},
	} {
		if _, err := Parse(args); err == nil || string(uxCode(err)) != "security_boundary" {
			t.Fatalf("%v error=%v", args, err)
		}
	}
}

func TestAuthLoginAcceptsOnlyOptionalHostname(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "login"},
		{"auth", "login", "--hostname", "gitlab.com"},
		{"auth", "login", "--hostname=gitlab.example.invalid"},
	} {
		if result, err := Parse(args); err != nil || result.Command == nil {
			t.Fatalf("valid login %v: result=%#v error=%v", args, result, err)
		}
	}
	for _, args := range [][]string{
		{"auth", "login", "--format", "json"},
		{"auth", "login", "--limit", "1"},
		{"auth", "login", "-R", "group/project"},
		{"auth", "login", "extra"},
	} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("login accepted wrapper bypass %v", args)
		}
	}
}

func TestTargetUsesExplicitThenEnvironmentThenLocalContext(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", "git@gitlab.local:group/project.git")
	parsedResult, err := Parse([]string{"issue", "list"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTarget(context.Background(), *parsedResult.Command, dir, func(name string) (string, bool) {
		if name == "GITLAB_HOST" {
			return "env.gitlab.local", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "env.gitlab.local" || target.Repo != "group/project" {
		t.Fatalf("target=%#v", target)
	}

	explicitResult, err := Parse([]string{"issue", "list", "--hostname", "explicit.gitlab.local", "-R", "other/project"})
	if err != nil {
		t.Fatal(err)
	}
	target, err = resolveTarget(context.Background(), *explicitResult.Command, dir, os.LookupEnv)
	if err != nil || target.Host != "explicit.gitlab.local" || target.Repo != "other/project" {
		t.Fatalf("explicit target=%#v err=%v", target, err)
	}
}

func uxCode(err error) uxv1.Code { return uxv1.AsError(err).Code }

func TestTargetDoesNotTreatKnownGitHubRemoteAsGitLabAuthority(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", "git@github.com:group/project.git")
	parsed, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTarget(context.Background(), *parsed.Command, dir, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("known GitHub remote was accepted as implicit GitLab authority")
	}
}

func TestTargetRequiresExplicitAuthorityForSelfManagedRemote(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", "git@gitlab.internal:group/project.git")
	parsed, err := Parse([]string{"issue", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTarget(context.Background(), *parsed.Command, dir, func(string) (string, bool) { return "", false }); err == nil || uxCode(err) != uxv1.CodeSafety {
		t.Fatalf("implicit self-managed authority error=%v", err)
	}

	explicit, err := Parse([]string{"issue", "list", "--hostname", "gitlab.internal"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveTarget(context.Background(), *explicit.Command, dir, func(string) (string, bool) { return "", false })
	if err != nil || target != (Target{Host: "gitlab.internal", Repo: "group/project"}) {
		t.Fatalf("explicit target=%#v err=%v", target, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, "missing-global"), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
