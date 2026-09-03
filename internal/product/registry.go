// Package product implements the human/agent-facing bounded hybrid command
// surface. The frozen native contract is routed before this package.
package product

import (
	"fmt"
	"sort"
	"strings"
)

type Definition struct {
	Path                []string
	Summary             string
	Details             string
	Usage               string
	Examples            []string
	RepoMode            RepoMode
	Positionals         int
	MaxPositions        int
	Flags               []FlagDefinition
	Schema              string
	Backend             string
	Write               bool
	NoLimit             bool
	RequireExplicitHost bool
	RequireExplicitRepo bool
}

type RepoMode int

const (
	RepoNone RepoMode = iota
	RepoOptional
	RepoRequired
)

type FlagDefinition struct {
	Name        string
	Value       string
	Description string
	Boolean     bool
	Required    bool
	Repeatable  bool
}

var definitions = []Definition{
	{Path: nil, Summary: "Show a bounded current-project dashboard.", Usage: "gl-axi [global flags]", RepoMode: RepoRequired, Schema: "dashboard", Backend: "official-glab"},
	{Path: []string{"auth", "login"}, Summary: "Authenticate through official glab in a human TTY.", Usage: "gl-axi auth login [--hostname HOST]", RepoMode: RepoNone, Schema: "auth-login", Backend: "official-glab"},
	{Path: []string{"auth", "status"}, Summary: "Check official-glab authentication without displaying a token.", Usage: "gl-axi auth status [--hostname HOST]", RepoMode: RepoNone, Schema: "auth-status", Backend: "official-glab"},
	{Path: []string{"issue", "list"}, Summary: "List project issues.", Usage: "gl-axi issue list [global flags]", RepoMode: RepoRequired, Schema: "issue-list", Backend: "official-glab"},
	{Path: []string{"issue", "view"}, Summary: "View one project issue.", Usage: "gl-axi issue view <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "issue-view", Backend: "official-glab"},
	{Path: []string{"issue", "edit"}, Summary: "Validate one exact project issue edit without mutation.", Details: "Requires caller-bound URL, state, and updated-at evidence.\nTitle and description are accepted only through private files.\nLabels are resolved exactly and unrelated labels are previewed as preserved.\nDry-run returns the complete validated preview. GitLab accepts no expected issue revision and only label names, so a non-no-op live request returns a bounded safety refusal and sends no PUT.", Usage: "gl-axi issue edit <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-state opened|closed --expected-updated-at TIMESTAMP [--title-file FILE] [--description-file FILE] [--add-label NAME]... [--remove-label NAME]... [--dry-run] [--format toon|json]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Flags: issueEditFlags(), Schema: "issue-edit", Backend: "official-glab", NoLimit: true, RequireExplicitHost: true, RequireExplicitRepo: true},
	{Path: []string{"mr", "list"}, Summary: "List project merge requests.", Usage: "gl-axi mr list [global flags]", RepoMode: RepoRequired, Schema: "mr-list", Backend: "official-glab"},
	{Path: []string{"mr", "view"}, Summary: "View one merge request.", Usage: "gl-axi mr view <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-view", Backend: "official-glab"},
	{Path: []string{"mr", "checks"}, Summary: "View the head pipeline and jobs for one merge request.", Usage: "gl-axi mr checks <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-checks", Backend: "official-glab"},
	{Path: []string{"mr", "discussions"}, Summary: "View bounded, read-only discussion evidence for one merge request.", Details: "Includes canonical source/target project identity and exact base/head binding.\nThe limit counts threads. Provider thread/note order is preserved.\nCompleteness is fail-closed and identity is rechecked around pagination.\nNo reply, resolve, or other mutation is exposed.", Usage: "gl-axi mr discussions <iid> [global flags]", Examples: []string{"gl-axi mr discussions 42 -R group/project --hostname gitlab.com --limit 1000 --format json"}, RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-discussions", Backend: "official-glab"},
	{Path: []string{"mr", "diff"}, Summary: "View a bounded, color-free merge-request diff.", Usage: "gl-axi mr diff <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-diff", Backend: "official-glab"},
	{Path: []string{"mr", "merge"}, Summary: "Immediately squash-merge one exact green merge request.", Usage: "gl-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Flags: mergeFlags(), Schema: "mr-merge", Backend: "official-glab", Write: true, NoLimit: true, RequireExplicitHost: true, RequireExplicitRepo: true},
	{Path: []string{"mr", "ensure"}, Summary: "Create or update exactly one matching open merge request.", Usage: "gl-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]", RepoMode: RepoRequired, Flags: ensureFlags(), Schema: "mr-ensure", Backend: "official-glab", Write: true},
	{Path: []string{"mr", "create-or-update"}, Summary: "Alias for bounded MR ensure semantics.", Usage: "gl-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]", RepoMode: RepoRequired, Flags: ensureFlags(), Schema: "mr-ensure", Backend: "official-glab", Write: true},
	{Path: []string{"pipeline", "list"}, Summary: "List project pipelines.", Usage: "gl-axi pipeline list [global flags]", RepoMode: RepoRequired, Schema: "pipeline-list", Backend: "official-glab"},
	{Path: []string{"pipeline", "view"}, Summary: "View one pipeline.", Usage: "gl-axi pipeline view <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "pipeline-view", Backend: "official-glab"},
	{Path: []string{"job", "list"}, Summary: "List jobs for one pipeline.", Usage: "gl-axi job list --pipeline-id ID [global flags]", RepoMode: RepoRequired, Flags: []FlagDefinition{{Name: "--pipeline-id", Value: "ID", Description: "Pipeline ID."}}, Schema: "job-list", Backend: "official-glab"},
	{Path: []string{"job", "view"}, Summary: "View one CI/CD job.", Usage: "gl-axi job view <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "job-view", Backend: "official-glab"},
	{Path: []string{"job", "trace"}, Summary: "View a bounded, redacted tail of one job trace.", Usage: "gl-axi job trace <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "job-trace", Backend: "official-glab"},
	{Path: []string{"release", "list"}, Summary: "List project releases and bounded download metadata.", Usage: "gl-axi release list [global flags]", RepoMode: RepoRequired, Schema: "release-list", Backend: "official-glab"},
	{Path: []string{"release", "view"}, Summary: "View a release and project-bound download metadata (latest when omitted).", Usage: "gl-axi release view [tag] [global flags]", RepoMode: RepoRequired, MaxPositions: 1, Schema: "release-view", Backend: "official-glab"},
	{Path: []string{"repo", "list"}, Summary: "List repositories visible to the official profile.", Usage: "gl-axi repo list [--hostname HOST] [--limit N]", RepoMode: RepoNone, Schema: "repo-list", Backend: "official-glab"},
	{Path: []string{"repo", "view"}, Summary: "View a project/repository.", Usage: "gl-axi repo view [namespace/project] [global flags]", RepoMode: RepoOptional, MaxPositions: 1, Schema: "repo-view", Backend: "official-glab"},
	{Path: []string{"label", "list"}, Summary: "List project labels.", Usage: "gl-axi label list [global flags]", RepoMode: RepoRequired, Schema: "label-list", Backend: "official-glab"},
	{Path: []string{"search", "issues"}, Summary: "Search issues in one project.", Usage: "gl-axi search issues <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "mrs"}, Summary: "Search merge requests in one project.", Usage: "gl-axi search mrs <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "repos"}, Summary: "Search projects/repositories on one host.", Usage: "gl-axi search repos <query> [--hostname HOST] [--limit N]", RepoMode: RepoNone, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "commits"}, Summary: "Search commits in one project.", Usage: "gl-axi search commits <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "code"}, Summary: "Search code blobs in one project.", Usage: "gl-axi search code <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"setup", "hooks"}, Summary: "Install or repair generated Agent Skill and session hooks.", Usage: "gl-axi setup hooks", RepoMode: RepoNone, Schema: "setup-hooks", Backend: "local"},
	{Path: []string{"update"}, Summary: "Check for or install a signed gl-axi release.", Usage: "gl-axi update [--check]", RepoMode: RepoNone, Flags: []FlagDefinition{{Name: "--check", Description: "Check only; do not replace the executable.", Boolean: true}}, Schema: "update", Backend: "local"},
}

func issueEditFlags() []FlagDefinition {
	return []FlagDefinition{
		{Name: "--title-file", Value: "FILE", Description: "Absolute mode-0600 title file."},
		{Name: "--description-file", Value: "FILE", Description: "Absolute mode-0600 description file."},
		{Name: "--add-label", Value: "NAME", Description: "Exact available label name to add; repeatable.", Repeatable: true},
		{Name: "--remove-label", Value: "NAME", Description: "Exact available label name to remove; repeatable.", Repeatable: true},
		{Name: "--expected-url", Value: "URL", Description: "Exact canonical issue URL.", Required: true},
		{Name: "--expected-state", Value: "STATE", Description: "Exact current state: opened or closed.", Required: true},
		{Name: "--expected-updated-at", Value: "TIMESTAMP", Description: "Exact current RFC 3339 updated-at value.", Required: true},
		{Name: "--dry-run", Description: "Return the validated preview; live changes are unavailable without an atomic provider precondition.", Boolean: true},
	}
}

func mergeFlags() []FlagDefinition {
	return []FlagDefinition{
		{Name: "--expected-url", Value: "URL", Description: "Exact canonical merge-request URL.", Required: true},
		{Name: "--expected-source", Value: "BRANCH", Description: "Exact reviewed source branch.", Required: true},
		{Name: "--expected-target", Value: "BRANCH", Description: "Exact reviewed target branch.", Required: true},
		{Name: "--expected-head", Value: "SHA", Description: "Reviewed lowercase 40- or 64-hex source head.", Required: true},
		{Name: "--authority", Value: "CLASS", Description: "Firstmate authority: captain-explicit or standing-yolo-green.", Required: true},
		{Name: "--squash", Description: "Require the sole supported immediate squash strategy.", Boolean: true, Required: true},
	}
}

func ensureFlags() []FlagDefinition {
	return []FlagDefinition{
		{Name: "--source", Value: "BRANCH", Description: "Source branch."},
		{Name: "--target", Value: "BRANCH", Description: "Target branch."},
		{Name: "--title-file", Value: "FILE", Description: "Absolute mode-0600 title file."},
		{Name: "--description-file", Value: "FILE", Description: "Absolute mode-0600 description file."},
	}
}

func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

func lookupDefinition(path []string) (Definition, bool) {
	for _, definition := range definitions {
		if strings.Join(definition.Path, " ") == strings.Join(path, " ") {
			return definition, true
		}
	}
	return Definition{}, false
}

func isParent(path []string) bool {
	for _, definition := range definitions {
		if len(definition.Path) > len(path) && strings.Join(definition.Path[:len(path)], " ") == strings.Join(path, " ") {
			return true
		}
	}
	return false
}

func TopHelp() string {
	groups := make(map[string][]Definition)
	for _, definition := range definitions {
		if len(definition.Path) == 0 {
			continue
		}
		groups[definition.Path[0]] = append(groups[definition.Path[0]], definition)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	out.WriteString("gl-axi - bounded GitLab experience for humans and agents\n\n")
	out.WriteString("Usage:\n  gl-axi [command] [args] [flags]\n\nCommands:\n")
	out.WriteString("  (none)       bounded current-project dashboard\n")
	for _, name := range names {
		leaves := make([]string, 0, len(groups[name]))
		topSummary := ""
		for _, definition := range groups[name] {
			if len(definition.Path) == 1 {
				topSummary = definition.Summary
			} else {
				leaves = append(leaves, definition.Path[1])
			}
		}
		if topSummary != "" {
			out.WriteString(fmt.Sprintf("  %-12s %s\n", name, topSummary))
			continue
		}
		sort.Strings(leaves)
		out.WriteString(fmt.Sprintf("  %-12s %s\n", name, strings.Join(leaves, ", ")))
	}
	out.WriteString("\nCommon flags (after the command; leaf help is authoritative):\n")
	out.WriteString("  -R, --repo NAMESPACE/PROJECT   select a repository (space or equals form)\n")
	out.WriteString("      --hostname HOST           select a GitLab host (or GITLAB_HOST)\n")
	out.WriteString("      --limit N                 display at most N items (hard maximum 1000)\n")
	out.WriteString("      --format toon|json        output format (default toon)\n")
	out.WriteString("  -h, --help                    show contextual help\n")
	out.WriteString("  -v, -V, --version             show version (the long form preserves the v1 handshake)\n")
	out.WriteString("\nBackends:\n  bounded product operations and human login use pinned official glab 1.112.0 (816e3a52);\n  exact glab-axi/v1 automation remains a standalone native backend.\n")
	out.WriteString("\nSecurity boundary:\n  only MR ensure and guarded immediate squash merge may write;\n  issue edit validates and previews but refuses live mutation because GitLab has no enforceable issue revision;\n  no generic API, approve, comment/reply/resolve, close/reopen/delete,\n  label-resource or MR-label mutation, repository/release mutation,\n  secrets/variables, pipeline mutation, or alternate merge strategy.\n")
	return out.String()
}

func Help(path []string) (string, bool) {
	if len(path) == 0 {
		return TopHelp(), true
	}
	if definition, ok := lookupDefinition(path); ok {
		return leafHelp(definition), true
	}
	if !isParent(path) {
		return "", false
	}
	var children []Definition
	for _, definition := range definitions {
		if len(definition.Path) == len(path)+1 && strings.Join(definition.Path[:len(path)], " ") == strings.Join(path, " ") {
			children = append(children, definition)
		}
	}
	sort.Slice(children, func(i, j int) bool { return strings.Join(children[i].Path, " ") < strings.Join(children[j].Path, " ") })
	var out strings.Builder
	out.WriteString("Usage:\n  gl-axi " + strings.Join(path, " ") + " <command> [flags]\n\nCommands:\n")
	for _, child := range children {
		out.WriteString(fmt.Sprintf("  %-18s %s\n", child.Path[len(child.Path)-1], child.Summary))
	}
	out.WriteString("\nRun gl-axi " + strings.Join(path, " ") + " <command> --help for details.\n")
	return out.String(), true
}

func CommandReferenceMarkdown() string {
	var out strings.Builder
	out.WriteString("# Command reference\n\nThis file is generated from the executable command registry.\n\n")
	for _, definition := range definitions {
		if len(definition.Path) == 0 {
			out.WriteString("## Dashboard\n\n")
		} else {
			out.WriteString("## `" + strings.Join(definition.Path, " ") + "`\n\n")
		}
		out.WriteString("```text\n" + definition.Usage + "\n```\n\n" + definition.Summary + "\n\n")
		if definition.Details != "" {
			out.WriteString(definition.Details + "\n\n")
		}
		if definition.Backend != "" {
			out.WriteString("Backend: `" + definition.Backend + "`. Schema: `schema/ux-v1/" + definition.Schema + ".schema.json`.\n\n")
		}
		if len(definition.Examples) > 0 {
			out.WriteString("Examples:\n\n```text\n" + strings.Join(definition.Examples, "\n") + "\n```\n\n")
		}
	}
	out.WriteString("## Permanent denials\n\nGeneric API, every live issue mutation, unguarded or alternate-strategy merge, approve, comment/note/reply/resolve, merge-request or label-resource mutation, close/reopen/delete, repository mutation, release mutation, secrets/variables, and pipeline/job mutation are denied. `issue edit --dry-run` retains exact-identity validation and preview, while non-no-op live requests fail closed before PUT.\n")
	return out.String()
}

// SkillMarkdown is generated from the same registry used by executable help.
// It intentionally teaches only declared reads and pinned guarded writes.
func SkillMarkdown() string {
	var out strings.Builder
	out.WriteString("---\nname: gl-axi\ndescription: Use bounded GitLab reads, exact-identity issue-edit preview, idempotent MR ensure, and guarded exact-head squash merge without generic API authority.\n---\n\n")
	out.WriteString("# gl-axi\n\nUse `gl-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.\n\n## Commands\n\n")
	for _, definition := range definitions {
		if len(definition.Path) == 0 || strings.Join(definition.Path, " ") == "auth login" || strings.Join(definition.Path, " ") == "setup hooks" || strings.Join(definition.Path, " ") == "update" {
			continue
		}
		out.WriteString("- `" + definition.Usage + "` - " + definition.Summary + "\n")
	}
	out.WriteString("\n## Safety\n\n- Ask a human to run `gl-axi auth login`; never drive login from an agent or request a token.\n- Use explicit `-R namespace/project --hostname host` for issue-edit preview and guarded merge.\n- Do not attempt generic API, live issue mutation, alternate merge strategies, approve, comment/note/reply/resolve, close/reopen/delete, label-resource or MR-label mutation, repository/release writes, secrets/variables, or pipeline mutations.\n- `issue edit` requires exact URL/state/updated-at evidence and private content files. Use `--dry-run` for a validated preview; a non-no-op live request returns `safety_violation` with no PUT because GitLab has no enforceable issue revision.\n- `mr ensure` / `mr create-or-update` accepts private title/description files. `mr merge` requires the exact URL, source branch, target branch, reviewed head, authority class, provider-enforced green policy, and `--squash`.\n- Never self-assert `--authority`; invoke guarded merge only through the pinned Firstmate lifecycle boundary after its separately shipped integration.\n- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.\n")
	return out.String()
}

// LegacySkillMarkdown keeps existing glab-axi agent installations functional
// while making the canonical command and migration explicit.
func LegacySkillMarkdown() string {
	legacy := strings.ReplaceAll(SkillMarkdown(), "gl-axi", "glab-axi")
	return strings.Replace(legacy, "# glab-axi\n\n", "# glab-axi compatibility alias\n\n`glab-axi` remains supported with no removal date. Prefer the canonical `gl-axi` command for new configuration.\n\n", 1)
}

func leafHelp(definition Definition) string {
	var out strings.Builder
	out.WriteString("Usage:\n  " + definition.Usage + "\n\n")
	out.WriteString(definition.Summary + "\n")
	if definition.Details != "" {
		out.WriteString(definition.Details + "\n")
	}
	if definition.Backend == "official-glab" {
		out.WriteString("Backend: pinned official glab 1.112.0 (816e3a52); output is bounded and normalized.\n")
	}
	if definition.Write {
		out.WriteString("Write boundary: this is a pinned provider-write contract with command-specific guards.\n")
	}
	if len(definition.Flags) > 0 {
		out.WriteString("\nCommand flags:\n")
		for _, flag := range definition.Flags {
			name := flag.Name
			if flag.Value != "" {
				name += " " + flag.Value
			}
			out.WriteString(fmt.Sprintf("  %-28s %s\n", name, flag.Description))
		}
	}
	if strings.Join(definition.Path, " ") == "auth login" {
		out.WriteString("\nTarget flag:\n      --hostname HOST\n")
	} else if definition.RepoMode != RepoNone {
		out.WriteString("\nTarget/output flags:\n  -R, --repo NAMESPACE/PROJECT\n      --hostname HOST\n")
		if !definition.NoLimit {
			out.WriteString("      --limit N\n")
		}
		out.WriteString("      --format toon|json\n")
	} else {
		out.WriteString("\nOutput flags:\n      --hostname HOST\n      --limit N\n      --format toon|json\n")
	}
	out.WriteString("  -h, --help\n")
	if len(definition.Examples) > 0 {
		out.WriteString("\nExamples:\n")
		for _, example := range definition.Examples {
			out.WriteString("  " + example + "\n")
		}
	}
	return out.String()
}
