---
name: glab-axi
description: Use bounded GitLab reads, typed nonblank issue creation/notes/state observations, best-effort guarded issue editing, MR ensure, and guarded exact-head squash merge without generic API authority.
---

# glab-axi compatibility alias

`glab-axi` remains supported with no removal date. Prefer the canonical `gl-axi` command for new configuration.

Use `glab-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.

## Commands

- `glab-axi issue create -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-url PROJECT_URL --title-file FILE --description-file FILE --auth-source native [--format toon|json]` - Create one ordinary issue from private title and nonblank description files.
- `glab-axi issue comment <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --body-file FILE --auth-source native [--format toon|json]` - Create one plain issue note (comment and note are aliases).
- `glab-axi issue note <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --body-file FILE --auth-source native [--format toon|json]` - Create one plain issue note (comment and note are aliases).
- `glab-axi issue close <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --expected-state opened|closed --auth-source native [--format toon|json]` - Observe an already-matching issue state; transitions are temporarily refused.
- `glab-axi issue reopen <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --expected-state opened|closed --auth-source native [--format toon|json]` - Observe an already-matching issue state; transitions are temporarily refused.
- `glab-axi auth status [--hostname HOST]` - Check official-glab authentication without displaying a token.
- `glab-axi issue list [global flags]` - List project issues.
- `glab-axi issue view <iid> [global flags]` - View one project issue.
- `glab-axi issue edit <iid> [--auth-source native] -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-state opened|closed --expected-updated-at TIMESTAMP [--title-file FILE] [--description-file FILE] [--add-label NAME]... [--remove-label NAME]... [--dry-run] [--format toon|json]` - Edit one exact project issue with best-effort drift checks.
- `glab-axi mr list [global flags]` - List project merge requests.
- `glab-axi mr view <iid> [global flags]` - View one merge request.
- `glab-axi mr checks <iid> [global flags]` - View the head pipeline and jobs for one merge request.
- `glab-axi mr discussions <iid> [global flags]` - View bounded, read-only discussion evidence for one merge request.
- `glab-axi mr diff <iid> [global flags]` - View a bounded, color-free merge-request diff.
- `glab-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]` - Immediately squash-merge one exact green merge request.
- `glab-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Create or update exactly one matching open merge request.
- `glab-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Alias for bounded MR ensure semantics.
- `glab-axi pipeline list [--ref REF] [--status STATUS] [--source SOURCE] [--user USERNAME] [--sha SHA] [--fields iid] [--web-base URL] [global flags]` - List project pipelines.
- `glab-axi pipeline view <id> [--ref REF] [--sha SHA] [--jobs] [--job-id ID] [--job-status STATUS] [--trace | --trace-failed] [--web-base URL] [global flags]` - View one pipeline.
- `glab-axi pipeline watch <id> [--timeout SECONDS] [--interval SECONDS] [--ref REF] [--sha SHA] [--web-base URL] [target/output flags]` - Watch one exact pipeline within a finite budget.
- `glab-axi job list --pipeline-id ID [--job-id ID] [--status STATUS] [--web-base URL] [global flags]` - List jobs for one pipeline.
- `glab-axi job artifacts <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA` - Read artifact metadata for one exact job and pipeline.
- `glab-axi job download <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA --destination ABSOLUTE_NEW_DIRECTORY` - Safely extract one exact job's artifact ZIP into a new directory.
- `glab-axi job view <id> [--pipeline-id ID] [--web-base URL] [global flags]` - View one CI/CD job.
- `glab-axi job trace <id> [--pipeline-id ID] [--web-base URL] [global flags]` - View a bounded, redacted tail of one job trace.
- `glab-axi release list [global flags]` - List project releases and bounded download metadata.
- `glab-axi release download <tag> --auth-source native --hostname HOST -R PROJECT --expected-sha SHA --asset-id ID --asset-name NAME --destination ABSOLUTE_NEW_DIRECTORY` - Download one exact release asset into a new private directory.
- `glab-axi release view [tag] [global flags]` - View a release and project-bound download metadata (latest when omitted).
- `glab-axi repo list [USER | --group FULL_PATH] [global flags]` - Discover repositories with explicit user/group ownership.
- `glab-axi repo view [namespace/project] [global flags]` - View a project/repository.
- `glab-axi label list [global flags]` - List project labels.
- `glab-axi search issues <query> [--scope project|host | --group FULL_PATH] [global flags]` - Search issues in a project, group, or explicit host scope.
- `glab-axi search mrs <query> [--scope project|host | --group FULL_PATH] [global flags]` - Search merge requests in a project, group, or explicit host scope.
- `glab-axi search repos <query> [--group FULL_PATH | --owner USER] [--language LANGUAGE] [--hostname HOST] [--limit N]` - Search projects/repositories on one host or within a group/user namespace.
- `glab-axi search commits <query> [global flags]` - Search commits in one project.
- `glab-axi search code <query> [global flags]` - Search code blobs in one project.
- `glab-axi board list (-R PROJECT | --group GROUP) [global flags]` - List GitLab issue boards in one project or group.
- `glab-axi board view <board-id> (-R PROJECT | --group GROUP) [global flags]` - View an issue board and its bounded list/column definitions.
- `glab-axi board issues <board-id> --list-id ID --allow-ordering-initialization --hostname HOST (-R PROJECT | --group GROUP) [global flags]` - List board issues with explicit consent to possible ordering initialization.
- `glab-axi work-item fields <iid> (-R PROJECT | --group GROUP) [global flags]` - List visible widget types and fixed fields for one work item.
- `glab-axi work-item hierarchy <iid> (-R PROJECT | --group GROUP) [global flags]` - Read the parent and bounded direct children of one work item.
- `glab-axi secret list --auth-source native [global flags] --scope SCOPE` - Manage project CI/CD secret metadata with exact-scope guards.
- `glab-axi secret set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD secret metadata with exact-scope guards.
- `glab-axi secret delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD secret metadata with exact-scope guards.
- `glab-axi variable list --auth-source native [global flags] --scope SCOPE` - Manage project CI/CD variable metadata with exact-scope guards.
- `glab-axi variable set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD variable metadata with exact-scope guards.
- `glab-axi variable delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD variable metadata with exact-scope guards.
- `glab-axi issue delete <IID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-issue URL --expected-id ID --expected-state STATE --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact issue.
- `glab-axi pipeline delete <ID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-pipeline URL --acknowledge-child-cancellation URL --expected-sha SHA --expected-ref REF --expected-status STATUS --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact pipeline.
- `glab-axi release delete <TAG> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-release URL --acknowledge-catalog-unpublication URL --expected-commit SHA --expected-created-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact release.
- `glab-axi snippet delete <ID> --auth-source native --hostname HOST --expected-url URL --confirm-delete-snippet URL --expected-author-id ID --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact snippet.
- `glab-axi snippet delete-project <ID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-snippet URL --expected-author-id ID --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact snippet.
- `glab-axi stack view [--mrs] [--limit N] --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - View a bounded local stack and optionally observe linked GitLab MRs.
- `glab-axi stack init <branches...> --base BRANCH --allow-local-metadata --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Register an existing local branch chain without switching branches.
- `glab-axi stack link <branches...> --base BRANCH --allow-local-metadata --mr BRANCH=IID ... --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Bind an existing local chain to exact existing same-project GitLab MRs.
- `glab-axi stack checkout BRANCH --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.
- `glab-axi stack up [N] --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.
- `glab-axi stack down [N] --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.
- `glab-axi stack top --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.
- `glab-axi stack bottom --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.
- `glab-axi stack trunk --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA --stack NAME --hostname HOST -R PROJECT [--format toon|json]` - Safely switch to an existing branch in the selected local stack.

