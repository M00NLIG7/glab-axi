package product

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

func planningDefinitions() []Definition {
	details := "Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.\nGitLab 19.3 schema; availability depends on provider version, tier and permissions.\nOnly authorized resources are visible. No generic GraphQL authority."
	scope := FlagDefinition{Name: "--group", Value: "FULL/PATH", Description: "Exact group path, mutually exclusive with --repo; never inferred from a project."}
	return []Definition{
		{Path: []string{"board", "list"}, Summary: "List GitLab issue boards in one project or group.", Details: details, Usage: "gl-axi board list (-R PROJECT | --group GROUP) [global flags]", RepoMode: RepoOptional, Flags: []FlagDefinition{scope}, Schema: "board-list", Backend: "official-glab"},
		{Path: []string{"board", "view"}, Summary: "View an issue board and its bounded list/column definitions.", Details: details + "\nThe limit counts columns, including provider-returned open/closed columns.\nList types are filters, not arbitrary custom fields. Hidden columns are not board lifecycle states.\nBoard scope filters are applied by GitLab, not projected as editable fields.", Usage: "gl-axi board view <board-id> (-R PROJECT | --group GROUP) [global flags]", RepoMode: RepoOptional, Positionals: 1, MaxPositions: 1, Flags: []FlagDefinition{scope}, Schema: "board-view", Backend: "official-glab"},
		{Path: []string{"board", "issues"}, Summary: "List board issues with explicit consent to possible ordering initialization.", Details: details + "\n--list-id is required; get list IDs from board view.\nGitLab applies the list and board filters. An issue can appear in multiple lists.\nThese are real issues, not independent Projects-v2 items, drafts or archived items.\nGitLab EE 19.3.0 BoardList.issues may initialize missing issue relative positions and shift sibling positions, including beyond displayed items.\nRequires --allow-ordering-initialization on every invocation and an explicit host. No automatic retry or rollback; the receipt does not claim the side effect occurred.\nBoards exist in Free/Premium/Ultimate; advanced board/list filters depend on tier. The version is a pinned schema baseline, not a server-version attestation.", Usage: "gl-axi board issues <board-id> --list-id ID --allow-ordering-initialization --hostname HOST (-R PROJECT | --group GROUP) [global flags]", RepoMode: RepoOptional, Positionals: 1, MaxPositions: 1, Flags: []FlagDefinition{scope, {Name: "--list-id", Value: "ID", Required: true, Description: "Exact positive board list ID; not a label ID."}, {Name: "--allow-ordering-initialization", Boolean: true, Required: true, Description: "Acknowledge that GitLab may initialize issue ordering and shift sibling positions."}}, Schema: "board-issues", Backend: "official-glab", Write: true, RequireExplicitHost: true},
		{Path: []string{"work-item", "fields"}, Summary: "List visible widget types and fixed fields for one work item.", Details: details + "\nReports widget availability on this exact item, not arbitrary custom-field definitions or values.\nWidget absence does not prove a tier entitlement. CUSTOM_FIELDS, if present, is a widget only.\nGroup work items require the provider's epics entitlement. Equal IIDs in other namespaces are not substitutes.", Usage: "gl-axi work-item fields <iid> (-R PROJECT | --group GROUP) [global flags]", RepoMode: RepoOptional, Positionals: 1, MaxPositions: 1, Flags: []FlagDefinition{scope}, Schema: "work-item-fields", Backend: "official-glab"},
		{Path: []string{"work-item", "hierarchy"}, Summary: "Read the parent and bounded direct children of one work item.", Details: details + "\nReads the HIERARCHY widget, never issue links. Depth is exactly one; no recursive tree claim.\nCompleteness covers authorized direct children only, not hidden descendants.\nAbsent widgets, denied parents and hidden-only children fail explicitly rather than appearing empty.", Usage: "gl-axi work-item hierarchy <iid> (-R PROJECT | --group GROUP) [global flags]", RepoMode: RepoOptional, Positionals: 1, MaxPositions: 1, Flags: []FlagDefinition{scope}, Schema: "work-item-hierarchy", Backend: "official-glab"},
	}
}

func isPlanningPath(path []string) bool {
	return len(path) == 2 && (path[0] == "board" || path[0] == "work-item")
}

