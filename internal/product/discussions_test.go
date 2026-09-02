package product

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	runtimepkg "gl-axi/internal/runtime"
)

const (
	discussionTestMRID      = int64(7007)
	discussionTestIID       = int64(7)
	discussionTestProjectID = int64(99)
	discussionTestMRURL     = "https://gitlab.com/group/project/-/merge_requests/7"
)

func TestMRDiscussionsNormalizesThreadsNotesResolutionAndPosition(t *testing.T) {
	unresolved := discussionTestNote(101, "first note", false, true, false)
	unresolved["type"] = "DiffNote"
	unresolved["position"] = map[string]any{
		"base_sha":  "1123456789012345678901234567890123456789",
		"start_sha": "2123456789012345678901234567890123456789",
		"head_sha":  "3123456789012345678901234567890123456789",
		"old_path":  "internal/old.go", "new_path": "internal/new.go", "position_type": "text",
		"old_line": 27, "new_line": 29,
		"line_range": map[string]any{
			"start": map[string]any{"line_code": "start-code", "type": "new", "new_line": 29},
			"end":   map[string]any{"line_code": "end-code", "type": "old", "old_line": 30, "new_line": 31},
		},
	}
	reply := discussionTestNote(102, "reply", false, true, false)
	system := discussionTestNote(103, "changed title", true, false, false)
	system["author"] = map[string]any{"id": 2, "username": "system-user", "name": "System User", "web_url": "https://untrusted.invalid/system-user"}
	system["web_url"] = "https://untrusted.invalid/fabricated-note-url"
	system["headers"] = map[string]any{"PRIVATE-TOKEN": "provider-header-sentinel"}
	system["cookies"] = "provider-cookie-sentinel"
	resolved := discussionTestNote(104, "resolved thread", false, true, true)
	resolved["resolved_by"] = map[string]any{"id": 3, "username": "resolver", "name": "Resolver", "web_url": "https://untrusted.invalid/resolver"}
	resolved["resolved_at"] = "2024-01-04T05:06:07Z"

	page := marshalDiscussionJSON(t, []any{
		map[string]any{"id": "thread-a", "individual_note": false, "notes": []any{unresolved, reply}},
		map[string]any{"id": "thread-system", "individual_note": true, "notes": []any{system}},
		map[string]any{"id": "thread-resolved", "individual_note": false, "notes": []any{resolved}},
	})
	delegate := discussionDelegate(page)
	stdout, stderr, deps := productTestDeps(t, delegate)
	code := Run(context.Background(), discussionCommand("json", 30), deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var envelope struct {
		Schema string `json:"schema"`
		OK     bool   `json:"ok"`
		Data   struct {
			MR          MRDiscussionIdentity `json:"mr"`
			Discussions []MRDiscussion       `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != uxv1.Schema || !envelope.OK || envelope.Data.MR != (MRDiscussionIdentity{ID: discussionTestMRID, IID: discussionTestIID, ProjectID: discussionTestProjectID, WebURL: discussionTestMRURL}) {
		t.Fatalf("unexpected envelope identity: %#v", envelope)
	}
	if len(envelope.Data.Discussions) != 3 || envelope.Data.Discussions[0].ID != "thread-a" || len(envelope.Data.Discussions[0].Notes) != 2 || envelope.Data.Discussions[1].ID != "thread-system" || envelope.Data.Discussions[2].ID != "thread-resolved" {
		t.Fatalf("discussion order or grouping changed: %#v", envelope.Data.Discussions)
	}
	first := envelope.Data.Discussions[0].Notes[0]
	if first.ID != 101 || first.Type != "DiffNote" || first.System || !first.Resolvable || first.Resolved || first.Position == nil || first.Position.OldPath != "internal/old.go" || first.Position.NewLine == nil || *first.Position.NewLine != 29 || first.Position.LineRange == nil || first.Position.LineRange.End.OldLine == nil || *first.Position.LineRange.End.OldLine != 30 {
		t.Fatalf("diff note was not normalized: %#v", first)
	}
	if !envelope.Data.Discussions[1].IndividualNote || !envelope.Data.Discussions[1].Notes[0].System || envelope.Data.Discussions[1].Notes[0].Resolvable {
		t.Fatalf("system note was not normalized: %#v", envelope.Data.Discussions[1])
	}
	resolvedNote := envelope.Data.Discussions[2].Notes[0]
	if !resolvedNote.Resolvable || !resolvedNote.Resolved || resolvedNote.ResolvedBy == nil || resolvedNote.ResolvedBy.ID != 3 || resolvedNote.ResolvedAt == nil {
		t.Fatalf("resolved note was not normalized: %#v", resolvedNote)
	}
	if !envelope.Meta.Complete || envelope.Meta.Truncated || envelope.Meta.Count != 3 || envelope.Meta.Limit != 30 || envelope.Meta.Backend != "official-glab" || envelope.Meta.Host != "gitlab.com" || envelope.Meta.Repo != "group/project" || envelope.Meta.UpstreamVersion != glab.SupportedVersion {
		t.Fatalf("unexpected metadata: %#v", envelope.Meta)
	}
	if strings.Contains(stdout.String(), "untrusted.invalid") || strings.Contains(stdout.String(), "provider-header-sentinel") || strings.Contains(stdout.String(), "provider-cookie-sentinel") || strings.Contains(stdout.String(), "noteable_id") || strings.Contains(stdout.String(), "project_id\":99,\"noteable") {
		t.Fatalf("unapproved upstream fields escaped into output: %s", stdout.String())
	}
	assertDiscussionReadRequests(t, delegate.requests, 1, 31)
}

func TestMRDiscussionsPaginationAndDisplayLimitPreserveProviderOrder(t *testing.T) {
	delegate := discussionDelegate(
		discussionPage(t, 1, 100),
		discussionPage(t, 101, 2),
	)
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), discussionCommand("json", 101), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope struct {
		Data struct {
			Discussions []MRDiscussion `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Discussions) != 101 || envelope.Data.Discussions[0].ID != "thread-1" || envelope.Data.Discussions[100].ID != "thread-101" {
		t.Fatalf("provider order changed: first=%q last=%q count=%d", envelope.Data.Discussions[0].ID, envelope.Data.Discussions[len(envelope.Data.Discussions)-1].ID, len(envelope.Data.Discussions))
	}
	if envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Count != 101 || envelope.Meta.Limit != 101 || envelope.Meta.Reason != "display_limit" {
		t.Fatalf("unexpected limit metadata: %#v", envelope.Meta)
	}
	if len(delegate.requests) != 3 || delegate.requests[1].Operation != glab.OpMRDiscussions || delegate.requests[1].Page != 1 || delegate.requests[1].PerPage != 100 || delegate.requests[2].Operation != glab.OpMRDiscussions || delegate.requests[2].Page != 2 || delegate.requests[2].PerPage != 100 {
		t.Fatalf("pagination requests=%#v", delegate.requests)
	}
	assertNoDiscussionWrites(t, delegate.requests)
}

func TestMRDiscussionsBoundsIndividualAndAggregateBodies(t *testing.T) {
	longBody := strings.Repeat("x", limits.MaxDescriptionBytes+4096)
	notes := make([]any, 0, 10)
	for index := 1; index <= 10; index++ {
		notes = append(notes, discussionTestNote(int64(index), longBody, false, false, false))
	}
	page := marshalDiscussionJSON(t, []any{map[string]any{"id": "large-thread", "individual_note": false, "notes": notes}})
	delegate := discussionDelegate(page)
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), discussionCommand("json", 1), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope struct {
		Data struct {
			Discussions []MRDiscussion `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, note := range envelope.Data.Discussions[0].Notes {
		total += len(note.Body)
		if len(note.Body) > limits.MaxDescriptionBytes || !strings.HasSuffix(note.Body, "…[truncated]") {
			t.Fatalf("body was not bounded: bytes=%d suffix=%q", len(note.Body), note.Body[max(0, len(note.Body)-32):])
		}
	}
	if total > limits.MaxDiscussionBodiesBytes || !envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "field_limit" {
		t.Fatalf("total_body=%d metadata=%#v", total, envelope.Meta)
	}
}

func TestMRDiscussionsBoundsPositionMetadata(t *testing.T) {
	note := discussionTestNote(1, "positioned", false, true, false)
	note["type"] = "DiffNote"
	note["position"] = map[string]any{
		"position_type": "text",
		"new_path":      strings.Repeat("p", limits.MaxDiscussionPathBytes+100),
		"new_line":      12,
	}
	page := marshalDiscussionJSON(t, []any{map[string]any{"id": "position-thread", "individual_note": false, "notes": []any{note}}})
	delegate := discussionDelegate(page)
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), discussionCommand("json", 1), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope struct {
		Data struct {
			Discussions []MRDiscussion `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	position := envelope.Data.Discussions[0].Notes[0].Position
	if position == nil || len(position.NewPath) > limits.MaxDiscussionPathBytes || !strings.HasSuffix(position.NewPath, "…[truncated]") || !envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "field_limit" {
		t.Fatalf("position=%#v metadata=%#v", position, envelope.Meta)
	}
}

func TestMRDiscussionsBoundsNestedNoteRecords(t *testing.T) {
	firstNotes := make([]any, 0, 600)
	secondNotes := make([]any, 0, 600)
	for index := 1; index <= 600; index++ {
		firstNotes = append(firstNotes, discussionTestNote(int64(index), "first", false, false, false))
		secondNotes = append(secondNotes, discussionTestNote(int64(1000+index), "second", false, false, false))
	}
	page := marshalDiscussionJSON(t, []any{
		map[string]any{"id": "large-thread-a", "individual_note": false, "notes": firstNotes},
		map[string]any{"id": "large-thread-b", "individual_note": false, "notes": secondNotes},
	})
	delegate := discussionDelegate(page)
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), discussionCommand("json", 2), deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope struct {
		Data struct {
			Discussions []MRDiscussion `json:"discussions"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Discussions) != 2 || len(envelope.Data.Discussions[0].Notes) != 500 || len(envelope.Data.Discussions[1].Notes) != 500 || envelope.Data.Discussions[0].Notes[499].ID != 500 || envelope.Data.Discussions[1].Notes[499].ID != 1500 || envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "nested_record_limit" {
		t.Fatalf("discussion_counts=%v/%v metadata=%#v", len(envelope.Data.Discussions[0].Notes), len(envelope.Data.Discussions[1].Notes), envelope.Meta)
	}
}

func TestMRDiscussionsRejectsMalformedIncompleteAndCrossResourceResponses(t *testing.T) {
	validNote := discussionTestNote(1, "safe", false, false, false)
	tests := []struct {
		name           string
		mrBody         []byte
		discussionBody []byte
		wantCode       uxv1.Code
	}{
		{name: "malformed JSON", discussionBody: []byte(`[{"private-token-sentinel":`), wantCode: uxv1.CodeUpstream},
		{name: "oversized page", discussionBody: append([]byte{'['}, bytes.Repeat([]byte{' '}, limits.MaxJSONPageBytes+1)...), wantCode: uxv1.CodeUpstream},
		{name: "null page", discussionBody: []byte(`null`), wantCode: uxv1.CodeUpstream},
		{name: "duplicate nested field", discussionBody: []byte(`[{"id":"thread","id":"other","individual_note":true,"notes":[]}]`), wantCode: uxv1.CodeUpstream},
		{name: "missing discussion id", discussionBody: marshalDiscussionJSON(t, []any{map[string]any{"individual_note": true, "notes": []any{validNote}}}), wantCode: uxv1.CodeUpstream},
		{name: "missing notes", discussionBody: marshalDiscussionJSON(t, []any{map[string]any{"id": "thread", "individual_note": false}}), wantCode: uxv1.CodeUpstream},
		{name: "missing note flags", discussionBody: marshalDiscussionJSON(t, []any{map[string]any{"id": "thread", "individual_note": true, "notes": []any{map[string]any{"id": 1, "body": "safe"}}}}), wantCode: uxv1.CodeUpstream},
		{name: "missing note resource identity", discussionBody: discussionPageWithMutation(t, validNote, func(note map[string]any) { delete(note, "noteable_id") }), wantCode: uxv1.CodeUpstream},
		{name: "wrong noteable MR", discussionBody: discussionPageWithMutation(t, validNote, func(note map[string]any) { note["noteable_id"] = 8008 }), wantCode: uxv1.CodeSafety},
		{name: "wrong project", discussionBody: discussionPageWithMutation(t, validNote, func(note map[string]any) { note["project_id"] = 100 }), wantCode: uxv1.CodeSafety},
		{name: "wrong IID when supplied", discussionBody: discussionPageWithMutation(t, validNote, func(note map[string]any) { note["noteable_iid"] = 8 }), wantCode: uxv1.CodeSafety},
		{name: "missing MR project identity", mrBody: []byte(`{"id":7007,"iid":7,"web_url":"` + discussionTestMRURL + `"}`), discussionBody: []byte(`[]`), wantCode: uxv1.CodeUpstream},
		{name: "wrong MR identity IID", mrBody: []byte(`{"id":7007,"iid":8,"project_id":99,"target_project_id":99,"web_url":"https://gitlab.com/group/project/-/merge_requests/8"}`), discussionBody: []byte(`[]`), wantCode: uxv1.CodeSafety},
		{name: "wrong MR URL IID", mrBody: []byte(`{"id":7007,"iid":7,"project_id":99,"target_project_id":99,"web_url":"https://gitlab.com/group/project/-/merge_requests/8"}`), discussionBody: []byte(`[]`), wantCode: uxv1.CodeSafety},
		{name: "wrong MR URL repository", mrBody: []byte(`{"id":7007,"iid":7,"project_id":99,"target_project_id":99,"web_url":"https://gitlab.com/other/project/-/merge_requests/7"}`), discussionBody: []byte(`[]`), wantCode: uxv1.CodeSafety},
		{name: "wrong MR URL authority", mrBody: []byte(`{"id":7007,"iid":7,"project_id":99,"target_project_id":99,"web_url":"https://evil.invalid/group/project/-/merge_requests/7"}`), discussionBody: []byte(`[]`), wantCode: uxv1.CodeSafety},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mrBody := test.mrBody
			if mrBody == nil {
				mrBody = discussionMRBody()
			}
			delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
				glab.OpMRView:        {{Body: mrBody, UpstreamVersion: glab.SupportedVersion}},
				glab.OpMRDiscussions: {{Body: test.discussionBody, UpstreamVersion: glab.SupportedVersion}},
			}}
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), discussionCommand("json", 30), deps)
			if got := uxv1.Code(readProductErrorCode(t, stdout.Bytes())); got != test.wantCode || code != uxv1.ExitCode(uxv1.NewError(test.wantCode, "test")) {
				t.Fatalf("exit=%d code=%s output=%s", code, got, stdout.String())
			}
			if strings.Contains(stdout.String(), "private-token-sentinel") || strings.Contains(stdout.String(), "evil.invalid") || strings.Contains(stdout.String(), "8008") {
				t.Fatalf("raw provider data leaked: %s", stdout.String())
			}
			assertNoDiscussionWrites(t, delegate.requests)
		})
	}
}

func TestMRDiscussionsMapsNotFoundAuthenticationAndProviderErrorsWithoutDiagnostics(t *testing.T) {
	secret := strings.Join([]string{"glpat", "discussion", "diagnostic", "sentinel"}, "-")
	tests := []struct {
		name      string
		operation glab.Operation
		err       error
		wantCode  uxv1.Code
		wantExit  int
	}{
		{name: "not found", operation: glab.OpMRView, err: uxv1.Wrap(uxv1.CodeNotFound, "GitLab resource was not found", errors.New(secret)), wantCode: uxv1.CodeNotFound, wantExit: 5},
		{name: "unauthorized", operation: glab.OpMRDiscussions, err: uxv1.Wrap(uxv1.CodeAuthentication, "official glab authentication failed", errors.New(secret)), wantCode: uxv1.CodeAuthentication, wantExit: 3},
		{name: "provider error", operation: glab.OpMRDiscussions, err: uxv1.Wrap(uxv1.CodeUpstream, "official glab operation failed", errors.New(secret)), wantCode: uxv1.CodeUpstream, wantExit: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delegate := discussionDelegate([]byte(`[]`))
			delegate.errors = map[glab.Operation][]error{test.operation: {test.err}}
			if test.operation == glab.OpMRView {
				delegate.responses[glab.OpMRView] = nil
			}
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), discussionCommand("json", 30), deps); code != test.wantExit {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			if got := uxv1.Code(readProductErrorCode(t, stdout.Bytes())); got != test.wantCode || strings.Contains(stdout.String(), secret) {
				t.Fatalf("code=%s output=%s", got, stdout.String())
			}
			assertNoDiscussionWrites(t, delegate.requests)
		})
	}
}

func TestMRDiscussionsRequiresSiblingMRTargetAuthority(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		args     []string
		wantExit int
		wantCode uxv1.Code
		wantHost string
		wantRepo string
	}{
		{name: "no ambient repository", args: []string{"mr", "discussions", "7", "--format", "json"}, wantExit: 2, wantCode: uxv1.CodeValidation},
		{name: "GitHub origin is not GitLab authority", remote: "git@github.com:group/project.git", args: []string{"mr", "discussions", "7", "--format", "json"}, wantExit: 2, wantCode: uxv1.CodeValidation},
		{name: "self-managed origin needs host authority", remote: "git@gitlab.internal:group/project.git", args: []string{"mr", "discussions", "7", "--format", "json"}, wantExit: 9, wantCode: uxv1.CodeSafety},
		{name: "gitlab.com origin supplies target", remote: "git@gitlab.com:group/project.git", args: []string{"mr", "discussions", "7", "--format", "json"}, wantExit: 0, wantHost: "gitlab.com", wantRepo: "group/project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.remote != "" {
				runGit(t, dir, "init")
				runGit(t, dir, "remote", "add", "origin", test.remote)
			}
			delegate := discussionDelegate([]byte(`[]`))
			var stdout, stderr strings.Builder
			deps := Dependencies{
				Runtime:     runtimepkg.Dependencies{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: dir, LookupEnv: func(string) (string, bool) { return "", false }},
				NewDelegate: func() delegateClient { return delegate },
			}
			if code := Run(context.Background(), test.args, deps); code != test.wantExit {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			if test.wantExit != 0 {
				if got := uxv1.Code(readProductErrorCode(t, []byte(stdout.String()))); got != test.wantCode || len(delegate.requests) != 0 {
					t.Fatalf("code=%s requests=%#v output=%s", got, delegate.requests, stdout.String())
				}
				return
			}
			if len(delegate.requests) != 2 || delegate.requests[0].Host != test.wantHost || delegate.requests[0].Repo != test.wantRepo {
				t.Fatalf("requests=%#v", delegate.requests)
			}
		})
	}
}

func TestMRDiscussionMutationShapesFailBeforeTargetOrDelegate(t *testing.T) {
	for _, args := range [][]string{
		{"mr", "discussions", "7", "--resolve"},
		{"mr", "discussions", "7", "--unresolve"},
		{"mr", "discussions", "7", "--reply", "text"},
		{"mr", "discussions", "7", "--body", "text"},
		{"mr", "discussions", "7", "--add-label", "reviewed"},
		{"mr", "resolve", "7"},
		{"mr", "reply", "7"},
		{"mr", "update", "7"},
	} {
		var stdout strings.Builder
		deps := Dependencies{
			Runtime: runtimepkg.Dependencies{
				Stdout: &stdout,
				Stderr: &strings.Builder{},
				LookupEnv: func(string) (string, bool) {
					t.Fatal("mutation shape resolved target environment")
					return "", false
				},
			},
			NewDelegate: func() delegateClient {
				t.Fatal("mutation shape constructed a delegate")
				return nil
			},
		}
		runArgs := append(append([]string(nil), args...), "--format", "json")
		if code := Run(context.Background(), runArgs, deps); code != 2 {
			t.Fatalf("args=%v exit=%d output=%s", runArgs, code, stdout.String())
		}
		if got := uxv1.Code(readProductErrorCode(t, []byte(stdout.String()))); got != uxv1.CodeSecurityBoundary {
			t.Fatalf("args=%v code=%s output=%s", runArgs, got, stdout.String())
		}
	}
}

func TestMRDiscussionsHelpIsVisibleAndSideEffectFree(t *testing.T) {
	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"--help"}, want: []string{"mr", "discussions"}},
		{args: []string{"mr", "--help"}, want: []string{"discussions", "read-only"}},
		{args: []string{"mr", "discussions", "--help"}, want: []string{"glab-axi mr discussions <iid>", "read-only", "limit counts threads", "No reply, resolve, or other mutation", "--hostname HOST", "--limit N", "--format toon|json"}},
	} {
		var stdout strings.Builder
		deps := Dependencies{
			ProgramName: "glab-axi",
			Runtime: runtimepkg.Dependencies{
				Stdout: &stdout,
				Stderr: &strings.Builder{},
				LookupEnv: func(string) (string, bool) {
					t.Fatal("help resolved target environment")
					return "", false
				},
			},
			NewDelegate: func() delegateClient {
				t.Fatal("help constructed a delegate")
				return nil
			},
		}
		if code := Run(context.Background(), test.args, deps); code != 0 {
			t.Fatalf("help %v exit=%d output=%s", test.args, code, stdout.String())
		}
		for _, want := range test.want {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("help %v missing %q: %s", test.args, want, stdout.String())
			}
		}
	}
}

func TestMRDiscussionsJSONAndTOONAreDeterministic(t *testing.T) {
	page := marshalDiscussionJSON(t, []any{map[string]any{
		"id": "thread-a", "individual_note": true,
		"notes": []any{discussionTestNote(101, "hello", false, false, false)},
	}})
	for _, format := range []string{"json", "toon"} {
		t.Run(format, func(t *testing.T) {
			outputs := make([]string, 2)
			for index := range outputs {
				delegate := discussionDelegate(page)
				stdout, stderr, deps := productTestDeps(t, delegate)
				if code := Run(context.Background(), discussionCommand(format, 30), deps); code != 0 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stderr=%s output=%s", code, stderr.String(), stdout.String())
				}
				outputs[index] = stdout.String()
			}
			if outputs[0] != outputs[1] {
				t.Fatalf("nondeterministic %s output:\nfirst=%s\nsecond=%s", format, outputs[0], outputs[1])
			}
			if !strings.Contains(outputs[0], "thread-a") || !strings.Contains(outputs[0], discussionTestMRURL) || strings.Contains(outputs[0], "noteable_id") {
				t.Fatalf("unexpected %s contract: %s", format, outputs[0])
			}
		})
	}
}

func discussionCommand(format string, limit int) []string {
	return []string{"mr", "discussions", strconv.FormatInt(discussionTestIID, 10), "-R", "group/project", "--hostname", "gitlab.com", "--limit", strconv.Itoa(limit), "--format", format}
}

func discussionMRBody() []byte {
	return []byte(`{"id":7007,"iid":7,"project_id":99,"target_project_id":99,"web_url":"` + discussionTestMRURL + `"}`)
}

func discussionDelegate(pages ...[]byte) *fakeDelegate {
	responses := make([]glab.Response, 0, len(pages))
	for _, page := range pages {
		responses = append(responses, glab.Response{Body: page, UpstreamVersion: glab.SupportedVersion})
	}
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMRView:        {{Body: discussionMRBody(), UpstreamVersion: glab.SupportedVersion}},
		glab.OpMRDiscussions: responses,
	}}
}

func discussionTestNote(id int64, body string, system, resolvable, resolved bool) map[string]any {
	return map[string]any{
		"id": id, "type": nil, "body": body,
		"author":     map[string]any{"id": 1, "username": "alice", "name": "Alice", "web_url": "https://untrusted.invalid/alice"},
		"created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:06Z",
		"system": system, "noteable_id": discussionTestMRID, "noteable_type": "MergeRequest",
		"project_id": discussionTestProjectID, "noteable_iid": nil,
		"resolvable": resolvable, "resolved": resolved, "resolved_by": nil, "resolved_at": nil,
	}
}

func discussionPage(t *testing.T, first, count int) []byte {
	t.Helper()
	items := make([]any, 0, count)
	for index := first; index < first+count; index++ {
		items = append(items, map[string]any{
			"id": "thread-" + strconv.Itoa(index), "individual_note": true,
			"notes": []any{discussionTestNote(int64(10000+index), "body "+strconv.Itoa(index), false, false, false)},
		})
	}
	return marshalDiscussionJSON(t, items)
}

func discussionPageWithMutation(t *testing.T, source map[string]any, mutate func(map[string]any)) []byte {
	t.Helper()
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var note map[string]any
	if err := json.Unmarshal(encoded, &note); err != nil {
		t.Fatal(err)
	}
	mutate(note)
	return marshalDiscussionJSON(t, []any{map[string]any{"id": "thread", "individual_note": true, "notes": []any{note}}})
}

func marshalDiscussionJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertDiscussionReadRequests(t *testing.T, requests []glab.Request, pages, firstPerPage int) {
	t.Helper()
	if len(requests) != pages+1 || requests[0].Operation != glab.OpMRView || requests[0].IID != discussionTestIID {
		t.Fatalf("requests=%#v", requests)
	}
	for index, request := range requests[1:] {
		if request.Operation != glab.OpMRDiscussions || request.Host != "gitlab.com" || request.Repo != "group/project" || request.IID != discussionTestIID || request.Page != index+1 || request.PerPage != firstPerPage {
			t.Fatalf("request[%d]=%#v", index+1, request)
		}
	}
	assertNoDiscussionWrites(t, requests)
}

func assertNoDiscussionWrites(t *testing.T, requests []glab.Request) {
	t.Helper()
	for _, request := range requests {
		switch request.Operation {
		case glab.OpMRView, glab.OpMRDiscussions:
		default:
			t.Fatalf("write-capable or unrelated operation invoked: %#v", request)
		}
		if request.InputFile != "" || request.Source != "" || request.Target != "" {
			t.Fatalf("discussion read carried mutation input: %#v", request)
		}
	}
}

func readProductErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v: %s", err, body)
	}
	return envelope.Error.Code
}
