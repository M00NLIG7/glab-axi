# Security model

## Trust boundaries

The operator controls the `gl-axi` executable, selected official `glab`
package/profile, explicit target flags, native private-host config, credential
source, local repository, private MR files, setup command, and release signing
key. Git remotes, PATH entries, official child output/stderr, provider JSON,
URLs, pagination, redirects, proxies, traces/diffs, update servers, and existing
agent config are untrusted.

The product and native lanes have different trust properties and always report
the backend. Official-glab results do not inherit claims made by the stricter
native HTTP transport.

## Product delegation controls

- exact official version/build `1.112.0 (816e3a52)`, with pinned public package checksums/source/help;
- regular executable resolution and no shell interpolation;
- operation enum with fixed argv builders—no public/raw argv or API method/path;
- strict command/flag/positional parsing before target, credentials, or child;
- permanent denied-command classification with zero child/request tests;
- closed stdin plus prompt/pager/editor/browser/debug/update suppression for
  data commands;
- three terminal-file checks plus a non-secret secure-store probe for human login;
- PTY/ConPTY login delegation so child stdin/stdout/stderr remain terminals while
  bounded output is relayed and monitored;
- ambient token/job-token removal and exact pinned plaintext-fallback warning
  cancellation, with warning state reconciled before success;
- 5-second version check, 30/45-second noninteractive operations, bounded
  stdout/stderr, and human login governed by caller/process cancellation;
- malformed, duplicate, ANSI-prefixed, trailing, non-UTF-8, or oversized
  data-command child output rejected;
- official MR-view `diff_refs.head_sha` normalized to the canonical `sha` field
  only when valid; dual values must match and no missing head is invented;
- controlled exit/error mapping without raw stderr or server body;
- typed normalization and per-command JSON schemas;
- HTTPS host and selected repository path validation on returned URLs;
- item/field/page/display caps with explicit completeness/truncation metadata;
- unknown CI/merge states normalize pending-compatible, never green;
- fixed internal API routes only where official v1.112.0 lacks safe JSON
  commands; public `api` remains denied.

Approved environment credentials pass directly to official glab for headless
product operations but are never placed in AXI argv/output; login removes them
before child execution.
Only `gitlab.com` can be inferred as a host authority. Self-managed remotes need
explicit `--hostname` or `GITLAB_HOST`, preventing an untrusted Git remote from
selecting the destination for an environment credential. `GLAB_DEBUG_HTTP`, CI auto-login, update
checks, and output helpers are forced off. The AXI does not inspect the official
profile or token source.

## Provider-write boundary

The only product provider-write families are MR ensure/create-or-update and the
pinned guarded immediate squash merge.

MR ensure permits only title/description on one exact open same-project
source/target pair. It uses validated project identity, all-page lookup,
duplicate denial, a second GET before POST, private mode-0600 JSON, one POST or
PUT maximum, response validation, and at most one bounded read-only
reconciliation after an unvalidated write. Create reconciliation repeats the
exact-branch lookup. Update reconciliation reads the canonical exact IID and
requires the pre-write MR/project IDs, URL, branches, and head plus the exact
requested content. REST `sha` and official-client `diff_refs.head_sha` are
accepted as two representations of that same head; conflicts, duplicates,
malformed values, or missing identity remain `ambiguous_update`. A verified create absence preserves only a bounded class for
recognized HTTP rejections; read failure, timeout, incomplete state, drift, or
any other uncertain result remains ambiguous.

Guarded merge requires explicit host/project/URL/IID/reviewed-head/authority and
`--squash` at parse time. It additionally requires:

- same-project identity and exact canonical returned URLs;
- provider-side successful-pipeline and resolved-discussion enforcement;
- open, non-draft, conflict-free, currently mergeable state with no existing
  auto-merge or explicit per-merge source-removal intent; a persisted/default
  `force_remove_source_branch` preference is allowed only because the fixed
  merge body explicitly overrides it;
- one exact successful head pipeline and every bounded page of current jobs and
  trigger bridges, with unknown/incomplete state non-green;
- an unconditional adjacent MR recheck;
- one private four-key merge body with expected SHA, squash enabled, auto-merge
  disabled, and source removal disabled;
- one PUT maximum under a 15-second phase timeout, never a mutation retry;
- strict merged identity, strategy, attribution, pipeline, and commit-SHA
  validation; and
- at most one bounded MR GET after an unvalidated outcome. Exact landed state
  reconciles; a still-open MR preserves only a recognized framed rejection;
  otherwise `ambiguous_merge` prevents a blind retry.

Generic API, alternate/unguarded merge, approval, comment/note,
close/reopen/delete, repository mutation, release/label mutation,
secrets/variables, and pipeline/job trigger/retry/cancel/delete remain denied
before child execution.

## Native v1 controls

- exact host/API/web origin and project identity;
- HTTPS only, TLS 1.2 minimum, normal hostname verification, optional explicit
  CA bundle, and no insecure mode;
- environment-token disagreement failure and no token flags/config/output;
- no-UI native keyring; transactional import and no plaintext fallback;
- descriptor-based no-follow private-file reads, private modes, size/UTF-8/NUL
  checks;
