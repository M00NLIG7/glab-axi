package product

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

type planJSON = map[string]any

const planPath = "team/nested/project"

func planScope(group bool) planJSON {
	path, kind, prefix := planPath, "Project", "/"
	if group {
		path, kind, prefix = "team/nested", "Group", "/groups/"
	}
	return planJSON{"id": "gid://gitlab/" + kind + "/10", "fullPath": path, "webUrl": "https://gitlab.com" + prefix + path}
}
func planBoard(group bool, id int) planJSON {
	path := "/" + planPath
	if group {
		path = "/groups/team/nested"
	}
	return planJSON{"id": fmt.Sprintf("gid://gitlab/Board/%d", id), "name": "Planning", "webUrl": fmt.Sprintf("https://gitlab.com%s/-/boards/%d", path, id), "hideBacklogList": false, "hideClosedList": true}
}
func planConnection(nodes ...any) planJSON {
	if nodes == nil {
		nodes = []any{}
	}
	return planJSON{"nodes": nodes, "pageInfo": planJSON{"hasNextPage": false, "endCursor": nil}}
}
func planColumn(id int) planJSON {
	return planJSON{"id": fmt.Sprintf("gid://gitlab/List/%d", id), "title": "Todo", "listType": "label", "position": 0, "label": planJSON{"id": "gid://gitlab/ProjectLabel/4", "title": "Todo", "color": "#112233"}}
}
func planItem(id int, workItem, group bool) planJSON {
	kind, state, itemType := "Issue", "opened", any(nil)
	if workItem {
		kind, state, itemType = "WorkItem", "OPEN", planJSON{"name": "Issue"}
	}
	path, nsid, prefix := planPath, "gid://gitlab/Namespaces::ProjectNamespace/11", "/"
	var project any = planScope(false)
	if group {
		path, nsid, prefix = "team/nested", "gid://gitlab/Group/10", "/groups/"
		project = nil
		itemType = planJSON{"name": "Epic"}
	}
	return planJSON{"id": fmt.Sprintf("gid://gitlab/%s/%d", kind, id), "iid": fmt.Sprint(id), "title": "Work", "state": state, "webUrl": fmt.Sprintf("https://gitlab.com%s%s/-/work_items/%d", prefix, path, id), "namespace": planJSON{"id": nsid, "fullPath": path}, "project": project, "workItemType": itemType}
}
func planDoc(op glab.Operation, group bool, nodes ...any) planJSON {
	scope := planScope(group)
	data := planJSON{"scope": scope}
	switch op {
	case glab.OpBoardList:
		scope["boards"] = planConnection(nodes...)
	case glab.OpBoardView:
		b := planBoard(group, 7)
		b["lists"] = planConnection(nodes...)
		scope["board"] = b
	case glab.OpBoardIssues:
		b := planBoard(group, 7)
		col := planColumn(8)
		col["issues"] = planConnection(nodes...)
		b["lists"] = planConnection(col)
		scope["board"] = b
	default:
		item := planItem(7, true, group)
		if op == glab.OpWorkItemFields {
			item["widgets"] = []any{planJSON{"type": "DESCRIPTION"}, planJSON{"type": "HIERARCHY"}, planJSON{"type": "CUSTOM_FIELDS"}}
		} else {
			item["widgets"] = []any{planJSON{"type": "HIERARCHY", "hasParent": false, "parent": nil, "hasChildren": len(nodes) > 0, "children": planConnection(nodes...)}}
		}
		ns := item["namespace"].(planJSON)
		data["namespace"] = planJSON{"id": ns["id"], "fullPath": ns["fullPath"], "workItem": item}
	}
	return planJSON{"data": data}
}
func planRoot(doc planJSON) planJSON {
	return doc["data"].(planJSON)["namespace"].(planJSON)["workItem"].(planJSON)
}
func planWidget(doc planJSON) planJSON { return planRoot(doc)["widgets"].([]any)[0].(planJSON) }
func planPage(doc planJSON, op glab.Operation) planJSON {
	d := doc["data"].(planJSON)
	switch op {
	case glab.OpBoardList:
		return d["scope"].(planJSON)["boards"].(planJSON)
	case glab.OpBoardView:
		return d["scope"].(planJSON)["board"].(planJSON)["lists"].(planJSON)
	case glab.OpBoardIssues:
		return d["scope"].(planJSON)["board"].(planJSON)["lists"].(planJSON)["nodes"].([]any)[0].(planJSON)["issues"].(planJSON)
	default:
		return planWidget(doc)["children"].(planJSON)
	}
}
func planMore(doc planJSON, op glab.Operation, cursor string) {
	planPage(doc, op)["pageInfo"] = planJSON{"hasNextPage": true, "endCursor": cursor}
}
func planArgs(op glab.Operation, group bool, limit int) []string {
	paths := map[glab.Operation][]string{glab.OpBoardList: {"board", "list"}, glab.OpBoardView: {"board", "view", "7"}, glab.OpBoardIssues: {"board", "issues", "7", "--list-id", "8", "--allow-ordering-initialization"}, glab.OpWorkItemFields: {"work-item", "fields", "7"}, glab.OpWorkItemHierarchy: {"work-item", "hierarchy", "7"}}
	args := append([]string{}, paths[op]...)
	if group {
		args = append(args, "--group", "team/nested")
	} else {
		args = append(args, "-R", planPath)
	}
	return append(args, "--hostname", "gitlab.com", "--format", "json", "--limit", fmt.Sprint(limit))
}

