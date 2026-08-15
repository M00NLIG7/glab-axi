package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/delegate/glab"
)

func TestMREnsureReplayAndDuplicateDenialMakeNoWrite(t *testing.T) {
	for _, test := range []struct {
		name     string
		matches  []upstreamMR
		wantCode int
	}{
		{"replay", []upstreamMR{ensureMR(7, "wanted", "body")}, 0},
		{"duplicates", []upstreamMR{ensureMR(7, "wanted", "body"), ensureMR(8, "wanted", "body")}, 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			delegate := ensureDelegate(t, test.matches)
			stdout, _, deps := productTestDeps(t, delegate)
			args := ensureArgs(t, "wanted", "body")
			if code := Run(context.Background(), args, deps); code != test.wantCode {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			for _, request := range delegate.requests {
				if request.Operation == glab.OpEnsureCreate || request.Operation == glab.OpEnsureUpdate {
					t.Fatalf("%s performed write %#v", test.name, request)
				}
			}
			if test.name == "replay" && !strings.Contains(stdout.String(), `"action":"unchanged"`) {
				t.Fatalf("replay output=%s", stdout.String())
			}
		})
	}
}

func TestMREnsureCreatesOnceAfterEmptyRecheck(t *testing.T) {
	desired := ensureMR(9, "wanted", "body")
	empty, _ := json.Marshal([]upstreamMR{})
	created, _ := json.Marshal(desired)
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureList: {
			{Body: empty, UpstreamVersion: glab.SupportedVersion},
			{Body: empty, UpstreamVersion: glab.SupportedVersion},
		},
		glab.OpEnsureCreate: {{Body: created, UpstreamVersion: glab.SupportedVersion}},
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"action":"created"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	writes := 0
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureCreate {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("writes=%d requests=%#v", writes, delegate.requests)
	}
}

func TestMREnsureRecheckObservesConcurrentCreateWithoutPost(t *testing.T) {
	desired := ensureMR(10, "wanted", "body")
	empty, _ := json.Marshal([]upstreamMR{})
	found, _ := json.Marshal([]upstreamMR{desired})
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureList: {
			{Body: empty, UpstreamVersion: glab.SupportedVersion},
			{Body: found, UpstreamVersion: glab.SupportedVersion},
		},
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureCreate || request.Operation == glab.OpEnsureUpdate {
			t.Fatalf("recheck performed a write: %#v", request)
		}
	}
}

func TestMREnsureReconcilesAmbiguousCreateWithoutSecondMutation(t *testing.T) {
	desired := ensureMR(9, "wanted", "body")
	empty, _ := json.Marshal([]upstreamMR{})
	reconciled, _ := json.Marshal([]upstreamMR{desired})
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{
		responses: map[glab.Operation][]glab.Response{
			glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
			glab.OpEnsureList: {
				{Body: empty, UpstreamVersion: glab.SupportedVersion},
				{Body: empty, UpstreamVersion: glab.SupportedVersion},
				{Body: reconciled, UpstreamVersion: glab.SupportedVersion},
			},
		},
		errors: map[glab.Operation][]error{glab.OpEnsureCreate: {uxv1.NewError(uxv1.CodeUpstream, "synthetic ambiguous transport")}},
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	writes := 0
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureCreate {
			writes++
		}
		if request.Operation == glab.OpEnsureUpdate {
			t.Fatal("create reconciliation sent an update")
		}
	}
	if writes != 1 || !strings.Contains(stdout.String(), `"action":"reconciled_create"`) {
		t.Fatalf("writes=%d output=%s requests=%#v", writes, stdout.String(), delegate.requests)
	}
	assertPrivateEnsurePayload(t, delegate, map[string]any{"source_branch": "feature", "target_branch": "main", "title": "wanted", "description": "body"})
}