## Local stacks

`stack` operates only on the current repository and requires explicit host/project matching its origin and an explicit stack name. `init` adopts existing branches without switching; `link` binds exact existing same-project MRs without provider writes. Navigation requires per-call checkout permission and expected current/destination heads. No stash, force, branch creation, fetch, push, submit, rebase or stack merge. See `docs/stacks.md`.

## Safety

- Ask a human to run `glab-axi auth login`; never drive login from an agent or request a token.
- Use explicit `-R namespace/project --hostname host` for issue writes, issue editing and guarded merge.
- Guarded deletion requires explicit native auth, exact reviewed identities and the leaf-specific URL confirmation. Personal snippets use `snippet delete`; project snippets use `snippet delete-project`. Release deletion retains its tag and may unpublish the project's CI/CD Catalog resource when its last catalog version is removed. It requires separate per-invocation `--acknowledge-catalog-unpublication URL` matching the release URL as well as release deletion confirmation, regardless of observed catalog state or version count. Snapshots cannot guarantee absence of catalog effects; receipts leave catalog unpublication unverified after any DELETE attempt, including ambiguous responses. Pipeline deletion removes related builds/logs/artifacts and may cancel surviving child pipelines and their jobs, even if parent deletion later fails. Pipeline deletion requires separate per-invocation `--acknowledge-child-cancellation URL` matching the parent URL as well as parent deletion confirmation, regardless of observed status. Child pipelines are not recursively deleted, and receipts do not verify child cancellation. Read leaf help for consequences and `contracts/resource-delete/v1.md` for races/receipts. Never blindly retry an ambiguous delete.
- Do not attempt generic API, issue mutation outside the declared contracts, alternate merge strategies, approve, MR comment/note/reply/resolve/close/reopen, MR delete, label-resource or MR-label mutation, repository writes, or other release/pipeline/job writes. Label deletion remains disabled pending an ID-exclusive provider capability.
- CI variable commands require --auth-source native, explicit host/repo and exact scope; set/delete require caller-bound prestate and --confirm. Values enter only via private files or piped stdin and are never displayed. Hidden mutations use exact metadata guards and report unavailable value verification; unhidden mutations require private previous-value checks. Success requires provider acknowledgment and bounded reconciliation. Native and official profiles may be different accounts; no fallback occurs. See docs/ci-variables.md.
- Issue create/comment/close/reopen require `--auth-source native`, caller-bound numeric project/issue identity and the configured canonical URL. One native environment/keyring identity handles the whole operation; no official-profile fallback or redirects. Content uses private files; quick-action-shaped lines are refused. No blind retries, deduplication, atomic state precondition, GitHub close reason, or bundled comment. Blank creation is temporarily refused before credentials or HTTP. Close/reopen transitions return unsupported with zero mutation attempts; already-matching states return read-only observations. Existing descriptions are not filtered. These temporary gaps do not establish full issue parity.
- `issue edit` requires exact URL/state/updated-at evidence and private content files. Use `--dry-run` for a validated preview. Live edits require `--auth-source native` and use one selected native identity throughout, without assuming it matches official glab. One PUT sends only changed fields, then verifies observed state. GitLab cannot enforce atomic revisions or numeric label identities: concurrent edits or label renames can race. Never blindly retry an ambiguous result.
- `mr ensure` / `mr create-or-update` accepts private title/description files. `mr merge` requires the exact URL, source branch, target branch, reviewed head, authority class, provider-enforced green policy, and `--squash`.
- `board issues` requires `--allow-ordering-initialization` and explicit scope/host. GitLab may initialize issue relative positions and shift sibling positions, including beyond displayed items; receipts never claim changes were measured. Do not use this command when mutation-free reads are required.
- Never self-assert `--authority`; invoke guarded merge only through the pinned Firstmate lifecycle boundary after its separately shipped integration.
- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.
