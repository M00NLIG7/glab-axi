---
name: gl-axi
description: Use bounded GitLab reads, idempotent MR ensure, and guarded exact-head squash merge without generic API authority.
---

# gl-axi

Use `gl-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.

## Commands

- `gl-axi auth status [--hostname HOST]` - Check official-glab authentication without displaying a token.
- `gl-axi issue list [global flags]` - List project issues.
- `gl-axi issue view <iid> [global flags]` - View one project issue.
- `gl-axi mr list [global flags]` - List project merge requests.
- `gl-axi mr view <iid> [global flags]` - View one merge request.
- `gl-axi mr checks <iid> [global flags]` - View the head pipeline and jobs for one merge request.
- `gl-axi mr discussions <iid> [global flags]` - View bounded, read-only discussion threads for one merge request.
- `gl-axi mr diff <iid> [global flags]` - View a bounded, color-free merge-request diff.
- `gl-axi mr merge <iid> -R NAMESPACE/PROJECT --hostname HOST --expected-url URL --expected-source BRANCH --expected-target BRANCH --expected-head SHA --authority captain-explicit|standing-yolo-green --squash [--format toon|json]` - Immediately squash-merge one exact green merge request.
- `gl-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Create or update exactly one matching open merge request.
- `gl-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` - Alias for bounded MR ensure semantics.
- `gl-axi pipeline list [global flags]` - List project pipelines.
- `gl-axi pipeline view <id> [global flags]` - View one pipeline.
- `gl-axi job list --pipeline-id ID [global flags]` - List jobs for one pipeline.
- `gl-axi job view <id> [global flags]` - View one CI/CD job.
- `gl-axi job trace <id> [global flags]` - View a bounded, redacted tail of one job trace.
- `gl-axi release list [global flags]` - List project releases and bounded download metadata.
- `gl-axi release view [tag] [global flags]` - View a release and project-bound download metadata (latest when omitted).
- `gl-axi repo list [--hostname HOST] [--limit N]` - List repositories visible to the official profile.
- `gl-axi repo view [namespace/project] [global flags]` - View a project/repository.
- `gl-axi label list [global flags]` - List project labels.
- `gl-axi search issues <query> [global flags]` - Search issues in one project.
- `gl-axi search mrs <query> [global flags]` - Search merge requests in one project.
- `gl-axi search repos <query> [--hostname HOST] [--limit N]` - Search projects/repositories on one host.
- `gl-axi search commits <query> [global flags]` - Search commits in one project.
- `gl-axi search code <query> [global flags]` - Search code blobs in one project.

## Safety

- Ask a human to run `gl-axi auth login`; never drive login from an agent or request a token.
- Use `-R namespace/project --hostname host` when context is ambiguous. Guarded merge requires both explicitly.
- Do not attempt generic API, alternate merge strategies, approve, comment/note/reply/resolve/label mutation, close/reopen/delete, repository/release writes, secrets/variables, or pipeline mutations.
- `mr ensure` / `mr create-or-update` accepts private title/description files. `mr merge` requires the exact URL, source branch, target branch, reviewed head, authority class, provider-enforced green policy, and `--squash`.
- Never self-assert `--authority`; invoke guarded merge only through the pinned Firstmate lifecycle boundary after its separately shipped integration.
- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.