func TestMREnsureReturnsClassifiedCreateRejectionAfterEmptyReconciliation(t *testing.T) {
	for _, test := range []struct {
		status   int
		wantCode uxv1.Code
		wantExit int
	}{
		{status: 400, wantCode: uxv1.CodeValidation, wantExit: 2},
		{status: 403, wantCode: uxv1.CodeForbidden, wantExit: 4},
		{status: 422, wantCode: uxv1.CodeValidation, wantExit: 2},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			empty, _ := json.Marshal([]upstreamMR{})
			project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
			rawChildOutput := fmt.Sprintf("provider-child-output-%d-sentinel", test.status)
			createErr, ok := uxv1.NewHTTPRejection(test.status)
			if !ok {
				t.Fatal("test status is not classified")
			}
			// A delegate must never put child output in Message, but make the
			// product boundary robust even against that unsafe implementation.
			createErr.Message = rawChildOutput
			createErr.Cause = errors.New(rawChildOutput)
			delegate := &fakeDelegate{
				responses: map[glab.Operation][]glab.Response{
					glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
					glab.OpEnsureList: {
						{Body: empty, UpstreamVersion: glab.SupportedVersion},
						{Body: empty, UpstreamVersion: glab.SupportedVersion},
						{Body: empty, UpstreamVersion: glab.SupportedVersion},
					},
				},
				errors: map[glab.Operation][]error{glab.OpEnsureCreate: {createErr}},
			}
			stdout, stderr, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != test.wantExit {
				t.Fatalf("exit=%d want=%d output=%s", code, test.wantExit, stdout.String())
			}
			var envelope uxv1.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || envelope.Error.Code != test.wantCode || envelope.Error.Code == uxv1.CodeAmbiguousCreate {
				t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
			}
			if strings.Contains(stdout.String(), rawChildOutput) || strings.Contains(stderr.String(), rawChildOutput) {
				t.Fatalf("raw child output leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			assertCreateFollowedByOneGET(t, delegate)
		})
	}
}

func TestMREnsureKeepsUncertainCreateFailuresAmbiguousAfterEmptyReconciliation(t *testing.T) {
	for _, test := range []struct {
		name     string
		response glab.Response
		err      error
		sentinel string
	}{
		{
			name:     "transport failure",
			err:      uxv1.Wrap(uxv1.CodeUpstream, "official glab operation failed", errors.New("transport-child-output-sentinel")),
			sentinel: "transport-child-output-sentinel",
		},
		{
			name:     "unverified child classification",
			err:      uxv1.Wrap(uxv1.CodeForbidden, "official glab operation was forbidden", errors.New("unverified-child-output-sentinel")),
			sentinel: "unverified-child-output-sentinel",
		},
		{
			name:     "malformed successful output",
			response: glab.Response{Body: []byte(`{"provider-malformed-success-sentinel":true}`), UpstreamVersion: glab.SupportedVersion},
			sentinel: "provider-malformed-success-sentinel",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			empty, _ := json.Marshal([]upstreamMR{})
			project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
			delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
				glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
				glab.OpEnsureList: {
					{Body: empty, UpstreamVersion: glab.SupportedVersion},
					{Body: empty, UpstreamVersion: glab.SupportedVersion},
					{Body: empty, UpstreamVersion: glab.SupportedVersion},
				},
				glab.OpEnsureCreate: {test.response},
			}}
			if test.err != nil {
				delegate.errors = map[glab.Operation][]error{glab.OpEnsureCreate: {test.err}}
			}
			stdout, stderr, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != 6 {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			var envelope uxv1.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error == nil || envelope.Error.Code != uxv1.CodeAmbiguousCreate {
				t.Fatalf("error=%#v output=%s", envelope.Error, stdout.String())
			}
			if strings.Contains(stdout.String(), test.sentinel) || strings.Contains(stderr.String(), test.sentinel) {
				t.Fatalf("raw child output leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			assertCreateFollowedByOneGET(t, delegate)
		})
	}
}

func TestMREnsureReconcilesMalformedSuccessfulMutationResponse(t *testing.T) {
	desired := ensureMR(10, "wanted", "body")
	empty, _ := json.Marshal([]upstreamMR{})
	reconciled, _ := json.Marshal([]upstreamMR{desired})
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureList: {
			{Body: empty, UpstreamVersion: glab.SupportedVersion},
			{Body: empty, UpstreamVersion: glab.SupportedVersion},
			{Body: reconciled, UpstreamVersion: glab.SupportedVersion},
		},
		glab.OpEnsureCreate: {{Body: []byte(`{"unexpected":true}`), UpstreamVersion: glab.SupportedVersion}},
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "wanted", "body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"action":"reconciled_create"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	writes := 0
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureCreate {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("writes=%d requests=%#v", writes, delegate.requests)
	}
}

func TestMREnsureReconcilesAmbiguousUpdateWithoutSecondMutation(t *testing.T) {
	existing := ensureMR(11, "old", "old body")
	desired := ensureMR(11, "new", "new body")
	initial, _ := json.Marshal([]upstreamMR{existing})
	reconciled, _ := json.Marshal([]upstreamMR{desired})
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{
		responses: map[glab.Operation][]glab.Response{
			glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
			glab.OpEnsureList: {
				{Body: initial, UpstreamVersion: glab.SupportedVersion},
				{Body: reconciled, UpstreamVersion: glab.SupportedVersion},
			},
		},
		errors: map[glab.Operation][]error{glab.OpEnsureUpdate: {uxv1.NewError(uxv1.CodeUpstream, "synthetic ambiguous transport")}},
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "new", "new body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	writes := 0
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureUpdate {
			writes++
		}
		if request.Operation == glab.OpEnsureCreate {
			t.Fatal("update reconciliation sent a create")
		}
	}
	if writes != 1 || !strings.Contains(stdout.String(), `"action":"reconciled_update"`) {
		t.Fatalf("writes=%d output=%s requests=%#v", writes, stdout.String(), delegate.requests)
	}
}

