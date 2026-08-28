package safeurl

import (
	"net/url"
	"strings"
	"testing"

	v1 "gl-axi/internal/contract/v1"
)

func TestAuthoritySupportsRelativeInstallAndEncodesProject(t *testing.T) {
	authority, err := NewAuthority("gitlab.example.invalid", "https://api.example.invalid/gitlab/api/v4", "https://web.example.invalid/gitlab")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := authority.Endpoint("projects/"+url.PathEscape("group/sub/project")+"/merge_requests", url.Values{"state": {"opened"}})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Host != "api.example.invalid" || endpoint.EscapedPath() != "/gitlab/api/v4/projects/group%2Fsub%2Fproject/merge_requests" || endpoint.Query().Get("state") != "opened" {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
	if got := authority.ExpectedProjectURL("group/sub/project"); got != "https://web.example.invalid/gitlab/group/sub/project" {
		t.Fatalf("unexpected project URL: %s", got)
	}
}

func TestReturnedURLsAreBoundToExactProject(t *testing.T) {
	authority, _ := NewAuthority("gitlab.example.invalid", "https://gitlab.example.invalid/api/v4", "https://gitlab.example.invalid")
	if err := authority.ValidateMRWebURL("https://gitlab.example.invalid/group/project/-/merge_requests/3", "group/project", 3); err != nil {
		t.Fatal(err)
	}
	unsafe := []string{
		"http://gitlab.example.invalid/group/project/-/merge_requests/3",
		"https://attacker.invalid/group/project/-/merge_requests/3",
		"https://gitlab.example.invalid/other/project/-/merge_requests/3",
		"https://user@gitlab.example.invalid/group/project/-/merge_requests/3",
		"https://gitlab.example.invalid/group/project/-/merge_requests/3?token=x",
	}
	for _, raw := range unsafe {
		if err := authority.ValidateMRWebURL(raw, "group/project", 3); v1.ExitCode(err) != 9 {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestProjectAndBranchValidationBoundaries(t *testing.T) {
	validProjects := []string{"group/project", "group/sub-group/project_1"}
	for _, value := range validProjects {
		if err := ValidateProject(value); err != nil {
			t.Fatalf("valid project %q rejected: %v", value, err)
		}
	}
	invalidProjects := []string{"project", "group//project", "../group/project", "group/project.git", "group/project?x"}
	for _, value := range invalidProjects {
		if err := ValidateProject(value); err == nil {
			t.Fatalf("invalid project %q accepted", value)
		}
	}
	validBranches := []string{"main", "feature/safe-name", "dash-branch"}
	for _, value := range validBranches {
		if err := ValidateBranch(value); err != nil {
			t.Fatalf("valid branch %q rejected: %v", value, err)
		}
	}
	invalidBranches := []string{"", "../main", ".hidden", "feature/.hidden", "feature//x", "x.lock", "x@{y", "x:y", strings.Repeat("x", 1025)}
	for _, value := range invalidBranches {
		if err := ValidateBranch(value); err == nil {
			t.Fatalf("invalid branch %q accepted", value)
		}
	}
}