func validatePlanningParsed(p Parsed) error {
	if err := glab.ValidatePlanningScope(p.Values["--repo"], p.Values["--group"]); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid planning scope", err)
	}
	if len(p.Positionals) > 0 {
		if _, err := canonicalPositive(p.Positionals[0]); err != nil {
			return err
		}
	}
	if raw := p.Values["--list-id"]; raw != "" {
		if _, err := canonicalPositive(raw); err != nil {
			return err
		}
	}
	return nil
}
func canonicalPositive(raw string) (int64, error) {
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 1 || strconv.FormatInt(n, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, "identifier must be a canonical positive integer")
	}
	return n, nil
}

// Planning output contracts intentionally do not reuse repository or issue
// shapes for groups, board items, or work-item namespace identity.
type PlanningScope struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	FullPath string `json:"full_path"`
	URL      string `json:"url"`
}
type PlanningBoard struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	HideBacklog bool   `json:"hide_backlog_list"`
	HideClosed  bool   `json:"hide_closed_list"`
}
type PlanningLabel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}
type PlanningColumn struct {
	ID       int64          `json:"id"`
	Title    string         `json:"title"`
	Type     string         `json:"type"`
	Position *int           `json:"position"`
	Label    *PlanningLabel `json:"label"`
}
type PlanningItem struct {
	ID        string         `json:"id"`
	IID       int64          `json:"iid"`
	Title     string         `json:"title"`
	State     string         `json:"state"`
	URL       string         `json:"url"`
	Type      string         `json:"type"`
	Namespace string         `json:"namespace"`
	Project   *PlanningScope `json:"project"`
}
type PlanningField struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type planningConnection struct {
	Nodes    []json.RawMessage `json:"nodes"`
	PageInfo *struct {
		Next   *bool   `json:"hasNextPage"`
		Cursor *string `json:"endCursor"`
	} `json:"pageInfo"`
}
type planningRawScope struct {
	ID       string              `json:"id"`
	FullPath string              `json:"fullPath"`
	URL      string              `json:"webUrl"`
	Boards   *planningConnection `json:"boards"`
	Board    *planningRawBoard   `json:"board"`
}
type planningRawBoard struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	URL         string              `json:"webUrl"`
	HideBacklog *bool               `json:"hideBacklogList"`
	HideClosed  *bool               `json:"hideClosedList"`
	Lists       *planningConnection `json:"lists"`
}
type planningRawColumn struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Type     string              `json:"listType"`
	Position *int                `json:"position"`
	Label    *PlanningLabel      `json:"label"`
	Issues   *planningConnection `json:"issues"`
}
type planningRawItem struct {
	ID           string `json:"id"`
	IID          string `json:"iid"`
	Title        string `json:"title"`
	State        string `json:"state"`
	URL          string `json:"webUrl"`
	WorkItemType *struct {
		Name string `json:"name"`
	} `json:"workItemType"`
	Namespace *struct {
		ID       string `json:"id"`
		FullPath string `json:"fullPath"`
	} `json:"namespace"`
	Project *planningRawScope `json:"project"`
	Widgets []planningWidget  `json:"widgets"`
}
type planningWidget struct {
	Type        string              `json:"type"`
	HasParent   *bool               `json:"hasParent"`
	HasChildren *bool               `json:"hasChildren"`
	Parent      *planningRawItem    `json:"parent"`
	Children    *planningConnection `json:"children"`
}
type planningResponse struct {
	Data *struct {
		Scope     *planningRawScope `json:"scope"`
		Namespace *struct {
			ID       string           `json:"id"`
			FullPath string           `json:"fullPath"`
			WorkItem *planningRawItem `json:"workItem"`
		} `json:"namespace"`
	} `json:"data"`
	Errors []struct {
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
}

func planningUnavailable(message string) error { return uxv1.NewError(uxv1.CodeUnsupported, message) }
func planningIdentityError() error {
	return uxv1.NewError(uxv1.CodeSafety, "GitLab planning response does not match the selected identity or scope")
}
func planningGID(raw, kind string) (int64, error) {
	prefix := "gid://gitlab/" + kind + "/"
	if !strings.HasPrefix(raw, prefix) {
		return 0, planningIdentityError()
	}
	n, err := canonicalPositive(strings.TrimPrefix(raw, prefix))
	if err != nil {
		return 0, planningIdentityError()
	}
	return n, nil
}
func planningURL(host, path string) string {
	return (&url.URL{Scheme: "https", Host: host, Path: path}).String()
}
func normalizePlanningScope(raw *planningRawScope, host, path, kind string) (PlanningScope, error) {
	if raw == nil {
		return PlanningScope{}, uxv1.NewError(uxv1.CodeNotFound, "planning scope is absent or inaccessible")
	}
	model := "Project"
	webPath := "/" + path
	if kind == "group" {
		model = "Group"
		webPath = "/groups/" + path
	}
	if _, err := planningGID(raw.ID, model); err != nil {
		return PlanningScope{}, err
	}
	if raw.FullPath != path || raw.URL != planningURL(host, webPath) {
		return PlanningScope{}, planningIdentityError()
	}
	return PlanningScope{Kind: kind, ID: raw.ID, FullPath: path, URL: raw.URL}, nil
}
func normalizePlanningBoard(raw *planningRawBoard, scope PlanningScope, host string, id int64) (PlanningBoard, bool, error) {
	if raw == nil {
		return PlanningBoard{}, false, uxv1.NewError(uxv1.CodeNotFound, "board is absent or inaccessible in the selected scope")
	}
	n, err := planningGID(raw.ID, "Board")
	if err != nil || (id != 0 && id != n) {
		return PlanningBoard{}, false, planningIdentityError()
	}
	path := "/" + scope.FullPath + "/-/boards/" + strconv.FormatInt(n, 10)
	if scope.Kind == "group" {
		path = "/groups" + path
	}
	if raw.URL != planningURL(host, path) {
		return PlanningBoard{}, false, planningIdentityError()
	}
	if raw.HideBacklog == nil || raw.HideClosed == nil {
		return PlanningBoard{}, false, malformed("board visibility")
	}
	name, tr, err := boundedText(raw.Name, "board name", 1024, true)
	return PlanningBoard{ID: n, Name: name, URL: raw.URL, HideBacklog: *raw.HideBacklog, HideClosed: *raw.HideClosed}, tr, err
}
func normalizePlanningColumn(raw planningRawColumn) (PlanningColumn, bool, error) {
	id, err := planningGID(raw.ID, "List")
	if err != nil {
		return PlanningColumn{}, false, err
	}
	switch raw.Type {
	case "backlog", "closed", "label", "assignee", "milestone", "iteration", "status":
	default:
		return PlanningColumn{}, false, planningUnavailable("provider board list type is not supported by the pinned schema")
	}
	title, tr, err := boundedText(raw.Title, "board list title", 1024, true)
	if err != nil {
		return PlanningColumn{}, false, err
	}
	if raw.Label != nil {
		validID := false
		for _, model := range []string{"Label", "ProjectLabel", "GroupLabel"} {
			if _, err := planningGID(raw.Label.ID, model); err == nil {
				validID = true
			}
		}
		if !validID {
			return PlanningColumn{}, false, planningIdentityError()
		}
		labelTitle, lt, err := boundedText(raw.Label.Title, "board list label", 1024, true)
		if err != nil {
			return PlanningColumn{}, false, err
		}
		tr = tr || lt
		raw.Label.Title = labelTitle
		if len(raw.Label.Color) != 7 || raw.Label.Color[0] != '#' {
			return PlanningColumn{}, false, malformed("label color")
		}
		if _, err := strconv.ParseUint(raw.Label.Color[1:], 16, 24); err != nil {
			return PlanningColumn{}, false, malformed("label color")
		}
	}
	if raw.Type == "label" && raw.Label == nil {
		return PlanningColumn{}, false, malformed("label list")
	}
	return PlanningColumn{ID: id, Title: title, Type: raw.Type, Position: raw.Position, Label: raw.Label}, tr, nil
}
func normalizePlanningItem(raw *planningRawItem, host string, workItem bool) (PlanningItem, bool, error) {
	if raw == nil {
		return PlanningItem{}, false, malformed("planning item")
	}
	model := "Issue"
	if workItem {
		model = "WorkItem"
	}
	if _, err := planningGID(raw.ID, model); err != nil {
		return PlanningItem{}, false, err
	}
	iid, err := canonicalPositive(raw.IID)
	if err != nil {
		return PlanningItem{}, false, malformed("planning item IID")
	}
	var project *PlanningScope
	var path string
	if raw.Project != nil {
		path = raw.Project.FullPath
		if err := safeurl.ValidateProject(path); err != nil {
			return PlanningItem{}, false, planningIdentityError()
		}
		p, err := normalizePlanningScope(raw.Project, host, path, "project")
		if err != nil {
			return PlanningItem{}, false, err
		}
		project = &p
	}
	itemType := "issue"
	if workItem {
		if raw.Namespace == nil || !validPlanningNamespaceID(raw.Namespace.ID, project == nil) || raw.WorkItemType == nil {
			return PlanningItem{}, false, malformed("work item namespace or type")
		}
		if project != nil && path != raw.Namespace.FullPath {
			return PlanningItem{}, false, planningIdentityError()
		}
		path = raw.Namespace.FullPath
		if project == nil {
			if err := glab.ValidatePlanningScope("", path); err != nil {
				return PlanningItem{}, false, planningIdentityError()
			}
		}
		itemType = raw.WorkItemType.Name
		if len(itemType) == 0 || len(itemType) > 128 || boundedEnum(itemType) == "unknown" {
			return PlanningItem{}, false, malformed("work item type")
		}
	} else if project == nil {
		return PlanningItem{}, false, planningIdentityError()
	}
	prefix := "/" + path + "/-/"
	if project == nil {
		prefix = "/groups" + prefix
	}
	suffix := strconv.FormatInt(iid, 10)
	allowed := raw.URL == planningURL(host, prefix+"work_items/"+suffix)
	if project != nil {
		allowed = allowed || raw.URL == planningURL(host, prefix+"issues/"+suffix)
	}
	if !allowed {
		return PlanningItem{}, false, planningIdentityError()
	}
	state := strings.ToLower(raw.State)
	if workItem && raw.State == "OPEN" {
		state = "opened"
	}
	if state != "opened" && state != "closed" {
		return PlanningItem{}, false, malformed("work item state")
	}
	title, tr, err := boundedText(raw.Title, "planning item title", 1024, true)
	return PlanningItem{ID: raw.ID, IID: iid, Title: title, State: state, URL: raw.URL, Type: itemType, Namespace: path, Project: project}, tr, err
}

func readPlanning(ctx context.Context, c delegateClient, r glab.Request, meta *uxv1.Meta, bytesRead *int) (planningResponse, error) {
	if err := ctx.Err(); err != nil {
		return planningResponse{}, uxv1.Wrap(uxv1.CodeCanceled, "planning read canceled", err)
	}
	response, err := c.Do(ctx, r)
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return planningResponse{}, err
	}
	*bytesRead += len(response.Body)
	if len(response.Body) > limits.MaxJSONPageBytes || *bytesRead > limits.MaxOperationBytes {
		return planningResponse{}, uxv1.NewError(uxv1.CodeUpstream, "planning read exceeded the response budget")
	}
	var doc planningResponse
	if err := decodeStrict(response.Body, &doc); err != nil {
		return doc, err
	}
	if len(doc.Errors) > 0 {
		// Do not surface arbitrary provider messages, queries or partial data.
		for _, e := range doc.Errors {
			switch e.Extensions.Code {
			case "undefinedField", "undefinedType", "argumentNotAccepted":
				return doc, planningUnavailable("GitLab does not support the pinned planning schema")
			case "FORBIDDEN", "forbidden":
				return doc, uxv1.NewError(uxv1.CodeForbidden, "GitLab denied the planning read")
			}
		}
		return doc, uxv1.NewError(uxv1.CodeUpstream, "GitLab rejected the planning query; no partial result accepted")
	}
	if doc.Data == nil {
		return doc, malformed("planning response")
	}
	return doc, nil
}
func planningConnectionPage(c *planningConnection, max int) ([]json.RawMessage, bool, string, error) {
	if c == nil {
		return nil, false, "", planningUnavailable("planning connection is unavailable; not an empty result")
	}
	if c.Nodes == nil || c.PageInfo == nil || c.PageInfo.Next == nil || len(c.Nodes) > max {
		return nil, false, "", malformed("planning connection")
	}
	next := *c.PageInfo.Next
	cursor := ""
	if c.PageInfo.Cursor != nil {
		cursor = *c.PageInfo.Cursor
	}
	if next && (len(c.Nodes) == 0 || cursor == "" || len(cursor) > 1024 || !validArgument(cursor)) {
		return nil, false, "", malformed("planning cursor")
	}
	return c.Nodes, next, cursor, nil
}

