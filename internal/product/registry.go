// Package product implements the human/agent-facing bounded hybrid command
// surface. The frozen native contract is routed before this package.
package product

import (
	"fmt"
	"sort"
	"strings"
)

type Definition struct {
	Path         []string
	Summary      string
	Usage        string
	Examples     []string
	RepoMode     RepoMode
	Positionals  int
	MaxPositions int
	Flags        []FlagDefinition
	Schema       string
	Backend      string
	Write        bool
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
}

var definitions = []Definition{
	{Path: nil, Summary: "Show a bounded current-project dashboard.", Usage: "glab-axi [global flags]", RepoMode: RepoRequired, Schema: "dashboard", Backend: "official-glab"},
	{Path: []string{"auth", "login"}, Summary: "Authenticate through official glab in a human TTY.", Usage: "glab-axi auth login [--hostname HOST]", RepoMode: RepoNone, Schema: "auth-login", Backend: "official-glab"},
	{Path: []string{"auth", "status"}, Summary: "Check official-glab authentication without displaying a token.", Usage: "glab-axi auth status [--hostname HOST]", RepoMode: RepoNone, Schema: "auth-status", Backend: "official-glab"},
	{Path: []string{"issue", "list"}, Summary: "List project issues.", Usage: "glab-axi issue list [global flags]", RepoMode: RepoRequired, Schema: "issue-list", Backend: "official-glab"},
	{Path: []string{"issue", "view"}, Summary: "View one project issue.", Usage: "glab-axi issue view <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "issue-view", Backend: "official-glab"},
	{Path: []string{"mr", "list"}, Summary: "List project merge requests.", Usage: "glab-axi mr list [global flags]", RepoMode: RepoRequired, Schema: "mr-list", Backend: "official-glab"},
	{Path: []string{"mr", "view"}, Summary: "View one merge request.", Usage: "glab-axi mr view <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-view", Backend: "official-glab"},
	{Path: []string{"mr", "checks"}, Summary: "View the head pipeline and jobs for one merge request.", Usage: "glab-axi mr checks <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-checks", Backend: "official-glab"},
	{Path: []string{"mr", "diff"}, Summary: "View a bounded, color-free merge-request diff.", Usage: "glab-axi mr diff <iid> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "mr-diff", Backend: "official-glab"},
	{Path: []string{"mr", "ensure"}, Summary: "Create or update exactly one matching open merge request.", Usage: "glab-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]", RepoMode: RepoRequired, Flags: ensureFlags(), Schema: "mr-ensure", Backend: "official-glab", Write: true},
	{Path: []string{"mr", "create-or-update"}, Summary: "Alias for bounded MR ensure semantics.", Usage: "glab-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]", RepoMode: RepoRequired, Flags: ensureFlags(), Schema: "mr-ensure", Backend: "official-glab", Write: true},
	{Path: []string{"pipeline", "list"}, Summary: "List project pipelines.", Usage: "glab-axi pipeline list [global flags]", RepoMode: RepoRequired, Schema: "pipeline-list", Backend: "official-glab"},
	{Path: []string{"pipeline", "view"}, Summary: "View one pipeline.", Usage: "glab-axi pipeline view <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "pipeline-view", Backend: "official-glab"},
	{Path: []string{"job", "list"}, Summary: "List jobs for one pipeline.", Usage: "glab-axi job list --pipeline-id ID [global flags]", RepoMode: RepoRequired, Flags: []FlagDefinition{{Name: "--pipeline-id", Value: "ID", Description: "Pipeline ID."}}, Schema: "job-list", Backend: "official-glab"},
	{Path: []string{"job", "view"}, Summary: "View one CI/CD job.", Usage: "glab-axi job view <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "job-view", Backend: "official-glab"},
	{Path: []string{"job", "trace"}, Summary: "View a bounded, redacted tail of one job trace.", Usage: "glab-axi job trace <id> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "job-trace", Backend: "official-glab"},
	{Path: []string{"release", "list"}, Summary: "List project releases.", Usage: "glab-axi release list [global flags]", RepoMode: RepoRequired, Schema: "release-list", Backend: "official-glab"},
	{Path: []string{"release", "view"}, Summary: "View a release (latest when tag is omitted).", Usage: "glab-axi release view [tag] [global flags]", RepoMode: RepoRequired, MaxPositions: 1, Schema: "release-view", Backend: "official-glab"},
	{Path: []string{"repo", "list"}, Summary: "List repositories visible to the official profile.", Usage: "glab-axi repo list [--hostname HOST] [--limit N]", RepoMode: RepoNone, Schema: "repo-list", Backend: "official-glab"},
	{Path: []string{"repo", "view"}, Summary: "View a project/repository.", Usage: "glab-axi repo view [namespace/project] [global flags]", RepoMode: RepoOptional, MaxPositions: 1, Schema: "repo-view", Backend: "official-glab"},
	{Path: []string{"label", "list"}, Summary: "List project labels.", Usage: "glab-axi label list [global flags]", RepoMode: RepoRequired, Schema: "label-list", Backend: "official-glab"},
	{Path: []string{"search", "issues"}, Summary: "Search issues in one project.", Usage: "glab-axi search issues <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "mrs"}, Summary: "Search merge requests in one project.", Usage: "glab-axi search mrs <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "repos"}, Summary: "Search projects/repositories on one host.", Usage: "glab-axi search repos <query> [--hostname HOST] [--limit N]", RepoMode: RepoNone, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "commits"}, Summary: "Search commits in one project.", Usage: "glab-axi search commits <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"search", "code"}, Summary: "Search code blobs in one project.", Usage: "glab-axi search code <query> [global flags]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1, Schema: "search", Backend: "official-glab"},
	{Path: []string{"setup", "hooks"}, Summary: "Install or repair generated Agent Skill and session hooks.", Usage: "glab-axi setup hooks", RepoMode: RepoNone, Schema: "setup-hooks", Backend: "local"},
	{Path: []string{"update"}, Summary: "Check for or install a signed glab-axi release.", Usage: "glab-axi update [--check]", RepoMode: RepoNone, Flags: []FlagDefinition{{Name: "--check", Description: "Check only; do not replace the executable.", Boolean: true}}, Schema: "update", Backend: "local"},
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
	out.WriteString("glab-axi - bounded GitLab experience for humans and agents\n\n")
	out.WriteString("Usage:\n  glab-axi [command] [args] [flags]\n\nCommands:\n")
	out.WriteString("  (none)       bounded current-project dashboard\n")
	for _, name := range names {
		leaves := make([]string, 0, len(groups[name]))
		for _, definition := range groups[name] {
			if len(definition.Path) == 1 {
				leaves = append(leaves, definition.Path[0])
			} else {
				leaves = append(leaves, definition.Path[1])
			}
		}
		sort.Strings(leaves)
		out.WriteString(fmt.Sprintf("  %-12s %s\n", name, strings.Join(leaves, ", ")))
	}
	out.WriteString("\nGlobal flags (after the command):\n")
	out.WriteString("  -R, --repo NAMESPACE/PROJECT   select a repository (space or equals form)\n")
	out.WriteString("      --hostname HOST           select a GitLab host (or GITLAB_HOST)\n")
	out.WriteString("      --limit N                 display at most N items (hard maximum 1000)\n")
	out.WriteString("      --format toon|json        output format (default toon)\n")
	out.WriteString("  -h, --help                    show contextual help\n")
	out.WriteString("  -v, -V, --version             show version (the long form preserves the v1 handshake)\n")
	out.WriteString("\nBackends:\n  safe reads and human login use pinned official glab 1.112.0; exact\n  glab-axi/v1 automation remains a standalone native backend.\n")
	out.WriteString("\nSecurity boundary:\n  no generic API, merge, approve, comment, close/reopen/delete, repository\n  mutation, release/label mutation, secrets/variables, or pipeline mutation.\n")
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
	out.WriteString("Usage:\n  glab-axi " + strings.Join(path, " ") + " <command> [flags]\n\nCommands:\n")
	for _, child := range children {
		out.WriteString(fmt.Sprintf("  %-18s %s\n", child.Path[len(child.Path)-1], child.Summary))
	}
	out.WriteString("\nRun glab-axi " + strings.Join(path, " ") + " <command> --help for details.\n")
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
		if definition.Backend != "" {
			out.WriteString("Backend: `" + definition.Backend + "`. Schema: `schema/ux-v1/" + definition.Schema + ".schema.json`.\n\n")
		}
	}
	out.WriteString("## Permanent denials\n\nGeneric API, merge, approve, comments/notes, close/reopen/delete, repository mutation, release/label mutation, secrets/variables, and pipeline/job mutation are rejected before child execution.\n")
	return out.String()
}

// SkillMarkdown is generated from the same registry used by executable help.
// It intentionally teaches only declared reads and the single MR ensure write.
func SkillMarkdown() string {
	var out strings.Builder
	out.WriteString("---\nname: glab-axi\ndescription: Use bounded GitLab reads and idempotent MR ensure without generic API or destructive authority.\n---\n\n")
	out.WriteString("# glab-axi\n\nUse `glab-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.\n\n## Commands\n\n")
	for _, definition := range definitions {
		if len(definition.Path) == 0 || strings.Join(definition.Path, " ") == "auth login" || strings.Join(definition.Path, " ") == "setup hooks" || strings.Join(definition.Path, " ") == "update" {
			continue
		}
		out.WriteString("- `" + definition.Usage + "` — " + definition.Summary + "\n")
	}
	out.WriteString("\n## Safety\n\n- Ask a human to run `glab-axi auth login`; never drive login from an agent or request a token.\n- Use `-R namespace/project --hostname host` when context is ambiguous.\n- Do not attempt generic API, merge, approve, comment, close/reopen/delete, repository/release/label writes, secrets/variables, or pipeline mutations.\n- `mr ensure` / `mr create-or-update` is the only provider write and requires private title/description files.\n- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.\n")
	return out.String()
}

func leafHelp(definition Definition) string {
	var out strings.Builder
	out.WriteString("Usage:\n  " + definition.Usage + "\n\n")
	out.WriteString(definition.Summary + "\n")
	if definition.Backend == "official-glab" {
		out.WriteString("Backend: pinned official glab 1.112.0; output is bounded and normalized.\n")
	}
	if definition.Write {
		out.WriteString("Write boundary: this is the only provider mutation in the initial parity release.\n")
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
	if definition.RepoMode != RepoNone {
		out.WriteString("\nTarget/output flags:\n  -R, --repo NAMESPACE/PROJECT\n      --hostname HOST\n      --limit N\n      --format toon|json\n")
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
