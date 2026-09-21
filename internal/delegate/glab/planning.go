package glab

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

const (
	OpBoardList         Operation = "board-list"
	OpBoardView         Operation = "board-view"
	OpBoardIssues       Operation = "board-issues"
	OpWorkItemFields    Operation = "work-item-fields"
	OpWorkItemHierarchy Operation = "work-item-hierarchy"
)

func isPlanningOperation(op Operation) bool {
	switch op {
	case OpBoardList, OpBoardView, OpBoardIssues, OpWorkItemFields, OpWorkItemHierarchy:
		return true
	}
	return false
}

// ValidatePlanningScope accepts paths, never numeric IDs or a guessed owner.
func ValidatePlanningScope(repo, group string) error {
	if (repo == "") == (group == "") {
		return uxv1.NewError(uxv1.CodeValidation, "select exactly one explicit --repo or --group path")
	}
	if repo != "" {
		return safeurl.ValidateProject(repo)
	}
	// The project validator already owns the segment grammar; a fixed final
	// segment permits a one-segment group without relaxing path validation.
	return safeurl.ValidateProject(group + "/scope")
}

const planningPageInfo = `pageInfo { hasNextPage endCursor }`
const planningScopeFields = `id fullPath webUrl`
const planningBoardFields = `id name webUrl hideBacklogList hideClosedList`
const planningListFields = `id title listType position label { id title color }`
const planningWorkItemFields = `id iid title state webUrl workItemType { name } namespace { id fullPath } project { id fullPath webUrl }`

// Only these fixed query documents can reach GraphQL. No caller supplies a
// query, field name, mutation, URL, or arbitrary variable map. POST is the
// GraphQL transport. BoardList.issues can initialize ordering despite being a
// query, so it is independently guarded and classified as write-capable.
func planningQuery(op Operation, group bool) (string, error) {
	scope := "project"
	if group {
		scope = "group"
	}
	head := `query PlanningRead($fullPath: ID!, $first: Int!, $after: String`
	switch op {
	case OpBoardList:
		return head + `) { scope: ` + scope + `(fullPath: $fullPath) { ` + planningScopeFields + ` boards(first: $first, after: $after) { nodes { ` + planningBoardFields + ` } ` + planningPageInfo + ` } } }`, nil
	case OpBoardView:
		return head + `, $board: BoardID!) { scope: ` + scope + `(fullPath: $fullPath) { ` + planningScopeFields + ` board(id: $board) { ` + planningBoardFields + ` lists(first: $first, after: $after) { nodes { ` + planningListFields + ` } ` + planningPageInfo + ` } } } }`, nil
	case OpBoardIssues:
		return head + `, $board: BoardID!, $list: ListID!) { scope: ` + scope + `(fullPath: $fullPath) { ` + planningScopeFields + ` board(id: $board) { ` + planningBoardFields + ` lists(id: $list, first: 2) { nodes { ` + planningListFields + ` issues(first: $first, after: $after) { nodes { id iid title state webUrl project { ` + planningScopeFields + ` } } ` + planningPageInfo + ` } } ` + planningPageInfo + ` } } } }`, nil
	case OpWorkItemFields:
		// Namespace.workItem is directly scoped; Group.workItems would include
		// descendant projects and can confuse equal IIDs in different namespaces.
		return `query PlanningRead($fullPath: ID!, $iid: String!) { scope: ` + scope + `(fullPath: $fullPath) { ` + planningScopeFields + ` } namespace(fullPath: $fullPath) { id fullPath workItem(iid: $iid) { ` + planningWorkItemFields + ` widgets { type } } } }`, nil
	case OpWorkItemHierarchy:
		return head + `, $iid: String!) { scope: ` + scope + `(fullPath: $fullPath) { ` + planningScopeFields + ` } namespace(fullPath: $fullPath) { id fullPath workItem(iid: $iid) { ` + planningWorkItemFields + ` widgets(onlyTypes: [HIERARCHY]) { type ... on WorkItemWidgetHierarchy { hasParent hasChildren parent { ` + planningWorkItemFields + ` } children(first: $first, after: $after) { nodes { ` + planningWorkItemFields + ` } ` + planningPageInfo + ` } } } } } }`, nil
	default:
		return "", uxv1.NewError(uxv1.CodeUnsupported, "undeclared planning operation")
	}
}

func buildPlanning(r Request) (invocation, error) {
	if r.Operation == OpBoardIssues && !r.AllowOrderingInitialization {
		return invocation{}, uxv1.NewError(uxv1.CodeSafety, "board issues requires explicit ordering-initialization acknowledgment")
	}
	if r.Operation != OpBoardIssues && r.AllowOrderingInitialization {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "ordering acknowledgment applies only to board issues")
	}
	if err := ValidatePlanningScope(r.Repo, r.Group); err != nil {
		return invocation{}, uxv1.Wrap(uxv1.CodeValidation, "invalid planning scope", err)
	}
	if r.PerPage < 1 || r.PerPage > 100 || r.Page < 1 || r.Page > limits.MaxPages {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "invalid planning pagination")
	}
	if len(r.Cursor) > 1024 || !utf8.ValidString(r.Cursor) || strings.ContainsAny(r.Cursor, "\x00\r\n") {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "invalid planning cursor")
	}
	if r.Operation != OpBoardList && r.Operation != OpWorkItemFields && r.Operation != OpWorkItemHierarchy && r.ID < 1 {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "board ID must be positive")
	}
	if r.Operation == OpBoardIssues && r.ListID < 1 {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "list ID must be positive")
	}
	if (r.Operation == OpWorkItemFields || r.Operation == OpWorkItemHierarchy) && r.IID < 1 {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "work-item IID must be positive")
	}
	query, err := planningQuery(r.Operation, r.Group != "")
	if err != nil {
		return invocation{}, err
	}
	path := r.Repo
	if r.Group != "" {
		path = r.Group
	}
	args := []string{"api", "graphql", "--method", "POST", "--hostname", r.Host, "--raw-field", "query=" + query, "--raw-field", "fullPath=" + path}
	if r.Operation != OpWorkItemFields {
		args = append(args, "--field", "first="+strconv.Itoa(r.PerPage))
		if r.Cursor != "" {
			args = append(args, "--raw-field", "after="+r.Cursor)
		}
	}
	switch r.Operation {
	case OpBoardView, OpBoardIssues:
		args = append(args, "--raw-field", fmt.Sprintf("board=gid://gitlab/Board/%d", r.ID))
	}
	if r.Operation == OpBoardIssues {
		args = append(args, "--raw-field", fmt.Sprintf("list=gid://gitlab/List/%d", r.ListID))
	}
	if r.Operation == OpWorkItemFields || r.Operation == OpWorkItemHierarchy {
		args = append(args, "--raw-field", "iid="+strconv.FormatInt(r.IID, 10))
	}
	return invocation{args: args, host: r.Host, maxStdout: limits.MaxJSONPageBytes, outputKind: outputJSON, write: r.Operation == OpBoardIssues}, nil
}
