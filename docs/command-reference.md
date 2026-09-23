# Command reference

This file is generated from the executable command registry.

## Dashboard

```text
gl-axi [global flags]
```

Show a bounded current-project dashboard.

Backend: `official-glab`. Schema: `schema/ux-v1/dashboard.schema.json`.

## `auth login`

```text
gl-axi auth login [--hostname HOST]
```

Authenticate through official glab in a human TTY.

Backend: `official-glab`. Schema: `schema/ux-v1/auth-login.schema.json`.

## `auth status`

```text
gl-axi auth status [--hostname HOST]
```

Check official-glab authentication without displaying a token.

Backend: `official-glab`. Schema: `schema/ux-v1/auth-status.schema.json`.

## `issue list`

```text
gl-axi issue list [global flags]
```

List project issues.

Backend: `official-glab`. Schema: `schema/ux-v1/issue-list.schema.json`.

## `issue view`

```text
gl-axi issue view <iid> [global flags]
```

View one project issue.

Backend: `official-glab`. Schema: `schema/ux-v1/issue-view.schema.json`.

## `issue edit`

```text
gl-axi issue edit <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-state opened|closed --expected-updated-at TIMESTAMP [--title-file FILE] [--description-file FILE] [--add-label NAME]... [--remove-label NAME]... [--dry-run] [--format toon|json]
```

Validate one exact project issue edit without mutation.

Requires caller-bound URL, state, and updated-at evidence.
Title and description are accepted only through private files.
Labels are resolved exactly and unrelated labels are previewed as preserved.
Dry-run returns the complete validated preview. GitLab accepts no expected issue revision and only label names, so a non-no-op live request returns a bounded safety refusal and sends no PUT.

Backend: `official-glab`. Schema: `schema/ux-v1/issue-edit.schema.json`.

## `mr list`

```text
gl-axi mr list [global flags]
```

List project merge requests.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-list.schema.json`.

## `mr view`

```text
gl-axi mr view <iid> [global flags]
```

View one merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-view.schema.json`.

## `mr checks`

```text
gl-axi mr checks <iid> [global flags]
```

View the head pipeline and jobs for one merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-checks.schema.json`.

## `mr discussions`

```text
gl-axi mr discussions <iid> [global flags]
```

View bounded, read-only discussion evidence for one merge request.

Includes canonical source/target project identity and exact base/head binding.
The limit counts threads. Provider thread/note order is preserved.
Completeness is fail-closed and identity is rechecked around pagination.
No reply, resolve, or other mutation is exposed.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-discussions.schema.json`.

Examples:

```text
gl-axi mr discussions 42 -R group/project --hostname gitlab.com --limit 1000 --format json
```

## `mr diff`

```text
gl-axi mr diff <iid> [global flags]
```

View a bounded, color-free merge-request diff.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-diff.schema.json`.

## `mr merge`

```text
gl-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]
```

Immediately squash-merge one exact green merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-merge.schema.json`.

## `mr ensure`

```text
gl-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]
```

Create or update exactly one matching open merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-ensure.schema.json`.

## `mr create-or-update`

```text
gl-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]
```

Alias for bounded MR ensure semantics.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-ensure.schema.json`.

## `pipeline list`

```text
gl-axi pipeline list [global flags]
```

List project pipelines.

Backend: `official-glab`. Schema: `schema/ux-v1/pipeline-list.schema.json`.

## `pipeline view`

```text
gl-axi pipeline view <id> [global flags]
```

View one pipeline.

Backend: `official-glab`. Schema: `schema/ux-v1/pipeline-view.schema.json`.

## `job list`

```text
gl-axi job list --pipeline-id ID [global flags]
```

List jobs for one pipeline.

Backend: `official-glab`. Schema: `schema/ux-v1/job-list.schema.json`.

## `job artifacts`

```text
gl-axi job artifacts <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA
```

Read artifact metadata for one exact job and pipeline.

