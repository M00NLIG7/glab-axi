package glab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The snapshot is a separate consumer/provider fixture. Updating it is an
// explicit contract change; ordinary tests never write source or fixtures.
func TestPlanningQueriesMatchPinnedFixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "contracts", "gitlab-planning", "v19.3.0")
	body, err := os.ReadFile(filepath.Join(root, "queries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]string
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, op := range []Operation{OpBoardList, OpBoardView, OpBoardIssues, OpWorkItemFields, OpWorkItemHierarchy} {
		for _, group := range []bool{false, true} {
			kind := "project"
			if group {
				kind = "group"
			}
			query, err := planningQuery(op, group)
			if err != nil {
				t.Fatal(err)
			}
			if fixture[string(op)+"/"+kind] != query {
				t.Fatalf("%s/%s differs from pinned provider query", op, kind)
			}
			if !strings.HasPrefix(query, "query PlanningRead(") || strings.Contains(query, "mutation") || strings.Contains(query, "__schema") {
				t.Fatal("query authority widened")
			}
		}
	}
	if len(fixture) != 10 {
		t.Fatalf("unexpected query inventory: %d", len(fixture))
	}
}
func TestPlanningBuilderOnlyTypedVariables(t *testing.T) {
	for _, op := range []Operation{OpBoardList, OpBoardView, OpBoardIssues, OpWorkItemFields, OpWorkItemHierarchy} {
		for _, group := range []bool{false, true} {
			r := Request{Operation: op, Host: "gitlab.example.invalid", Repo: "team/nested/project", ID: 7, IID: 8, ListID: 9, Page: 2, PerPage: 31, Cursor: "@cursor:=value"}
			r.AllowOrderingInitialization = op == OpBoardIssues
			if group {
				r.Repo = ""
				r.Group = "team/nested"
			}
			inv, err := build(r)
			if err != nil {
				t.Fatal(err)
			}
			query, _ := planningQuery(op, group)
			scope := r.Repo
			if group {
				scope = r.Group
			}
			want := []string{"api", "graphql", "--method", "POST", "--hostname", r.Host, "--raw-field", "query=" + query, "--raw-field", "fullPath=" + scope}
			if op != OpWorkItemFields {
				want = append(want, "--field", "first=31", "--raw-field", "after=@cursor:=value")
			}
			if op == OpBoardView || op == OpBoardIssues {
				want = append(want, "--raw-field", "board=gid://gitlab/Board/7")
			}
			if op == OpBoardIssues {
				want = append(want, "--raw-field", "list=gid://gitlab/List/9")
			}
			if op == OpWorkItemFields || op == OpWorkItemHierarchy {
				want = append(want, "--raw-field", "iid=8")
			}
			if inv.write != (op == OpBoardIssues) || inv.outputKind != outputJSON || !reflect.DeepEqual(inv.args, want) {
				t.Fatalf("op=%s inv=%+v", op, inv)
			}
		}
	}
}
func TestPlanningBuilderRefusesMalformedBeforeChild(t *testing.T) {
	good := Request{Operation: OpBoardIssues, Host: "gitlab.com", Repo: "team/project", ID: 7, ListID: 8, Page: 1, PerPage: 31, AllowOrderingInitialization: true}
	mutations := []func(*Request){
		func(r *Request) { r.AllowOrderingInitialization = false },
		func(r *Request) { r.Operation = OpBoardList },
		func(r *Request) { r.Host = "gitlab.com/evil" }, func(r *Request) { r.Repo = "../project" }, func(r *Request) { r.Group = "team" },
		func(r *Request) { r.Repo = ""; r.Group = "a//b" }, func(r *Request) { r.Page = 0 }, func(r *Request) { r.Page = 11 }, func(r *Request) { r.PerPage = 101 }, func(r *Request) { r.PerPage = 0 },
		func(r *Request) { r.ID = 0 }, func(r *Request) { r.ListID = -1 }, func(r *Request) { r.Cursor = "bad\nvalue" }, func(r *Request) { r.Cursor = strings.Repeat("a", 1025) },
		func(r *Request) { r.Operation = OpWorkItemHierarchy; r.IID = 0 },
	}
	for i, mutate := range mutations {
		r := good
		mutate(&r)
		if _, err := build(r); err == nil {
			t.Fatalf("case %d accepted %+v", i, r)
		}
	}
}
