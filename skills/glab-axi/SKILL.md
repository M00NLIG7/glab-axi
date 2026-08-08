---
name: glab-axi
description: Use bounded GitLab reads and idempotent MR ensure without generic API or destructive authority.
---

# glab-axi

Use `glab-axi` rather than official `glab` directly when operating as an agent. Human authentication is the only interactive command.

## Commands

- `glab-axi auth status [--hostname HOST]` — Check official-glab authentication without displaying a token.
- `glab-axi issue list [global flags]` — List project issues.
- `glab-axi issue view <iid> [global flags]` — View one project issue.
- `glab-axi mr list [global flags]` — List project merge requests.
- `glab-axi mr view <iid> [global flags]` — View one merge request.
- `glab-axi mr checks <iid> [global flags]` — View the head pipeline and jobs for one merge request.
- `glab-axi mr diff <iid> [global flags]` — View a bounded, color-free merge-request diff.
- `glab-axi mr ensure --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` — Create or update exactly one matching open merge request.
- `glab-axi mr create-or-update --source BRANCH --target BRANCH --title-file FILE --description-file FILE [global flags]` — Alias for bounded MR ensure semantics.
- `glab-axi pipeline list [global flags]` — List project pipelines.
- `glab-axi pipeline view <id> [global flags]` — View one pipeline.
- `glab-axi job list --pipeline-id ID [global flags]` — List jobs for one pipeline.
- `glab-axi job view <id> [global flags]` — View one CI/CD job.
- `glab-axi job trace <id> [global flags]` — View a bounded, redacted tail of one job trace.
- `glab-axi release list [global flags]` — List project releases and bounded download metadata.
- `glab-axi release view [tag] [global flags]` — View a release and project-bound download metadata (latest when omitted).
- `glab-axi repo list [--hostname HOST] [--limit N]` — List repositories visible to the official profile.
- `glab-axi repo view [namespace/project] [global flags]` — View a project/repository.
- `glab-axi label list [global flags]` — List project labels.
- `glab-axi search issues <query> [global flags]` — Search issues in one project.
- `glab-axi search mrs <query> [global flags]` — Search merge requests in one project.
- `glab-axi search repos <query> [--hostname HOST] [--limit N]` — Search projects/repositories on one host.
- `glab-axi search commits <query> [global flags]` — Search commits in one project.
- `glab-axi search code <query> [global flags]` — Search code blobs in one project.

## Safety

- Ask a human to run `glab-axi auth login`; never drive login from an agent or request a token.
- Use `-R namespace/project --hostname host` when context is ambiguous.
- Do not attempt generic API, merge, approve, comment, close/reopen/delete, repository/release/label writes, secrets/variables, or pipeline mutations.
- `mr ensure` / `mr create-or-update` is the only provider write and requires private title/description files.
- Output identifies `backend`, completeness, truncation, host, and repository. Treat incomplete results as incomplete.
