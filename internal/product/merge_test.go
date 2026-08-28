package product

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	runtimepkg "gl-axi/internal/runtime"
)

const (
	mergeTestHead      = "0123456789abcdef0123456789abcdef01234567"
	mergeTestSquash    = "1111111111111111111111111111111111111111"
	mergeTestCommit    = "2222222222222222222222222222222222222222"
	mergeTestURL       = "https://gitlab.com/group/project/-/merge_requests/42"
	mergeTestSource    = "feature"
	mergeTestTarget    = "main"
	mergeErrorSentinel = "merge-provider-error-sentinel"
)

func TestMRMergePhaseBudgetsReserveReconciliation(t *testing.T) {
	if got := limits.MergePreflightOperation + limits.MergeMutationOperation + limits.MergeReconcileOperation; got != limits.WriteOperation {
		t.Fatalf("merge phase budgets=%s, outer write budget=%s", got, limits.WriteOperation)
	}
}

func TestMRMergeParserDenialsConstructNoDelegate(t *testing.T) {
	base := mergeArgs()
	cases := []struct {
		name string
		args []string
		help bool
	}{
		{name: "help", args: []string{"mr", "merge", "--help"}, help: true},
		{name: "unknown flag", args: appendCopy(base, "--unknown")},
		{name: "duplicate squash", args: appendCopy(base, "--squash")},
		{name: "missing hostname", args: removeFlag(base, "--hostname", true)},
		{name: "missing repo", args: removeFlag(base, "--repo", true)},
		{name: "missing expected URL", args: removeFlag(base, "--expected-url", true)},
		{name: "missing expected source", args: removeFlag(base, "--expected-source", true)},
		{name: "missing expected target", args: removeFlag(base, "--expected-target", true)},
		{name: "missing expected head", args: removeFlag(base, "--expected-head", true)},
		{name: "malformed expected source", args: replaceArg(base, mergeTestSource, "feature..other")},
		{name: "dot-prefixed expected source component", args: replaceArg(base, mergeTestSource, "feature/.hidden")},
		{name: "dash-prefixed expected source", args: replaceArg(base, mergeTestSource, "-release")},
		{name: "malformed expected target", args: replaceArg(base, mergeTestTarget, "main.lock")},
		{name: "standalone at expected target", args: replaceArg(base, mergeTestTarget, "@")},
		{name: "same expected branches", args: replaceArg(base, mergeTestTarget, mergeTestSource)},
		{name: "missing authority", args: removeFlag(base, "--authority", true)},
		{name: "missing squash", args: removeFlag(base, "--squash", false)},
		{name: "noncanonical IID", args: replaceArg(base, "42", "042")},
		{name: "uppercase head", args: replaceArg(base, mergeTestHead, strings.ToUpper(mergeTestHead))},
		{name: "zero head", args: replaceArg(base, mergeTestHead, strings.Repeat("0", 40))},
		{name: "invalid authority", args: replaceArg(base, "captain-explicit", "agent-claimed")},
		{name: "URL userinfo", args: replaceArg(base, mergeTestURL, "https://user@gitlab.com/group/project/-/merge_requests/42")},
		{name: "URL query", args: replaceArg(base, mergeTestURL, mergeTestURL+"?merge=true")},
		{name: "URL fragment", args: replaceArg(base, mergeTestURL, mergeTestURL+"#discussion")},
		{name: "URL host mismatch", args: replaceArg(base, mergeTestURL, "https://evil.example/group/project/-/merge_requests/42")},
		{name: "URL project mismatch", args: replaceArg(base, mergeTestURL, "https://gitlab.com/other/project/-/merge_requests/42")},
		{name: "URL IID mismatch", args: replaceArg(base, mergeTestURL, "https://gitlab.com/group/project/-/merge_requests/43")},
		{name: "newline", args: replaceArg(base, mergeTestHead, mergeTestHead+"\n")},
		{name: "extra positional", args: appendCopy(base, "43")},
		{name: "limit is not accepted", args: appendCopy(base, "--limit", "1")},
		{name: "GitHub alias", args: []string{"pr", "merge", "42"}},
		{name: "workflow alias", args: []string{"workflow", "run", "ci"}},
	}
	for _, flag := range []string{"--merge", "--rebase", "--method", "--auto", "--auto-merge", "--when-pipeline-succeeds", "--delete-branch", "--remove-source-branch", "--message", "--squash-message", "--subject", "--body", "--body-file", "--admin", "--yes", "-s"} {
		cases = append(cases, struct {
			name string
			args []string
			help bool
		}{name: "forbidden " + flag, args: appendCopy(base, flag)})
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			deps := Dependencies{
				Runtime: productRuntimeNoDiscovery(t, &stdout),
				NewDelegate: func() delegateClient {
					t.Fatal("denied merge constructed an official-glab delegate")
					return nil
				},
			}
			code := Run(context.Background(), test.args, deps)
			if test.help {
				if code != 0 || !strings.Contains(stdout.String(), "Immediately squash-merge") || !strings.Contains(stdout.String(), "--expected-source BRANCH") || !strings.Contains(stdout.String(), "--expected-target BRANCH") {
					t.Fatalf("help exit=%d output=%s", code, stdout.String())
				}
				return
			}
			if code == 0 {
				t.Fatalf("denied argv succeeded: %v output=%s", test.args, stdout.String())
			}
		})
	}
}

