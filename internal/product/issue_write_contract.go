package product

import (
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/safeurl"
)

func issueWriteDefinitions() []Definition {
	common := []FlagDefinition{
		{Name: "--expected-project-id", Value: "ID", Description: "Exact numeric project identity, not just its mutable path.", Required: true},
		{Name: "--expected-url", Value: "URL", Description: "Exact canonical project URL for create, issue URL otherwise.", Required: true},
	}
	existing := append(append([]FlagDefinition{}, common...), FlagDefinition{Name: "--expected-issue-id", Value: "ID", Description: "Exact global issue ID (distinct from the project IID).", Required: true})
	var out []Definition
	for _, action := range []string{"create", "comment", "note", "close", "reopen"} {
		d := Definition{Path: []string{"issue", action}, RepoMode: RepoRequired, Schema: "issue-write", Backend: "official-glab", Write: true, NoLimit: true, RequireExplicitHost: true, RequireExplicitRepo: true}
		d.Flags = append([]FlagDefinition{}, existing...)
		d.Positionals, d.MaxPositions = 1, 1
		d.Usage = "gl-axi issue " + action + " <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL"
		d.Details = "One mutation attempt per invocation; no blind retry or cross-invocation deduplication.\nNumeric identities and canonical URLs are required. All reads/writes are bounded.\nGitLab supplies no atomic expected revision: preflight checks are observations, not compare-and-swap."
		switch action {
		case "create":
			d.Positionals, d.MaxPositions = 0, 0
			d.Summary = "Create one ordinary issue from private title and description files."
			d.Flags = append(append([]FlagDefinition{}, common...), FlagDefinition{Name: "--title-file", Value: "FILE", Description: "Absolute private UTF-8 title file (at most 1024 bytes).", Required: true}, FlagDefinition{Name: "--description-file", Value: "FILE", Description: "Absolute private UTF-8 description file (at most 131072 bytes).", Required: true})
			d.Usage = "gl-axi issue create -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-url PROJECT_URL --title-file FILE --description-file FILE"
			d.Details += "\nNo title search or replay inference: a lost response is ambiguous, and another invocation can create a duplicate."
		case "comment", "note":
			d.Summary = "Create one plain issue note (comment and note are aliases)."
			d.Flags = append(d.Flags, FlagDefinition{Name: "--body-file", Value: "FILE", Description: "Absolute private UTF-8 nonempty body file (at most 131072 bytes).", Required: true})
			d.Usage += " --body-file FILE"
			d.Details += "\nOnly the direct create response can identify this note. Never searches the latest comment as proof.\nA lost response is ambiguous; another invocation can create a duplicate."
		case "close", "reopen":
			d.Summary = "Request one reversible GitLab issue state transition."
			d.Flags = append(d.Flags, FlagDefinition{Name: "--expected-state", Value: "STATE", Description: "Observed preflight state: opened or closed; NOT a server-side precondition.", Required: true})
			d.Usage += " --expected-state opened|closed"
			d.Details += "\nReturns unchanged only if the bound preflight state already matches.\nSuccess reports the desired state observed after an accepted response, not exclusive authorship.\nNo GitHub close reason and no bundled comment. A lost response stays ambiguous even when the desired state is observed."
		}
		if action == "create" || action == "comment" || action == "note" {
			d.Details += "\nQuick-action-shaped lines (including in code blocks) are rejected before child work, not executed. No attachments or secondary writes."
		}
		d.Usage += " [--format toon|json]"
		out = append(out, d)
	}
	return out
}

func isIssueWrite(parsed Parsed) bool {
	if len(parsed.Definition.Path) != 2 || parsed.Definition.Path[0] != "issue" {
		return false
	}
	switch parsed.Definition.Path[1] {
	case "create", "comment", "note", "close", "reopen":
		return true
	default:
		return false
	}
}

func issueWriteID(raw, flag string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, flag+" must be a canonical positive integer")
	}
	return id, nil
}

func validateIssueWriteParsed(parsed Parsed) error {
	if err := safeurl.ValidateHost(parsed.Values["--hostname"]); err != nil {
		return uxv1.NewError(uxv1.CodeValidation, "invalid GitLab hostname")
	}
	if err := safeurl.ValidateProject(parsed.Values["--repo"]); err != nil {
		return uxv1.NewError(uxv1.CodeValidation, "invalid repository target")
	}
	if _, err := issueWriteID(parsed.Values["--expected-project-id"], "--expected-project-id"); err != nil {
		return err
	}
	action := parsed.Definition.Path[1]
	expectedURL := canonicalProjectURL(parsed.Values["--hostname"], parsed.Values["--repo"])
	if action != "create" {
		iid, err := issueEditIID(parsed)
		if err != nil {
			return err
		}
		if _, err := issueWriteID(parsed.Values["--expected-issue-id"], "--expected-issue-id"); err != nil {
			return err
		}
		expectedURL = canonicalIssueURL(parsed.Values["--hostname"], parsed.Values["--repo"], iid)
	}
	if parsed.Values["--expected-url"] != expectedURL {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url does not exactly match the selected target")
	}
	if action == "close" || action == "reopen" {
		if s := parsed.Values["--expected-state"]; s != "opened" && s != "closed" {
			return uxv1.NewError(uxv1.CodeValidation, "--expected-state must be opened or closed")
		}
	}
	return nil
}

// GitLab executes slash commands in descriptions and notes. Conservatively
// reject every slash-leading line, even in Markdown fences, rather than trying
// to reproduce the provider's evolving Markdown/quick-action parser.
func validateIssueWriteBody(body string) error {
	for _, line := range strings.FieldsFunc(body, func(r rune) bool { return r == '\n' || r == '\r' || r == '\u0085' || r == '\u2028' || r == '\u2029' }) {
		if strings.HasPrefix(strings.TrimSpace(line), "/") {
			return uxv1.NewError(uxv1.CodeSecurityBoundary, "GitLab quick-action-shaped lines are not allowed in issue write content")
		}
	}
	return nil
}
