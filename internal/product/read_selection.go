package product

import (
	"net/url"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

const readSelectionDetails = "Default fields and identity/state validation always remain.\nDefaults are unchanged: lists omit description, views include it up to 131072 UTF-8 bytes.\nmeta.complete describes the item set; meta.truncated also reports field cuts. Provider page/byte/time bounds always apply.\nSee docs/read-parity.md for filter semantics, field names, and remaining reference differences."

func readListFlags(mr bool) []FlagDefinition {
	state := "open|closed|all"
	if mr {
		state = "open|closed|merged|all"
	}
	flags := []FlagDefinition{
		{Name: "--state", Value: state, Description: "GitLab state; defaults to open (returned as opened). Closed excludes merged MRs."},
		{Name: "--label", Value: "NAME", Description: "Require every exact label; repeat up to 20 times. No commas or provider selector keywords.", Repeatable: true},
		{Name: "--author", Value: "USERNAME", Description: "Exact author username (not @me)."},
		{Name: "--assignee", Value: "USERNAME", Description: "Exact assignee username (not @me)."},
	}
	if mr {
		flags = append(flags,
			FlagDefinition{Name: "--source-branch", Value: "BRANCH", Description: "Exact source branch."},
			FlagDefinition{Name: "--target-branch", Value: "BRANCH", Description: "Exact target branch."},
			FlagDefinition{Name: "--draft", Boolean: true, Description: "Only draft MRs; omit to include draft and non-draft MRs."})
	} else {
		flags = append(flags,
			FlagDefinition{Name: "--milestone", Value: "TITLE", Description: "Exact milestone title, not provider selectors (None, Any, Upcoming, Started, #upcoming, #started, No Milestone, Any Milestone)."},
			FlagDefinition{Name: "--sort", Value: "created|updated", Description: "Descending creation or update time. Comment-count sorting is not supported."})
	}
	fields := "description,author,labels,created_at,updated_at"
	if mr {
		fields += ",base_sha,head_sha,head_pipeline,raw_merge_status"
	}
	flags = append(flags, FlagDefinition{Name: "--fields", Value: "FIELD,...", Description: "Add optional fields: " + fields + ". Default fields always remain; description adds list bodies."})
	return flags
}

func listFilters(parsed Parsed) glab.ListFilters {
	return glab.ListFilters{
		State: parsed.Values["--state"], Labels: parsed.MultiValues["--label"],
		Author: parsed.Values["--author"], Assignee: parsed.Values["--assignee"], Milestone: parsed.Values["--milestone"],
		Sort: parsed.Values["--sort"], SourceBranch: parsed.Values["--source-branch"], TargetBranch: parsed.Values["--target-branch"],
		Draft: parsed.Booleans["--draft"],
	}
}

type readSelection struct {
	body bool
}

func parseReadSelection(parsed Parsed) (readSelection, error) {
	selection := readSelection{body: parsed.Definition.Path[1] == "view"}
	if raw := parsed.Values["--fields"]; raw != "" {
		allowed := map[string]bool{"description": true, "author": true, "labels": true, "created_at": true, "updated_at": true}
		if parsed.Definition.Path[0] == "mr" {
			for _, field := range []string{"base_sha", "head_sha", "head_pipeline", "raw_merge_status"} {
				allowed[field] = true
			}
		}
		fields := map[string]bool{}
		for _, field := range strings.Split(raw, ",") {
			if !allowed[field] || fields[field] {
				return selection, uxv1.NewError(uxv1.CodeValidation, "--fields must contain distinct supported optional field names")
			}
			fields[field] = true
		}
		selection.body = fields["description"]
	}
	return selection, nil
}

func validateReadParsed(parsed Parsed) error {
	if parsed.Definition.Path[1] == "list" {
		op := glab.OpIssueList
		if parsed.Definition.Path[0] == "mr" {
			op = glab.OpMRList
		}
		if err := listFilters(parsed).Validate(op); err != nil {
			return err
		}
	} else {
		if _, err := mergeIID(parsed); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "resource IID must be a canonical positive integer")
		}
	}
	_, err := parseReadSelection(parsed)
	return err
}

func (s readSelection) description(raw string) (string, bool, error) {
	if !s.body {
		return "", false, nil
	}
	return boundedText(raw, "description", limits.MaxDescriptionBytes, false)
}

func (s readSelection) issue(item upstreamIssue, target Target, iid int64) (Issue, bool, error) {
	out, cut, err := normalizeIssue(item, target.Host, target.Repo, false)
	if err != nil {
		return Issue{}, false, err
	}
	if err = readResourceIdentity(out.WebURL, target, "issues", out.IID, iid); err != nil {
		return Issue{}, false, err
	}
	description, bodyCut, err := s.description(item.Description)
	out.Description = description
	return out, cut || bodyCut, err
}

func (s readSelection) mr(item upstreamMR, target Target, iid int64, filters glab.ListFilters) (MergeRequest, bool, error) {
	out, cut, err := normalizeMR(item, target.Host, target.Repo, false)
	if err != nil {
		return MergeRequest{}, false, err
	}
	if err = readResourceIdentity(out.WebURL, target, "merge_requests", out.IID, iid); err != nil {
		return MergeRequest{}, false, err
	}
	if filters.SourceBranch != "" && item.SourceBranch != filters.SourceBranch || filters.TargetBranch != "" && item.TargetBranch != filters.TargetBranch {
		return MergeRequest{}, false, uxv1.NewError(uxv1.CodeSafety, "official glab returned a merge request outside the selected branches")
	}
	description, bodyCut, err := s.description(item.Description)
	out.Description = description
	return out, cut || bodyCut, err
}

func readResourceIdentity(raw string, target Target, resource string, actual, expected int64) error {
	parsed, err := url.Parse(raw)
	if err == nil && (expected == 0 || actual == expected) {
		resources := []string{resource}
		if resource == "issues" {
			resources = append(resources, "work_items")
		}
		for _, kind := range resources {
			path := (&url.URL{Path: "/" + target.Repo + "/-/" + kind + "/" + strconv.FormatInt(actual, 10)}).EscapedPath()
			if parsed.EscapedPath() == path {
				return nil
			}
		}
	}
	return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different resource identity")
}
