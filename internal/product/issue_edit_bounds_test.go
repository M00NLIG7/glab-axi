package product

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestIssueEditRejectsQuickActionInputBeforeProvider(t *testing.T) {
	Run := runIssueEditStateMachine
	for _, body := range []string{"/close", "ordinary\n/assign @someone", " \t/label other", "```\n/merge\n```", "text\r/confidential", "/unknown-future-action"} {
		t.Run(fmt.Sprintf("%q", body), func(t *testing.T) {
			delegate := &fakeDelegate{}
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), issueEditArgs(t, nil, &body, nil, nil, false, "json"), deps); code != 9 || len(delegate.requests) != 0 {
				t.Fatalf("unsafe input: exit=%d requests=%v output=%s", code, delegate.requests, stdout)
			}
		})
	}
	before := issueEditFixture()
	before.Description = "/close"
	delegate := issueEditDelegate(before, before, nil)
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code != 9 {
		t.Fatalf("unsafe existing body: %s", stdout)
	}
	assertIssueEditNoMutation(t, delegate)
}

func TestIssueEditHardPageAndByteBounds(t *testing.T) {
	Run := runIssueEditStateMachine
	for _, phase := range []string{"preflight", "postwrite"} {
		for _, mode := range []string{"pages", "page bytes", "operation bytes", "too many labels"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				before, after := issueEditFixture(), issueEditFixture()
				after.Labels = []string{"bug", "keep", "triage"}
				delegate := issueEditDelegate(before, before, issueEditCatalog())
				var pages []glab.Response
				for page := 1; page <= limits.MaxPages; page++ {
					labels := make([]issueEditLabel, issueEditPageSize)
					for i := range labels {
						id := int64(page*100 + i)
						labels[i] = issueEditLabel{ID: id, Name: fmt.Sprintf("label-%d", id)}
					}
					if mode == "too many labels" {
						labels = append(labels, issueEditLabel{ID: 9999, Name: "extra"})
					}
					response := issueEditResponse(labels)
					if mode == "page bytes" {
						response.Body = []byte(strings.Repeat(" ", limits.MaxJSONPageBytes+1))
					}
					if mode == "operation bytes" {
						response.Body = append(response.Body, []byte(strings.Repeat(" ", limits.MaxJSONPageBytes-len(response.Body)))...)
					}
					pages = append(pages, response)
				}
				if phase == "preflight" {
					delegate.responses[glab.OpIssueEditLabelList] = pages
				} else {
					delegate.responses[glab.OpIssueEditLabelList] = append(delegate.responses[glab.OpIssueEditLabelList], pages...)
					delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{issueEditResponse(after)}
					delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(after))
				}
				stdout, _, deps := productTestDeps(t, delegate)
				code := Run(context.Background(), issueEditArgs(t, nil, nil, []string{"triage"}, nil, false, "json"), deps)
				if code == 0 {
					t.Fatalf("unbounded success: %s", stdout)
				}
				if phase == "preflight" {
					assertIssueEditNoMutation(t, delegate)
				} else {
					assertIssueEditOneMutation(t, delegate)
					assertIssueEditAmbiguous(t, code, stdout.Bytes())
				}
				for _, request := range delegate.requests {
					if request.Operation == glab.OpIssueEditLabelList && (request.Page > limits.MaxPages || request.PerPage != issueEditPageSize) {
						t.Fatalf("invalid page: %#v", request)
					}
				}
			})
		}
	}
}
