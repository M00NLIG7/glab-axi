package product

func deletionDefinitions() []Definition {
	common := "Requires explicit --auth-source native and operation-specific URL confirmation. The native identity may differ from the official profile; there is no fallback. Rechecks exact identity before one DELETE and reads back the result. No redirect, retry, local cleanup, atomic revision guarantee or undelete. An initial 404 is not proof of prior deletion. Unknown outcomes return a non-retryable ambiguity receipt. See contracts/resource-delete/v1.md."
	var out []Definition
	for _, item := range []struct {
		group, command, selector, details string
		personal                          bool
		flags                             []FlagDefinition
	}{
		{"issue", "delete", "IID", "Permanently deletes the selected issue, not merely its open/closed state.", false, []FlagDefinition{
			deletionFlag("--expected-id", "ID", "Exact global issue ID, distinct from the positional project IID."),
			deletionFlag("--expected-state", "STATE", "Exact issue state: opened or closed."),
			deletionFlag("--expected-updated-at", "TIMESTAMP", "Exact RFC 3339 updated_at from the reviewed resource."),
		}},
		{"pipeline", "delete", "ID", "Deletes the pipeline and its immediately related builds, logs, artifacts and triggers, and expires its caches. Child pipelines are not recursively deleted. This is not individual job erasure.", false, []FlagDefinition{
			deletionFlag("--expected-sha", "SHA", "Exact lowercase pipeline commit SHA."),
			deletionFlag("--expected-ref", "REF", "Exact pipeline Git ref."),
			deletionFlag("--expected-status", "STATUS", "Exact current pipeline status."),
			deletionFlag("--expected-updated-at", "TIMESTAMP", "Exact RFC 3339 updated_at from the reviewed resource."),
		}},
		{"release", "delete", "TAG", "Deletes only release metadata. The tag is never deleted; its exact name and commit are checked before and after. Concurrent tag movement or release recreation cannot be made atomic with this operation.", false, []FlagDefinition{
			deletionFlag("--expected-commit", "SHA", "Exact lowercase commit of the release and retained tag."),
			deletionFlag("--expected-created-at", "TIMESTAMP", "Exact release created_at, to detect replacement before deletion."),
		}},
		{"snippet", "delete", "ID", "Deletes one personal snippet and its provider-managed content. Requires a null project_id and the authenticated author. No project selector is accepted; project snippets use snippet delete-project.", true, []FlagDefinition{
			deletionFlag("--expected-author-id", "ID", "Exact snippet author ID."),
			deletionFlag("--expected-updated-at", "TIMESTAMP", "Exact RFC 3339 updated_at from the reviewed resource."),
		}},
		{"snippet", "delete-project", "ID", "Deletes one project snippet and its provider-managed content. The returned project_id, author and URL must match; never falls back to personal scope.", false, []FlagDefinition{
			deletionFlag("--expected-author-id", "ID", "Exact snippet author ID."),
			deletionFlag("--expected-updated-at", "TIMESTAMP", "Exact RFC 3339 updated_at from the reviewed resource."),
		}},
	} {
		flags := []FlagDefinition{
			deletionFlag("--expected-url", "URL", "Exact canonical resource URL on the configured native web authority."),
			deletionFlag("--confirm-delete-"+item.group, "URL", "Destructive opt-in for this operation; must equal --expected-url."),
		}
		mode, projectUsage := RepoNone, ""
		if !item.personal {
			mode, projectUsage = RepoRequired, " -R PROJECT --expected-project-id ID"
			flags = append(flags, deletionFlag("--expected-project-id", "ID", "Exact numeric project ID, independently checked against --repo."))
		}
		flags = append(flags, item.flags...)
		usage := "gl-axi " + item.group + " " + item.command + " <" + item.selector + "> --auth-source native --hostname HOST" + projectUsage + " --expected-url URL --confirm-delete-" + item.group + " URL"
		for _, flag := range item.flags {
			usage += " " + flag.Name + " " + flag.Value
		}
		out = append(out, Definition{
			Path: []string{item.group, item.command}, Summary: "Guardedly delete one exact " + item.group + ".",
			Details: item.details + "\n" + common, Usage: usage + " [--format toon|json]",
			RepoMode: mode, Positionals: 1, MaxPositions: 1, Flags: flags, Schema: "resource-delete", Backend: "native",
			Write: true, NoLimit: true, NativeAuth: true, RequireNativeAuth: true,
			RequireExplicitHost: true, RequireExplicitRepo: !item.personal,
		})
	}
	return out
}

func deletionFlag(name, value, description string) FlagDefinition {
	return FlagDefinition{Name: name, Value: value, Description: description, Required: true}
}

func isResourceDeletion(path []string) bool {
	if len(path) != 2 {
		return false
	}
	switch path[0] {
	case "issue", "pipeline", "release":
		return path[1] == "delete"
	case "snippet":
		return path[1] == "delete" || path[1] == "delete-project"
	}
	return false
}
