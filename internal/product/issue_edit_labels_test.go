package product

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func TestIssueEditNestedScopesUseLastSeparator(t *testing.T) {
	const (
		backendReview      = "workflow::backend::review"
		backendDevelopment = "workflow::backend::development"
		frontendReview     = "workflow::frontend::review"
		workflowReview     = "workflow::review"
	)
	catalog := []issueEditLabel{
		{ID: 10, Name: backendReview},
		{ID: 11, Name: backendDevelopment},
		{ID: 12, Name: frontendReview},
		{ID: 13, Name: workflowReview},
	}
	for _, test := range []struct {
		name         string
		before       []string
		add          []string
		remove       []string
		wantLabels   []string
		wantConflict bool
	}{
		{name: "distinct nested scopes", before: []string{"keep", backendReview}, add: []string{frontendReview}, wantLabels: []string{"keep", backendReview, frontendReview}},
		{name: "flat and nested scopes", before: []string{"keep", workflowReview}, add: []string{backendReview}, wantLabels: []string{"keep", backendReview, workflowReview}},
		{name: "two distinct nested additions", before: []string{"keep"}, add: []string{backendReview, frontendReview}, wantLabels: []string{"keep", backendReview, frontendReview}},
		{name: "implicit nested replacement", before: []string{"keep", backendReview}, add: []string{backendDevelopment}, wantConflict: true},
		{name: "two same-scope nested additions", before: []string{"keep"}, add: []string{backendReview, backendDevelopment}, wantConflict: true},
		{name: "explicit nested replacement", before: []string{"keep", backendReview}, add: []string{backendDevelopment}, remove: []string{backendReview}, wantLabels: []string{"keep", backendDevelopment}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := issueEditFixture()
			before.Labels = test.before
			delegate := issueEditDelegate(before, before, catalog)
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), issueEditArgs(t, nil, nil, test.add, test.remove, true, "json"), deps)
			assertIssueEditNoMutation(t, delegate)
			var envelope struct {
				OK    bool            `json:"ok"`
				Data  issueEditOutput `json:"data"`
				Error *uxv1.Error     `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if test.wantConflict {
				if code != 6 || envelope.OK || envelope.Error == nil || envelope.Error.Code != uxv1.CodeConflict {
					t.Fatalf("expected scoped-label conflict: exit=%d output=%s", code, stdout)
				}
				return
			}
			edit := envelope.Data.Edit
			if code != 0 || !envelope.OK || edit.Action != "preview" || !edit.DryRun || edit.Changes.Labels == nil {
				t.Fatalf("exit=%d output=%s", code, stdout)
			}
			labels := edit.Changes.Labels
			if labels.Before.Values == nil || labels.After.Values == nil || !reflect.DeepEqual(*labels.Before.Values, test.before) || !reflect.DeepEqual(*labels.After.Values, test.wantLabels) {
				t.Fatalf("unexpected scoped-label receipt: %s", stdout)
			}
			if countOperation(delegate.requests, glab.OpIssueEditView) != 2 || countOperation(delegate.requests, glab.OpIssueEditLabelList) != 2 {
				t.Fatalf("preview skipped repeated validation: %#v", delegate.requests)
			}
		})
	}
}

func TestIssueEditScopedLabelsRequireExplicitReplacement(t *testing.T) {
	Run := runIssueEditStateMachine
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
