# Security model

## Trust boundaries

The operator controls the `gl-axi` executable, selected official `glab`
package/profile, explicit target flags, native private-host config, credential
source, local repository, private issue/MR files, setup command, and release
signing key. Git remotes, PATH entries, official child output/stderr, provider
JSON, URLs, pagination, redirects, proxies, traces/diffs, update servers, and existing
agent config are untrusted.

The delegated and native backends have different trust properties and report
the backend. Official-glab results do not inherit claims made by the stricter
native HTTP transport.

## Product delegation controls

- exact official version/build `1.112.0 (816e3a52)`, with pinned public package checksums/source/help;
- regular executable resolution and no shell interpolation;
- operation enum with fixed argv builders—no public/raw argv or API method/path;
- strict command/flag/positional parsing before target, credentials, or child;
- denied-command classification with zero child/request tests;
- closed stdin plus prompt/pager/editor/browser/debug/update suppression for
  data commands;
- three terminal-file checks plus a non-secret secure-store probe for human login;
- PTY/ConPTY login delegation so child stdin/stdout/stderr remain terminals while
  bounded output is relayed and monitored;
- ambient token/job-token removal and exact pinned plaintext-fallback warning
  cancellation, with warning state reconciled before success;
- 5-second version check, ordinary 30/45-second noninteractive operations,
  bounded stdout/stderr, and human login governed by caller/process cancellation;
  `pipeline watch` uses the finite budget in the
  [CI read contract](../contracts/read-parity/ci-reads.json);
- malformed, duplicate, ANSI-prefixed, trailing, non-UTF-8, or oversized
  data-command child output rejected;
- official MR-view `diff_refs.head_sha` and `diff_refs.base_sha` normalized to
  canonical `sha` and `base_sha` fields only when valid; dual values must match
  and missing identity is never invented;
- controlled exit/error mapping without raw stderr or server body;
- typed normalization and per-command JSON schemas;
- HTTPS host and selected repository path validation on returned URLs;
- item/field/page/display caps with explicit completeness/truncation metadata;
- unknown CI/merge states normalize pending-compatible, never green;
- fixed internal API routes only where official v1.112.0 lacks safe JSON
  commands; MR discussion and canonical project-identity routes are GET-only,
  the fork route accepts only a provider-bound positive project ID, issue-edit
  validation owns only exact project/issue/label GETs and exposes no issue PUT,
  and public `api` remains denied;
- MR discussion evidence binds canonical source and target project IDs, paths,
  and URLs to MR global/project IDs, IID, branches, base/head SHAs, URL, and
  `updated_at`, then rechecks those identities after pagination;
- MR discussion notes must repeat the selected MR/project global identities;
  normalized threads repeat that binding, preserve provider ordering, expose
  explicit resolution state, omit note/author URLs, and remain within thread,
  nested-note, field, aggregate-body, page, and operation limits;
