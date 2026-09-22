package glab

import (
	"path/filepath"
	"testing"
)

// Ordinary MR writes and exact-note readback are deliberately NOT new official
// CLI operations. Their native-only product routing must never fall back here.
func TestMRWriteDelegationRemainsDenied(t *testing.T) {
	for _, operation := range []Operation{"mr-state-update", "mr-note-create", "mr-note-view"} {
		_, err := build(Request{Operation: operation, Host: "gitlab.example.invalid", Repo: "group/project", IID: 42, ID: 501, InputFile: filepath.Join(t.TempDir(), "body.json")})
		if err == nil {
			t.Fatalf("unsafe delegated operation became available: %s", operation)
		}
	}
}