func TestMRMergeSuccessUsesOnePrivatePUTAndClosedOutput(t *testing.T) {
	open := mergeMRFixture("opened")
	merged := mergeMRFixture("merged")
	delegate := mergeDelegate(open, open, mergeJobFixture("success", false), nil)
	delegate.responses[glab.OpMRMerge] = []glab.Response{mergeResponse(merged)}

	stdout, stderr, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	assertOneMergeMutation(t, delegate)
	wantOperations := []glab.Operation{glab.OpMergeProject, glab.OpMergeMRView, glab.OpMergeJobList, glab.OpMergeBridgeList, glab.OpMergeMRView, glab.OpMRMerge}
	if len(delegate.requests) != len(wantOperations) {
		t.Fatalf("merge request sequence=%#v", delegate.requests)
	}
	for index, operation := range wantOperations {
		if delegate.requests[index].Operation != operation {
			t.Fatalf("request %d operation=%s want=%s sequence=%#v", index, delegate.requests[index].Operation, operation, delegate.requests)
		}
	}
	assertPrivateMergePayload(t, delegate)

	var envelope struct {
		OK   bool        `json:"ok"`
		Data mergeOutput `json:"data"`
		Meta uxv1.Meta   `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	got := envelope.Data.Merge
	if !envelope.OK || got.Action != "merged" || got.IID != 42 || got.WebURL != mergeTestURL || got.SourceBranch != mergeTestSource || got.TargetBranch != mergeTestTarget || got.SourceHeadSHA != mergeTestHead || got.SquashCommitSHA != mergeTestSquash || got.ResultCommitSHA != mergeTestCommit || got.Pipeline.ID != 77 || got.Pipeline.Status != "success" || got.Authority != "captain-explicit" {
		t.Fatalf("unexpected merge output: %#v", envelope)
	}
	if envelope.Meta.Count != 1 || envelope.Meta.Limit != 0 || envelope.Meta.Backend != "official-glab" {
		t.Fatalf("unexpected merge metadata: %#v", envelope.Meta)
	}
}

func TestMRMergeExpectedBranchMismatchNeverPUT(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*upstreamMR)
		wantMessage string
	}{
		{
			name: "source branch changed with the same head",
			mutate: func(record *upstreamMR) {
				record.SourceBranch = "renamed-feature"
			},
			wantMessage: "merge request source branch does not match --expected-source",
		},
		{
			name: "target retargeted with the same head",
			mutate: func(record *upstreamMR) {
				record.TargetBranch = "release"
			},
			wantMessage: "merge request target branch does not match --expected-target",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := mergeMRFixture("opened")
			adjacent := mergeMRFixture("opened")
			test.mutate(&adjacent)
			delegate := mergeDelegate(first, adjacent, mergeJobFixture("success", false), nil)
			stdout, _, deps := productTestDeps(t, delegate)

			if code := Run(context.Background(), mergeArgs(), deps); code != 6 {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			var envelope uxv1.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || envelope.Error.Code != uxv1.CodeConflict || envelope.Error.Message != test.wantMessage {
				t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
			}
			if countOperation(delegate.requests, glab.OpMergeMRView) != 2 || countOperation(delegate.requests, glab.OpMRMerge) != 0 {
				t.Fatalf("branch mismatch crossed the adjacent-read guard: %#v", delegate.requests)
			}
		})
	}
}

func TestMRMergeForceRemovalDefaultIsOverriddenByFixedFalsePayload(t *testing.T) {
	open := mergeMRFixture("opened")
	open.ForceRemoveSourceBranch = true
	merged := mergeMRFixture("merged")
	merged.ForceRemoveSourceBranch = true
	delegate := mergeDelegate(open, open, mergeJobFixture("success", false), nil)
	delegate.responses[glab.OpMRMerge] = []glab.Response{mergeResponse(merged)}

	stdout, stderr, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"action":"merged"`) {
		t.Fatalf("force-only merge did not succeed: %s", stdout.String())
	}
	assertOneMergeMutation(t, delegate)
	assertPrivateMergePayload(t, delegate)
}

