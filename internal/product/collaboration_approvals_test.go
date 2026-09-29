package product

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/delegate/glab"
)

func TestCollaborationReviewerAvailabilityAndLimits(t *testing.T) {
	for _, reviewers := range []string{`[]`, `null`, `[{"id":1,"username":"alice"},{"id":2,"username":"bob"}]`} {
		f := collaborationDelegate(t, false)
		body := mutateDiscussionMRBody(t, func(m map[string]any) {
			var users any
			if err := json.Unmarshal([]byte(reviewers), &users); err != nil {
				t.Fatal(err)
			}
			m["reviewers"] = users
		})
		for i := range f.responses[glab.OpMRView] {
			f.responses[glab.OpMRView][i].Body = body
		}
		stdout, _, deps := productTestDeps(t, f)
		if code := Run(context.Background(), collaborationCommand("mr", "approvals", 1), deps); code != 0 {
			t.Fatalf("exit=%d %s", code, stdout.String())
		}
		if reviewers == `null` && !strings.Contains(stdout.String(), `"reason":"state_unknown"`) {
			t.Fatalf("%s", stdout.String())
		}
		if strings.Contains(reviewers, `"bob"`) && (!strings.Contains(stdout.String(), `"truncated":true`) || strings.Contains(stdout.String(), `"username":"bob"`)) {
			t.Fatalf("limit not applied: %s", stdout.String())
		}
	}
	// A changed user beyond the display prefix must still conflict.
	f := collaborationDelegate(t, false)
	for i := range f.responses[glab.OpMRView] {
		f.responses[glab.OpMRView][i].Body = mutateDiscussionMRBody(t, func(m map[string]any) {
			m["reviewers"] = []any{map[string]any{"id": 1, "username": "alice"}, map[string]any{"id": i + 2, "username": "bob"}}
		})
	}
	stdout, _, deps := productTestDeps(t, f)
	if code := Run(context.Background(), collaborationCommand("mr", "approvals", 1), deps); code != 6 {
		t.Fatalf("exit=%d %s", code, stdout.String())
	}
}

func TestCollaborationApprovalUsersValidatedBeforeTrimming(t *testing.T) {
	for _, users := range []string{
		`[{"user":{"id":1,"username":"alice"}},{"user":{"id":2,"username":"bob"}}]`,
		`[{"user":{"id":1,"username":"alice"}},{"user":{"id":1,"username":"alice"}}]`,
		`[{"user":{"id":1,"username":"alice"}},{"user":null}]`,
	} {
		f := collaborationDelegate(t, false)
		f.responses[glab.OpMRApprovals][0].Body = []byte(`{"id":7007,"iid":7,"project_id":99,"approved":true,"approved_by":` + users + `}`)
		stdout, _, deps := productTestDeps(t, f)
		code := Run(context.Background(), collaborationCommand("mr", "approvals", 1), deps)
		if strings.Contains(users, `"bob"`) {
			if code != 0 || !strings.Contains(stdout.String(), `"truncated":true`) || strings.Contains(stdout.String(), `"username":"bob"`) {
				t.Fatalf("exit=%d %s", code, stdout.String())
			}
		} else if code != 8 {
			t.Fatalf("invalid unselected record accepted: %s", stdout.String())
		}
	}
}

func TestCollaborationApprovalForkIdentity(t *testing.T) {
	for _, wrongSource := range []bool{false, true} {
		f := collaborationDelegate(t, false)
		body := mutateDiscussionMRBody(t, func(m map[string]any) { m["source_project_id"] = 199; m["reviewers"] = []any{} })
		for i := range f.responses[glab.OpMRView] {
			f.responses[glab.OpMRView][i].Body = body
		}
		source := discussionProjectBody(199, "fork/project")
		f.responses[glab.OpMRDiscussionsSourceProject] = []glab.Response{{Body: source}, {Body: source}}
		if wrongSource {
			f.responses[glab.OpMRDiscussionsSourceProject][1].Body = discussionProjectBody(200, "fork/project")
		}
		stdout, _, deps := productTestDeps(t, f)
		code := Run(context.Background(), collaborationCommand("mr", "approvals", 30), deps)
		if wrongSource {
			if code != 9 {
				t.Fatalf("exit=%d %s", code, stdout.String())
			}
		} else if code != 0 || !strings.Contains(stdout.String(), `"same_project":false`) || !strings.Contains(stdout.String(), `"full_path":"fork/project"`) {
			t.Fatalf("exit=%d %s", code, stdout.String())
		}
		for _, r := range f.requests {
			if r.Operation == glab.OpMRDiscussionsSourceProject && r.ID != 199 {
				t.Fatalf("unbound source route: %#v", r)
			}
		}
	}
}

func TestCollaborationApprovalCountPreservesEnvelopeMaximum(t *testing.T) {
	f := collaborationDelegate(t, false)
	users, approvers := []any{}, []any{}
	for i := 1; i <= 1001; i++ {
		user := map[string]any{"id": i, "username": "user" + strconv.Itoa(i)}
		users = append(users, user)
		approvers = append(approvers, map[string]any{"user": user})
	}
	mr := mutateDiscussionMRBody(t, func(m map[string]any) { m["reviewers"] = users })
	for i := range f.responses[glab.OpMRView] {
		f.responses[glab.OpMRView][i].Body = mr
	}
	f.responses[glab.OpMRApprovals][0].Body = marshalDiscussionJSON(t, map[string]any{"id": 7007, "iid": 7, "project_id": 99, "approved": true, "approved_by": approvers})
	stdout, _, deps := productTestDeps(t, f)
	if code := Run(context.Background(), collaborationCommand("mr", "approvals", 1000), deps); code != 0 {
		t.Fatalf("exit=%d %s", code, stdout.String())
	}
	var out struct {
		Data struct {
			Reviewers MRReviewerAssignments `json:"reviewers"`
			Approvals MRApprovalSummary     `json:"approvals"`
		} `json:"data"`
		Meta struct {
			Count     int  `json:"count"`
			Complete  bool `json:"complete"`
			Truncated bool `json:"truncated"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Meta.Count != 1000 || out.Meta.Complete || !out.Meta.Truncated || len(out.Data.Reviewers.Users) != 1000 || len(out.Data.Approvals.ApprovedBy) != 1000 {
		t.Fatal("independent bounds or envelope count violated")
	}
}

func TestCollaborationIssueDuplicateAcrossPagesAndPageOverflow(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		pages := [][]byte{collaborationIssuePage(t, 0, 101)}
		if duplicate {
			pages = [][]byte{collaborationIssuePage(t, 0, 100), collaborationIssuePage(t, 0, 1)}
		}
		stdout, _, deps := productTestDeps(t, collaborationDelegate(t, true, pages...))
		if code := Run(context.Background(), collaborationCommand("issue", "discussions", 101), deps); code != 8 {
			t.Fatalf("exit=%d %s", code, stdout.String())
		}
	}
}
