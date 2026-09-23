package glab

import (
	"context"
	"errors"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

func TestIssueWriteDelegationUnavailable(t *testing.T) {
	for _, operation := range []Operation{"issue-write-project", "issue-write-view", "issue-create", "issue-note-create", "issue-state"} {
		t.Run(string(operation), func(t *testing.T) {
			lookups := 0
			client := NewClient(ClientConfig{LookPath: func(string) (string, error) {
				lookups++
				return "", errors.New("no delegated executable")
			}})
			_, err := client.Do(context.Background(), Request{Operation: operation, Host: "gitlab.com", Repo: "group/project", IID: 42})
			if err == nil || uxv1.AsError(err).Code != uxv1.CodeUnsupported || lookups != 0 {
				t.Fatalf("unused delegated operation accepted: error=%v executable lookups=%d", err, lookups)
			}
		})
	}
}
