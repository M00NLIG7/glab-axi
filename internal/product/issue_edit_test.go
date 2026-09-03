package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

const (
	issueEditTestURL       = "https://gitlab.com/group/project/-/issues/42"
	issueEditTestTimestamp = "2026-08-15T12:00:00Z"
)

var (
	issueEditTestTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	issueEditNextTime = time.Date(2026, 8, 15, 12, 0, 1, 0, time.UTC)
)

func TestIssueEditValidationBudgetFitsReadOperation(t *testing.T) {
	if limits.IssueEditPreflight <= 0 || limits.IssueEditPreflight > limits.ShortOperation {
		t.Fatalf("issue-edit validation budget=%s, outer read budget=%s", limits.IssueEditPreflight, limits.ShortOperation)
	}
	definition, ok := lookupDefinition([]string{"issue", "edit"})
	if !ok || definition.Write {
		t.Fatalf("issue edit must remain a read-only validation surface: %#v", definition)
	}
}

func TestIssueEditFieldCombinationsRefuseBeforeMutationWithDeterministicReceipt(t *testing.T) {
	tests := []struct {
		name        string
		title       *string
		description *string
		add         []string
		remove      []string
		wantFields  []string
	}{
		{name: "title only", title: stringPointer("new title"), wantFields: []string{"title"}},
		{name: "description only", description: stringPointer("new body"), wantFields: []string{"description"}},
		{name: "description clear", description: stringPointer(""), wantFields: []string{"description"}},
		{name: "labels only", add: []string{"triage"}, remove: []string{"bug"}, wantFields: []string{"labels"}},
		{name: "combined", title: stringPointer("combined title"), description: stringPointer("combined body"), add: []string{"triage"}, remove: []string{"bug"}, wantFields: []string{"title", "description", "labels"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := issueEditFixture()
			delegate := issueEditDelegate(before, before, issueEditCatalog())
			stdout, stderr, deps := productTestDeps(t, delegate)
			args := issueEditArgs(t, test.title, test.description, test.add, test.remove, false, "json")
			if code := Run(context.Background(), args, deps); code != 9 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
			}
			assertIssueEditNoMutation(t, delegate)
			if got := countOperation(delegate.requests, glab.OpIssueEditView); got != 2 {
				t.Fatalf("issue reads=%d requests=%#v", got, delegate.requests)
			}
			wantLabelReads := 0
			if len(test.add)+len(test.remove) > 0 {
				wantLabelReads = 2
			}
			if got := countOperation(delegate.requests, glab.OpIssueEditLabelList); got != wantLabelReads {
				t.Fatalf("label reads=%d want=%d requests=%#v", got, wantLabelReads, delegate.requests)
			}
			wantOperations := []glab.Operation{glab.OpIssueEditProject, glab.OpIssueEditView, glab.OpIssueEditView}
			if wantLabelReads > 0 {
				wantOperations = []glab.Operation{glab.OpIssueEditProject, glab.OpIssueEditView, glab.OpIssueEditLabelList, glab.OpIssueEditView, glab.OpIssueEditLabelList}
			}
			gotOperations := make([]glab.Operation, len(delegate.requests))
			for index, request := range delegate.requests {
				gotOperations[index] = request.Operation
			}
			if !reflect.DeepEqual(gotOperations, wantOperations) {
				t.Fatalf("provider operation order=%v want=%v", gotOperations, wantOperations)
			}
			if len(delegate.inputBodies) != 0 {
				t.Fatalf("refusal created a private mutation body: %q", delegate.inputBodies)
			}

			var envelope struct {
				OK    bool      `json:"ok"`
				Meta  uxv1.Meta `json:"meta"`
				Error struct {
					Code      uxv1.Code       `json:"code"`
					Message   string          `json:"message"`
					Retryable bool            `json:"retryable"`
					Receipt   issueEditOutput `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(stdout.Bytes(), []byte(`"data":`)) {
				t.Fatalf("refusal mixed success data into the strict failure envelope: %s", stdout.String())
			}
			edit := envelope.Error.Receipt.Edit
			if envelope.OK || envelope.Error.Code != uxv1.CodeSafety || envelope.Error.Message != issueEditProviderPreconditionMessage || envelope.Error.Retryable || edit.Action != "refused" || edit.Outcome != "not_applied" || edit.DryRun || edit.RefusalReason != issueEditProviderPreconditionReason || fmt.Sprint(edit.ChangedFields) != fmt.Sprint(test.wantFields) || edit.Identity.ProjectID != 101 || edit.Identity.ProjectFullPath != "group/project" || edit.Identity.IssueID != 1001 || edit.Identity.IID != 42 || edit.Identity.WebURL != issueEditTestURL || edit.Expected.UpdatedAt != issueEditTestTimestamp || edit.ResultingUpdatedAt != issueEditTestTimestamp {
				t.Fatalf("unexpected refusal receipt: %#v", envelope)
			}
			if envelope.Meta.Backend != "official-glab" || envelope.Meta.Host != "gitlab.com" || envelope.Meta.Repo != "group/project" || envelope.Meta.Complete || envelope.Meta.Reason != issueEditProviderPreconditionReason || envelope.Meta.UpstreamVersion != glab.SupportedVersion {
				t.Fatalf("unexpected refusal metadata: %#v", envelope.Meta)
			}
			if test.name == "labels only" || test.name == "combined" {
				if edit.Changes.Labels == nil || edit.Changes.Labels.Before.Values == nil || edit.Changes.Labels.After.Values == nil || fmt.Sprint(*edit.Changes.Labels.Before.Values) != fmt.Sprint([]string{"bug", "keep"}) || fmt.Sprint(*edit.Changes.Labels.After.Values) != fmt.Sprint([]string{"keep", "triage"}) {
					t.Fatalf("unrelated labels were not evidenced as preserved: %#v", edit.Changes.Labels)
				}
			}
		})
	}
}

func TestIssueEditNoOpAndPreviewPerformFullValidationWithoutMutation(t *testing.T) {
	t.Run("no-op", func(t *testing.T) {
		before := issueEditFixture()
		delegate := issueEditDelegate(before, before, issueEditCatalog())
		stdout, _, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, stringPointer(before.Title), nil, []string{"bug"}, []string{"unused"}, false, "json"), deps); code != 0 {
			t.Fatalf("exit=%d output=%s", code, stdout.String())
		}
		assertIssueEditNoMutation(t, delegate)
		if countOperation(delegate.requests, glab.OpIssueEditView) != 2 || countOperation(delegate.requests, glab.OpIssueEditLabelList) != 2 {
			t.Fatalf("no-op skipped adjacent validation: %#v", delegate.requests)
		}
		var envelope struct {
			Data issueEditOutput `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Edit.Action != "unchanged" || envelope.Data.Edit.Outcome != "not_applied" || envelope.Data.Edit.DryRun || len(envelope.Data.Edit.ChangedFields) != 0 || !strings.Contains(stdout.String(), `"changed_fields":[]`) {
			t.Fatalf("no-op receipt=%#v raw=%s", envelope.Data.Edit, stdout.String())
		}
	})

	t.Run("preview", func(t *testing.T) {
		before := issueEditFixture()
		delegate := issueEditDelegate(before, before, issueEditCatalog())
		stdout, _, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, stringPointer("preview title"), nil, []string{"triage"}, nil, true, "json"), deps); code != 0 {
			t.Fatalf("exit=%d output=%s", code, stdout.String())
		}
		assertIssueEditNoMutation(t, delegate)
		if countOperation(delegate.requests, glab.OpIssueEditView) != 2 || countOperation(delegate.requests, glab.OpIssueEditLabelList) != 2 {
			t.Fatalf("preview skipped validation: %#v", delegate.requests)
		}
		var envelope struct {
			Data issueEditOutput `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		edit := envelope.Data.Edit
		if edit.Action != "preview" || edit.Outcome != "not_applied" || !edit.DryRun || fmt.Sprint(edit.ChangedFields) != fmt.Sprint([]string{"title", "labels"}) || edit.ResultingUpdatedAt != issueEditTestTimestamp || !strings.Contains(stdout.String(), `"remove":[]`) {
			t.Fatalf("preview receipt=%#v raw=%s", edit, stdout.String())
		}
	})
}

func TestPinnedIssueEditConsumerContractDrivesGrammar(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "issue-edit", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Schema  string `json:"schema"`
		Surface string `json:"surface"`
		GlabAXI struct {
			RequiredEnvelope        string `json:"required_envelope"`
			RequiredBackend         string `json:"required_backend"`
			RequiredUpstreamVersion string `json:"required_upstream_version"`
		} `json:"glab_axi"`
		PlannedInvocation struct {
			RequiredExplicitInputs []string `json:"required_explicit_inputs"`
			ForbiddenFields        []string `json:"forbidden_fields"`
			PreviewFlag            string   `json:"preview_flag"`
		} `json:"planned_invocation"`
		SuccessContract struct {
			DataSchema string   `json:"data_schema"`
			Actions    []string `json:"actions"`
			Outcomes   []string `json:"outcomes"`
		} `json:"success_contract"`
		RefusalContract struct {
			Error         string `json:"error"`
			Action        string `json:"action"`
			Outcome       string `json:"outcome"`
			Reason        string `json:"reason"`
			ReceiptSchema string `json:"receipt_schema"`
		} `json:"refusal_contract"`
		ProviderContract struct {
			IssueReads           int      `json:"issue_reads_per_validation"`
			MutationAttempts     int      `json:"mutation_attempts_maximum"`
			PostReads            int      `json:"issue_reads_after_attempt"`
			LabelCatalog         string   `json:"label_catalog"`
			RequestedFields      []string `json:"requested_fields"`
			ProviderPrecondition string   `json:"provider_precondition"`
		} `json:"provider_contract"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "glab-axi/issue-edit-consumer-contract/v1" || contract.Surface != "exact-identity GitLab issue edit validation" || contract.GlabAXI.RequiredEnvelope != uxv1.Schema || contract.GlabAXI.RequiredBackend != "official-glab" || contract.GlabAXI.RequiredUpstreamVersion != glab.SupportedVersion || contract.PlannedInvocation.PreviewFlag != "--dry-run" || contract.SuccessContract.DataSchema != "schema/ux-v1/issue-edit.schema.json" || contract.RefusalContract.Error != string(uxv1.CodeSafety) || contract.RefusalContract.Action != "refused" || contract.RefusalContract.Outcome != "not_applied" || contract.RefusalContract.Reason != issueEditProviderPreconditionReason || contract.RefusalContract.ReceiptSchema != contract.SuccessContract.DataSchema || contract.ProviderContract.IssueReads != 2 || contract.ProviderContract.MutationAttempts != 0 || contract.ProviderContract.PostReads != 0 || contract.ProviderContract.LabelCatalog != "complete bounded project and ancestor catalog, read twice when labels are requested" || contract.ProviderContract.ProviderPrecondition == "" {
		t.Fatalf("unexpected issue-edit contract: %#v", contract)
	}
	wantInputs := []string{"iid", "nested_project", "host", "canonical_issue_url", "expected_state", "expected_updated_at", "at_least_one_field_change"}
	wantActions := []string{"preview", "unchanged"}
	wantFields := []string{"title", "description", "add_labels", "remove_labels"}
	if !reflect.DeepEqual(contract.PlannedInvocation.RequiredExplicitInputs, wantInputs) || !reflect.DeepEqual(contract.SuccessContract.Actions, wantActions) || !reflect.DeepEqual(contract.SuccessContract.Outcomes, []string{"not_applied"}) || !reflect.DeepEqual(contract.ProviderContract.RequestedFields, wantFields) || len(contract.PlannedInvocation.ForbiddenFields) != 9 {
		t.Fatalf("incomplete issue-edit contract: %#v", contract)
	}
	result, err := Parse([]string{
		"issue", "edit", "42", "--repo", "group/project", "--hostname", "gitlab.com",
		"--expected-url", issueEditTestURL, "--expected-state", "opened", "--expected-updated-at", issueEditTestTimestamp,
		"--title-file", "/private/title", "--add-label", "triage", "--add-label", "ready", "--dry-run", "--format", "json",
	})
	if err != nil || result.Command == nil {
		t.Fatalf("contract invocation failed to parse: result=%#v error=%v", result, err)
	}
	parsed := result.Command
	if strings.Join(parsed.Definition.Path, " ") != "issue edit" || parsed.Positionals[0] != "42" || !parsed.Booleans["--dry-run"] || !reflect.DeepEqual(parsed.MultiValues["--add-label"], []string{"triage", "ready"}) {
		t.Fatalf("contract invocation changed meaning: %#v", parsed)
	}
}

func TestIssueEditSchemasPinStructuredSafetyRefusal(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "ux-v1", "issue-edit.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receiptSchema struct {
		Properties struct {
			Edit struct {
				Properties struct {
					Action struct {
						Enum []string `json:"enum"`
					} `json:"action"`
					Outcome struct {
						Const string `json:"const"`
					} `json:"outcome"`
					RefusalReason struct {
						Const string `json:"const"`
					} `json:"refusal_reason"`
				} `json:"properties"`
			} `json:"edit"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &receiptSchema); err != nil {
		t.Fatal(err)
	}
	edit := receiptSchema.Properties.Edit.Properties
	if !reflect.DeepEqual(edit.Action.Enum, []string{"preview", "unchanged", "refused"}) || edit.Outcome.Const != "not_applied" || edit.RefusalReason.Const != issueEditProviderPreconditionReason {
		t.Fatalf("unexpected receipt schema: %#v", edit)
	}

	data, err = os.ReadFile(filepath.Join("..", "..", "schema", "glab-axi-ux-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelopeSchema struct {
		Properties struct {
			Error struct {
				Properties struct {
					Receipt struct {
						Ref        string `json:"$ref"`
						Properties struct {
							Edit struct {
								Properties struct {
									Action struct {
										Const string `json:"const"`
									} `json:"action"`
								} `json:"properties"`
							} `json:"edit"`
						} `json:"properties"`
					} `json:"receipt"`
				} `json:"properties"`
			} `json:"error"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &envelopeSchema); err != nil {
		t.Fatal(err)
	}
	if envelopeSchema.Properties.Error.Properties.Receipt.Ref != "ux-v1/issue-edit.schema.json" || envelopeSchema.Properties.Error.Properties.Receipt.Properties.Edit.Properties.Action.Const != "refused" {
		t.Fatalf("unexpected refusal receipt schema reference: %#v", envelopeSchema)
	}
}

func TestIssueEditParserRefusalsConstructNoDelegate(t *testing.T) {
	base := []string{
		"issue", "edit", "42", "--repo", "group/project", "--hostname", "gitlab.com",
		"--expected-url", issueEditTestURL, "--expected-state", "opened", "--expected-updated-at", issueEditTestTimestamp,
		"--title-file", "/private/title", "--format", "json",
	}
	tooManyLabels := appendCopy(base)
	for index := 0; index <= limits.MaxIssueEditLabels; index++ {
		tooManyLabels = append(tooManyLabels, "--add-label", fmt.Sprintf("label-%03d", index))
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing hostname", args: removeFlag(base, "--hostname", true)},
		{name: "missing repo", args: removeFlag(base, "--repo", true)},
		{name: "missing expected URL", args: removeFlag(base, "--expected-url", true)},
		{name: "missing expected state", args: removeFlag(base, "--expected-state", true)},
		{name: "missing expected timestamp", args: removeFlag(base, "--expected-updated-at", true)},
		{name: "no requested field", args: removeFlag(base, "--title-file", true)},
		{name: "noncanonical IID", args: replaceArg(base, "42", "042")},
		{name: "wrong URL host", args: replaceArg(base, issueEditTestURL, "https://evil.example/group/project/-/issues/42")},
		{name: "wrong URL project", args: replaceArg(base, issueEditTestURL, "https://gitlab.com/other/project/-/issues/42")},
		{name: "wrong URL IID", args: replaceArg(base, issueEditTestURL, "https://gitlab.com/group/project/-/issues/43")},
		{name: "URL query", args: replaceArg(base, issueEditTestURL, issueEditTestURL+"?edit=1")},
		{name: "invalid state", args: replaceArg(base, "opened", "open")},
		{name: "invalid timestamp", args: replaceArg(base, issueEditTestTimestamp, "yesterday")},
		{name: "limit denied", args: appendCopy(base, "--limit", "1")},
		{name: "duplicate add", args: appendCopy(base, "--add-label", "bug", "--add-label", "BUG")},
		{name: "overlap", args: appendCopy(base, "--add-label", "bug", "--remove-label", "BUG")},
		{name: "empty label", args: appendCopy(base, "--add-label", " ")},
		{name: "comma label", args: appendCopy(base, "--add-label", "bug,triage")},
		{name: "oversized label", args: appendCopy(base, "--add-label", strings.Repeat("x", limits.MaxLabelNameBytes+1))},
		{name: "too many labels", args: tooManyLabels},
		{name: "inline title denied", args: appendCopy(base, "--title", "unsafe")},
		{name: "state mutation denied", args: appendCopy(base, "--state-event", "close")},
		{name: "assignee mutation denied", args: appendCopy(base, "--assignee-id", "7")},
		{name: "unguarded update alias", args: []string{"issue", "update", "42"}},
		{name: "close remains denied", args: []string{"issue", "close", "42"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			deps := Dependencies{
				Runtime: productRuntimeNoDiscovery(t, &stdout),
				NewDelegate: func() delegateClient {
					t.Fatal("refused issue edit constructed an official-glab delegate")
					return nil
				},
			}
			if code := Run(context.Background(), test.args, deps); code == 0 {
				t.Fatalf("refused argv succeeded: %v output=%s", test.args, stdout.String())
			}
		})
	}
}

func TestIssueEditRefusesWrongStaleOrChangingIdentityWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		first  upstreamIssue
		second upstreamIssue
	}{
		{
			name:  "wrong project ID",
			first: func() upstreamIssue { issue := issueEditFixture(); issue.ProjectID = 202; return issue }(),
		},
		{
			name:  "wrong IID",
			first: func() upstreamIssue { issue := issueEditFixture(); issue.IID = 43; return issue }(),
		},
		{
			name: "redirected URL",
			first: func() upstreamIssue {
				issue := issueEditFixture()
				issue.WebURL = "https://gitlab.com/group/project/-/issues/43"
				return issue
			}(),
		},
		{
			name: "cross-host URL",
			first: func() upstreamIssue {
				issue := issueEditFixture()
				issue.WebURL = "https://evil.example/group/project/-/issues/42"
				return issue
			}(),
		},
		{
			name: "cross-project URL",
			first: func() upstreamIssue {
				issue := issueEditFixture()
				issue.WebURL = "https://gitlab.com/other/project/-/issues/42"
				return issue
			}(),
		},
		{
			name: "stale timestamp",
			first: func() upstreamIssue {
				issue := issueEditFixture()
				issue.UpdatedAt = timePointer(issueEditNextTime)
				return issue
			}(),
		},
		{
			name:  "stale state",
			first: func() upstreamIssue { issue := issueEditFixture(); issue.State = "closed"; return issue }(),
		},
		{
			name: "duplicate attached label identities",
			first: func() upstreamIssue {
				issue := issueEditFixture()
				issue.Labels = []string{"Bug", "bug"}
				return issue
			}(),
		},
		{
			name:  "target changes between validation reads",
			first: issueEditFixture(),
			second: func() upstreamIssue {
				issue := issueEditFixture()
				issue.Description = "changed without timestamp"
				return issue
			}(),
		},
		{
			name:  "global issue identity changes between validation reads",
			first: issueEditFixture(),
			second: func() upstreamIssue {
				issue := issueEditFixture()
				issue.ID = 1002
				return issue
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := test.first
			if first.ID == 0 {
				first = issueEditFixture()
			}
			second := test.second
			if second.ID == 0 {
				second = first
			}
			delegate := issueEditDelegate(first, second, nil)
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code == 0 {
				t.Fatalf("unsafe edit succeeded: %s", stdout.String())
			}
			assertIssueEditNoMutation(t, delegate)
		})
	}

	t.Run("wrong canonical project", func(t *testing.T) {
		delegate := issueEditDelegate(issueEditFixture(), issueEditFixture(), nil)
		delegate.responses[glab.OpIssueEditProject] = []glab.Response{issueEditResponse(issueEditProject{ID: 101, PathWithNamespace: "renamed/project", WebURL: "https://gitlab.com/renamed/project"})}
		stdout, _, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code == 0 {
			t.Fatalf("wrong project succeeded: %s", stdout.String())
		}
		assertIssueEditNoMutation(t, delegate)
	})
}

func TestIssueEditLabelResolutionConsumesEveryPageInBothSnapshots(t *testing.T) {
	before := issueEditFixture()
	pageOne := make([]issueEditLabel, issueEditPageSize)
	for index := range pageOne {
		pageOne[index] = issueEditLabel{ID: int64(index + 1), Name: fmt.Sprintf("other-%03d", index)}
	}
	pageTwo := []issueEditLabel{{ID: 200, Name: "triage"}}
	delegate := issueEditDelegate(before, before, nil)
	delegate.responses[glab.OpIssueEditLabelList] = []glab.Response{
		issueEditResponse(pageOne), issueEditResponse(pageTwo),
		issueEditResponse(pageOne), issueEditResponse(pageTwo),
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), issueEditArgs(t, nil, nil, []string{"triage"}, nil, true, "json"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	assertIssueEditNoMutation(t, delegate)
	var pages []int
	for _, request := range delegate.requests {
		if request.Operation == glab.OpIssueEditLabelList {
			pages = append(pages, request.Page)
		}
	}
	if !reflect.DeepEqual(pages, []int{1, 2, 1, 2}) || !strings.Contains(stdout.String(), `"id":200`) {
		t.Fatalf("label pagination=%v output=%s", pages, stdout.String())
	}
}

func TestIssueEditLabelResolutionRefusalsNeverMutate(t *testing.T) {
	tests := []struct {
		name    string
		catalog []issueEditLabel
		add     []string
		remove  []string
	}{
		{name: "missing label", catalog: issueEditCatalog(), add: []string{"missing"}},
		{name: "ambiguous label", catalog: []issueEditLabel{{ID: 1, Name: "Bug"}, {ID: 2, Name: "bug"}}, add: []string{"bug"}},
		{name: "unexpected case resolution", catalog: []issueEditLabel{{ID: 1, Name: "Bug"}}, add: []string{"bug"}},
		{name: "duplicate provider label ID", catalog: []issueEditLabel{{ID: 1, Name: "triage"}, {ID: 1, Name: "other"}}, add: []string{"triage"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := issueEditFixture()
			delegate := issueEditDelegate(before, before, test.catalog)
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), issueEditArgs(t, nil, nil, test.add, test.remove, false, "json"), deps); code == 0 {
				t.Fatalf("unsafe label edit succeeded: %s", stdout.String())
			}
			assertIssueEditNoMutation(t, delegate)
		})
	}

	t.Run("label identity changes after exact issue revalidation", func(t *testing.T) {
		before := issueEditFixture()
		delegate := issueEditDelegate(before, before, issueEditCatalog())
		delegate.responses[glab.OpIssueEditLabelList] = []glab.Response{
			issueEditResponse(issueEditCatalog()),
			issueEditResponse([]issueEditLabel{{ID: 99, Name: "triage"}, {ID: 11, Name: "bug"}, {ID: 12, Name: "keep"}, {ID: 13, Name: "unused"}}),
		}
		stdout, _, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, []string{"triage"}, nil, false, "json"), deps); code == 0 {
			t.Fatalf("changed final label identity succeeded: %s", stdout.String())
		}
		assertIssueEditNoMutation(t, delegate)
		if got := countOperation(delegate.requests, glab.OpIssueEditLabelList); got != 2 {
			t.Fatalf("final label reads=%d requests=%#v", got, delegate.requests)
		}
	})
}

