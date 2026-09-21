package product

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func TestBoardIssuesOrderingGuardAndReceipt(t *testing.T) {
	for _, group := range []bool{false, true} {
		args := planArgs(glab.OpBoardIssues, group, 30)
		unguarded := []string{}
		for _, arg := range args {
			if arg != "--allow-ordering-initialization" {
				unguarded = append(unguarded, arg)
			}
		}
		delegate := &fakeDelegate{}
		stdout, _, deps := productTestDeps(t, delegate)
		constructed := false
		deps.NewDelegate = func() delegateClient { constructed = true; return delegate }
		if Run(context.Background(), unguarded, deps) == 0 || constructed || len(delegate.requests) != 0 {
			t.Fatalf("unguarded query: %s", stdout.String())
		}
		out, d := planRun(t, glab.OpBoardIssues, group, 30, planDoc(glab.OpBoardIssues, group, planItem(9, false, false)))
		if !out.OK || len(d.requests) != 1 || !d.requests[0].AllowOrderingInitialization {
			t.Fatalf("guard missing %+v", out)
		}
		var receipt PlanningOrderingReceipt
		if err := json.Unmarshal(out.Data["ordering"], &receipt); err != nil {
			t.Fatal(err)
		}
		wantScope := "project"
		wantPath := planPath
		if group {
			wantScope, wantPath = "group", "team/nested"
		}
		if !receipt.Acknowledged || receipt.ProviderSchema != "GitLab EE 19.3.0" || receipt.Tiers == "" || receipt.Host != "gitlab.com" || receipt.ScopeKind != wantScope || receipt.ScopePath != wantPath || receipt.BoardID != 7 || receipt.ListID != 8 || receipt.Outcome != "may_have_occurred" || receipt.RequestsAttempted != 1 || receipt.Effect != planningOrderingEffect {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
}

func TestBoardOrderingUncertainFailureHasReceiptAndNeverRetries(t *testing.T) {
	for _, code := range []uxv1.Code{uxv1.CodeCanceled, uxv1.CodeUpstream, uxv1.CodeRateLimited, uxv1.CodeForbidden} {
		providerErr := uxv1.NewError(code, "fixture failure")
		providerErr.Retryable = true
		d := &fakeDelegate{errors: map[glab.Operation][]error{glab.OpBoardIssues: {providerErr}}}
		stdout, _, deps := productTestDeps(t, d)
		if Run(context.Background(), planArgs(glab.OpBoardIssues, false, 30), deps) == 0 {
			t.Fatal("failure succeeded")
		}
		var out struct {
			OK    bool `json:"ok"`
			Error struct {
				Code      uxv1.Code `json:"code"`
				Retryable bool      `json:"retryable"`
				Receipt   struct {
					Ordering PlanningOrderingReceipt `json:"board_ordering"`
				} `json:"receipt"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.OK || out.Error.Code != code || out.Error.Retryable || out.Error.Receipt.Ordering.Outcome != "may_have_occurred" || len(d.requests) != 1 || providerErr.Receipt != nil {
			t.Fatalf("failure receipt %s", stdout.String())
		}
	}
}

func TestPlanningMetadataDoesNotOptInOrFetchIssues(t *testing.T) {
	for _, op := range []glab.Operation{glab.OpBoardList, glab.OpBoardView, glab.OpWorkItemFields, glab.OpWorkItemHierarchy} {
		out, d := planRun(t, op, false, 30, planDoc(op, false))
		if !out.OK {
			t.Fatalf("%+v", out)
		}
		for _, r := range d.requests {
			if r.Operation == glab.OpBoardIssues || r.AllowOrderingInitialization {
				t.Fatalf("metadata write %+v", r)
			}
		}
		args := append(planArgs(op, false, 30), "--allow-ordering-initialization")
		if _, err := Parse(args); err == nil {
			t.Fatal("metadata command accepted ordering flag")
		}
	}
	for _, words := range []string{"--allow-ordering-initialization", "sibling positions", "GitLab EE 19.3.0", "Free/Premium/Ultimate"} {
		help, _ := Help([]string{"board", "issues"})
		if !strings.Contains(help, words) {
			t.Fatalf("help omits %q", words)
		}
	}
}