Requires explicit native authentication and caller-bound pipeline/ref/commit identity. Artifacts are job-owned, not a run-level collection.

Backend: `native`. Schema: `schema/ux-v1/job-artifacts.schema.json`.

## `job download`

```text
gl-axi job download <job-id> --auth-source native --hostname HOST -R PROJECT --pipeline-id ID --expected-ref REF --expected-sha SHA --destination ABSOLUTE_NEW_DIRECTORY
```

Safely extract one exact job's artifact ZIP into a new directory.

Checks project/pipeline/job/ref/commit identity before and after transfer. Bounded ZIP extraction validates paths, entry types, collisions, CRC and expansion before output writes. Maximum archive 64 MiB, expansion 256 MiB, 1000 paths, 128 directories. Portable ASCII paths only; output permissions are private and executable bits are not retained. Archive SHA-256 is a receipt, not a provider-authenticated digest. Redirects/CDN transfers are refused. Existing directories/files are never merged or replaced.

Backend: `native`. Schema: `schema/ux-v1/download.schema.json`.

## `job view`

```text
gl-axi job view <id> [global flags]
```

View one CI/CD job.

Backend: `official-glab`. Schema: `schema/ux-v1/job-view.schema.json`.

## `job trace`

```text
gl-axi job trace <id> [global flags]
```

View a bounded, redacted tail of one job trace.

Backend: `official-glab`. Schema: `schema/ux-v1/job-trace.schema.json`.

## `release list`

```text
gl-axi release list [global flags]
```

List project releases and bounded download metadata.

Backend: `official-glab`. Schema: `schema/ux-v1/release-list.schema.json`.

## `release download`

```text
gl-axi release download <tag> --auth-source native --hostname HOST -R PROJECT --expected-sha SHA --asset-id ID --asset-name NAME --destination ABSOLUTE_NEW_DIRECTORY
```

Download one exact release asset into a new private directory.

Selects a complete bounded link catalog by exact tag/commit, link ID and name. Supports only the same project's GitLab generic-package files (provider SHA-256/size verified) or job-owned raw artifacts at the release commit (SHA-256 receipt only). Rechecks metadata before publication. Maximum 64 MiB, 10 pages/catalog and 45-second native lifetime (caller deadlines may be shorter). No arbitrary/external URLs, redirect/CDN transfer, overwrite, glob selection, archive extraction or public-only fallback.

Backend: `native`. Schema: `schema/ux-v1/download.schema.json`.

## `release view`

```text
gl-axi release view [tag] [global flags]
```

View a release and project-bound download metadata (latest when omitted).

Backend: `official-glab`. Schema: `schema/ux-v1/release-view.schema.json`.

## `repo list`

```text
gl-axi repo list [--hostname HOST] [--limit N]
```

List repositories visible to the official profile.

Backend: `official-glab`. Schema: `schema/ux-v1/repo-list.schema.json`.

## `repo view`

```text
gl-axi repo view [namespace/project] [global flags]
```

View a project/repository.

Backend: `official-glab`. Schema: `schema/ux-v1/repo-view.schema.json`.

## `label list`

```text
gl-axi label list [global flags]
```

List project labels.

Backend: `official-glab`. Schema: `schema/ux-v1/label-list.schema.json`.

## `search issues`

```text
gl-axi search issues <query> [global flags]
```

Search issues in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search mrs`

```text
gl-axi search mrs <query> [global flags]
```

Search merge requests in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search repos`

```text
gl-axi search repos <query> [--hostname HOST] [--limit N]
```

Search projects/repositories on one host.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search commits`

```text
gl-axi search commits <query> [global flags]
```

Search commits in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search code`

```text
gl-axi search code <query> [global flags]
```

Search code blobs in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `setup hooks`

```text
gl-axi setup hooks
```

Install or repair generated Agent Skill and session hooks.

Backend: `local`. Schema: `schema/ux-v1/setup-hooks.schema.json`.

## `update`

