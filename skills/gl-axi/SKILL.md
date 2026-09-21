---
name: gl-axi
description: Use bounded GitLab reads, typed issue creation/notes/state changes, issue-edit preview, MR ensure, and guarded exact-head squash merge without generic API authority.
---

# gl-axi

Use `gl-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.

## Commands

- `gl-axi issue create -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-url PROJECT_URL --title-file FILE --description-file FILE [--format toon|json]` - Create one ordinary issue from private title and description files.
- `gl-axi issue comment <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --body-file FILE [--format toon|json]` - Create one plain issue note (comment and note are aliases).
- `gl-axi issue note <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --body-file FILE [--format toon|json]` - Create one plain issue note (comment and note are aliases).
- `gl-axi issue close <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --expected-state opened|closed [--format toon|json]` - Request one reversible GitLab issue state transition.
- `gl-axi issue reopen <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-project-id ID --expected-issue-id ID --expected-url URL --expected-state opened|closed [--format toon|json]` - Request one reversible GitLab issue state transition.
- `gl-axi auth status [--hostname HOST]` - Check official-glab authentication without displaying a token.
- `gl-axi issue list [global flags]` - List project issues.
- `gl-axi issue view <iid> [global flags]` - View one project issue.
- `gl-axi issue edit <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-state opened|closed --expected-updated-at TIMESTAMP [--title-file FILE] [--description-file FILE] [--add-label NAME]... [--remove-label NAME]... [--dry-run] [--format toon|json]` - Validate one exact project issue edit without mutation.
- `gl-axi mr list [global flags]` - List project merge requests.
- `gl-axi mr view <iid> [global flags]` - View one merge request.
- `gl-axi mr checks <iid> [global flags]` - View the head pipeline and jobs for one merge request.
- `gl-axi mr discussions <iid> [global flags]` - View bounded, read-only discussion evidence for one merge request.
- `gl-axi mr diff <iid> [global flags]` - View a bounded, color-free merge-request diff.
- `gl-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]` - Immediately squash-merge one exact green merge request.
- `gl-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Create or update exactly one matching open merge request.
- `gl-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Alias for bounded MR ensure semantics.
- `gl-axi pipeline list [global flags]` - List project pipelines.
- `gl-axi pipeline view <id> [global flags]` - View one pipeline.
- `gl-axi job list --pipeline-id ID [global flags]` - List jobs for one pipeline.
- `gl-axi job artifacts <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA` - Read artifact metadata for one exact job and pipeline.
- `gl-axi job download <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA --destination ABSOLUTE_NEW_DIRECTORY` - Safely extract one exact job's artifact ZIP into a new directory.
- `gl-axi job view <id> [global flags]` - View one CI/CD job.
- `gl-axi job trace <id> [global flags]` - View a bounded, redacted tail of one job trace.
- `gl-axi release list [global flags]` - List project releases and bounded download metadata.
- `gl-axi release download <tag> --auth-source native --hostname HOST -R PROJECT --expected-sha SHA --asset-id ID --asset-name NAME --destination ABSOLUTE_NEW_DIRECTORY` - Download one exact release asset into a new private directory.
- `gl-axi release view [tag] [global flags]` - View a release and project-bound download metadata (latest when omitted).
- `gl-axi repo list [--hostname HOST] [--limit N]` - List repositories visible to the official profile.
- `gl-axi repo view [namespace/project] [global flags]` - View a project/repository.
- `gl-axi label list [global flags]` - List project labels.
- `gl-axi search issues <query> [global flags]` - Search issues in one project.
- `gl-axi search mrs <query> [global flags]` - Search merge requests in one project.
- `gl-axi search repos <query> [--hostname HOST] [--limit N]` - Search projects/repositories on one host.
- `gl-axi search commits <query> [global flags]` - Search commits in one project.
- `gl-axi search code <query> [global flags]` - Search code blobs in one project.
- `gl-axi board list (-R PROJECT | --group GROUP) [global flags]` - List GitLab issue boards in one project or group.
- `gl-axi board view <board-id> (-R PROJECT | --group GROUP) [global flags]` - View an issue board and its bounded list/column definitions.
- `gl-axi board issues <board-id> --list-id ID --allow-ordering-initialization --hostname HOST (-R PROJECT | --group GROUP) [global flags]` - List board issues with explicit consent to possible ordering initialization.
- `gl-axi work-item fields <iid> (-R PROJECT | --group GROUP) [global flags]` - List visible widget types and fixed fields for one work item.
- `gl-axi work-item hierarchy <iid> (-R PROJECT | --group GROUP) [global flags]` - Read the parent and bounded direct children of one work item.
- `gl-axi secret list --auth-source native [global flags] --scope SCOPE` - Manage project CI/CD secret metadata with exact-scope guards.
- `gl-axi secret set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD secret metadata with exact-scope guards.
- `gl-axi secret delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD secret metadata with exact-scope guards.
- `gl-axi variable list --auth-source native [global flags] --scope SCOPE` - Manage project CI/CD variable metadata with exact-scope guards.
- `gl-axi variable set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD variable metadata with exact-scope guards.
- `gl-axi variable delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]` - Manage project CI/CD variable metadata with exact-scope guards.
- `gl-axi issue delete <IID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-issue URL --expected-id ID --expected-state STATE --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact issue.
- `gl-axi pipeline delete <ID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-pipeline URL --acknowledge-child-cancellation URL --expected-sha SHA --expected-ref REF --expected-status STATUS --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact pipeline.
- `gl-axi release delete <TAG> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-release URL --acknowledge-catalog-unpublication URL --expected-commit SHA --expected-created-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact release.
- `gl-axi snippet delete <ID> --auth-source native --hostname HOST --expected-url URL --confirm-delete-snippet URL --expected-author-id ID --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact snippet.
- `gl-axi snippet delete-project <ID> --auth-source native --hostname HOST -R PROJECT --expected-project-id ID --expected-url URL --confirm-delete-snippet URL --expected-author-id ID --expected-updated-at TIMESTAMP [--format toon|json]` - Guardedly delete one exact snippet.

