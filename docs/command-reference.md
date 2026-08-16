# Command reference

This file is generated from the executable command registry.

## Dashboard

```text
glab-axi [global flags]
```

Show a bounded current-project dashboard.

Backend: `official-glab`. Schema: `schema/ux-v1/dashboard.schema.json`.

## `auth login`

```text
glab-axi auth login [--hostname HOST]
```

Authenticate through official glab in a human TTY.

Backend: `official-glab`. Schema: `schema/ux-v1/auth-login.schema.json`.

## `auth status`

```text
glab-axi auth status [--hostname HOST]
```

Check official-glab authentication without displaying a token.

Backend: `official-glab`. Schema: `schema/ux-v1/auth-status.schema.json`.

## `issue list`

```text
glab-axi issue list [global flags]
```

List project issues.

Backend: `official-glab`. Schema: `schema/ux-v1/issue-list.schema.json`.

## `issue view`

```text
glab-axi issue view <iid> [global flags]
```

View one project issue.

Backend: `official-glab`. Schema: `schema/ux-v1/issue-view.schema.json`.

## `mr list`

```text
glab-axi mr list [global flags]
```

List project merge requests.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-list.schema.json`.

## `mr view`

```text
glab-axi mr view <iid> [global flags]
```

View one merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-view.schema.json`.

## `mr checks`

```text
glab-axi mr checks <iid> [global flags]
```

View the head pipeline and jobs for one merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-checks.schema.json`.

## `mr diff`

```text
glab-axi mr diff <iid> [global flags]
```

View a bounded, color-free merge-request diff.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-diff.schema.json`.

## `mr merge`

```text
glab-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]
```

Immediately squash-merge one exact green merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-merge.schema.json`.

## `mr ensure`

```text
glab-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]
```

Create or update exactly one matching open merge request.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-ensure.schema.json`.

## `mr create-or-update`

```text
glab-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]
```

Alias for bounded MR ensure semantics.

Backend: `official-glab`. Schema: `schema/ux-v1/mr-ensure.schema.json`.

## `pipeline list`

```text
glab-axi pipeline list [global flags]
```

List project pipelines.

Backend: `official-glab`. Schema: `schema/ux-v1/pipeline-list.schema.json`.

## `pipeline view`

```text
glab-axi pipeline view <id> [global flags]
```

View one pipeline.

Backend: `official-glab`. Schema: `schema/ux-v1/pipeline-view.schema.json`.

## `job list`

```text
glab-axi job list --pipeline-id ID [global flags]
```

List jobs for one pipeline.

Backend: `official-glab`. Schema: `schema/ux-v1/job-list.schema.json`.

## `job view`

```text
glab-axi job view <id> [global flags]
```

View one CI/CD job.

Backend: `official-glab`. Schema: `schema/ux-v1/job-view.schema.json`.

## `job trace`

```text
glab-axi job trace <id> [global flags]
```

View a bounded, redacted tail of one job trace.

Backend: `official-glab`. Schema: `schema/ux-v1/job-trace.schema.json`.

## `release list`

```text
glab-axi release list [global flags]
```

List project releases and bounded download metadata.

Backend: `official-glab`. Schema: `schema/ux-v1/release-list.schema.json`.

## `release view`

```text
glab-axi release view [tag] [global flags]
```

View a release and project-bound download metadata (latest when omitted).

Backend: `official-glab`. Schema: `schema/ux-v1/release-view.schema.json`.

## `repo list`

```text
glab-axi repo list [--hostname HOST] [--limit N]
```

List repositories visible to the official profile.

Backend: `official-glab`. Schema: `schema/ux-v1/repo-list.schema.json`.

## `repo view`

```text
glab-axi repo view [namespace/project] [global flags]
```

View a project/repository.

Backend: `official-glab`. Schema: `schema/ux-v1/repo-view.schema.json`.

## `label list`

```text
glab-axi label list [global flags]
```

List project labels.

Backend: `official-glab`. Schema: `schema/ux-v1/label-list.schema.json`.

## `search issues`

```text
glab-axi search issues <query> [global flags]
```

Search issues in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search mrs`

```text
glab-axi search mrs <query> [global flags]
```

Search merge requests in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search repos`

```text
glab-axi search repos <query> [--hostname HOST] [--limit N]
```

Search projects/repositories on one host.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search commits`

```text
glab-axi search commits <query> [global flags]
```

Search commits in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `search code`

```text
glab-axi search code <query> [global flags]
```

Search code blobs in one project.

Backend: `official-glab`. Schema: `schema/ux-v1/search.schema.json`.

## `setup hooks`

```text
glab-axi setup hooks
```

Install or repair generated Agent Skill and session hooks.

Backend: `local`. Schema: `schema/ux-v1/setup-hooks.schema.json`.

## `update`

```text
glab-axi update [--check]
```

Check for or install a signed glab-axi release.

Backend: `local`. Schema: `schema/ux-v1/update.schema.json`.

## Permanent denials

Generic API, unguarded or alternate-strategy merge, approve, comments/notes, close/reopen/delete, repository mutation, release/label mutation, secrets/variables, and pipeline/job mutation are rejected before child execution.