- typed route constructors only; no native generic request method;
- same-project MRs and validated returned URLs/branches/IDs/SHAs;
- POST/PUT redirects refused; GET redirects exact-origin/prefix only;
- same-origin/same-route pagination, loop detection, and hard caps;
- bounded GET retry; one mutation plus read-after-ambiguity reconciliation;
- stale MR/head-pipeline/local SHA never green;
- unknown/manual/allowed-failure CI fail-closed normalization;
- signal/context cancellation across HTTP and retry waits;
- bounded redacted trace tail.

Environment proxy discovery occurs only after authority selection; proxy URLs
are never printed. Per-host `proxy_disabled` turns it off. Cross-origin
redirects are rejected before forwarding credentials.

## Hard limits

| Input/output | Limit |
|---|---:|
| host | 253 bytes |
| project | 1,024 bytes / 32 segments |
| branch | 1,024 bytes |
| title | 1,024 bytes |
| description | 128 KiB |
| native JSON page | 2 MiB |
| operation/output | 8 MiB |
| interactive official login output | 8 MiB (relayed, not retained) |
| official data-command child stderr | 4 KiB (never rendered raw) |
| guarded merge phases | 20 s preflight / 15 s PUT / 10 s reconcile (45 s total) |
| pagination | 10 pages / 1,000 items (merge jobs + bridges combined) |
| release download metadata | 100 entries |
| trace tail | 256 KiB |
| product diff | 1 MiB |
| release executable/custody | 128 MiB |
| setup/config/manifest | 1 MiB |

A limit overflow is an error unless a product display/field/trace/diff contract
explicitly returns bounded content with `complete:false` or `truncated:true`.
Partial CI or duplicate-MR lookup is never used for a green/unique decision.

## Deterministic exits

| Exit | Meaning |
|---:|---|
| 0 | success |
| 2 | validation, unsupported, or security-boundary input |
| 3 | authentication or human-interaction required |
| 4 | authenticated but forbidden |
| 5 | resource not found |
| 6 | conflict/duplicate/ambiguous create, update, or merge |
| 7 | rate limited |
| 8 | dependency/version/network/timeout/malformed upstream/internal |
| 9 | authority, URL, secure-storage, TLS, redirect, or local safety violation |
| 130 / 143 | canceled by SIGINT / SIGTERM |

`glab-axi/v1` retains its exact error enum/exit mapping. Product-only dependency,
interactive, and security-boundary codes exist only in `glab-axi/ux-v1`.
Help always exits 0 and performs no auth/dependency/network work.

## Setup/update controls

`setup hooks` plans all files before writes, refuses symlinks/non-regular or
malformed hook structures, preserves unrelated configuration, writes mode-0600
files atomically, and rolls back prior targets on failure. Hook commands are a
fixed portable `gl-axi` or `glab-axi` compatibility name, never a shell-quoted
arbitrary path. Setup always installs the canonical skill and never
authenticates.

Release CI publishes both the canonical `gl-axi` executable and explicit
`glab-axi` compatibility alias, but never the test-only executable named `glab`. It verifies the private
signing secret against a separately configured public key while platform build
jobs receive only that public value. Raw product binaries and packages receive
SHA-256 checksums; checksums have a detached Ed25519 signature and the public key
is published for comparison with a captain-pinned out-of-band value. Raw update
artifacts are covered by an Ed25519-signed canonical manifest. Release binaries
embed only the public key. Separate signed manifests and name-specific
handshakes prevent a canonical update from being confused with a compatibility
candidate. Self-update is explicit and human confirmed, validates
signature/size/checksum/standalone handshake, detects path
replacement, and uses same-directory atomic rename with rollback. Development,
symlinked/package-managed, unsupported-platform, wrong-key, wrong-checksum, and
wrong-handshake paths refuse replacement. No update check occurs during help,
version, dashboard, or native contract execution.

## Residual risks

- Official glab itself is a substantial trusted dependency. Exact-version/help
  CI catches declared drift; behavior or supply-chain compromise within that
  release remains residual.
- Secure-store availability can change between probe and official storage. The
  pinned fallback-warning kill switch reduces but cannot eliminate OS-level
  races; human onboarding acceptance must inspect the resulting storage policy.
- Windows interactive delegation requires the supported Windows ConPTY API. On
  macOS/Linux, stdin remains the human terminal while child output uses a PTY;
  on Windows, terminal input passes through one fixed, wiped relay buffer. A
  platform that cannot establish that monitored terminal boundary fails closed.
- Delegated fixed API calls, including guarded merge, inherit official-glab/
  profile TLS/proxy and HTTP redirect behavior. Exact-version TLS contract tests
  prove the expected one-request paths; native private-host transport controls
  remain stronger.
- Job traces/diffs/descriptions may contain application secrets unknown to
  generic redaction. Least privilege and GitLab masked variables remain
  required.
- Environment tokens are visible to processes authorized to inspect the caller.
- Installed no-mistakes v1.45.4 passes legacy MR content in compatibility argv;
  the direct v1 run-private consumer avoids that exposure.
- A project token with `api` can be powerful within its project; keep it scoped
  and short-lived.