## Safety

- Ask a human to run `gl-axi auth login`; never drive login from an agent or request a token.
- Use explicit `-R namespace/project --hostname host` for issue writes, issue-edit preview and guarded merge.
- Guarded deletion requires explicit native auth, exact reviewed identities and the leaf-specific URL confirmation. Personal snippets use `snippet delete`; project snippets use `snippet delete-project`. Release deletion retains its tag and may unpublish the project's CI/CD Catalog resource when its last catalog version is removed. It requires separate per-invocation `--acknowledge-catalog-unpublication URL` matching the release URL as well as release deletion confirmation, regardless of observed catalog state or version count. Snapshots cannot guarantee absence of catalog effects; receipts leave catalog unpublication unverified after any DELETE attempt, including ambiguous responses. Pipeline deletion removes related builds/logs/artifacts and may cancel surviving child pipelines and their jobs, even if parent deletion later fails. Pipeline deletion requires separate per-invocation `--acknowledge-child-cancellation URL` matching the parent URL as well as parent deletion confirmation, regardless of observed status. Child pipelines are not recursively deleted, and receipts do not verify child cancellation. Read leaf help for consequences and `contracts/resource-delete/v1.md` for races/receipts. Never blindly retry an ambiguous delete.
- Do not attempt generic API, existing-issue content/label mutation, alternate merge strategies, approve, MR comment/note/reply/resolve/close/reopen, MR delete, label-resource or MR-label mutation, repository writes, or other release/pipeline/job writes. Label deletion remains disabled pending an ID-exclusive provider capability.
- CI variable commands require --auth-source native, explicit host/repo and exact scope; set/delete require caller-bound prestate and --confirm. Values enter only via private files or piped stdin and are never displayed. Hidden mutations use exact metadata guards and report unavailable value verification; unhidden mutations require private previous-value checks. Success requires provider acknowledgment and bounded reconciliation. Native and official profiles may be different accounts; no fallback occurs. See docs/ci-variables.md.
- Issue create/comment/close/reopen require caller-bound numeric project/issue identity and exact URL. Content uses private files; quick-action-shaped lines are refused. No blind retries, deduplication, atomic state precondition, GitHub close reason, or bundled comment. State success reports an observed postcondition, not exclusive authorship.
- `issue edit` requires exact URL/state/updated-at evidence and private content files. Use `--dry-run` for a validated preview; a non-no-op live request returns `safety_violation` with no PUT because GitLab has no enforceable issue revision.
- `mr ensure` / `mr create-or-update` accepts private title/description files. `mr merge` requires the exact URL, source branch, target branch, reviewed head, authority class, provider-enforced green policy, and `--squash`.
- `board issues` requires `--allow-ordering-initialization` and explicit scope/host. GitLab may initialize issue relative positions and shift sibling positions, including beyond displayed items; receipts never claim changes were measured. Do not use this command when mutation-free reads are required.
- Never self-assert `--authority`; invoke guarded merge only through the pinned Firstmate lifecycle boundary after its separately shipped integration.
- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.