```text
gl-axi update [--check]
```

Check for or install a signed gl-axi release.

Backend: `local`. Schema: `schema/ux-v1/update.schema.json`.

## `board list`

```text
gl-axi board list (-R PROJECT | --group GROUP) [global flags]
```

List GitLab issue boards in one project or group.

Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.
GitLab 19.3 schema; availability depends on provider version, tier and permissions.
Only authorized resources are visible. No generic GraphQL authority.

Backend: `official-glab`. Schema: `schema/ux-v1/board-list.schema.json`.

## `board view`

```text
gl-axi board view <board-id> (-R PROJECT | --group GROUP) [global flags]
```

View an issue board and its bounded list/column definitions.

Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.
GitLab 19.3 schema; availability depends on provider version, tier and permissions.
Only authorized resources are visible. No generic GraphQL authority.
The limit counts columns, including provider-returned open/closed columns.
List types are filters, not arbitrary custom fields. Hidden columns are not board lifecycle states.
Board scope filters are applied by GitLab, not projected as editable fields.

Backend: `official-glab`. Schema: `schema/ux-v1/board-view.schema.json`.

## `board issues`

```text
gl-axi board issues <board-id> --list-id ID --allow-ordering-initialization --hostname HOST (-R PROJECT | --group GROUP) [global flags]
```

List board issues with explicit consent to possible ordering initialization.

Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.
GitLab 19.3 schema; availability depends on provider version, tier and permissions.
Only authorized resources are visible. No generic GraphQL authority.
--list-id is required; get list IDs from board view.
GitLab applies the list and board filters. An issue can appear in multiple lists.
These are real issues, not independent Projects-v2 items, drafts or archived items.
GitLab EE 19.3.0 BoardList.issues may initialize missing issue relative positions and shift sibling positions, including beyond displayed items.
Requires --allow-ordering-initialization on every invocation and an explicit host. No automatic retry or rollback; the receipt does not claim the side effect occurred.
Boards exist in Free/Premium/Ultimate; advanced board/list filters depend on tier. The version is a pinned schema baseline, not a server-version attestation.

Backend: `official-glab`. Schema: `schema/ux-v1/board-issues.schema.json`.

## `work-item fields`

```text
gl-axi work-item fields <iid> (-R PROJECT | --group GROUP) [global flags]
```

List visible widget types and fixed fields for one work item.

Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.
GitLab 19.3 schema; availability depends on provider version, tier and permissions.
Only authorized resources are visible. No generic GraphQL authority.
Reports widget availability on this exact item, not arbitrary custom-field definitions or values.
Widget absence does not prove a tier entitlement. CUSTOM_FIELDS, if present, is a widget only.
Group work items require the provider's epics entitlement. Equal IIDs in other namespaces are not substitutes.

Backend: `official-glab`. Schema: `schema/ux-v1/work-item-fields.schema.json`.

## `work-item hierarchy`

```text
gl-axi work-item hierarchy <iid> (-R PROJECT | --group GROUP) [global flags]
```

Read the parent and bounded direct children of one work item.

Requires exactly one explicit -R NAMESPACE/PROJECT or --group FULL/PATH.
GitLab 19.3 schema; availability depends on provider version, tier and permissions.
Only authorized resources are visible. No generic GraphQL authority.
Reads the HIERARCHY widget, never issue links. Depth is exactly one; no recursive tree claim.
Completeness covers authorized direct children only, not hidden descendants.
Absent widgets, denied parents and hidden-only children fail explicitly rather than appearing empty.

Backend: `official-glab`. Schema: `schema/ux-v1/work-item-hierarchy.schema.json`.

## `secret list`

```text
gl-axi secret list --auth-source native [global flags] --scope SCOPE
```