type planEnvelope struct {
	OK    bool                       `json:"ok"`
	Data  map[string]json.RawMessage `json:"data"`
	Error *uxv1.Error                `json:"error"`
	Meta  uxv1.Meta                  `json:"meta"`
}

func planRun(t *testing.T, op glab.Operation, group bool, limit int, docs ...planJSON) (planEnvelope, *fakeDelegate) {
	t.Helper()
	d := &fakeDelegate{responses: map[glab.Operation][]glab.Response{}}
	for _, doc := range docs {
		body, e := json.Marshal(doc)
		if e != nil {
			t.Fatal(e)
		}
		d.responses[op] = append(d.responses[op], glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion})
	}
	stdout, stderr, deps := productTestDeps(t, d)
	code := Run(context.Background(), planArgs(op, group, limit), deps)
	var envelope planEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v %s", err, stdout)
	}
	if (code == 0) != envelope.OK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	return envelope, d
}
func TestPlanningReadsNativeScopesAndOutcomes(t *testing.T) {
	for _, group := range []bool{false, true} {
		for _, op := range []glab.Operation{glab.OpBoardList, glab.OpBoardView, glab.OpBoardIssues, glab.OpWorkItemFields, glab.OpWorkItemHierarchy} {
			t.Run(fmt.Sprintf("%s/group=%t", op, group), func(t *testing.T) {
				var node any
				switch op {
				case glab.OpBoardList:
					node = planBoard(group, 1)
				case glab.OpBoardView:
					node = planColumn(8)
				case glab.OpBoardIssues:
					node = planItem(9, false, false)
				case glab.OpWorkItemHierarchy:
					node = planItem(9, true, group)
				}
				nodes := []any{}
				if node != nil {
					nodes = append(nodes, node)
				}
				doc := planDoc(op, group, nodes...)
				out, d := planRun(t, op, group, 30, doc)
				if !out.OK || !out.Meta.Complete || out.Meta.Truncated || len(d.requests) != 1 {
					t.Fatalf("out=%+v", out)
				}
				if string(out.Data["visibility"]) != `"authorized_only"` {
					t.Fatal(out.Data)
				}
				var scope PlanningScope
				if err := json.Unmarshal(out.Data["scope"], &scope); err != nil {
					t.Fatal(err)
				}
				if (scope.Kind == "group") != group || (out.Meta.Repo == "") != group {
					t.Fatalf("scope=%+v meta=%+v", scope, out.Meta)
				}
				if op == glab.OpWorkItemFields && string(out.Data["custom_field_definitions"]) != `"not_exposed"` {
					t.Fatal("custom fields mislabeled")
				}
			})
		}
	}
}
func TestPlanningEmptyIsNotUnavailable(t *testing.T) {
	for _, op := range []glab.Operation{glab.OpBoardList, glab.OpBoardView, glab.OpBoardIssues, glab.OpWorkItemHierarchy} {
		out, _ := planRun(t, op, false, 30, planDoc(op, false))
		if !out.OK || !out.Meta.Complete || out.Meta.Count != 0 {
			t.Fatalf("%s %+v", op, out)
		}
		doc := planDoc(op, false)
		planPage(doc, op)["nodes"] = nil
		out, _ = planRun(t, op, false, 30, doc)
		if out.OK || out.Meta.Complete {
			t.Fatalf("null nodes %s %+v", op, out)
		}
	}
}
func TestPlanningFailuresAreTypedAndFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		op     glab.Operation
		mutate func(planJSON)
		code   uxv1.Code
	}{
		{"scope hidden", glab.OpBoardList, func(d planJSON) { d["data"].(planJSON)["scope"] = nil }, uxv1.CodeNotFound},
		{"scope confusion", glab.OpBoardList, func(d planJSON) { d["data"].(planJSON)["scope"].(planJSON)["id"] = "gid://gitlab/Group/10" }, uxv1.CodeSafety},
		{"nested namespace confusion", glab.OpBoardList, func(d planJSON) { d["data"].(planJSON)["scope"].(planJSON)["fullPath"] = "team/project" }, uxv1.CodeSafety},
		{"wrong host", glab.OpBoardList, func(d planJSON) {
			d["data"].(planJSON)["scope"].(planJSON)["webUrl"] = "https://evil.invalid/" + planPath
		}, uxv1.CodeSafety},
		{"board absent", glab.OpBoardView, func(d planJSON) { d["data"].(planJSON)["scope"].(planJSON)["board"] = nil }, uxv1.CodeNotFound},
		{"board ID confusion", glab.OpBoardView, func(d planJSON) {
			d["data"].(planJSON)["scope"].(planJSON)["board"].(planJSON)["id"] = "gid://gitlab/Board/8"
		}, uxv1.CodeSafety},
		{"missing schema", glab.OpBoardList, func(d planJSON) {
			d["errors"] = []any{planJSON{"message": "secret provider text", "extensions": planJSON{"code": "undefinedField"}}}
		}, uxv1.CodeUnsupported},
		{"denied partial data", glab.OpBoardList, func(d planJSON) { d["errors"] = []any{planJSON{"extensions": planJSON{"code": "FORBIDDEN"}}} }, uxv1.CodeForbidden},
		{"null connection", glab.OpBoardList, func(d planJSON) { d["data"].(planJSON)["scope"].(planJSON)["boards"] = nil }, uxv1.CodeUnsupported},
		{"unknown list type", glab.OpBoardView, func(d planJSON) {
			planPage(d, glab.OpBoardView)["nodes"] = []any{planJSON{"id": "gid://gitlab/List/8", "listType": "future"}}
		}, uxv1.CodeUnsupported},
		{"item absent denied or tier gated", glab.OpWorkItemFields, func(d planJSON) { d["data"].(planJSON)["namespace"].(planJSON)["workItem"] = nil }, uxv1.CodeNotFound},
		{"wrong item iid", glab.OpWorkItemFields, func(d planJSON) { planRoot(d)["iid"] = "9" }, uxv1.CodeSafety},
		{"work item project confusion", glab.OpWorkItemFields, func(d planJSON) { planRoot(d)["project"].(planJSON)["id"] = "gid://gitlab/Project/20" }, uxv1.CodeSafety},
		{"missing widget", glab.OpWorkItemHierarchy, func(d planJSON) { planRoot(d)["widgets"] = []any{} }, uxv1.CodeUnsupported},
		{"links are not hierarchy", glab.OpWorkItemHierarchy, func(d planJSON) { planWidget(d)["type"] = "LINKED_ITEMS" }, uxv1.CodeUnsupported},
		{"hidden parent", glab.OpWorkItemHierarchy, func(d planJSON) { planWidget(d)["hasParent"] = true }, uxv1.CodeForbidden},
		{"hidden children", glab.OpWorkItemHierarchy, func(d planJSON) { planWidget(d)["hasChildren"] = true }, uxv1.CodeForbidden},
		{"missing child", glab.OpWorkItemHierarchy, func(d planJSON) {
			planWidget(d)["hasChildren"] = true
			planPage(d, glab.OpWorkItemHierarchy)["nodes"] = []any{nil}
		}, uxv1.CodeSafety},
		{"self cycle", glab.OpWorkItemHierarchy, func(d planJSON) {
			planWidget(d)["hasChildren"] = true
			planPage(d, glab.OpWorkItemHierarchy)["nodes"] = []any{planItem(7, true, false)}
		}, uxv1.CodeSafety},
		{"duplicate child", glab.OpWorkItemHierarchy, func(d planJSON) {
			planWidget(d)["hasChildren"] = true
			planPage(d, glab.OpWorkItemHierarchy)["nodes"] = []any{planItem(8, true, false), planItem(8, true, false)}
		}, uxv1.CodeUpstream},
		{"parent child cycle", glab.OpWorkItemHierarchy, func(d planJSON) {
			w := planWidget(d)
			w["hasParent"] = true
			w["parent"] = planItem(8, true, false)
			w["hasChildren"] = true
			planPage(d, glab.OpWorkItemHierarchy)["nodes"] = []any{planItem(8, true, false)}
		}, uxv1.CodeSafety},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := planDoc(tt.op, false)
			tt.mutate(doc)
			out, _ := planRun(t, tt.op, false, 30, doc)
			if out.OK || out.Meta.Complete || out.Error == nil || out.Error.Code != tt.code || out.Data != nil {
				t.Fatalf("out=%+v err=%+v", out, out.Error)
			}
		})
	}
}
func TestPlanningCursorPaginationAndTruncation(t *testing.T) {
	for _, op := range []glab.Operation{glab.OpBoardList, glab.OpBoardView, glab.OpBoardIssues, glab.OpWorkItemHierarchy} {
		node := func(id int) any {
			switch op {
			case glab.OpBoardList:
				return planBoard(false, id)
			case glab.OpBoardView:
				return planColumn(id)
			default:
				return planItem(id, op == glab.OpWorkItemHierarchy, false)
			}
		}
		a, b := planDoc(op, false, node(20)), planDoc(op, false, node(21))
		planMore(a, op, "cursor-one")
		out, d := planRun(t, op, false, 30, a, b)
		if !out.OK || !out.Meta.Complete || out.Meta.Count != 2 || len(d.requests) != 2 || d.requests[1].Cursor != "cursor-one" || d.requests[1].PerPage != 31 {
			t.Fatalf("%s out=%+v calls=%+v", op, out, d.requests)
		}
		out, d = planRun(t, op, false, 1, a)
		if !out.OK || out.Meta.Complete || !out.Meta.Truncated || out.Meta.Reason != "display_limit" || len(d.requests) != 1 {
			t.Fatalf("%s limit %+v", op, out)
		}
		out, _ = planRun(t, op, false, 1, planDoc(op, false, node(20)))
		if !out.OK || !out.Meta.Complete || out.Meta.Truncated {
			t.Fatalf("exact limit %+v", out)
		}
		b = planDoc(op, false, node(20))
		out, _ = planRun(t, op, false, 30, a, b)
		if out.OK || out.Error.Code != uxv1.CodeUpstream {
			t.Fatalf("duplicate across pages %+v", out)
		}
		b = planDoc(op, false, node(21))
		planMore(b, op, "cursor-one")
		out, _ = planRun(t, op, false, 30, a, b)
		if out.OK {
			t.Fatal("repeated cursor succeeded")
		}
		pages := []planJSON{}
		for i := 1; i <= 10; i++ {
			doc := planDoc(op, false, node(i+20))
			planMore(doc, op, fmt.Sprint(i))
			pages = append(pages, doc)
		}
		out, d = planRun(t, op, false, 1000, pages...)
		if !out.OK || out.Meta.Complete || out.Meta.Reason != "hard_page_limit" || len(d.requests) != 10 {
			t.Fatalf("hard page %+v", out)
		}
	}
	doc := planDoc(glab.OpBoardList, false, planBoard(false, 1))
	planPage(doc, glab.OpBoardList)["nodes"].([]any)[0].(planJSON)["name"] = strings.Repeat("界", 1000)
	out, _ := planRun(t, glab.OpBoardList, false, 30, doc)
	if !out.OK || !out.Meta.Complete || !out.Meta.Truncated || out.Meta.Reason != "field_limit" {
		t.Fatalf("field %+v", out)
	}
	out, _ = planRun(t, glab.OpWorkItemFields, false, 2, planDoc(glab.OpWorkItemFields, false))
	if !out.OK || out.Meta.Complete || out.Meta.Count != 2 || out.Meta.Reason != "display_limit" {
		t.Fatalf("fields %+v", out)
	}
}
func TestPlanningMalformedInputDoesNoDependencyWork(t *testing.T) {
	for _, args := range [][]string{
		{"board", "list"}, {"board", "view", "0", "-R", planPath}, {"board", "view", "01", "-R", planPath},
		{"board", "issues", "7", "-R", planPath}, {"board", "issues", "7", "--list-id", "-1", "-R", planPath},
		{"board", "list", "--group", "../group"}, {"board", "list", "--group", "10", "-R", planPath},
		{"work-item", "hierarchy", "7", "--group", "group%2Fsub"}, {"work-item", "hierarchy", "7", "-R", "group"},
		{"work-item", "fields", "7", "-R", planPath, "--field", "raw"}, {"board", "list", "-R", planPath, "--closed"},
		{"board", "list", "-R", planPath, "--hostname", "evil/path"}, {"board", "issues", "7", "--list-id", "1", "-R", planPath, "--query", "raw"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			d := &fakeDelegate{}
			stdout, _, deps := productTestDeps(t, d)
			made := false
			deps.NewDelegate = func() delegateClient { made = true; return d }
			if Run(context.Background(), append(args, "--format=json"), deps) == 0 || made || len(d.requests) != 0 {
				t.Fatalf("work occurred: %s", stdout)
			}
		})
	}
}
func TestPlanningCancelTimeoutAndByteBounds(t *testing.T) {
	d := &fakeDelegate{doFunc: func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded read")
		}
		<-ctx.Done()
		return glab.Response{}, uxv1.Wrap(uxv1.CodeCanceled, "canceled", ctx.Err()), true
	}}
	stdout, _, deps := productTestDeps(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if Run(ctx, planArgs(glab.OpBoardList, false, 30), deps) != 130 || !strings.Contains(stdout.String(), `"canceled"`) {
		t.Fatalf("cancel %s", stdout)
	}
	d = &fakeDelegate{doFunc: func(context.Context, glab.Request) (glab.Response, error, bool) {
		return glab.Response{Body: []byte(strings.Repeat(" ", limits.MaxJSONPageBytes+1))}, nil, true
	}}
	stdout, _, deps = productTestDeps(t, d)
	if Run(context.Background(), planArgs(glab.OpBoardList, false, 30), deps) != 8 {
		t.Fatalf("byte bound %s", stdout)
	}
}
