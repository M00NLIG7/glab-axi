package product

const mrEnsureMetadataDetails = "Optional --assignee-id and --reviewer-id (repeatable, at most 20), --milestone-id, and --draft select creation metadata and require --auth-source native. The complete native operation uses one selected environment/keyring identity, which may differ from the official profile.\nWith these selectors an existing match must already have all selected metadata and exact title/body; no replacement PUT is attempted.\nNumeric IDs are explicit provider identities, not username or milestone-name lookups."

func mrWriteDefinition(action string, note bool) Definition {
	usage := "gl-axi mr " + action + " <iid> --auth-source native -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --expected-state opened|closed"
	summary := "Observe an already-target-state MR; actual close/reopen transitions are temporarily refused."
	if note {
		usage += " --body-file FILE"
		summary = "Create one ordinary MR note (not an approval or review)."
	}
	return Definition{Path: []string{"mr", action}, Summary: summary,
		Details: "Requires explicit native environment/keyring authentication for the complete operation, with no official-profile fallback or account-equivalence claim.\nRequires same-project identity, exact configured web URL, branches/head/state, and a stable preflight recheck.\nNotes make one mutation attempt with no blind retry. GitLab supplies no atomic expected-revision guard. Note success requires an attributable POST note ID and exact readback; a lost ID remains ambiguous.\nClose/reopen return zero-write no-ops only when already in the requested state; transitions refuse before mutation to avoid collateral description and deployment effects.\nNo quick actions, reactions, attachments, reply, resolution, approval, or merge behavior.",
		Usage:   usage + " [--format toon|json]", RepoMode: RepoRequired, Positionals: 1, MaxPositions: 1,
		Flags: mrWriteFlags(note), Schema: "mr-write", Backend: "native", Write: true, NoLimit: true, RequireExplicitHost: true, RequireExplicitRepo: true, NativeAuth: true, RequireNativeAuth: true}
}