Manage project CI/CD secret metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-list.schema.json`.

## `secret set`

```text
gl-axi secret set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]
```

Manage project CI/CD secret metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.
Unavailable on Windows until private-file ACL verification is supported.
One mutation, no retry; preflight is not atomic CAS. Updates preserve type and protection.
Existing entries require exact class/type/protected/raw. Unhidden entries also require a private expected-value file; hidden values cannot be verified and reject that flag.
Success requires provider acknowledgment plus bounded reconciliation; hidden set observes metadata only, and delete observes absence. Lost responses remain ambiguous.
secret set creates hidden+masked entries or rotates existing hidden entries, never silently promotes masked/unhidden entries.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-mutation.schema.json`.

## `secret delete`

```text
gl-axi secret delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]
```

Manage project CI/CD secret metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.
Unavailable on Windows until private-file ACL verification is supported.
One mutation, no retry; preflight is not atomic CAS. Updates preserve type and protection.
Existing entries require exact class/type/protected/raw. Unhidden entries also require a private expected-value file; hidden values cannot be verified and reject that flag.
Success requires provider acknowledgment plus bounded reconciliation; hidden set observes metadata only, and delete observes absence. Lost responses remain ambiguous.
secret set creates hidden+masked entries or rotates existing hidden entries, never silently promotes masked/unhidden entries.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-mutation.schema.json`.

## `variable list`

```text
gl-axi variable list --auth-source native [global flags] --scope SCOPE
```

Manage project CI/CD variable metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-list.schema.json`.

## `variable set`

```text
gl-axi variable set KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --value-file FILE|- --type TYPE --protected BOOL --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]
```

Manage project CI/CD variable metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.
Unavailable on Windows until private-file ACL verification is supported.
One mutation, no retry; preflight is not atomic CAS. Updates preserve type and protection.
Existing entries require exact class/type/protected/raw. Unhidden entries also require a private expected-value file; hidden values cannot be verified and reject that flag.
Success requires provider acknowledgment plus bounded reconciliation; hidden set observes metadata only, and delete observes absence. Lost responses remain ambiguous.
secret set creates hidden+masked entries or rotates existing hidden entries, never silently promotes masked/unhidden entries.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-mutation.schema.json`.

## `variable delete`

```text
gl-axi variable delete KEY --auth-source native -R NAMESPACE/PROJECT --hostname HOST --scope SCOPE --expected-project-id ID --expected-project-url URL --expected-class CLASS [prestate flags] --confirm [--format toon|json]
```

Manage project CI/CD variable metadata with exact-scope guards.

Requires explicit native environment/keyring authentication for the full operation; no official-profile fallback or account-equivalence claim.
Requires GitLab 17.6 or newer. Lists never emit values or descriptions.
secret lists hidden, masked, and protected classes distinctly; variable lists only ordinary unmasked, unhidden, unprotected entries.
No group, instance, inherited, dotenv, bulk, or raw API authority. See docs/ci-variables.md.
Unavailable on Windows until private-file ACL verification is supported.
One mutation, no retry; preflight is not atomic CAS. Updates preserve type and protection.
Existing entries require exact class/type/protected/raw. Unhidden entries also require a private expected-value file; hidden values cannot be verified and reject that flag.
Success requires provider acknowledgment plus bounded reconciliation; hidden set observes metadata only, and delete observes absence. Lost responses remain ambiguous.
secret set creates hidden+masked entries or rotates existing hidden entries, never silently promotes masked/unhidden entries.

Backend: `native`. Schema: `schema/ux-v1/ci-variable-mutation.schema.json`.

## Current undeclared operations

Generic API, direct issue mutation, unguarded or alternate-strategy merge, approve, comment/note/reply/resolve, merge-request or label-resource mutation, issue/MR close/reopen/delete, repository mutation, release mutation, and pipeline/job mutation are denied. CI variable set/delete are separately guarded native-only operations. `issue edit --dry-run` retains exact-identity validation and preview, while non-no-op live requests fail closed before PUT. `board issues` is the disclosed exception for possible issue ordering initialization: it requires a per-invocation acknowledgment and returns an uncertainty receipt.