- completeness and truncation follow each command's contract; the
  [planning contract](../contracts/gitlab-planning/v19.3.0/README.md#comparison-and-material-differences)
  distinguishes collection completeness from field truncation, and the
  [collaboration contract](../contracts/collaboration-reads/README.md#meaning-and-uncertainty)
  defines successful but incomplete approval evidence;
  malformed, unsupported, unavailable, or drifting identity
  returns a controlled incomplete error instead of partial trusted evidence.

Approved environment credentials pass directly to official glab for headless
product operations but are never placed in AXI argv/output; login removes them
before child execution.
Only `gitlab.com` can be inferred as a host authority. Self-managed remotes need
explicit `--hostname` or `GITLAB_HOST`, preventing an untrusted Git remote from
selecting the destination for an environment credential. `GLAB_DEBUG_HTTP`, CI auto-login, update
checks, and output helpers are forced off. The AXI does not inspect the official
profile or token source.

## Provider-write boundary

The executable command registry owns the provider-write allowlist; see the
[generated command reference](command-reference.md) for declared operations and
feature-contract pointers. Guarded native project-variable controls are in
[CI variables](ci-variables.md).

`board issues` requires `--allow-ordering-initialization`, an explicit host,
and exact project/group, board and list selectors before any child work. The
pinned GitLab EE 19.3.0 GraphQL resolver can initialize missing relative positions
and shift sibling positions, including beyond displayed issues. This is not a
mutation-free read. The success receipt and failures after delegation disclose
`may_have_occurred`, not a changed-record count. No automatic retry or rollback
is attempted. Default `board list` and `board view` never select issues; all
query documents and tier differences are pinned in
`contracts/gitlab-planning/v19.3.0/`.

Issue create/note and state observations require `--auth-source native` and are
pinned in `contracts/issue-writes/v1.json` and `provider-v1.json`. One native client
and credential handles every request; no official-profile fallback or account
equivalence is assumed. The shared boundary refuses all redirects before a second
request. Caller-bound numeric project and issue identities must match canonical
HTTPS URLs under the configured native web authority, including API/web mappings.
Create/note mutations use numeric project routes. Private descriptor-validated
content is bounded before credential resolution, and request JSON stays in memory.

New slash-leading description/comment lines are denied even inside code fences.
After original input bounds, title normalization strips only surrounding ASCII
whitespace. New bodies remove carriage returns and trailing ASCII whitespace;
leading/internal body whitespace and Unicode spaces remain unchanged. Hashes and
exact direct response checks use the canonical request. Normalized-empty and
whitespace-only create descriptions return `unsupported` before credentials or
HTTP, preventing default-template quick actions without filler or compensation.
This temporarily excludes title-only and blank creation, even without a template.

Create/note allow at most one mutation attempt without retry or reconciliation
searches. Their direct responses must prove exact identity and content. Unconfirmed
outcomes remain ambiguous, and another invocation can duplicate the resource.
Close/reopen send no PUT: an actual transition returns `unsupported` with a
`refused` receipt, zero attempts and `mutation_response=not_attempted`.
Already-matching bound states return `unchanged` with `postcondition=preflight`.
Those reads establish observations only, never an atomic precondition or exclusive
authorship. Existing descriptions are not filtered, normalized or resubmitted;
fenced content does not prevent a read-only no-op. Snapshot checks cannot prevent
concurrent content sanitization by GitLab, so no state mutation path remains.

Receipts disclose attempts, response evidence and observed state without private
content or raw provider errors. For refused/no-op state requests, `requested_sha256`
hashes intent only, not a transmitted payload. `retry_safe` and
`atomic_precondition` remain false. No GitHub close reasons or bundled comments
are supported. The [temporary parity gaps](../contracts/issue-writes/review-blockers.md)
remain explicit; the increment does not claim full issue parity or authorize
collateral content/quick-action effects.

Issue-edit validation requires explicit host/project and caller-supplied
canonical URL, state, and `updated_at` for one canonical positive IID. It binds
project ID, full path, and URL plus issue global/project IDs. Title and
description use descriptor-based private-file reads and established size caps.
Requested label names are bounded and comma-free, resolve to one numeric
identity in two complete project/ancestor catalog snapshots, and cannot
duplicate, overlap, resolve through a case alias, or drift. Proposed add/remove
semantics preserve unrelated labels.

The exact issue is read twice and requested labels are resolved before and after
the second read. Any stale state/time, identity mismatch, mutable snapshot
drift, malformed response, or label drift fails closed. Preview returns the
bounded proposed change receipt, and an exact no-op returns `unchanged`.
GitLab's issue PUT accepts no expected issue revision and only label names, so
it cannot atomically bind the validated issue and requested numeric label
identities. A non-no-op live request therefore
returns `safety_violation` with a deterministic `refused`/`not_applied` receipt
under `error.receipt` before mutation. Issue edit has no content/label PUT operation,
creates no mutation body, performs no post-write reconciliation, and cannot
expose residual TOCTOU
as a supported write.

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

Guarded merge requires explicit host/project/URL/IID, exact reviewed source and
target branches, reviewed head, authority, and `--squash` at parse time. It
additionally requires:

- same-project identity, exact canonical returned URLs, and exact
  caller-expected source and target branches;
- provider-side successful-pipeline and resolved-discussion enforcement;
- open, non-draft, conflict-free, currently mergeable state with no existing
  auto-merge or explicit per-merge source-removal intent; a persisted/default
  `force_remove_source_branch` preference is allowed only because the fixed
  merge body explicitly overrides it;
- one exact successful head pipeline and every bounded page of current jobs and
  trigger bridges, with unknown/incomplete state non-green;
- an unconditional adjacent canonical MR recheck that refuses source or target
  branch drift before the PUT;
- one private four-key merge body with expected SHA, squash enabled, auto-merge
  disabled, and source removal disabled;
- one PUT maximum under a 15-second phase timeout, never a mutation retry;
- strict merged identity, expected-branch receipt, strategy, attribution,
  pipeline, and commit-SHA validation; and
- at most one bounded MR GET after an unvalidated outcome. Only exact landed
  state retaining both caller-expected branches reconciles; a still-open MR
  preserves only a recognized framed rejection, and otherwise
  `ambiguous_merge` prevents a blind retry.

Generic API, existing-issue content/label mutation, alternate/unguarded merge,
approval, MR comment/note/reply/resolve/close/reopen, merge-request or label-resource
mutation, MR delete, repository mutation, and other release/pipeline/job writes
remain denied. Issue-edit preview
changes no issue field or label. The exact native
deletion exception below grants no broader write authority.

## Guarded native resource deletion

Only the issue, pipeline, release and separately scoped personal/project snippet
delete leaves are enabled. They require explicit native auth, host/project
selectors, caller-reviewed identities/revisions and operation-specific URL
confirmation. A single native client covers all preflight, recheck, DELETE and
readback requests. Project path selection is bound to a numeric project ID before
mutation. No redirect, write retry, local cleanup, broad `--yes`, tag deletion,
job erasure or child-pipeline deletion is exposed.

Pipeline deletion also requires `--acknowledge-child-cancellation URL` matching
the exact parent URL on every invocation, separately from parent deletion
confirmation. Missing or mismatched acknowledgment refuses before credential or
provider access, including when the reviewed parent status is terminal. GitLab
cancels cancelable jobs before removal and may cancel surviving child pipelines
and their jobs, even if parent deletion later fails. No status or child snapshot
can guarantee absence of that effect across a concurrent change. Receipts record
this acknowledgment and leave child cancellation unverified after any DELETE
attempt; they do not report observed child states or counts. No separate child
cancellation request is issued.

Release deletion also requires `--acknowledge-catalog-unpublication URL` matching
the exact release URL on every invocation, separately from release deletion
confirmation. Missing or mismatched acknowledgment refuses before credential or
provider access. Deleting the last catalog version may unpublish the project's
surviving CI/CD Catalog resource. No catalog-state or version-count snapshot
guarantees absence of that effect across a concurrent change or waives consent.
Receipts record this acknowledgment and leave catalog unpublication unverified
after any DELETE attempt, including ambiguous responses. No separate catalog
mutation request is issued. The release tag and its commit remain independently
checked before and after deletion; tag deletion is never requested.

A successful receipt requires the exact DELETE acknowledgment and scoped 404
readback with an accessible matching parent/account. The release tag must still
match. Initial 404 and 404 after an unacknowledged write never mean successful
deletion. 401/403/network/malformed errors never substitute for not-found. Errors
after the first exact preflight carry bounded non-retryable receipts; intended
effects are not represented as observed effects. Preflight is not atomic with
concurrent updates, permission changes or release/tag recreation, and no undelete
is promised. Label deletion stays disabled because the provider's numeric-ID to
name fallback can target a different label after the selected label disappears.

The pinned routes, status semantics, bounds and temporary label gap live in
[`contracts/resource-delete/v1.md`](../contracts/resource-delete/v1.md). The
Windows persisted-native-config/self-managed mapping limitation is retained.

## Explicit product-native downloads

Only registered native-capable leaves accept `--auth-source native`; the new
download leaves require it, explicit host/project and exact resource selectors.
The existing native resolver/configuration is reused, without a new store,
profile parser, credential export or fallback. One identity/authority covers
all metadata, selection, transfer and recheck requests. Native and official
profiles may represent different accounts.

The product-native client rejects every automatic redirect, including
same-origin cross-path writes, before transmission of a second request. It
never automatically retries. Credential/Host/cookie/framing overrides are
refused; request paths cannot override the configured API origin. Bounded
response headers exclude cookies, credentials and Location; pagination values
remain untrusted and are never automatically followed. Existing delegated
commands retain their defaults and do not inherit these stronger claims.

Downloads publish only into a new directory. Archive entry/path/expansion/CRC
checks complete before extracted-content writes. Links, devices, traversal,
collisions, ZIP64 and nonportable paths fail closed. Unix directory-relative
no-follow operations and Windows relative handles/reparse checks protect
writes; rollback removes only recorded owned entries. Publication never
replaces an existing file or directory. A foreign entry introduced into staging
is preserved and reported as incomplete cleanup, not recursively deleted.
Cooperative cancellation before publication returns `canceled` after owned
cleanup; incomplete cleanup takes precedence and returns `safety_violation`.
A process crash or forced kill can leave private staging.

Private direct artifact/package responses are supported. No credential-free
CDN/storage transfer or arbitrary external release link is claimed: redirects
are controlled refusals. Integrity evidence differs by asset kind and is
specified in the [download contract](../contracts/downloads/v1.json).
Destination-parent directories and the process account remain operator-controlled:
a malicious process with the same OS account can
modify owned data, and no filesystem API here claims isolation from that
account's full privileges.

## Project CI variable controls

See [CI variables](ci-variables.md) and `contracts/ci-variables/v1.json` for the
separate guarded project list/set/delete contract. The complete operation uses
one explicitly selected native client, without official-profile fallback. Provider
values/descriptions are removed at each read boundary, never rendered. Both list families expose only
safe metadata; ordinary variables cannot alias masked, hidden, or protected
entries. Mutations require explicit host/project/scope, expected numeric project
identity, confirmation, and exact metadata guards. Unhidden entries also require
private previous-value checks; hidden values are unavailable and never treated
as matching. One mutation requires provider acknowledgment plus bounded
reconciliation for success; lost responses remain ambiguous. Hidden set observes
metadata, and delete observes exact absence. Receipts disclose unavailable hidden
value verification and the lack
of provider CAS and immutable variable identity. Unsupported version/capability,
incomplete inventories, class transitions, and uncertain outcomes fail closed.

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

Product-native transport and download bounds are owned by the
[download contract](../contracts/downloads/v1.json), including the client
lifetime and the shorter download CLI deadline. CI-variable operation bounds
are documented in [CI variables](ci-variables.md#outcomes-races-and-bounds).
Deletion-specific request, phase and response bounds live in the
[resource-deletion contract](../contracts/resource-delete/v1.md#outcome-and-concurrency-rules).
Product CI-read request, trace-selection, final-data and watch budgets are
pinned in the [CI read contract](../contracts/read-parity/ci-reads.json).

| Input/output | Limit |
|---|---:|
| host | 253 bytes |
| project | 1,024 bytes / 32 segments |
| branch | 1,024 bytes |
| title | 1,024 bytes |
| requested label name / changes | 1,024 bytes / 100 |
| exact issue labels / label catalog | 1,000 / 10 pages |
| description / individual discussion body | 128 KiB |
| all discussion bodies / nested notes | 2 MiB / 1,000 notes |
| JSON page | 2 MiB |
| shared operation/output ceiling (command-specific budgets may be lower) | 8 MiB |
| interactive official login output | 8 MiB (relayed, not retained) |
| official data-command child stderr | 4 KiB (never rendered raw) |
| issue-edit validation | 20 s preflight (30 s outer read budget), no PUT |
| guarded merge phases | 20 s preflight / 15 s PUT / 10 s reconcile (45 s total) |
| pagination | 10 pages / 1,000 items (merge jobs + bridges combined) |
| existing release-view metadata | 100 entries |
| ZIP/file paths | portable ASCII / 1,024 bytes per relative path / 255 bytes per component |
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
| 6 | conflict/duplicate, ambiguous MR create/update/merge, ambiguous resource deletion, or ambiguous CI-variable mutation |
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
- Delegated fixed API calls, including issue-edit validation and guarded merge,
  inherit official-glab/profile TLS, proxy, and redirect behavior. Returned
  project and resource identities must still match the selected canonical
  target. Exact-version TLS contract tests prove the expected issue GET paths
  and guarded MR mutation paths; native private-host transport controls remain
  stronger.
- Job traces/diffs/descriptions may contain application secrets unknown to
  generic redaction. Least privilege and GitLab masked variables remain
  required.
- Environment tokens are visible to processes authorized to inspect the caller.
- Installed no-mistakes v1.45.4 passes legacy MR content in compatibility argv;
  the direct v1 run-private consumer avoids that exposure.
- A project token with `api` can be powerful within its project; keep it scoped
  and short-lived.