func executePlanning(ctx context.Context, c delegateClient, target Target, p Parsed, meta uxv1.Meta) (out commandOutput, resultErr error) {
	operation := map[string]glab.Operation{"board list": glab.OpBoardList, "board view": glab.OpBoardView, "board issues": glab.OpBoardIssues, "work-item fields": glab.OpWorkItemFields, "work-item hierarchy": glab.OpWorkItemHierarchy}[strings.Join(p.Definition.Path, " ")]
	r := glab.Request{Operation: operation, Host: target.Host, Repo: target.Repo, Group: p.Values["--group"], PerPage: min(100, p.Limit+1), AllowOrderingInitialization: p.Booleans["--allow-ordering-initialization"]}
	if len(p.Positionals) > 0 {
		r.ID, _ = canonicalPositive(p.Positionals[0])
		r.IID = r.ID
	}
	if p.Values["--list-id"] != "" {
		r.ListID, _ = canonicalPositive(p.Values["--list-id"])
	}
	kind, path := "project", target.Repo
	if r.Group != "" {
		kind, path = "group", r.Group
	}
	data := map[string]any{"visibility": "authorized_only"}
	ordering := PlanningOrderingReceipt{Acknowledged: r.AllowOrderingInitialization, ProviderSchema: "GitLab EE 19.3.0", Tiers: "Free, Premium, Ultimate; advanced board/list features are tier-dependent", Host: target.Host, ScopeKind: kind, ScopePath: path, BoardID: r.ID, ListID: r.ListID, Effect: planningOrderingEffect, Outcome: "not_attempted"}
	if operation == glab.OpBoardIssues {
		defer func() {
			if resultErr != nil && ordering.RequestsAttempted > 0 {
				cloned := *uxv1.AsError(resultErr)
				cloned.Receipt = map[string]any{"board_ordering": ordering}
				cloned.Retryable = false
				resultErr = &cloned
			} else if resultErr == nil {
				data["ordering"] = ordering
			}
		}()
	}
	boards := []PlanningBoard{}
	columns := []PlanningColumn{}
	items := []PlanningItem{}
	seen := map[string]bool{}
	seenItemIdentity := map[string]string{}
	cursors := map[string]bool{}
	bytesRead := 0
	fieldTruncated := false
	var scope PlanningScope
	var board PlanningBoard
	var column PlanningColumn
	var root PlanningItem
	var parent *PlanningItem
	for page := 1; page <= limits.MaxPages; page++ {
		r.Page = page
		if operation == glab.OpBoardIssues {
			ordering.RequestsAttempted++
			ordering.Outcome = "may_have_occurred"
		}
		doc, err := readPlanning(ctx, c, r, &meta, &bytesRead)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		currentScope, err := normalizePlanningScope(doc.Data.Scope, target.Host, path, kind)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		if page > 1 && scope != currentScope {
			return commandOutput{meta: meta}, planningIdentityError()
		}
		scope = currentScope
		data["scope"] = scope
		var connection *planningConnection
		switch operation {
		case glab.OpBoardList:
			connection = doc.Data.Scope.Boards
		case glab.OpBoardView, glab.OpBoardIssues:
			b, tr, err := normalizePlanningBoard(doc.Data.Scope.Board, scope, target.Host, r.ID)
			if err != nil {
				return commandOutput{meta: meta}, err
			}
			fieldTruncated = fieldTruncated || tr
			if page > 1 && board != b {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "board changed during pagination")
			}
			board = b
			connection = doc.Data.Scope.Board.Lists
			data["board"] = board
			data["membership"] = "provider_board_and_list_filters"
			data["scope_filters"] = "provider_applied_not_projected"
			if operation == glab.OpBoardIssues {
				nodes, next, _, err := planningConnectionPage(connection, 2)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				if len(nodes) == 0 {
					return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeNotFound, "list is absent or inaccessible in the selected board")
				}
				if len(nodes) != 1 || next {
					return commandOutput{meta: meta}, planningIdentityError()
				}
				var raw planningRawColumn
				if err := decodeStrict(nodes[0], &raw); err != nil {
					return commandOutput{meta: meta}, err
				}
				col, tr, err := normalizePlanningColumn(raw)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				fieldTruncated = fieldTruncated || tr
				if col.ID != r.ListID {
					return commandOutput{meta: meta}, planningIdentityError()
				}
				if page > 1 && !reflect.DeepEqual(column, col) {
					return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "board list changed during pagination")
				}
				column = col
				data["list"] = col
				connection = raw.Issues
			}
		case glab.OpWorkItemFields, glab.OpWorkItemHierarchy:
			ns := doc.Data.Namespace
			if ns == nil || ns.WorkItem == nil {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeNotFound, "work item is absent, inaccessible or tier-gated in this namespace")
			}
			if ns.FullPath != path || ns.ID == "" || ns.WorkItem.Namespace == nil || ns.WorkItem.Namespace.ID != ns.ID {
				return commandOutput{meta: meta}, planningIdentityError()
			}
			item, tr, err := normalizePlanningItem(ns.WorkItem, target.Host, true)
			if err != nil {
				return commandOutput{meta: meta}, err
			}
			fieldTruncated = fieldTruncated || tr
			if item.IID != r.IID || item.Namespace != path || (item.Project == nil) != (kind == "group") || (kind == "project" && item.Project.ID != scope.ID) {
				return commandOutput{meta: meta}, planningIdentityError()
			}
			if page > 1 && !reflect.DeepEqual(root, item) {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "work item changed during pagination")
			}
			root = item
			data["work_item"] = item
			widgets := ns.WorkItem.Widgets
			if widgets == nil {
				return commandOutput{meta: meta}, planningUnavailable("work-item widgets are unavailable")
			}
			if len(widgets) > 64 {
				return commandOutput{meta: meta}, malformed("work-item widgets")
			}
			if operation == glab.OpWorkItemFields {
				fields := []PlanningField{{Name: "iid", Kind: "fixed"}, {Name: "title", Kind: "fixed"}, {Name: "state", Kind: "fixed"}, {Name: "work_item_type", Kind: "fixed"}}
				for _, w := range widgets {
					if !planningWidgetType(w.Type) || seen[w.Type] {
						return commandOutput{meta: meta}, planningUnavailable("unknown or duplicate work-item widget type")
					}
					seen[w.Type] = true
					fields = append(fields, PlanningField{Name: w.Type, Kind: "widget"})
				}
				meta.Count = min(len(fields), p.Limit)
				if len(fields) > p.Limit {
					fields = fields[:p.Limit]
					meta.Complete = false
					meta.Truncated = true
					meta.Reason = "display_limit"
				}
				data["fields"] = fields
				data["custom_field_definitions"] = "not_exposed"
				if fieldTruncated {
					meta.Truncated = true
					if meta.Reason == "" {
						meta.Reason = "field_limit"
					}
				}
				return commandOutput{data: data, meta: meta}, nil
			}
			if len(widgets) != 1 || widgets[0].Type != "HIERARCHY" {
				return commandOutput{meta: meta}, planningUnavailable("hierarchy widget is unavailable for this work-item type, tier or permission")
			}
			w := widgets[0]
			if w.HasParent == nil || w.HasChildren == nil {
				return commandOutput{meta: meta}, malformed("hierarchy visibility")
			}
			if *w.HasParent != (w.Parent != nil) {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeForbidden, "hierarchy parent is unavailable; not an absent relationship")
			}
			var currentParent *PlanningItem
			if w.Parent != nil {
				parentItem, tr, err := normalizePlanningItem(w.Parent, target.Host, true)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				fieldTruncated = fieldTruncated || tr
				currentParent = &parentItem
				if samePlanningItem(parentItem, root) {
					return commandOutput{meta: meta}, planningIdentityError()
				}
			}
			if page > 1 && !reflect.DeepEqual(parent, currentParent) {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "hierarchy parent changed during pagination")
			}
			parent = currentParent
			data["parent"] = parent
			data["depth"] = 1
			connection = w.Children
			if connection != nil && connection.Nodes != nil && ((page == 1 && *w.HasChildren && len(connection.Nodes) == 0) || (!*w.HasChildren && len(connection.Nodes) > 0)) {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeForbidden, "hierarchy children are unavailable or inconsistent; not an empty relationship")
			}
		}
		nodes, next, cursor, err := planningConnectionPage(connection, r.PerPage)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		for _, node := range nodes {
			key := ""
			switch operation {
			case glab.OpBoardList:
				var raw planningRawBoard
				if err := decodeStrict(node, &raw); err != nil {
					return commandOutput{meta: meta}, err
				}
				b, tr, err := normalizePlanningBoard(&raw, scope, target.Host, 0)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				fieldTruncated = fieldTruncated || tr
				key = raw.ID
				boards = append(boards, b)
			case glab.OpBoardView:
				var raw planningRawColumn
				if err := decodeStrict(node, &raw); err != nil {
					return commandOutput{meta: meta}, err
				}
				col, tr, err := normalizePlanningColumn(raw)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				fieldTruncated = fieldTruncated || tr
				key = raw.ID
				columns = append(columns, col)
			default:
				var raw planningRawItem
				if err := decodeStrict(node, &raw); err != nil {
					return commandOutput{meta: meta}, err
				}
				item, tr, err := normalizePlanningItem(&raw, target.Host, operation == glab.OpWorkItemHierarchy)
				if err != nil {
					return commandOutput{meta: meta}, err
				}
				fieldTruncated = fieldTruncated || tr
				key = raw.ID
				if operation == glab.OpBoardIssues {
					if (kind == "project" && (item.Namespace != path || item.Project.ID != scope.ID)) || (kind == "group" && !strings.HasPrefix(item.Namespace, path+"/")) {
						return commandOutput{meta: meta}, planningIdentityError()
					}
				} else if samePlanningItem(item, root) || parent != nil && samePlanningItem(item, *parent) {
					return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "cyclic direct work-item hierarchy")
				}
				naturalKey := fmt.Sprintf("%s#%d", item.Namespace, item.IID)
				if previous := seenItemIdentity[naturalKey]; previous != "" && previous != item.ID {
					return commandOutput{meta: meta}, planningIdentityError()
				}
				seenItemIdentity[naturalKey] = item.ID
				items = append(items, item)
			}
			if seen[key] {
				return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeUpstream, "duplicate planning node during pagination")
			}
			seen[key] = true
		}
		count := len(seen)
		meta.Count = min(count, p.Limit)
		if next && cursors[cursor] {
			return commandOutput{meta: meta}, malformed("repeated planning cursor")
		}
		if count > p.Limit || count == p.Limit && next {
			meta.Complete = false
			meta.Truncated = true
			meta.Reason = "display_limit"
			break
		}
		if !next {
			meta.Complete = true
			break
		}
		if page == limits.MaxPages {
			meta.Complete = false
			meta.Truncated = true
			meta.Reason = "hard_page_limit"
			break
		}
		cursors[cursor] = true
		r.Cursor = cursor
	}
	if fieldTruncated {
		meta.Truncated = true
		if meta.Reason == "" {
			meta.Reason = "field_limit"
		}
	}
	switch operation {
	case glab.OpBoardList:
		data["boards"] = boards[:min(len(boards), p.Limit)]
	case glab.OpBoardView:
		data["lists"] = columns[:min(len(columns), p.Limit)]
	case glab.OpBoardIssues:
		data["issues"] = items[:min(len(items), p.Limit)]
	case glab.OpWorkItemHierarchy:
		data["children"] = items[:min(len(items), p.Limit)]
	default:
		return commandOutput{meta: meta}, fmt.Errorf("unreachable planning operation")
	}
	return commandOutput{data: data, meta: meta}, nil
}

