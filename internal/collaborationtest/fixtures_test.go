package collaborationtest

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/limits"
)

const (
	discussionTestBaseSHA = "1123456789012345678901234567890123456789"
	discussionTestHeadSHA = "3123456789012345678901234567890123456789"
)

// These synthetic provider records deliberately include fields the public CLI
// must not echo. Their semantics match the versioned collaboration contract;
// the executable tests do not import the implementation's normalization logic.
func collaborationCommand(group, leaf string, limit int) []string {
	return []string{group, leaf, "7", "-R", "group/project", "--hostname", "gitlab.com", "--limit", strconv.Itoa(limit), "--format", "json"}
}

func collaborationIssueBody() []byte {
	return []byte(`{"id":7007,"iid":7,"project_id":99,"web_url":"https://gitlab.com/group/project/-/issues/7","updated_at":"2024-02-03T04:05:06Z"}`)
}

func collaborationMRBody(t *testing.T) []byte {
	t.Helper()
	var mr map[string]any
	body := []byte(`{"id":7007,"iid":7,"project_id":99,"source_project_id":99,"target_project_id":99,"source_branch":"feature","target_branch":"main","base_sha":"` + discussionTestBaseSHA + `","sha":"` + discussionTestHeadSHA + `","web_url":"https://gitlab.com/group/project/-/merge_requests/7","updated_at":"2024-02-03T04:05:06Z"}`)
	if err := json.Unmarshal(body, &mr); err != nil {
		t.Fatal(err)
	}
	mr["reviewers"] = []any{map[string]any{"id": 1, "username": "alice", "name": "Alice"}}
	return marshalDiscussionJSON(t, mr)
}

func collaborationApprovalBody() []byte {
	return []byte(`{"id":7007,"iid":7,"project_id":99,"approved":true,"approvals_required":1,"approvals_left":0,"approved_by":[{"user":{"id":1,"username":"alice","name":"Alice","email":"unapproved-user-field"}}]}`)
}

func discussionProjectBody(id int64, fullPath string) []byte {
	return []byte(`{"id":` + strconv.FormatInt(id, 10) + `,"path_with_namespace":"` + fullPath + `","web_url":"https://gitlab.com/` + fullPath + `"}`)
}

func collaborationIssuePage(t *testing.T, first, count int) []byte {
	t.Helper()
	items := make([]any, 0, count)
	for index := first; index < first+count; index++ {
		items = append(items, map[string]any{
			"id": "thread-" + strconv.Itoa(index), "individual_note": true,
			"notes": []any{issueNote(int64(10000+index), "body "+strconv.Itoa(index))},
		})
	}
	return marshalDiscussionJSON(t, items)
}

func collaborationLongIssuePage(t *testing.T) []byte {
	return marshalDiscussionJSON(t, []any{map[string]any{"id": "long-note", "individual_note": true, "notes": []any{issueNote(1, strings.Repeat("é", limits.MaxDescriptionBytes))}}})
}

func issueNote(id int64, body string) map[string]any {
	return map[string]any{
		"id": id, "type": nil, "body": body,
		"author":     map[string]any{"id": 1, "username": "alice", "name": "Alice", "web_url": "https://untrusted.invalid/alice"},
		"created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:06Z",
		"system": false, "noteable_id": int64(7007), "noteable_type": "Issue",
		"project_id": int64(99), "noteable_iid": nil,
		"resolvable": false, "resolved": false, "resolved_by": nil, "resolved_at": nil,
	}
}

func marshalDiscussionJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
