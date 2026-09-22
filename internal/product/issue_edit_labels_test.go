package product

import (
	"context"
	"testing"

	"gl-axi/internal/delegate/glab"
)

func TestIssueEditScopedLabelsRequireExplicitReplacement(t *testing.T) {
	for _, mode := range []string{"implicit replacement", "explicit replacement", "two additions"} {
		t.Run(mode, func(t *testing.T) {
			before, after := issueEditFixture(), issueEditFixture()
			before.Labels = []string{"keep", "workflow::old"}
			after.Labels = []string{"keep", "workflow::new"}
			catalog := []issueEditLabel{{ID: 10, Name: "workflow::old"}, {ID: 11, Name: "workflow::new"}, {ID: 12, Name: "workflow::other"}}
			delegate := issueEditDelegate(before, before, catalog)
			delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{issueEditResponse(after)}
			delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(after))
			delegate.responses[glab.OpIssueEditLabelList] = append(delegate.responses[glab.OpIssueEditLabelList], issueEditResponse(catalog))
			add, remove := []string{"workflow::new"}, []string(nil)
			if mode == "explicit replacement" {
				remove = []string{"workflow::old"}
			}
			if mode == "two additions" {
				before.Labels = []string{"keep"}
				delegate.responses[glab.OpIssueEditView][0] = issueEditResponse(before)
				add = append(add, "workflow::other")
			}
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), issueEditArgs(t, nil, nil, add, remove, false, "json"), deps)
			if mode == "explicit replacement" {
				if code != 0 {
					t.Fatalf("exit=%d output=%s", code, stdout)
				}
				assertIssueEditOneMutation(t, delegate)
			} else {
				if code == 0 {
					t.Fatalf("implicit replacement: %s", stdout)
				}
				assertIssueEditNoMutation(t, delegate)
			}
		})
	}
}
