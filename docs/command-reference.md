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

View bounded, read-only discussion threads for one merge request.

The limit counts threads. Provider thread/note order is preserved.
No reply, resolve, or other mutation is exposed.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-discussions.schema.json`.

Examples:

```text
gl-axi mr discussions 42 -R group/project --hostname gitlab.com --limit 30 --format json
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

## Permanent denials

Generic API, unguarded or alternate-strategy merge, approve, comment/note/reply/resolve/label mutation, close/reopen/delete, repository mutation, release mutation, secrets/variables, and pipeline/job mutation are rejected before child execution.
