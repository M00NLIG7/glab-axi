package glab

import (
	"strings"
	"unicode"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/safeurl"
)

// ListFilters is a closed, read-only selection contract. It never carries
// arbitrary upstream flags, query keys, or search expressions.
type ListFilters struct {
	State        string
	Labels       []string
	Author       string
	Assignee     string
	Milestone    string
	Sort         string
	SourceBranch string
	TargetBranch string
	Draft        bool
	NotDraft     bool
}

// Validate runs both before target discovery and at the delegate boundary.
func (f ListFilters) Validate(operation Operation) error {
	invalid := func() error { return uxv1.NewError(uxv1.CodeValidation, "invalid issue or merge-request list filters") }
	if operation != OpIssueList && operation != OpMRList {
		return invalid()
	}
	switch f.State {
	case "", "open", "closed", "all":
	case "merged":
		if operation != OpMRList {
			return invalid()
		}
	default:
		return invalid()
	}
	for _, username := range []string{f.Author, f.Assignee} {
		if username == "" {
			continue
		}
		if len(username) > 255 || !asciiAlnum(username[0]) {
			return invalid()
		}
		for _, ch := range username {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '-') {
				return invalid()
			}
		}
	}
	if len(f.Labels) > 20 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, label := range f.Labels {
		if !literalFilter(label) || seen[label] {
			return invalid()
		}
		seen[label] = true
	}
	if f.Milestone != "" {
		if operation != OpIssueList || !literalFilter(f.Milestone) {
			return invalid()
		}
		switch strings.ToLower(f.Milestone) {
		case "#started", "#upcoming", "no milestone", "any milestone":
			return invalid()
		}
	}
	if f.Sort != "" && (operation != OpIssueList || f.Sort != "created" && f.Sort != "updated") {
		return invalid()
	}
	if operation == OpIssueList && (f.SourceBranch != "" || f.TargetBranch != "" || f.Draft || f.NotDraft) {
		return invalid()
	}
	if f.Draft && f.NotDraft {
		return invalid()
	}
	for _, branch := range []string{f.SourceBranch, f.TargetBranch} {
		if branch != "" {
			if err := safeurl.ValidateBranch(branch); err != nil {
				return invalid()
			}
		}
	}
	return nil
}

func asciiAlnum(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func literalFilter(value string) bool {
	return validateText(value, "list filter", 255) == nil && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, ",\\\"") && !strings.ContainsFunc(value, unicode.IsControl) &&
		!strings.HasPrefix(value, "-") && !strings.EqualFold(value, "none") && !strings.EqualFold(value, "any") &&
		!strings.EqualFold(value, "upcoming") && !strings.EqualFold(value, "started")
}

func (f ListFilters) argv(operation Operation) ([]string, error) {
	if err := f.Validate(operation); err != nil {
		return nil, err
	}
	var args []string
	switch f.State {
	case "all":
		args = append(args, "--all")
	case "closed":
		args = append(args, "--closed")
	case "merged":
		args = append(args, "--merged")
	}
	for _, label := range f.Labels {
		args = append(args, "--label="+label)
	}
	for _, flag := range []struct{ name, value string }{
		{"--author", f.Author}, {"--assignee", f.Assignee}, {"--milestone", f.Milestone},
		{"--source-branch", f.SourceBranch}, {"--target-branch", f.TargetBranch},
	} {
		if flag.value != "" {
			args = append(args, flag.name+"="+flag.value)
		}
	}
	if f.Sort != "" {
		args = append(args, "--order="+f.Sort+"_at", "--sort=desc")
	}
	if f.Draft {
		args = append(args, "--draft")
	}
	if f.NotDraft {
		args = append(args, "--not-draft")
	}
	return args, nil
}