func TestIssueEditIssueDocumentRequiresFieldsAndAcceptsExplicitNullDescription(t *testing.T) {
	body := []byte(`{"id":1001,"iid":42,"project_id":101,"title":"old title","description":null,"state":"opened","web_url":"https://gitlab.com/group/project/-/issues/42","labels":[],"updated_at":"2026-08-15T12:00:00Z"}`)
	issue, err := decodeIssueEditIssue(body)
	if err != nil || issue.Description != "" || issue.Labels == nil {
		t.Fatalf("issue=%#v error=%v", issue, err)
	}
}

func TestIssueEditMalformedResponsesAndPrivateFilesRefuseBeforeMutation(t *testing.T) {
	tests := []struct {
		name      string
		operation glab.Operation
		body      string
		labels    bool
	}{
		{name: "malformed project", operation: glab.OpIssueEditProject, body: `{"id":`},
		{name: "malformed issue", operation: glab.OpIssueEditView, body: `{"id":1001}`},
		{name: "missing issue description", operation: glab.OpIssueEditView, body: `{"id":1001,"iid":42,"project_id":101,"title":"old title","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/42","labels":[],"updated_at":"2026-08-15T12:00:00Z"}`},
		{name: "missing issue labels", operation: glab.OpIssueEditView, body: `{"id":1001,"iid":42,"project_id":101,"title":"old title","description":"old body","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/42","updated_at":"2026-08-15T12:00:00Z"}`},
		{name: "malformed labels", operation: glab.OpIssueEditLabelList, body: `{"labels":[]}`, labels: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := issueEditFixture()
			delegate := issueEditDelegate(before, before, issueEditCatalog())
			delegate.responses[test.operation] = []glab.Response{{Body: []byte(test.body), UpstreamVersion: glab.SupportedVersion}}
			add := []string(nil)
			if test.labels {
				add = []string{"triage"}
			}
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), issueEditArgs(t, stringPointer("new title"), nil, add, nil, false, "json"), deps); code == 0 {
				t.Fatalf("malformed response succeeded: %s", stdout.String())
			}
			assertIssueEditNoMutation(t, delegate)
		})
	}

	assertLocalFileRefusal := func(t *testing.T, flag, path string) {
		t.Helper()
		args := append(issueEditBaseArgs(), flag, path, "--format", "json")
		delegate := &fakeDelegate{}
		stdout, _, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), args, deps); code == 0 {
			t.Fatalf("unsafe private input succeeded: %s", stdout.String())
		}
		if len(delegate.requests) != 0 {
			t.Fatalf("unsafe private input reached provider: %#v", delegate.requests)
		}
	}

	t.Run("oversized description file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "description")
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, limits.MaxDescriptionBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		assertLocalFileRefusal(t, "--description-file", path)
	})
	t.Run("nonregular description file", func(t *testing.T) {
		assertLocalFileRefusal(t, "--description-file", t.TempDir())
	})

	if runtime.GOOS != "windows" {
		t.Run("symlink title file", func(t *testing.T) {
			dir := t.TempDir()
			realPath := filepath.Join(dir, "real")
			linkPath := filepath.Join(dir, "link")
			if err := os.WriteFile(realPath, []byte("new title"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(realPath, linkPath); err != nil {
				t.Fatal(err)
			}
			assertLocalFileRefusal(t, "--title-file", linkPath)
		})
		t.Run("nonprivate title file", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "title")
			if err := os.WriteFile(path, []byte("new title"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			assertLocalFileRefusal(t, "--title-file", path)
		})
	}
}

func TestIssueEditCancellationAfterFinalValidationNeverMutates(t *testing.T) {
	before := issueEditFixture()
	delegate := issueEditDelegate(before, before, nil)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	delegate.doFunc = func(_ context.Context, request glab.Request) (glab.Response, error, bool) {
		if request.Operation == glab.OpIssueEditView && countOperation(delegate.requests, glab.OpIssueEditView) == 2 {
			cancel()
			return issueEditResponse(before), nil, true
		}
		return glab.Response{}, nil, false
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(parent, issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code != 130 || parent.Err() != context.Canceled || !strings.Contains(stdout.String(), `"code":"canceled"`) {
		t.Fatalf("exit/output/cancellation mismatch: parent=%v output=%s", parent.Err(), stdout.String())
	}
	assertIssueEditNoMutation(t, delegate)
}

func TestIssueEditValidationTimeoutNeverMutates(t *testing.T) {
	before := issueEditFixture()
	delegate := issueEditDelegate(before, before, nil)
	parent, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	delegate.doFunc = func(ctx context.Context, request glab.Request) (glab.Response, error, bool) {
		if request.Operation == glab.OpIssueEditView && countOperation(delegate.requests, glab.OpIssueEditView) == 2 {
			<-ctx.Done()
			return issueEditResponse(before), nil, true
		}
		return glab.Response{}, nil, false
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(parent, issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json"), deps); code != 8 || parent.Err() != context.DeadlineExceeded || !strings.Contains(stdout.String(), `"code":"upstream_error"`) {
		t.Fatalf("exit/output/timeout mismatch: parent=%v output=%s", parent.Err(), stdout.String())
	}
	assertIssueEditNoMutation(t, delegate)
}

func TestIssueEditTOONAndLargeEvidenceAreBounded(t *testing.T) {
	t.Run("description", func(t *testing.T) {
		before := issueEditFixture()
		large := strings.Repeat("x", issueEditInlineTextBytes+1)
		delegate := issueEditDelegate(before, before, nil)
		stdout, stderr, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, nil, &large, nil, nil, false, "toon"), deps); code != 9 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%s output=%s", code, stderr.String(), stdout.String())
		}
		output := stdout.String()
		if !strings.Contains(output, `action: "refused"`) || !strings.Contains(output, `outcome: "not_applied"`) || !strings.Contains(output, `refusal_reason: "provider_precondition_unavailable"`) || !strings.Contains(output, "sha256:") || strings.Contains(output, large) || len(output) > 16<<10 {
			t.Fatalf("TOON evidence was not bounded: bytes=%d output=%s", len(output), output)
		}
		assertIssueEditNoMutation(t, delegate)
	})

	t.Run("label set", func(t *testing.T) {
		before := issueEditFixture()
		before.Labels = make([]string, issueEditInlineLabels+1)
		for index := range before.Labels {
			before.Labels[index] = fmt.Sprintf("unrelated-label-%03d", index)
		}
		delegate := issueEditDelegate(before, before, []issueEditLabel{{ID: 10, Name: "triage"}})
		stdout, stderr, deps := productTestDeps(t, delegate)
		if code := Run(context.Background(), issueEditArgs(t, nil, nil, []string{"triage"}, nil, true, "json"), deps); code != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%s output=%s", code, stderr.String(), stdout.String())
		}
		output := stdout.String()
		if strings.Count(output, `"sha256":`) != 2 || strings.Contains(output, `"values":`) || strings.Contains(output, "unrelated-label-100") || len(output) > 16<<10 {
			t.Fatalf("label-set evidence was not bounded: bytes=%d output=%s", len(output), output)
		}
		assertIssueEditNoMutation(t, delegate)
	})
}