func TestMREnsureUpdateUsesOnePrivateTypedPayload(t *testing.T) {
	existing := ensureMR(11, "old", "old body")
	updated := ensureMR(11, "new", "new body")
	matchBody, _ := json.Marshal([]upstreamMR{existing})
	updatedBody, _ := json.Marshal(updated)
	project := []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpEnsureProject: {{Body: project, UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureList:    {{Body: matchBody, UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureUpdate:  {{Body: updatedBody, UpstreamVersion: glab.SupportedVersion}},
	}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), ensureArgs(t, "new", "new body"), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	writes := 0
	for _, request := range delegate.requests {
		if request.Operation == glab.OpEnsureUpdate {
			writes++
			if request.IID != 11 {
				t.Fatalf("updated IID=%d", request.IID)
			}
		}
	}
	if writes != 1 || !strings.Contains(stdout.String(), `"action":"updated"`) {
		t.Fatalf("writes=%d output=%s", writes, stdout.String())
	}
	assertPrivateEnsurePayload(t, delegate, map[string]any{"title": "new", "description": "new body"})
}

func assertCreateFollowedByOneGET(t *testing.T, delegate *fakeDelegate) {
	t.Helper()
	creates := 0
	createIndex := -1
	for index, request := range delegate.requests {
		switch request.Operation {
		case glab.OpEnsureCreate:
			creates++
			createIndex = index
		case glab.OpEnsureUpdate:
			t.Fatalf("create path sent an update: requests=%#v", delegate.requests)
		}
	}
	if creates != 1 || createIndex+1 >= len(delegate.requests) || delegate.requests[createIndex+1].Operation != glab.OpEnsureList {
		t.Fatalf("create was not followed by exactly one GET reconciliation: requests=%#v", delegate.requests)
	}
	for _, request := range delegate.requests[createIndex+1:] {
		if request.Operation == glab.OpEnsureCreate {
			t.Fatalf("create path retried POST: requests=%#v", delegate.requests)
		}
	}
}

func ensureDelegate(t *testing.T, matches []upstreamMR) *fakeDelegate {
	t.Helper()
	body, err := json.Marshal(matches)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpEnsureProject: {{Body: []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`), UpstreamVersion: glab.SupportedVersion}},
		glab.OpEnsureList:    {{Body: body, UpstreamVersion: glab.SupportedVersion}},
	}}
}

func ensureMR(iid int64, title, description string) upstreamMR {
	return upstreamMR{
		ID: 1000 + iid, IID: iid, Title: title, Description: description, State: "opened",
		WebURL:       "https://gitlab.com/group/project/-/merge_requests/" + fmt.Sprint(iid),
		SourceBranch: "feature", TargetBranch: "main", SourceProjectID: 101, TargetProjectID: 101,
		SHA: "0123456789012345678901234567890123456789", DetailedMergeStatus: "checking",
	}
}

func ensureArgs(t *testing.T, title, description string) []string {
	t.Helper()
	dir := t.TempDir()
	titlePath := filepath.Join(dir, "title")
	descriptionPath := filepath.Join(dir, "description")
	if err := os.WriteFile(titlePath, []byte(title+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptionPath, []byte(description), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"mr", "ensure", "--source", "feature", "--target", "main", "--title-file", titlePath, "--description-file", descriptionPath, "-R", "group/project", "--hostname", "gitlab.com", "--format", "json"}
}

func assertPrivateEnsurePayload(t *testing.T, delegate *fakeDelegate, want map[string]any) {
	t.Helper()
	if delegate.inputErr != nil || len(delegate.inputBodies) != 1 || len(delegate.inputModes) != 1 {
		t.Fatalf("private inputs bodies=%d modes=%d err=%v", len(delegate.inputBodies), len(delegate.inputModes), delegate.inputErr)
	}
	if delegate.inputModes[0].Perm() != 0o600 {
		t.Fatalf("input mode=%v", delegate.inputModes[0].Perm())
	}
	var got map[string]any
	if err := json.Unmarshal(delegate.inputBodies[0], &got); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("payload=%v want=%v", got, want)
	}
	for _, request := range delegate.requests {
		if request.InputFile != "" {
			if _, err := os.Stat(request.InputFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("private input still exists: %v", err)
			}
		}
	}
}