func TestMRMergeForceRemovalDefaultAllowsExactReplayWithoutPUT(t *testing.T) {
	merged := mergeMRFixture("merged")
	merged.ForceRemoveSourceBranch = true
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMergeProject:    {mergeResponse(mergeProjectFixture(true, true))},
		glab.OpMergeMRView:     {mergeResponse(merged)},
		glab.OpMergeJobList:    {mergeResponse([]upstreamJob{mergeJobFixture("success", false)})},
		glab.OpMergeBridgeList: {mergeResponse([]upstreamJob{})},
	}}

	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMRMerge) != 0 || !strings.Contains(stdout.String(), `"action":"already_merged"`) {
		t.Fatalf("requests=%#v output=%s", delegate.requests, stdout.String())
	}
}

func TestMRMergeExplicitSourceRemovalPreflightNeverPUT(t *testing.T) {
	open := mergeMRFixture("opened")
	open.ShouldRemoveSourceBranch = true
	delegate := mergeDelegateFirstRead(open)

	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 9 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope uxv1.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Code != uxv1.CodeSafety || envelope.Error.Message != "source-branch removal is outside the guarded merge contract" {
		t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMRMerge) != 0 {
		t.Fatalf("explicit source-removal preflight issued PUT: %#v", delegate.requests)
	}
}

func TestMRMergeExplicitSourceRemovalPostconditionIsNotAccepted(t *testing.T) {
	open := mergeMRFixture("opened")
	merged := mergeMRFixture("merged")
	merged.ShouldRemoveSourceBranch = true
	delegate := mergeDelegate(open, open, mergeJobFixture("success", false), nil)
	delegate.responses[glab.OpMRMerge] = []glab.Response{mergeResponse(merged)}
	delegate.responses[glab.OpMergeMRView] = append(delegate.responses[glab.OpMergeMRView], mergeResponse(merged))

	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 6 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope uxv1.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Code != uxv1.CodeAmbiguousMerge {
		t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMRMerge) != 1 || countOperation(delegate.requests, glab.OpMergeMRView) != 3 {
		t.Fatalf("explicit source-removal postcondition request counts changed: %#v", delegate.requests)
	}
	assertPrivateMergePayload(t, delegate)
}

