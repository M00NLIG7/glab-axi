package glab

import (
	"path/filepath"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

func TestRepoAdminOperationsAreNotDelegated(t *testing.T) {
	for _, operation := range []Operation{"repo-admin-user", "repo-admin-namespace", "repo-admin-project", "repo-admin-create", "repo-admin-edit", "repo-admin-fork"} {
		_, err := build(Request{Operation: operation, Host: "gitlab.example.invalid", Repo: "team/sub/project", ID: 101, InputFile: filepath.Join(t.TempDir(), "input.json")})
		if err == nil || uxv1.AsError(err).Code != uxv1.CodeUnsupported {
			t.Fatalf("retired administration operation %s was delegated: %v", operation, err)
		}
	}
}
