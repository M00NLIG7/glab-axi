package product

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

const readSelectionDetails = "Selection changes only optional fields; required identity/state fields and validation always remain.\nDefaults are unchanged: lists omit description, views include it up to 131072 UTF-8 bytes.\nUse --fields description,labels to opt into list bodies or select view fields, and --body-limit to lower the cap.\nmeta.complete describes the item set; meta.truncated also reports field cuts. Provider page/byte/time bounds always apply.\nSee docs/read-parity.md for filter semantics, field names, and remaining reference differences."

func readListFlags(mr bool) []FlagDefinition {
	state := "open|opened|closed|all"
	if mr {
		state = "open|opened|closed|merged|all"
	}
	flags := []FlagDefinition{
		{Name: "--state", Value: state, Description: "GitLab state; defaults to opened. Closed excludes merged MRs."},
		{Name: "--label", Value: "NAME", Description: "Require every exact label; repeat up to 20 times. No commas or provider selector keywords.", Repeatable: true},
		{Name: "--author", Value: "USERNAME", Description: "Exact author username (not @me)."},
		{Name: "--assignee", Value: "USERNAME", Description: "Exact assignee username (not @me)."},
		{Name: "--milestone", Value: "TITLE", Description: "Exact milestone title, not provider selector keywords."},
	}
	if mr {
		flags = append(flags,
			FlagDefinition{Name: "--source-branch", Value: "BRANCH", Description: "Exact source branch."},
			FlagDefinition{Name: "--target-branch", Value: "BRANCH", Description: "Exact target branch."},
			FlagDefinition{Name: "--draft", Boolean: true, Description: "Only draft MRs; mutually exclusive with --not-draft."},
			FlagDefinition{Name: "--not-draft", Boolean: true, Description: "Only non-draft MRs."})
	} else {
		flags = append(flags, FlagDefinition{Name: "--sort", Value: "created|updated", Description: "Descending creation or update time. Comment-count sorting is not supported."})
	}
	return append(flags, readSelectionFlags(mr)...)
}

func readSelectionFlags(mr bool) []FlagDefinition {
	fields := "description,author,labels,created_at,updated_at"
	if mr {
		fields += ",base_sha,head_sha,head_pipeline,raw_merge_status"
	}
	return []FlagDefinition{
		{Name: "--fields", Value: "FIELD,...", Description: "Select optional fields: " + fields + ". Required identity/state fields always remain. Omit for existing defaults."},
		{Name: "--body-limit", Value: "BYTES", Description: "Description cap 0..131072 UTF-8 bytes (default 131072); list requires --fields description. Never raises provider/operation bounds."},
	}
}

func listFilters(parsed Parsed) glab.ListFilters {
	return glab.ListFilters{
		State: parsed.Values["--state"], Labels: parsed.MultiValues["--label"],
		Author: parsed.Values["--author"], Assignee: parsed.Values["--assignee"], Milestone: parsed.Values["--milestone"],
		Sort: parsed.Values["--sort"], SourceBranch: parsed.Values["--source-branch"], TargetBranch: parsed.Values["--target-branch"],
		Draft: parsed.Booleans["--draft"], NotDraft: parsed.Booleans["--not-draft"],
	}
}

type readSelection struct {
	fields    map[string]bool // nil preserves the pre-existing defaults
	body      bool
	bodyLimit int
}

func parseReadSelection(parsed Parsed) (readSelection, error) {
	selection := readSelection{body: parsed.Definition.Path[1] == "view", bodyLimit: limits.MaxDescriptionBytes}
	if raw := parsed.Values["--fields"]; raw != "" {
		allowed := map[string]bool{"description": true, "author": true, "labels": true, "created_at": true, "updated_at": true}
		if parsed.Definition.Path[0] == "mr" {
			for _, field := range []string{"base_sha", "head_sha", "head_pipeline", "raw_merge_status"} {
				allowed[field] = true
			}
		}
		selection.fields = map[string]bool{}
		for _, field := range strings.Split(raw, ",") {
			if !allowed[field] || selection.fields[field] {
				return selection, uxv1.NewError(uxv1.CodeValidation, "--fields must contain distinct supported optional field names")
			}
			selection.fields[field] = true
		}
		selection.body = selection.fields["description"]
	}
	if raw := parsed.Values["--body-limit"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > limits.MaxDescriptionBytes || strconv.Itoa(n) != raw || !selection.body {
			return selection, uxv1.NewError(uxv1.CodeValidation, "--body-limit requires a selected description and a canonical byte count from 0 through 131072")
		}
		selection.bodyLimit = n
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

// Body selection is a local rendering choice, never additional upstream argv.
// Even very small caps remain byte-bounded, including truncation markers.
func (s readSelection) description(raw string) (string, bool, error) {
	if !s.body {
		return "", false, nil
	}
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, '\x00') {
		return "", false, malformed("description")
	}
	if len(raw) <= s.bodyLimit {
		return raw, false, nil
	}
	marker := "…[truncated]"
	if len(marker) > s.bodyLimit {
		marker = ""
	}
	cut := s.bodyLimit - len(marker)
	for cut > 0 && !utf8.RuneStart(raw[cut]) {
		cut--
	}
	return raw[:cut] + marker, true, nil
}

func (s readSelection) keep(field string) bool { return s.fields == nil || s.fields[field] }

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
	if !s.keep("author") {
		out.Author = ""
	}
	if !s.keep("labels") {
		out.Labels = nil
	}
	if !s.keep("created_at") {
		out.CreatedAt = nil
	}
	if !s.keep("updated_at") {
		out.UpdatedAt = nil
	}
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
	if !s.keep("author") {
		out.Author = ""
	}
	if !s.keep("labels") {
		out.Labels = nil
	}
	if !s.keep("created_at") {
		out.CreatedAt = nil
	}
	if !s.keep("updated_at") {
		out.UpdatedAt = nil
	}
	if !s.keep("base_sha") {
		out.BaseSHA = ""
	}
	if !s.keep("head_sha") {
		out.HeadSHA = ""
	}
	if !s.keep("head_pipeline") {
		out.HeadPipeline = nil
	}
	if !s.keep("raw_merge_status") {
		out.RawMergeStatus = ""
	}
	return out, cut || bodyCut, err
}

func readResourceIdentity(raw string, target Target, resource string, actual, expected int64) error {
	parsed, err := url.Parse(raw)
	path := (&url.URL{Path: "/" + target.Repo + "/-/" + resource + "/" + strconv.FormatInt(actual, 10)}).EscapedPath()
	if err != nil || parsed.EscapedPath() != path || expected != 0 && actual != expected {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different resource identity")
	}
	return nil
}
