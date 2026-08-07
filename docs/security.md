# Security model

## Trust boundary

The local operator controls the executable, private host config, credential
source, repository identity, and private MR content files. Git remotes, proxy
environment, API responses, pagination links, redirects, job names/statuses,
and traces are untrusted.

## Controls

- strict argv grammars; no shell execution or token flags;
- HTTPS only, TLS 1.2 minimum, normal hostname verification, optional explicit
  CA bundle, and no insecure mode;
- exact configured API/web origins and project identity before project actions;
- same-project MRs only; returned IDs, branches, paths, SHAs, and URLs validated;
- POST/PUT redirects refused; GET redirects stay on the exact API origin/prefix;
- same-origin, same-route pagination with page, byte, and item caps;
- tokens only in request headers; no plaintext keyring fallback;
- typed JSON decoding with bounded bodies; server error bodies are not rendered;
- bounded retries for GET only; mutations use read-after-ambiguity reconciliation;
- 30-second read and 45-second write operation deadlines plus connect, TLS, and
  response-header deadlines;
- signal/context cancellation across requests and retry waits;
- trace tail ring, UTF-8 repair, truncation marker, and credential-pattern
  redaction;
- unknown CI/merge states and stale SHA relationships never normalize green.

Environment proxy discovery is supported only after authority selection. Proxy
URLs are never printed. `proxy_disabled` turns it off for a host. Cross-origin
redirects are rejected before credential forwarding.

## Hard limits

| Input/output | Limit |
|---|---:|
| host | 253 bytes |
| project | 1,024 bytes / 32 segments |
| branch | 1,024 bytes |
| title | 1,024 bytes |
| description | 128 KiB |
| JSON page | 2 MiB |
| operation bodies/output | 8 MiB |
| pagination | 10 pages / 1,000 jobs |
| trace tail | 256 KiB |
| compatibility stderr | 4 KiB |

Limit overflow is an error. Partial pagination is never returned as an all-green
snapshot.

## Deterministic exits

| Exit | Meaning |
|---:|---|
| 0 | success |
| 2 | bad/unsupported syntax or local input |
| 3 | missing, invalid, or expired authentication |
| 4 | authenticated but forbidden |
| 5 | resource not found |
| 6 | duplicate/conflicting/ambiguous mutation |
| 7 | rate limited after bounded policy |
| 8 | network, TLS, timeout, malformed/oversized upstream, or internal failure |
| 9 | host/project/URL/fork/redirect safety violation |
| 130 / 143 | canceled by SIGINT / SIGTERM |

## Explicitly absent capabilities

There is no merge, close, reopen, approve, delete, note/comment, reviewer,
label, branch/repository write, pipeline retry/cancel, arbitrary REST, GraphQL,
webhook, issue, release, package, browser, login, extension, or shell command.
The HTTP route constructors contain no endpoint for those actions.

## Residual risks

- The v1.45.4 adapter receives MR title/description in argv because its consumer
  does so. Use only in an isolated, explicitly authorized integration.
- Job traces can contain application-specific secrets that generic redaction
  cannot recognize. GitLab masked variables and least-privilege CI remain
  required.
- An environment token is visible to processes with authority to inspect the
  caller. The OS keyring is preferred for persistent local use.
- A project access token with `api` is powerful inside its project. Keep it
  short-lived and scoped to one project.
