package product

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/safeurl"
)

const (
	issueEditPageSize                    = 100
	issueEditInlineTextBytes             = 4 << 10
	issueEditInlineLabels                = 100
	issueEditInlineLabelBytes            = 16 << 10
	issueEditProviderPreconditionReason  = "provider_precondition_unavailable"
	issueEditProviderPreconditionMessage = "GitLab issue edit refused before mutation: the provider cannot enforce the expected issue revision or requested numeric label identities; use --dry-run for a validated preview"
)

type issueEditProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type issueEditLabel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type issueEditIdentity struct {
	Host            string `json:"host"`
	ProjectID       int64  `json:"project_id"`
	ProjectFullPath string `json:"project_full_path"`
	ProjectWebURL   string `json:"project_web_url"`
	IssueID         int64  `json:"issue_id"`
	IID             int64  `json:"iid"`
	WebURL          string `json:"web_url"`
}

type issueEditExpected struct {
	WebURL    string `json:"web_url"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
}

type issueEditTextEvidence struct {
	Bytes  int     `json:"bytes"`
	Value  *string `json:"value,omitempty"`
	SHA256 string  `json:"sha256,omitempty"`
}

type issueEditTextChange struct {
	Before issueEditTextEvidence `json:"before"`
	After  issueEditTextEvidence `json:"after"`
}

type issueEditLabelSetEvidence struct {
	Count  int       `json:"count"`
	Bytes  int       `json:"bytes"`
	Values *[]string `json:"values,omitempty"`
	SHA256 string    `json:"sha256,omitempty"`
}

type issueEditLabelChange struct {
	Add    []issueEditLabel          `json:"add"`
	Remove []issueEditLabel          `json:"remove"`
	Before issueEditLabelSetEvidence `json:"before"`
	After  issueEditLabelSetEvidence `json:"after"`
}

type issueEditChanges struct {
	Title       *issueEditTextChange  `json:"title,omitempty"`
	Description *issueEditTextChange  `json:"description,omitempty"`
	Labels      *issueEditLabelChange `json:"labels,omitempty"`
}

type issueEditResult struct {
	Action             string            `json:"action"`
	Outcome            string            `json:"outcome"`
	DryRun             bool              `json:"dry_run"`
	RefusalReason      string            `json:"refusal_reason,omitempty"`
	Identity           issueEditIdentity `json:"identity"`
	Expected           issueEditExpected `json:"expected"`
	ChangedFields      []string          `json:"changed_fields"`
	Changes            issueEditChanges  `json:"changes"`
	ResultingUpdatedAt string            `json:"resulting_updated_at"`
}

type issueEditOutput struct {
	Edit issueEditResult `json:"edit"`
}

type issueEditRequested struct {
	Title        *string
	Description  *string
	AddLabels    []string
	RemoveLabels []string
}

type issueEditPlan struct {
	changedFields []string
	changes       issueEditChanges
}

type issueEditReadBudget struct {
	bytes int
}

func (b *issueEditReadBudget) add(body []byte) error {
	if len(body) > limits.MaxJSONPageBytes {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab issue-edit response exceeded the JSON page limit")
	}
	b.bytes += len(body)
	if b.bytes > limits.MaxOperationBytes {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab issue-edit data exceeded the operation limit")
	}
	return nil
}

func validateIssueEditParsed(parsed Parsed) error {
	iid, err := issueEditIID(parsed)
	if err != nil {
		return err
	}
	if err := safeurl.ValidateHost(parsed.Values["--hostname"]); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid GitLab hostname", err)
	}
	if err := safeurl.ValidateProject(parsed.Values["--repo"]); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid repository target", err)
	}
	if got, want := parsed.Values["--expected-url"], canonicalIssueURL(parsed.Values["--hostname"], parsed.Values["--repo"], iid); got != want {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url does not exactly match the selected GitLab issue")
	}
	switch parsed.Values["--expected-state"] {
	case "opened", "closed":
	default:
		return uxv1.NewError(uxv1.CodeValidation, "--expected-state must be opened or closed")
	}
	if _, err := parseIssueEditTimestamp(parsed.Values["--expected-updated-at"]); err != nil {
		return err
	}
	if parsed.Values["--title-file"] == "" && parsed.Values["--description-file"] == "" && len(parsed.MultiValues["--add-label"]) == 0 && len(parsed.MultiValues["--remove-label"]) == 0 {
		return uxv1.NewError(uxv1.CodeValidation, "issue edit requires at least one requested field change")
	}
	return validateRequestedIssueLabels(parsed.MultiValues["--add-label"], parsed.MultiValues["--remove-label"])
}

func issueEditIID(parsed Parsed) (int64, error) {
	if len(parsed.Positionals) != 1 {
		return 0, uxv1.NewError(uxv1.CodeValidation, "issue IID is required")
	}
	raw := parsed.Positionals[0]
	iid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || iid < 1 || strconv.FormatInt(iid, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, "issue IID must be a canonical positive integer")
	}
	return iid, nil
}

func canonicalIssueURL(host, repo string, iid int64) string {
	return (&url.URL{Scheme: "https", Host: host, Path: "/" + repo + "/-/issues/" + strconv.FormatInt(iid, 10)}).String()
}

func parseIssueEditTimestamp(raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || value.IsZero() {
		return time.Time{}, uxv1.NewError(uxv1.CodeValidation, "--expected-updated-at must be a valid nonzero RFC 3339 timestamp")
	}
	return value.UTC(), nil
}

func validateRequestedIssueLabels(add, remove []string) error {
	if len(add)+len(remove) > limits.MaxIssueEditLabels {
		return uxv1.NewError(uxv1.CodeValidation, "issue edit requested too many label changes")
	}
	seenAdd := make([]string, 0, len(add))
	seenRemove := make([]string, 0, len(remove))
	for _, group := range []struct {
		name   string
		values []string
		seen   *[]string
	}{
		{name: "--add-label", values: add, seen: &seenAdd},
		{name: "--remove-label", values: remove, seen: &seenRemove},
	} {
		for _, value := range group.values {
			if !validRequestedIssueLabel(value) {
				return uxv1.NewError(uxv1.CodeValidation, group.name+" must be an exact bounded label name without a comma")
			}
			for _, prior := range *group.seen {
				if strings.EqualFold(prior, value) {
					return uxv1.NewError(uxv1.CodeValidation, "duplicate "+group.name+" value")
				}
			}
			*group.seen = append(*group.seen, value)
		}
	}
	for _, addName := range add {
		for _, removeName := range remove {
			if strings.EqualFold(addName, removeName) {
				return uxv1.NewError(uxv1.CodeValidation, "the same label cannot be added and removed")
			}
		}
	}
	return nil
}

func validRequestedIssueLabel(value string) bool {
	return value != "" && len(value) <= limits.MaxLabelNameBytes && utf8.ValidString(value) && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n,") && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
}

func executeIssueEdit(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, _ := issueEditIID(parsed)
	expectedAt, _ := parseIssueEditTimestamp(parsed.Values["--expected-updated-at"])
	requested, err := loadIssueEditRequested(parsed)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	expectedURL := parsed.Values["--expected-url"]
	expectedState := parsed.Values["--expected-state"]
	budget := &issueEditReadBudget{}

	preflightCtx, cancelPreflight := context.WithTimeout(ctx, limits.IssueEditPreflight)
	defer cancelPreflight()
	project, err := loadIssueEditProject(preflightCtx, client, target, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	before, err := loadIssueEditIssue(preflightCtx, client, target, iid, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := validateIssueEditExpected(before, target, project.ID, iid, 0, expectedURL, expectedState, expectedAt); err != nil {
		return commandOutput{meta: meta}, err
	}

	var resolvedAdd, resolvedRemove []issueEditLabel
	if len(requested.AddLabels)+len(requested.RemoveLabels) > 0 {
		catalog, err := loadIssueEditLabels(preflightCtx, client, target, &meta, budget)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		resolvedAdd, err = resolveIssueEditLabels(catalog, requested.AddLabels, "add")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		resolvedRemove, err = resolveIssueEditLabels(catalog, requested.RemoveLabels, "remove")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
	}

	plan, err := buildIssueEditPlan(before, requested, resolvedAdd, resolvedRemove)
	if err != nil {
		return commandOutput{meta: meta}, err
	}

	adjacent, err := loadIssueEditIssue(preflightCtx, client, target, iid, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := validateIssueEditExpected(adjacent, target, project.ID, iid, before.ID, expectedURL, expectedState, expectedAt); err != nil {
		return commandOutput{meta: meta}, err
	}
	if !sameIssueEditSnapshot(before, adjacent) {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "issue changed during edit validation")
	}
	if len(requested.AddLabels)+len(requested.RemoveLabels) > 0 {
		catalog, err := loadIssueEditLabels(preflightCtx, client, target, &meta, budget)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		finalAdd, err := resolveIssueEditLabels(catalog, requested.AddLabels, "add")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		finalRemove, err := resolveIssueEditLabels(catalog, requested.RemoveLabels, "remove")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		if !sameIssueEditLabelIdentities(resolvedAdd, finalAdd) || !sameIssueEditLabelIdentities(resolvedRemove, finalRemove) {
			return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "requested label identity changed during issue edit validation")
		}
	}
	if preflightErr := preflightCtx.Err(); preflightErr != nil {
		return commandOutput{meta: meta}, issueEditValidationContextError(preflightErr)
	}
	if parsed.Booleans["--dry-run"] {
		return issueEditReceipt("preview", true, target, project, adjacent, expectedURL, expectedState, expectedAt, plan, meta), nil
	}
	if len(plan.changedFields) == 0 {
		return issueEditReceipt("unchanged", false, target, project, adjacent, expectedURL, expectedState, expectedAt, plan, meta), nil
	}
	return issueEditProviderPreconditionRefusal(target, project, adjacent, expectedURL, expectedState, expectedAt, plan, meta)
}

func loadIssueEditRequested(parsed Parsed) (issueEditRequested, error) {
	requested := issueEditRequested{
		AddLabels:    append([]string(nil), parsed.MultiValues["--add-label"]...),
		RemoveLabels: append([]string(nil), parsed.MultiValues["--remove-label"]...),
	}
	if path := parsed.Values["--title-file"]; path != "" {
		value, err := privatefile.Read(path, limits.MaxTitleBytes, true)
		if err != nil {
			return issueEditRequested{}, err
		}
		if strings.TrimSpace(value) == "" {
			return issueEditRequested{}, uxv1.NewError(uxv1.CodeValidation, "issue title must not be empty")
		}
		requested.Title = &value
	}
	if path := parsed.Values["--description-file"]; path != "" {
		value, err := privatefile.Read(path, limits.MaxDescriptionBytes, false)
		if err != nil {
			return issueEditRequested{}, err
		}
		requested.Description = &value
	}
	return requested, nil
}

func loadIssueEditProject(ctx context.Context, client delegateClient, target Target, meta *uxv1.Meta, budget *issueEditReadBudget) (issueEditProject, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpIssueEditProject, Host: target.Host, Repo: target.Repo})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if err != nil {
		return issueEditProject{}, err
	}
	if err := budget.add(response.Body); err != nil {
		return issueEditProject{}, err
	}
	var project issueEditProject
	if err := decodeStrict(response.Body, &project); err != nil {
		return issueEditProject{}, err
	}
	if project.ID < 1 || project.PathWithNamespace != target.Repo || project.WebURL != canonicalProjectURL(target.Host, target.Repo) {
		return issueEditProject{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different issue-edit project identity")
	}
	return project, nil
}

func loadIssueEditIssue(ctx context.Context, client delegateClient, target Target, iid int64, meta *uxv1.Meta, budget *issueEditReadBudget) (upstreamIssue, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpIssueEditView, Host: target.Host, Repo: target.Repo, IID: iid})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if err != nil {
		return upstreamIssue{}, err
	}
	if err := budget.add(response.Body); err != nil {
		return upstreamIssue{}, err
	}
	return decodeIssueEditIssue(response.Body)
}

func decodeIssueEditIssue(body []byte) (upstreamIssue, error) {
	var fields map[string]json.RawMessage
	if err := decodeStrict(body, &fields); err != nil {
		return upstreamIssue{}, err
	}
	for _, name := range []string{"id", "iid", "project_id", "title", "description", "state", "web_url", "labels", "updated_at"} {
		value, ok := fields[name]
		isNull := bytes.Equal(bytes.TrimSpace(value), []byte("null"))
		if !ok || (isNull && name != "description") {
			return upstreamIssue{}, uxv1.NewError(uxv1.CodeUpstream, "official glab returned an incomplete issue-edit issue document")
		}
	}
	var issue upstreamIssue
	if err := decodeStrict(body, &issue); err != nil {
		return upstreamIssue{}, err
	}
	return issue, nil
}

func loadIssueEditLabels(ctx context.Context, client delegateClient, target Target, meta *uxv1.Meta, budget *issueEditReadBudget) ([]issueEditLabel, error) {
	labels := make([]issueEditLabel, 0)
	seenIDs := make(map[int64]bool)
	for page := 1; page <= limits.MaxPages; page++ {
		response, err := client.Do(ctx, glab.Request{
			Operation: glab.OpIssueEditLabelList, Host: target.Host, Repo: target.Repo, Page: page, PerPage: issueEditPageSize,
		})
		if response.UpstreamVersion != "" {
			meta.UpstreamVersion = response.UpstreamVersion
		}
		if err != nil {
			return nil, err
		}
		if err := budget.add(response.Body); err != nil {
			return nil, err
		}
		var pageLabels []issueEditLabel
		if err := decodeStrict(response.Body, &pageLabels); err != nil {
			return nil, err
		}
		if len(pageLabels) > issueEditPageSize {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many issue-edit labels in one page")
		}
		for _, label := range pageLabels {
			if label.ID < 1 || !validProviderIssueLabel(label.Name) {
				return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned invalid issue-edit label identity")
			}
			if seenIDs[label.ID] {
				return nil, uxv1.NewError(uxv1.CodeSafety, "official glab returned duplicate issue-edit label identities")
			}
			seenIDs[label.ID] = true
			labels = append(labels, label)
			if len(labels) > limits.MaxIssueLabels {
				return nil, uxv1.NewError(uxv1.CodeUpstream, "issue-edit label catalog exceeded the operation limit")
			}
		}
		if len(pageLabels) < issueEditPageSize {
			return labels, nil
		}
	}
	return nil, uxv1.NewError(uxv1.CodeConflict, "issue-edit label lookup exceeded the hard page limit")
}

func resolveIssueEditLabels(catalog []issueEditLabel, names []string, action string) ([]issueEditLabel, error) {
	resolved := make([]issueEditLabel, 0, len(names))
	for _, name := range names {
		matches := make([]issueEditLabel, 0, 1)
		for _, candidate := range catalog {
			if strings.EqualFold(candidate.Name, name) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 0 {
			return nil, uxv1.NewError(uxv1.CodeConflict, "requested "+action+" label is unavailable")
		}
		if len(matches) != 1 {
			return nil, uxv1.NewError(uxv1.CodeConflict, "requested "+action+" label identity is ambiguous")
		}
		if matches[0].Name != name {
			return nil, uxv1.NewError(uxv1.CodeConflict, "requested "+action+" label resolved to an unexpected identity")
		}
		resolved = append(resolved, matches[0])
	}
	sort.Slice(resolved, func(i, j int) bool {
		if resolved[i].Name != resolved[j].Name {
			return resolved[i].Name < resolved[j].Name
		}
		return resolved[i].ID < resolved[j].ID
	})
	return resolved, nil
}

func sameIssueEditLabelIdentities(left, right []issueEditLabel) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateIssueEditExpected(record upstreamIssue, target Target, projectID, iid, expectedIssueID int64, expectedURL, expectedState string, expectedAt time.Time) error {
	if err := validateIssueEditIdentity(record, target, projectID, iid, expectedIssueID, expectedURL, expectedState); err != nil {
		return err
	}
	if !record.UpdatedAt.Equal(expectedAt) {
		return uxv1.NewError(uxv1.CodeConflict, "issue updated_at does not match --expected-updated-at")
	}
	return nil
}

// expectedIssueID is zero only for the first read that establishes the global
// issue identity. Every subsequent validation read pins it.
func validateIssueEditIdentity(record upstreamIssue, target Target, projectID, iid, expectedIssueID int64, expectedURL, expectedState string) error {
	if record.ID < 1 || (expectedIssueID > 0 && record.ID != expectedIssueID) || record.IID != iid || record.ProjectID != projectID {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different issue identity")
	}
	if record.WebURL != expectedURL || record.WebURL != canonicalIssueURL(target.Host, target.Repo, iid) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different issue URL")
	}
	if record.State != "opened" && record.State != "closed" {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid issue state")
	}
	if record.State != expectedState {
		return uxv1.NewError(uxv1.CodeConflict, "issue state does not match --expected-state")
	}
	if record.UpdatedAt == nil || record.UpdatedAt.IsZero() {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid issue updated_at")
	}
	if record.Title == "" || len(record.Title) > limits.MaxTitleBytes || !validIssueEditText(record.Title) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid issue title")
	}
	if len(record.Description) > limits.MaxDescriptionBytes || !validIssueEditText(record.Description) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid issue description")
	}
	if _, err := canonicalIssueEditLabels(record.Labels); err != nil {
		return err
	}
	return nil
}

func validIssueEditText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validProviderIssueLabel(value string) bool {
	return value != "" && len(value) <= limits.MaxLabelNameBytes && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') &&
		!strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}

func canonicalIssueEditLabels(values []string) ([]string, error) {
	if len(values) > limits.MaxIssueLabels {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "issue label count exceeded the operation limit")
	}
	out := make([]string, len(values))
	copy(out, values)
	sort.Strings(out)
	total := 0
	seen := make([]string, 0, len(out))
	for _, value := range out {
		if !validProviderIssueLabel(value) {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid issue label")
		}
		total += len(value)
		if total > limits.MaxOperationBytes {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "issue labels exceeded the operation limit")
		}
		for _, prior := range seen {
			if strings.EqualFold(prior, value) {
				return nil, uxv1.NewError(uxv1.CodeSafety, "official glab returned duplicate or ambiguous issue labels")
			}
		}
		seen = append(seen, value)
	}
	return out, nil
}

func buildIssueEditPlan(record upstreamIssue, requested issueEditRequested, add, remove []issueEditLabel) (issueEditPlan, error) {
	plan := issueEditPlan{changedFields: make([]string, 0, 3)}
	if requested.Title != nil && record.Title != *requested.Title {
		value := *requested.Title
		plan.changedFields = append(plan.changedFields, "title")
		plan.changes.Title = &issueEditTextChange{Before: issueEditTextValue(record.Title), After: issueEditTextValue(value)}
	}
	if requested.Description != nil && record.Description != *requested.Description {
		value := *requested.Description
		plan.changedFields = append(plan.changedFields, "description")
		plan.changes.Description = &issueEditTextChange{Before: issueEditTextValue(record.Description), After: issueEditTextValue(value)}
	}

	beforeLabels, err := canonicalIssueEditLabels(record.Labels)
	if err != nil {
		return issueEditPlan{}, err
	}
	desired := make(map[string]bool, len(beforeLabels)+len(add))
	for _, name := range beforeLabels {
		desired[name] = true
	}
	actualRemove := make([]issueEditLabel, 0, len(remove))
	for _, label := range remove {
		present, exact := issueEditLabelPresence(beforeLabels, label.Name)
		if present && !exact {
			return issueEditPlan{}, uxv1.NewError(uxv1.CodeConflict, "remove label is attached through an unexpected identity")
		}
		if exact {
			delete(desired, label.Name)
			actualRemove = append(actualRemove, label)
		}
	}
	actualAdd := make([]issueEditLabel, 0, len(add))
	for _, label := range add {
		present, exact := issueEditLabelPresence(beforeLabels, label.Name)
		if present && !exact {
			return issueEditPlan{}, uxv1.NewError(uxv1.CodeConflict, "add label is attached through an unexpected identity")
		}
		if !present {
			desired[label.Name] = true
			actualAdd = append(actualAdd, label)
		}
	}
	if len(actualAdd)+len(actualRemove) > 0 {
		afterLabels := make([]string, 0, len(desired))
		for name := range desired {
			afterLabels = append(afterLabels, name)
		}
		sort.Strings(afterLabels)
		if _, err := canonicalIssueEditLabels(afterLabels); err != nil {
			return issueEditPlan{}, err
		}
		plan.changedFields = append(plan.changedFields, "labels")
		plan.changes.Labels = &issueEditLabelChange{
			Add: actualAdd, Remove: actualRemove,
			Before: issueEditLabelSetValue(beforeLabels), After: issueEditLabelSetValue(afterLabels),
		}
	}
	return plan, nil
}

func issueEditLabelPresence(labels []string, requested string) (present, exact bool) {
	for _, label := range labels {
		if strings.EqualFold(label, requested) {
			return true, label == requested
		}
	}
	return false, false
}

func sameIssueEditSnapshot(left, right upstreamIssue) bool {
	if left.ID != right.ID || left.IID != right.IID || left.ProjectID != right.ProjectID || left.Title != right.Title || left.Description != right.Description || left.State != right.State || left.WebURL != right.WebURL {
		return false
	}
	if left.UpdatedAt == nil || right.UpdatedAt == nil || !left.UpdatedAt.Equal(*right.UpdatedAt) {
		return false
	}
	leftLabels, leftErr := canonicalIssueEditLabels(left.Labels)
	rightLabels, rightErr := canonicalIssueEditLabels(right.Labels)
	return leftErr == nil && rightErr == nil && equalStrings(leftLabels, rightLabels)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func issueEditTextValue(value string) issueEditTextEvidence {
	evidence := issueEditTextEvidence{Bytes: len(value)}
	if len(value) <= issueEditInlineTextBytes {
		copyValue := value
		evidence.Value = &copyValue
		return evidence
	}
	sum := sha256.Sum256([]byte(value))
	evidence.SHA256 = hex.EncodeToString(sum[:])
	return evidence
}

func issueEditLabelSetValue(values []string) issueEditLabelSetEvidence {
	sorted := make([]string, len(values))
	copy(sorted, values)
	sort.Strings(sorted)
	encoded, _ := json.Marshal(sorted)
	evidence := issueEditLabelSetEvidence{Count: len(sorted), Bytes: len(encoded)}
	if len(sorted) <= issueEditInlineLabels && len(encoded) <= issueEditInlineLabelBytes {
		copyValues := make([]string, len(sorted))
		copy(copyValues, sorted)
		evidence.Values = &copyValues
		return evidence
	}
	sum := sha256.Sum256(encoded)
	evidence.SHA256 = hex.EncodeToString(sum[:])
	return evidence
}

func issueEditReceipt(action string, dryRun bool, target Target, project issueEditProject, issue upstreamIssue, expectedURL, expectedState string, expectedAt time.Time, plan issueEditPlan, meta uxv1.Meta) commandOutput {
	updatedAt := ""
	if issue.UpdatedAt != nil {
		updatedAt = issue.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	changedFields := make([]string, len(plan.changedFields))
	copy(changedFields, plan.changedFields)
	edit := issueEditResult{
		Action: action, Outcome: "not_applied", DryRun: dryRun,
		Identity: issueEditIdentity{
			Host: target.Host, ProjectID: project.ID, ProjectFullPath: project.PathWithNamespace,
			ProjectWebURL: project.WebURL, IssueID: issue.ID, IID: issue.IID, WebURL: issue.WebURL,
		},
		Expected:           issueEditExpected{WebURL: expectedURL, State: expectedState, UpdatedAt: expectedAt.UTC().Format(time.RFC3339Nano)},
		ChangedFields:      changedFields,
		Changes:            plan.changes,
		ResultingUpdatedAt: updatedAt,
	}
	if action == "refused" {
		edit.RefusalReason = issueEditProviderPreconditionReason
	}
	return commandOutput{data: issueEditOutput{Edit: edit}, meta: meta}
}

func issueEditProviderPreconditionRefusal(target Target, project issueEditProject, before upstreamIssue, expectedURL, expectedState string, expectedAt time.Time, plan issueEditPlan, meta uxv1.Meta) (commandOutput, error) {
	result := issueEditReceipt("refused", false, target, project, before, expectedURL, expectedState, expectedAt, plan, meta)
	result.meta.Reason = issueEditProviderPreconditionReason
	refusal := uxv1.NewError(uxv1.CodeSafety, issueEditProviderPreconditionMessage)
	refusal.Receipt = result.data
	return result, refusal
}

func issueEditValidationContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return uxv1.Wrap(uxv1.CodeCanceled, "issue edit validation was canceled", err)
	}
	return uxv1.Wrap(uxv1.CodeUpstream, "issue edit validation timed out", err)
}
