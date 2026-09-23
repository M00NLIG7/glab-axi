package product

import (
	"net/url"
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
		d := Definition{Path: []string{"issue", action}, RepoMode: RepoRequired, Schema: "issue-write", Backend: "native", Write: true, NoLimit: true, RequireExplicitHost: true, RequireExplicitRepo: true, NativeAuth: true, RequireNativeAuth: true}
		d.Flags = append([]FlagDefinition{}, existing...)
		d.Positionals, d.MaxPositions = 1, 1
		d.Usage = "gl-axi issue " + action + " <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL"
		d.Details = "At most one mutation attempt per invocation; no blind retry or cross-invocation deduplication.\nNumeric identities and canonical URLs are required. All reads/writes are bounded.\nGitLab supplies no atomic expected revision: preflight checks are observations, not compare-and-swap."
		switch action {
		case "create":
			d.Positionals, d.MaxPositions = 0, 0
			d.Summary = "Create one ordinary issue from private title and nonblank description files."
			d.Flags = append(append([]FlagDefinition{}, common...), FlagDefinition{Name: "--title-file", Value: "FILE", Description: "Absolute private UTF-8 title file (at most 1024 bytes).", Required: true}, FlagDefinition{Name: "--description-file", Value: "FILE", Description: "Absolute private UTF-8 nonblank description file (at most 131072 bytes).", Required: true})
			d.Usage = "gl-axi issue create -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-url PROJECT_URL --title-file FILE --description-file FILE"
			d.Details += "\nBlank descriptions and title-only creation are temporarily refused before credentials or HTTP because default templates may execute quick actions.\nTitles strip surrounding ASCII whitespace after the original file limit is enforced; internal and Unicode whitespace remain unchanged.\nNo title search or replay inference: a lost response is ambiguous, and another invocation can create a duplicate."
		case "comment", "note":
			d.Summary = "Create one plain issue note (comment and note are aliases)."
			d.Flags = append(d.Flags, FlagDefinition{Name: "--body-file", Value: "FILE", Description: "Absolute private UTF-8 nonempty body file (at most 131072 bytes).", Required: true})
			d.Usage += " --body-file FILE"
			d.Details += "\nOnly the direct create response can identify this note. Never searches the latest comment as proof.\nA lost response is ambiguous; another invocation can create a duplicate."
		case "close", "reopen":
			d.Summary = "Observe an already-matching issue state; transitions are temporarily refused."
			d.Flags = append(d.Flags, FlagDefinition{Name: "--expected-state", Value: "STATE", Description: "Observed preflight state: opened or closed; NOT a server-side precondition.", Required: true})
			d.Usage += " --expected-state opened|closed"
			d.Details += "\nReturns unchanged only if the bound preflight state already matches. This is a read-only observation.\nOtherwise returns unsupported with a refused receipt and zero mutation attempts: GitLab state updates can rewrite existing content.\nExisting descriptions, including fenced code, are not filtered. No PUT, GitHub close reason or bundled comment."
		}
		if action == "create" || action == "comment" || action == "note" {
			d.Details += "\nNew quick-action-shaped lines (including in code blocks) are rejected before credential resolution. No attachments or secondary writes.\nNew descriptions and notes remove carriage returns and trailing ASCII whitespace before hashing and submission; direct response content must match exactly."
		}
		d.Usage += " --auth-source native [--format toon|json]"
		d.Details += "\nNative opt-in uses the existing environment/keyring identity for the full operation, never the official profile. The accounts may differ. Native persisted-config/self-managed mapping on Windows remains unproven."
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
	expectedSuffix := "/" + parsed.Values["--repo"]
	if action != "create" {
		iid, err := issueEditIID(parsed)
		if err != nil {
			return err
		}
		if _, err := issueWriteID(parsed.Values["--expected-issue-id"], "--expected-issue-id"); err != nil {
			return err
		}
		expectedSuffix += "/-/issues/" + strconv.FormatInt(iid, 10)
	}
	// Native configuration may bind a distinct web host and path prefix. Reject
	// malformed/wrong-resource selectors here, then compare the full canonical
	// URL with the resolved authority before the first HTTP request.
	expected, err := url.Parse(parsed.Values["--expected-url"])
	if err != nil || expected.Scheme != "https" || expected.Host == "" || expected.User != nil || expected.RawQuery != "" || expected.Fragment != "" || !strings.HasSuffix(expected.EscapedPath(), expectedSuffix) || safeurl.ValidateHost(expected.Host) != nil {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url must be an exact HTTPS URL for the selected resource")
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
