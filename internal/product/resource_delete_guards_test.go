package product

import (
	"bytes"
	"context"
	"strings"
	"testing"

	runtimepkg "gl-axi/internal/runtime"
)

func TestResourceDeletionExpectedIdentityGuards(t *testing.T) {
	for _, item := range deletionCases() {
		t.Run(item.name, func(t *testing.T) {
			inputs := map[string][]string{}
			base := item.args()
			inputs["other-host"] = deletionReplaceFlag(base, "--hostname", "other.delete.example", false)
			wrongURL := deletionReplaceFlag(base, "--expected-url", "https://other.example/not-this-resource", false)
			if item.group == "pipeline" {
				wrongURL = deletionReplaceFlag(wrongURL, "--acknowledge-child-cancellation", "https://other.example/not-this-resource", false)
			}
			if item.group == "release" {
				wrongURL = deletionReplaceFlag(wrongURL, "--acknowledge-catalog-unpublication", "https://other.example/not-this-resource", false)
			}
			inputs["other-url"] = deletionReplaceFlag(wrongURL, item.confirmation, "https://other.example/not-this-resource", false)
			wrongSelector := append([]string(nil), base...)
			wrongSelector[2] = "999"
			inputs["other-selector"] = wrongSelector
			if !item.personal {
				inputs["other-project-path"] = deletionReplaceFlag(base, "--repo", "group/sibling", false)
				inputs["other-project-id"] = deletionReplaceFlag(base, "--expected-project-id", "102", false)
			}
			switch item.group {
			case "issue":
				inputs["global-id-not-iid"] = deletionReplaceFlag(base, "--expected-id", "42", false)
				inputs["state"] = deletionReplaceFlag(base, "--expected-state", "closed", false)
			case "pipeline":
				inputs["commit"] = deletionReplaceFlag(base, "--expected-sha", strings.Repeat("b", 40), false)
				inputs["ref"] = deletionReplaceFlag(base, "--expected-ref", "sibling", false)
				inputs["status"] = deletionReplaceFlag(base, "--expected-status", "failed", false)
				inputs["job-is-not-pipeline-selector"] = append(append([]string(nil), base...), "--job-id", "88")
			case "release":
				inputs["commit"] = deletionReplaceFlag(base, "--expected-commit", strings.Repeat("b", 40), false)
				inputs["created-at"] = deletionReplaceFlag(base, "--expected-created-at", "2026-09-02T12:00:00Z", false)
				inputs["tag-delete-not-authorized"] = append(append([]string(nil), base...), "--cleanup-tag")
			case "snippet":
				inputs["author"] = deletionReplaceFlag(base, "--expected-author-id", "8", false)
			}
			if item.group != "release" {
				inputs["revision"] = deletionReplaceFlag(base, "--expected-updated-at", "2026-09-02T12:00:00Z", false)
			}
			for name, args := range inputs {
				t.Run(name, func(t *testing.T) {
					f := newDeletionFixture(t, item, "success")
					code, out := f.run(context.Background(), args, true)
					if code == 0 || out.OK || f.deletes != 0 {
						t.Fatalf("exit=%d ok=%v writes=%d", code, out.OK, f.deletes)
					}
				})
			}
		})
	}
}

func TestResourceDeletionLabelsAndBroaderDestructionStayDisabled(t *testing.T) {
	for _, args := range [][]string{
		{"label", "delete", "42"},
		{"repo", "delete", "group/project"},
		{"job", "erase", "88"},
		{"mr", "delete", "42"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			var stdout bytes.Buffer
			deps := Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &stdout, Stderr: &bytes.Buffer{}, LookupEnv: func(string) (string, bool) { t.Fatal("denial consulted credentials"); return "", false }}, NewDelegate: func() delegateClient { t.Fatal("denial consulted official glab"); return nil }}
			if code := Run(context.Background(), append(args, "--auth-source", "native", "--format", "json"), deps); code != 2 {
				t.Fatalf("exit=%d", code)
			}
		})
	}
}

func TestResourceDeletionNativeSelectorAndRequiredGuards(t *testing.T) {
	for _, item := range deletionCases() {
		t.Run(item.name, func(t *testing.T) {
			parsed, err := Parse(item.args())
			if err != nil || parsed.Command == nil {
				t.Fatalf("valid exact selectors rejected: %v", err)
			}
			if !parsed.Command.Definition.Write || !parsed.Command.Definition.RequireNativeAuth {
				t.Fatal("deletion lost its explicit native write contract")
			}
			for _, flag := range item.expected {
				if !strings.HasPrefix(flag, "--") {
					continue
				}
				if _, err := Parse(deletionReplaceFlag(item.args(), flag, "", true)); err == nil {
					t.Fatalf("missing %s accepted", flag)
				}
			}
		})
	}
}