func TestMRMergeExactReplayReturnsAlreadyMergedWithoutPUT(t *testing.T) {
	merged := mergeMRFixture("merged")
	merged.MergeCommitSHA = ""
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMergeProject:    {mergeResponse(mergeProjectFixture(true, true))},
		glab.OpMergeMRView:     {mergeResponse(merged)},
		glab.OpMergeJobList:    {mergeResponse([]upstreamJob{mergeJobFixture("success", false)})},
		glab.OpMergeBridgeList: {mergeResponse([]upstreamJob{})},
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMRMerge) != 0 || !strings.Contains(stdout.String(), `"action":"already_merged"`) || !strings.Contains(stdout.String(), `"result_commit_sha":"`+mergeTestSquash+`"`) || strings.Contains(stdout.String(), `"merge_commit_sha"`) {
		t.Fatalf("requests=%#v output=%s", delegate.requests, stdout.String())
	}
}

func TestMRMergePreflightDenialsNeverPUT(t *testing.T) {
	tests := []struct {
		name     string
		delegate func() *fakeDelegate
	}{
		{
			name: "project lacks pipeline policy",
			delegate: func() *fakeDelegate {
				return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
					glab.OpMergeProject: {mergeResponse(mergeProjectFixture(false, true))},
				}}
			},
		},
		{
			name: "returned URL mismatch",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.WebURL = "https://gitlab.com/other/project/-/merge_requests/42"
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "fork source project",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.SourceProjectID = 202
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "draft",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.Draft = true
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "conflict",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.HasConflicts = true
				record.DetailedMergeStatus = "cannot_be_merged"
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "approval still required",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.DetailedMergeStatus = "not_approved"
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "unresolved discussion",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.BlockingDiscussionsResolved = false
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "stale head",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.SHA = strings.Repeat("a", 40)
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "existing auto-merge",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.MergeWhenPipelineSucceeds = true
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "source removal requested",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.ShouldRemoveSourceBranch = true
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "no pipeline",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.HeadPipeline = nil
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "running pipeline",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				record.HeadPipeline.Status = "running"
				return mergeDelegateFirstRead(record)
			},
		},
		{
			name: "failed required job",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				return mergeDelegate(record, record, mergeJobFixture("failed", false), nil)
			},
		},
		{
			name: "job pipeline mismatch",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				job := mergeJobFixture("success", false)
				job.Pipeline.ID = 78
				return mergeDelegate(record, record, job, nil)
			},
		},
		{
			name: "unknown job status",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				return mergeDelegate(record, record, mergeJobFixture("provider_future_state", false), nil)
			},
		},
		{
			name: "failed trigger bridge",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("opened")
				return mergeDelegate(record, record, mergeJobFixture("success", false), ptrJob(mergeBridgeFixture("failed", false)))
			},
		},
		{
			name: "second read drift",
			delegate: func() *fakeDelegate {
				first := mergeMRFixture("opened")
				second := mergeMRFixture("opened")
				second.SHA = strings.Repeat("b", 40)
				return mergeDelegate(first, second, mergeJobFixture("success", false), nil)
			},
		},
		{
			name: "preexisting non-squash merge",
			delegate: func() *fakeDelegate {
				record := mergeMRFixture("merged")
				record.Squash = false
				record.SquashCommitSHA = ""
				return mergeDelegateFirstRead(record)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delegate := test.delegate()
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), mergeArgs(), deps); code == 0 {
				t.Fatalf("denial succeeded: output=%s requests=%#v", stdout.String(), delegate.requests)
			}
			if countOperation(delegate.requests, glab.OpMRMerge) != 0 {
				t.Fatalf("preflight denial issued PUT: %#v", delegate.requests)
			}
		})
	}
}