func issueEditFixture() upstreamIssue {
	return upstreamIssue{
		ID: 1001, IID: 42, ProjectID: 101, Title: "old title", Description: "old body", State: "opened",
		WebURL: issueEditTestURL, Labels: []string{"keep", "bug"}, UpdatedAt: timePointer(issueEditTestTime),
	}
}

func issueEditCatalog() []issueEditLabel {
	return []issueEditLabel{{ID: 10, Name: "triage"}, {ID: 11, Name: "bug"}, {ID: 12, Name: "keep"}, {ID: 13, Name: "unused"}}
}

func issueEditDelegate(first, second upstreamIssue, labels []issueEditLabel) *fakeDelegate {
	issueResponses := []glab.Response{issueEditResponse(first), issueEditResponse(second)}
	responses := map[glab.Operation][]glab.Response{
		glab.OpIssueEditProject: {issueEditResponse(issueEditProject{ID: 101, PathWithNamespace: "group/project", WebURL: "https://gitlab.com/group/project"})},
		glab.OpIssueEditView:    issueResponses,
	}
	if labels != nil {
		responses[glab.OpIssueEditLabelList] = []glab.Response{issueEditResponse(labels), issueEditResponse(labels)}
	}
	return &fakeDelegate{responses: responses}
}

func issueEditResponse(value any) glab.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
}