func samePlanningItem(a, b PlanningItem) bool {
	return a.ID == b.ID || a.Namespace == b.Namespace && a.IID == b.IID
}

func validPlanningNamespaceID(id string, group bool) bool {
	kind := "Namespaces::ProjectNamespace"
	if group {
		kind = "Group"
	}
	_, err := planningGID(id, kind)
	return err == nil
}

func planningWidgetType(s string) bool {
	switch s {
	case "DESCRIPTION", "HIERARCHY", "LABELS", "ASSIGNEES", "START_AND_DUE_DATE", "MILESTONE", "NOTES", "NOTIFICATIONS", "CURRENT_USER_TODOS", "AWARD_EMOJI", "LINKED_ITEMS", "PARTICIPANTS", "TIME_TRACKING", "DESIGNS", "DEVELOPMENT", "CRM_CONTACTS", "EMAIL_PARTICIPANTS", "LINKED_RESOURCES", "ERROR_TRACKING", "WEIGHT", "VERIFICATION_STATUS", "ITERATION", "HEALTH_STATUS", "PROGRESS", "REQUIREMENT_LEGACY", "TEST_REPORTS", "COLOR", "CUSTOM_FIELDS", "VULNERABILITIES", "STATUS", "AI_SESSION", "AGENT_PLAN":
		return true
	}
	return false
}