func TestMRMergeIncompletePaginationNeverPUT(t *testing.T) {
	open := mergeMRFixture("opened")
	responses := make([]glab.Response, 0, 10)
	for page := 0; page < 10; page++ {
		items := make([]upstreamJob, 0, mergePageSize)
		for offset := 0; offset < mergePageSize; offset++ {
			item := mergeJobFixture("success", false)
			item.ID = int64(page*mergePageSize + offset + 1)
			item.WebURL = "https://gitlab.com/group/project/-/jobs/" + fmt.Sprint(item.ID)
			items = append(items, item)
		}
		responses = append(responses, mergeResponse(items))
	}
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMergeProject: {mergeResponse(mergeProjectFixture(true, true))},
		glab.OpMergeMRView:  {mergeResponse(open)},
		glab.OpMergeJobList: responses,
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), mergeArgs(), deps); code == 0 {
		t.Fatalf("incomplete pagination succeeded: %s", stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMergeJobList) != 10 || countOperation(delegate.requests, glab.OpMRMerge) != 0 {
		t.Fatalf("requests=%#v", delegate.requests)
	}
}

func TestMRMergeAmbiguityReconcilesWithOneGETAndNeverRetriesPUT(t *testing.T) {
	retargetedMerged := mergeMRFixture("merged")
	retargetedMerged.TargetBranch = "release"
	tests := []struct {
		name       string
		response   glab.Response
		writeErr   error
		reconciled upstreamMR
		wantAction string
		wantCode   uxv1.Code
	}{
		{
			name: "transport then merged", writeErr: uxv1.NewError(uxv1.CodeUpstream, mergeErrorSentinel),
			reconciled: mergeMRFixture("merged"), wantAction: "reconciled_merged",
		},
		{
			name: "malformed success then merged", response: mergeResponse(map[string]any{"malformed": true, "description": mergeErrorSentinel}),
			reconciled: mergeMRFixture("merged"), wantAction: "reconciled_merged",
		},
		{
			name: "transport then retargeted merge remains ambiguous", writeErr: uxv1.NewError(uxv1.CodeUpstream, mergeErrorSentinel),
			reconciled: retargetedMerged, wantCode: uxv1.CodeAmbiguousMerge,
		},
		{
			name: "transport then open remains ambiguous", writeErr: uxv1.NewError(uxv1.CodeUpstream, mergeErrorSentinel),
			reconciled: mergeMRFixture("opened"), wantCode: uxv1.CodeAmbiguousMerge,
		},
		{
			name: "definite conflict then open remains conflict", writeErr: mergeHTTPError(t, 409),
			reconciled: mergeMRFixture("opened"), wantCode: uxv1.CodeConflict,
		},
		{
			name: "definite 406 then open remains conflict", writeErr: &uxv1.Error{Code: uxv1.CodeConflict, Message: "bounded", StatusCode: 406},
			reconciled: mergeMRFixture("opened"), wantCode: uxv1.CodeConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			open := mergeMRFixture("opened")
			delegate := mergeDelegate(open, open, mergeJobFixture("success", false), nil)
			delegate.responses[glab.OpMergeMRView] = append(delegate.responses[glab.OpMergeMRView], mergeResponse(test.reconciled))
			delegate.responses[glab.OpMRMerge] = []glab.Response{test.response}
			if test.writeErr != nil {
				delegate.errors = map[glab.Operation][]error{glab.OpMRMerge: {test.writeErr}}
			}
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), mergeArgs(), deps)
			if strings.Contains(stdout.String(), mergeErrorSentinel) {
				t.Fatalf("raw merge provider error leaked: %s", stdout.String())
			}
			if countOperation(delegate.requests, glab.OpMRMerge) != 1 || countOperation(delegate.requests, glab.OpMergeMRView) != 3 {
				t.Fatalf("mutation/reconciliation counts changed: %#v", delegate.requests)
			}
			if test.wantAction != "" {
				var envelope struct {
					OK   bool        `json:"ok"`
					Data mergeOutput `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				got := envelope.Data.Merge
				if code != 0 || !envelope.OK || got.Action != test.wantAction || got.SourceBranch != mergeTestSource || got.TargetBranch != mergeTestTarget || got.SourceHeadSHA != mergeTestHead {
					t.Fatalf("exit=%d receipt=%#v output=%s", code, got, stdout.String())
				}
				return
			}
			if code != 6 {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			var envelope uxv1.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || envelope.Error.Code != test.wantCode {
				t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
			}
		})
	}
}

func TestMRMergeCallerCancellationAfterPUTIsAmbiguousWithoutReconciliation(t *testing.T) {
	open := mergeMRFixture("opened")
	delegate := mergeDelegate(open, open, mergeJobFixture("success", false), nil)
	parent, cancel := context.WithCancel(context.Background())
	delegate.doFunc = func(_ context.Context, request glab.Request) (glab.Response, error, bool) {
		if request.Operation != glab.OpMRMerge {
			return glab.Response{}, nil, false
		}
		cancel()
		return glab.Response{UpstreamVersion: glab.SupportedVersion, Write: true}, uxv1.Wrap(uxv1.CodeCanceled, "synthetic cancellation", context.Canceled), true
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(parent, mergeArgs(), deps); code != 6 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if countOperation(delegate.requests, glab.OpMRMerge) != 1 || countOperation(delegate.requests, glab.OpMergeMRView) != 2 || !strings.Contains(stdout.String(), `"code":"ambiguous_merge"`) {
		t.Fatalf("requests=%#v output=%s", delegate.requests, stdout.String())
	}
}

func mergeArgs() []string {
	return []string{
		"mr", "merge", "42",
		"--repo", "group/project",
		"--hostname", "gitlab.com",
		"--expected-url", mergeTestURL,
		"--expected-source", mergeTestSource,
		"--expected-target", mergeTestTarget,
		"--expected-head", mergeTestHead,
		"--authority", "captain-explicit",
		"--squash", "--format", "json",
	}
}

func mergeProjectFixture(pipelinePolicy, discussionPolicy bool) mergeProject {
	return mergeProject{
		ID: 101, PathWithNamespace: "group/project", WebURL: "https://gitlab.com/group/project",
		OnlyAllowMergeIfPipelineSucceeds:          pipelinePolicy,
		OnlyAllowMergeIfAllDiscussionsAreResolved: discussionPolicy,
	}
}

func mergeMRFixture(state string) upstreamMR {
	record := upstreamMR{
		ID: 1001, IID: 42, Title: "Guarded merge", State: state, WebURL: mergeTestURL,
		SourceBranch: mergeTestSource, TargetBranch: mergeTestTarget, SourceProjectID: 101, TargetProjectID: 101,
		SHA: mergeTestHead, HasConflicts: false, DetailedMergeStatus: "can_be_merged",
		BlockingDiscussionsResolved: true,
		HeadPipeline: &upstreamPipeline{
			ID: 77, Status: "success", SHA: mergeTestHead,
			WebURL: "https://gitlab.com/group/project/-/pipelines/77",
		},
	}
	if state == "merged" {
		mergedAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
		record.Squash = true
		record.SquashCommitSHA = mergeTestSquash
		record.MergeCommitSHA = mergeTestCommit
		record.MergedAt = &mergedAt
		record.MergeUser = &upstreamUser{Username: "merge-bot"}
	}
	return record
}

func mergeJobFixture(status string, allowFailure bool) upstreamJob {
	return upstreamJob{
		ID: 501, Name: "verify", Stage: "test", Status: status, AllowFailure: allowFailure,
		WebURL:   "https://gitlab.com/group/project/-/jobs/501",
		Pipeline: &upstreamPipeline{ID: 77, SHA: mergeTestHead, Status: "success"},
	}
}

func mergeBridgeFixture(status string, allowFailure bool) upstreamJob {
	item := mergeJobFixture(status, allowFailure)
	item.ID = 601
	item.Name = "downstream"
	item.WebURL = "https://gitlab.com/group/project/-/jobs/601"
	return item
}

func ptrJob(value upstreamJob) *upstreamJob { return &value }

func mergeDelegate(first, second upstreamMR, job upstreamJob, bridge *upstreamJob) *fakeDelegate {
	bridges := []upstreamJob{}
	if bridge != nil {
		bridges = append(bridges, *bridge)
	}
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMergeProject:    {mergeResponse(mergeProjectFixture(true, true))},
		glab.OpMergeMRView:     {mergeResponse(first), mergeResponse(second)},
		glab.OpMergeJobList:    {mergeResponse([]upstreamJob{job})},
		glab.OpMergeBridgeList: {mergeResponse(bridges)},
	}}
}

func mergeDelegateFirstRead(record upstreamMR) *fakeDelegate {
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMergeProject: {mergeResponse(mergeProjectFixture(true, true))},
		glab.OpMergeMRView:  {mergeResponse(record)},
	}}
}

func mergeResponse(value any) glab.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
}

func mergeHTTPError(t *testing.T, status int) error {
	t.Helper()
	err, ok := uxv1.NewHTTPRejection(status)
	if !ok {
		t.Fatalf("status %d is not classified", status)
	}
	return err
}

func assertOneMergeMutation(t *testing.T, delegate *fakeDelegate) {
	t.Helper()
	if got := countOperation(delegate.requests, glab.OpMRMerge); got != 1 {
		t.Fatalf("merge mutations=%d requests=%#v", got, delegate.requests)
	}
}

func countOperation(requests []glab.Request, operation glab.Operation) int {
	count := 0
	for _, request := range requests {
		if request.Operation == operation {
			count++
		}
	}
	return count
}

func assertPrivateMergePayload(t *testing.T, delegate *fakeDelegate) {
	t.Helper()
	if delegate.inputErr != nil || len(delegate.inputBodies) != 1 || len(delegate.inputModes) != 1 {
		t.Fatalf("private inputs bodies=%d modes=%d err=%v", len(delegate.inputBodies), len(delegate.inputModes), delegate.inputErr)
	}
	if delegate.inputModes[0].Perm() != 0o600 {
		t.Fatalf("private input mode=%v", delegate.inputModes[0].Perm())
	}
	var got map[string]any
	if err := json.Unmarshal(delegate.inputBodies[0], &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"sha": mergeTestHead, "squash": true, "should_remove_source_branch": false, "auto_merge": false,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || len(got) != 4 {
		t.Fatalf("payload=%v want=%v", got, want)
	}
	for _, request := range delegate.requests {
		if request.Operation == glab.OpMRMerge {
			if _, err := os.Stat(request.InputFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("private merge input still exists: %v", err)
			}
		}
	}
}

func productRuntimeNoDiscovery(t *testing.T, stdout *bytes.Buffer) runtimepkg.Dependencies {
	t.Helper()
	return runtimepkg.Dependencies{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: io.Discard, Cwd: t.TempDir(),
		LookupEnv: func(string) (string, bool) {
			t.Fatal("denied merge resolved environment or repository context")
			return "", false
		},
	}
}

func appendCopy(base []string, values ...string) []string {
	out := append([]string(nil), base...)
	return append(out, values...)
}

func replaceArg(base []string, old, replacement string) []string {
	out := append([]string(nil), base...)
	for index, value := range out {
		if value == old {
			out[index] = replacement
			return out
		}
	}
	panic("test argument not found: " + old)
}

func removeFlag(base []string, flag string, hasValue bool) []string {
	out := make([]string, 0, len(base))
	for index := 0; index < len(base); index++ {
		if base[index] == flag {
			if hasValue {
				index++
			}
			continue
		}
		out = append(out, base[index])
	}
	return out
}
