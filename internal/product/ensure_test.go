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
