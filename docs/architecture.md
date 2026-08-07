# Architecture

## Boundary

Both commands share the same typed Go core:

```text
strict argv parser
  -> config + exact repository identity
  -> noninteractive credential resolver
  -> application use case
  -> typed GitLab endpoints
  -> bounded TOON/JSON or pinned compatibility JSON
```

`cmd/glab-axi` and `cmd/glab-compat` are separate entry points. The compatibility
adapter imports the typed core; it never spawns `glab-axi` or parses terminal
text.

## Authority and identity

Non-secret config binds a logical host and Git remote hosts to a complete HTTPS
API base and web base. `gitlab.com` has a built-in root mapping. Every private
host, alternate API host, port, or relative-URL installation must be explicit.
A remote URL alone never selects where a credential is sent.

Before project operations, the client fetches project metadata and requires
`path_with_namespace` and the returned web URL to match the configured identity.
MRs must have source and target project IDs equal to that project. Fork MRs are
outside v1.

## Typed API

The HTTP layer constructs only these routes:

- `GET /user`
- `GET /projects/:path`
- `GET|POST /projects/:path/merge_requests`
- `GET|PUT /projects/:path/merge_requests/:iid`
- `GET /projects/:path/pipelines/:id/jobs`
- `GET /projects/:path/jobs/:id/trace`

The MR update body contains only `title` and `description`. There is no generic
request method exposed to either CLI.

## Idempotence

`mr ensure` performs exact all-page lookup before create. Create performs a
second lookup immediately before POST. Existing desired content is a replay;
different content is updated only by native `ensure`. HTTP 409/422 and ambiguous
write failures trigger GET-only reconciliation. The client never blindly sends
a second POST or PUT. Multiple matching open MRs fail as a conflict.

## CI snapshots

Each invocation takes one snapshot; an external consumer owns polling. Every
jobs page is required. MR SHA, head-pipeline SHA, and available local HEAD must
agree before success jobs are returned. A mismatch becomes a synthetic pending
check. Unknown job and merge states normalize to pending-compatible values.
Optional failures/manual jobs become skipped; blocking manual jobs remain
pending.

## Output

Native JSON uses `schema: glab-axi/v1`; schemas are under `schema/`. Default
TOON uses the same field names, deterministic key ordering, quoted strings, and
compact scalar-object tables. Output, response pages, operation totals, traces,
and errors all have hard limits.

The compatibility binary emits only one legacy JSON document (or bounded raw
trace bytes) and never emits banners, ANSI, progress, prompts, or update notices.
