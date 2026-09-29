package product

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func collaborationCommand(group, leaf string, limit int) []string {
	return []string{group, leaf, "7", "-R", "group/project", "--hostname", "gitlab.com", "--limit", strconv.Itoa(limit), "--format", "json"}
}

func collaborationIssueBody() []byte {
	return []byte(`{"id":7007,"iid":7,"project_id":99,"web_url":"https://gitlab.com/group/project/-/issues/7","updated_at":"2024-02-03T04:05:06Z"}`)
}

func collaborationMRBody(t *testing.T) []byte {
	return mutateDiscussionMRBody(t, func(m map[string]any) {
		m["reviewers"] = []any{map[string]any{"id": 1, "username": "alice", "name": "Alice"}}
	})
}

func collaborationApprovalBody() []byte {
	return []byte(`{"id":7007,"iid":7,"project_id":99,"approved":true,"approvals_required":1,"approvals_left":0,"approved_by":[{"user":{"id":1,"username":"alice","name":"Alice","email":"unapproved-user-field"}}]}`)
}

func collaborationIssuePage(t *testing.T, first, count int) []byte {
	return []byte(strings.ReplaceAll(string(discussionPage(t, first, count)), `"MergeRequest"`, `"Issue"`))
}

func collaborationDelegate(t *testing.T, issue bool, pages ...[]byte) *fakeDelegate {
	t.Helper()
	f := discussionDelegate()
	response := func(body []byte) glab.Response {
		return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
	}
	if issue {
		f.responses[glab.OpIssueEditProject] = f.responses[glab.OpMRDiscussionsTargetProject]
		f.responses[glab.OpIssueEditView] = []glab.Response{response(collaborationIssueBody()), response(collaborationIssueBody())}
		for _, page := range pages {
			f.responses[glab.OpIssueDiscussions] = append(f.responses[glab.OpIssueDiscussions], response(page))
		}
	} else {
		f.responses[glab.OpMRView] = []glab.Response{response(collaborationMRBody(t)), response(collaborationMRBody(t))}
		f.responses[glab.OpMRApprovals] = []glab.Response{response(collaborationApprovalBody())}
	}
	return f
}

func TestCollaborationIssueDiscussionBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                string
		limit, count, pages int
		complete            bool
		reason              string
	}{
		{"empty", 1, 0, 1, true, ""},
		{"exact limit", 1, 1, 1, true, ""},
		{"limit plus probe", 1, 2, 1, false, "display_limit"},
		{"two pages", 101, 101, 2, true, ""},
		{"page cap", 1000, 1000, 10, false, "hard_page_limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pages := [][]byte{}
			width := min(test.limit+1, 100)
			for i := 0; i < test.pages; i++ {
				pages = append(pages, collaborationIssuePage(t, i*width, min(width, test.count-i*width)))
			}
			f := collaborationDelegate(t, true, pages...)
			stdout, stderr, deps := productTestDeps(t, f)
			if code := Run(context.Background(), collaborationCommand("issue", "discussions", test.limit), deps); code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var out struct {
				Data struct {
					Issue       IssueDiscussionIdentity `json:"issue"`
					Discussions []IssueDiscussion       `json:"discussions"`
				} `json:"data"`
				Meta uxv1.Meta `json:"meta"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Meta.Complete != test.complete || out.Meta.Reason != test.reason || out.Meta.Count != min(test.limit, test.count) || out.Data.Issue.ID != 7007 || len(out.Data.Discussions) != min(test.limit, test.count) {
				t.Fatalf("output=%s", stdout.String())
			}
			for _, d := range out.Data.Discussions {
				if d.IssueID != 7007 || d.ProjectID != 99 || d.IssueIID != 7 {
					t.Fatalf("unbound: %#v", d)
				}
			}
			for i, r := range f.requests[2 : 2+test.pages] {
				if r.Operation != glab.OpIssueDiscussions || r.Page != i+1 || r.PerPage != width || r.Host != "gitlab.com" || r.Repo != "group/project" || r.IID != 7 {
					t.Fatalf("request=%#v", r)
				}
			}
			if len(f.requests) != test.pages+4 {
				t.Fatalf("requests=%#v", f.requests)
			}
		})
	}
}

func TestCollaborationIssueDiscussionNestedAndUTF8Bounds(t *testing.T) {
	for _, nested := range []bool{false, true} {
		notes := []any{}
		count := 1
		body := strings.Repeat("é", limits.MaxDescriptionBytes)
		if nested {
			count, body = limits.MaxDiscussionNotes+1, "body"
		}
		for i := 0; i < count; i++ {
			n := discussionTestNote(int64(i+1), body, false, false, false)
			n["noteable_type"] = "Issue"
			notes = append(notes, n)
		}
		page := marshalDiscussionJSON(t, []any{map[string]any{"id": "thread", "individual_note": false, "notes": notes}})
		stdout, _, deps := productTestDeps(t, collaborationDelegate(t, true, page))
		if code := Run(context.Background(), collaborationCommand("issue", "discussions", 30), deps); code != 0 {
			t.Fatalf("exit=%d %s", code, stdout.String())
		}
		var out struct {
			Data struct {
				Discussions []IssueDiscussion `json:"discussions"`
			} `json:"data"`
			Meta uxv1.Meta `json:"meta"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Meta.Complete || !out.Meta.Truncated {
			t.Fatalf("meta=%#v", out.Meta)
		}
		if nested && len(out.Data.Discussions[0].Notes) != limits.MaxDiscussionNotes {
			t.Fatal("nested cap not applied")
		}
		for _, note := range out.Data.Discussions[0].Notes {
			if !utf8.ValidString(note.Body) || len(note.Body) > limits.MaxDescriptionBytes {
				t.Fatal("invalid bounded body")
			}
		}
	}
}

func TestCollaborationApprovalsStatesAndUnknowns(t *testing.T) {
	for _, test := range []struct {
		name                 string
		replace, with, state string
		complete             bool
	}{
		{"approved", `"approved":true`, `"approved":true`, "approved", true},
		{"not approved", `"approved":true`, `"approved":false`, "not_approved", true},
		{"missing", `"approved":true,`, ``, "unknown", false},
		{"null", `"approved":true`, `"approved":null`, "unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := collaborationDelegate(t, false)
			f.responses[glab.OpMRApprovals][0].Body = []byte(strings.Replace(string(collaborationApprovalBody()), test.replace, test.with, 1))
			stdout, _, deps := productTestDeps(t, f)
			if code := Run(context.Background(), collaborationCommand("mr", "approvals", 30), deps); code != 0 {
				t.Fatalf("exit=%d %s", code, stdout.String())
			}
			var out struct {
				Data struct {
					Reviewers MRReviewerAssignments `json:"reviewers"`
					Approvals MRApprovalSummary     `json:"approvals"`
				} `json:"data"`
				Meta uxv1.Meta `json:"meta"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Data.Approvals.State != test.state || out.Data.Approvals.Tier != "unknown" || out.Meta.Complete != test.complete || len(out.Data.Reviewers.Users) != 1 || len(out.Data.Approvals.ApprovedBy) != 1 || strings.Contains(stdout.String(), "unapproved-user-field") {
				t.Fatalf("%s", stdout.String())
			}
		})
	}
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f := collaborationDelegate(t, false)
			err, ok := uxv1.NewHTTPRejection(status)
			if !ok {
				err = uxv1.NewError(uxv1.CodeUpstream, "error")
			}
			f.errors = map[glab.Operation][]error{glab.OpMRApprovals: {err}}
			stdout, _, deps := productTestDeps(t, f)
			code := Run(context.Background(), collaborationCommand("mr", "approvals", 30), deps)
			if status == 403 || status == 404 {
				if code != 0 || !strings.Contains(stdout.String(), `"availability":"unavailable"`) || !strings.Contains(stdout.String(), `"complete":false`) {
					t.Fatalf("exit=%d %s", code, stdout.String())
				}
			} else if code == 0 {
				t.Fatal("provider error swallowed")
			}
		})
	}
}

func TestCollaborationFailClosedIdentityAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name          string
		issue         bool
		op            glab.Operation
		replace, with string
		recheck       bool
	}{
		{"issue host", true, glab.OpIssueEditView, "gitlab.com", "evil.invalid", false},
		{"issue project", true, glab.OpIssueEditView, `"project_id":99`, `"project_id":100`, false},
		{"issue iid", true, glab.OpIssueEditView, `"iid":7`, `"iid":8`, false},
		{"issue recheck", true, glab.OpIssueEditView, `"id":7007`, `"id":7008`, true},
		{"issue project recheck", true, glab.OpIssueEditProject, `"id":99`, `"id":100`, true},
		{"note target", true, glab.OpIssueDiscussions, `"noteable_id":7007`, `"noteable_id":7008`, false},
		{"note project", true, glab.OpIssueDiscussions, `"project_id":99`, `"project_id":100`, false},
		{"note type", true, glab.OpIssueDiscussions, `"Issue"`, `"MergeRequest"`, false},
		{"note iid", true, glab.OpIssueDiscussions, `"noteable_iid":null`, `"noteable_iid":8`, false},
		{"duplicate json", true, glab.OpIssueDiscussions, `"system":false`, `"system":false,"system":true`, false},
		{"unknown resolution", true, glab.OpIssueDiscussions, `"resolvable":false`, `"resolvable":true`, false},
		{"approval id", false, glab.OpMRApprovals, `"id":7007`, `"id":7008`, false},
		{"approval iid", false, glab.OpMRApprovals, `"iid":7`, `"iid":8`, false},
		{"approval project", false, glab.OpMRApprovals, `"project_id":99`, `"project_id":100`, false},
		{"approval bool", false, glab.OpMRApprovals, `"approved":true`, `"approved":"yes"`, false},
		{"approval count", false, glab.OpMRApprovals, `"approvals_left":0`, `"approvals_left":-1`, false},
		{"approval duplicate", false, glab.OpMRApprovals, `"approved":true`, `"approved":false,"approved":true`, false},
		{"mr host", false, glab.OpMRView, "gitlab.com", "evil.invalid", false},
		{"mr project", false, glab.OpMRView, `"target_project_id":99`, `"target_project_id":100`, false},
		{"mr iid", false, glab.OpMRView, `"iid":7`, `"iid":8`, false},
		{"mr head recheck", false, glab.OpMRView, discussionTestHeadSHA, discussionTestBaseSHA, true},
		{"mr base recheck", false, glab.OpMRView, discussionTestBaseSHA, discussionTestHeadSHA, true},
		{"mr reviewers recheck", false, glab.OpMRView, `"username":"alice"`, `"username":"bob"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := collaborationDelegate(t, test.issue, collaborationIssuePage(t, 0, 1))
			i := 0
			if test.recheck {
				i = 1
			}
			body := string(f.responses[test.op][i].Body)
			if !strings.Contains(body, test.replace) {
				t.Fatalf("bad fixture mutation %s in %s", test.replace, body)
			}
			f.responses[test.op][i].Body = []byte(strings.Replace(body, test.replace, test.with, 1))
			// Make the resolvable note's required state truly unavailable.
			if test.name == "unknown resolution" {
				f.responses[test.op][i].Body = []byte(strings.Replace(string(f.responses[test.op][i].Body), `"resolved":false`, `"resolved":null`, 1))
			}
			group, leaf := "mr", "approvals"
			if test.issue {
				group, leaf = "issue", "discussions"
			}
			stdout, _, deps := productTestDeps(t, f)
			if code := Run(context.Background(), collaborationCommand(group, leaf, 30), deps); code == 0 || strings.Contains(stdout.String(), `"data":`) {
				t.Fatalf("accepted: %s", stdout.String())
			}
		})
	}
}

func TestCollaborationCancellationAndByteBudgets(t *testing.T) {
	for _, issue := range []bool{false, true} {
		f := collaborationDelegate(t, issue, collaborationIssuePage(t, 0, 1))
		ctx, cancel := context.WithCancel(context.Background())
		f.doFunc = func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
			if r.Operation == glab.OpMRApprovals || r.Operation == glab.OpIssueDiscussions {
				cancel()
			}
			return glab.Response{}, nil, false
		}
		group, leaf := "mr", "approvals"
		if issue {
			group, leaf = "issue", "discussions"
		}
		stdout, _, deps := productTestDeps(t, f)
		if code := Run(ctx, collaborationCommand(group, leaf, 30), deps); code != 130 {
			t.Fatalf("exit=%d %s", code, stdout.String())
		}
		cancel()
	}
	f := collaborationDelegate(t, true)
	f.doFunc = func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
		if r.Operation == glab.OpIssueDiscussions {
			page := collaborationIssuePage(t, (r.Page-1)*100, 100)
			// Legal JSON whitespace counts against the transport budget.
			page = append(page, []byte(strings.Repeat(" ", limits.MaxJSONPageBytes-len(page)))...)
			return glab.Response{Body: page}, nil, true
		}
		return glab.Response{}, nil, false
	}
	stdout, _, deps := productTestDeps(t, f)
	if code := Run(context.Background(), collaborationCommand("issue", "discussions", 1000), deps); code != 8 {
		t.Fatalf("exit=%d %s", code, stdout.String())
	}
	if len(f.requests) != 6 {
		t.Fatalf("operation budget did not stop on fourth page: %d", len(f.requests))
	}
}