func issueEditBaseArgs() []string {
	return []string{
		"issue", "edit", "42", "--repo", "group/project", "--hostname", "gitlab.com",
		"--expected-url", issueEditTestURL, "--expected-state", "opened", "--expected-updated-at", issueEditTestTimestamp,
	}
}

func issueEditArgs(t *testing.T, title, description *string, add, remove []string, dryRun bool, format string) []string {
	t.Helper()
	args := issueEditBaseArgs()
	dir := t.TempDir()
	if title != nil {
		path := filepath.Join(dir, "title")
		if err := os.WriteFile(path, []byte(*title+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--title-file", path)
	}
	if description != nil {
		path := filepath.Join(dir, "description")
		if err := os.WriteFile(path, []byte(*description), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--description-file", path)
	}
	for _, label := range add {
		args = append(args, "--add-label", label)
	}
	for _, label := range remove {
		args = append(args, "--remove-label", label)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if format != "toon" {
		args = append(args, "--format", format)
	}
	return args
}

func assertIssueEditNoMutation(t *testing.T, delegate *fakeDelegate) {
	t.Helper()
	if len(delegate.inputBodies) != 0 || len(delegate.inputModes) != 0 || delegate.inputErr != nil {
		t.Fatalf("issue edit constructed private mutation input: bodies=%d modes=%d err=%v", len(delegate.inputBodies), len(delegate.inputModes), delegate.inputErr)
	}
	for _, request := range delegate.requests {
		if request.InputFile != "" {
			t.Fatalf("issue edit sent mutation input: %#v", request)
		}
		switch request.Operation {
		case glab.OpIssueEditProject, glab.OpIssueEditView, glab.OpIssueEditLabelList:
		default:
			t.Fatalf("issue edit reached non-read operation: %#v", request)
		}
	}
}

func stringPointer(value string) *string { return &value }

func timePointer(value time.Time) *time.Time { return &value }
