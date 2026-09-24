# Architecture

## One implementation, two executable names, two non-fallback lanes

```text
cmd/gl-axi (canonical) / cmd/glab-axi (compatibility alias)
  -> router
     |-> exact/explicit glab-axi/v1 grammar
     |    -> native config + native credential resolver
     |    -> typed direct REST service
     |    -> frozen v1 TOON/JSON
     |
     `-> product registry and strict parser
          |-> local help/setup/signed update
          |-> declared --auth-source native product operations
          |    -> native config/resolver + productnative bounded HTTP
          |    -> feature-owned download / issue-write / CI-variable / deletion handlers
          |    -> exact identities; downloads use safedownload publication
          |    -> glab-axi/ux-v1 TOON/JSON
          `-> default typed official-glab adapter (exactly 1.112.0 / 816e3a52)
               -> fixed argv builders
               -> bounded child execution
               -> typed normalization
               -> glab-axi/ux-v1 TOON/JSON
```

Both executable names enter the same router; only the version handshake retains
the selected name. The router recognizes the exact legacy automation forms
before creating an official-glab adapter. `--contract glab-axi/v1` is the explicit alias. Native
commands do not resolve PATH, inspect official config, emit update notices, or
fall back to product authentication. Default product commands do not read the
native keyring. Declared product-native leaves explicitly reuse the existing
native resolver, never the official profile. The two credential stores are not
copied, exported, or automatically interoperated.

`cmd/glab-compat` imports the native typed core for one pinned legacy consumer.
It never spawns `gl-axi`, and normal product builds/releases never produce an
executable named `glab`.

## Product command registry

`internal/product/registry.go` assembles executable policy, including the planning
definitions in `internal/product/planning.go`. The registry owns every command
path, usage, repository requirement, accepted command flag, backend, output
schema, and write classification. Top/parent/leaf help, the Agent Skill, and
`docs/command-reference.md` derive from that registry.

Parsing is command-first and fail-closed. Only declared global flags are
accepted; `-R`, `--repo`, and long flags support space or equals forms. Duplicate
aliases, unknown flags, NUL/newline values, excess positionals, and undeclared
subcommands fail before target resolution. Denied command names have a
separate `security_boundary` error and never construct a child process.

Common target selection is documented in
[Product commands](../README.md#product-commands); command-specific requirements
are in the [generated command reference](command-reference.md). Where host
inference is permitted, precedence is explicit `--hostname`, `GITLAB_HOST`, an
exact `gitlab.com` origin, then `gitlab.com`. Git context never exposes a native
credential or changes the native API authority mapping.

## Explicit product-native boundary

`Definition.NativeAuth` enables the single shared `--auth-source native`
selector; `RequireNativeAuth` also requires deliberate selection on new
native-only leaves. `openNative` opens one `internal/productnative.Client` per
complete operation, using existing native configuration, credential resolution
and TLS patterns. The client owns the selected authority/credential, refuses
all redirects and automatic retries, uses fresh HTTP/1.1 connections to avoid
HTTP/2 refused-stream replay, and bounds requests, responses and lifetime.
[`internal/productnative/client.go`](../internal/productnative/client.go) owns
the shared transport limits. Feature handlers retain their own route, identity,
expected-state authority and operation budgets, pinned in the
[download contract](../contracts/downloads/v1.json),
[issue-write contract](../contracts/issue-writes/provider-v1.json),
[resource-deletion contract](../contracts/resource-delete/v1.md), and
[CI-variable documentation](ci-variables.md#outcomes-races-and-bounds).
This internal request interface does not expose generic user HTTP authority.

`internal/safedownload` pins destination-parent directory descriptors and uses
exclusive creation plus atomic no-replace directory publication. It never
merges into an existing destination. Archive parsing is bounded before ZIP file
table allocation, and complete path/type/collision/size/CRC validation precedes
content writes. Unix uses no-follow openat operations and rename-exclusive
publication; Windows uses relative NT handles, reparse-point refusal, private
DACLs and handle-based no-replace publication. Rollback removes only recorded
operation-owned entries, not arbitrary destination contents.

Job artifact selection binds project, job, pipeline, ref and commit before and
after download. Release selection binds project, tag/commit and exact link
ID/name, with complete paginated catalogs and matching project-backed package
or raw-job routes. Generic package bytes are checked against provider SHA-256
and size. Job archive CRC/size and local SHA-256 receipts do not pretend to be a
provider digest. All redirect/CDN/external-link cases fail closed in this
increment; direct authenticated private responses are supported. The provider
and consumer evidence is in `contracts/downloads/v1.json`.

## Pinned official-glab adapter

`internal/delegate/glab` exposes an operation enum and typed request, not a raw
argv function. Each operation has one fixed builder in
`contracts/official-glab/v1.112.0/capabilities.json`. The runtime:

1. resolves a regular executable named `glab` without a shell;
2. executes `glab version` under a five-second/output cap;
3. accepts only exact semantic version `1.112.0` and packaged build `816e3a52`;
4. disables update checks, debug HTTP, CI auto-login, color, pagers, editors,
   browsers, and prompts for data commands;
5. preserves approved GitLab token environment sources for headless reads but
   strips them from human login;
6. bounds stdout/stderr and noninteractive operation time;
7. rejects malformed, duplicate, prefixed, trailing, oversized, or non-UTF-8 output;
8. at the `mr view` adapter boundary, reconciles the pinned client's
   `diff_refs.head_sha` and `diff_refs.base_sha` with REST-shaped `sha` and
   `base_sha` fields, requiring equality when both representations are present
   and refusing invalid, missing, or conflicting identity during evidence or
   post-write proof; and
9. maps child failures to controlled errors without rendering upstream stderr.

Most reads use official commands with documented JSON output. Operations for
which v1.112.0 has no safe dedicated JSON command, including job detail/trace,
bounded search, MR discussions, delegated issue-edit validation, MR ensure, and
guarded MR merge, use internal fixed `glab api` routes. The
[official adapter contract](../contracts/official-glab/v1.112.0/README.md)
owns the distinction between delegated issue reads and native-only edits.
The public AXI has no `api` command, endpoint/method/header/body authority, or
passthrough. Every fixed API argv is represented in the upstream capability
fixture and exact-argv tests. Guarded merge callers cannot choose any route,
method, query, header, or body field.

List adapters normally request a one-item probe beyond the display limit, use
at most 100 items/page and 10 pages, and never claim completeness when a
display/provider hard limit is reached. Repository creation-order search is the
exception to display-limit probing; its complete-collection requirement is owned
by the [discovery and search contract](../contracts/discovery-search/README.md).
MR discussions resolve the selected target project to a canonical numeric ID,
full path, and validated URL, then bind the requested IID
to its provider MR ID, source/target project IDs, branches, authoritative
base/head SHAs, URL, and `updated_at`. A fork source project is resolved through
a fixed GET whose positive numeric ID comes only from that bound MR. Project and
MR identities are re-read after pagination; any drift rejects the evidence
window. Discussion notes must repeat the bound MR and target-project global IDs,
and each normalized thread repeats them to prevent cross-request substitution.
Threads expose explicit `resolved`, `unresolved`, or `not_resolvable` state. A
missing or null note-resolution value is accepted only when the provider
explicitly marks the note non-resolvable.

Discussion reads preserve provider thread/note order, cap nested notes at 1,000,
cap each body at the description bound and all emitted note bodies at 2 MiB,
and omit provider fields such as author-profile and note URLs. Any page,
display, nested-record, or field truncation makes the evidence incomplete with
a machine-readable reason. Normalizers keep documented fields only, normalize
unknown CI/merge states to pending-compatible values, validate HTTPS host and
repository URL paths, cap individual fields, and set backend/completeness/
truncation metadata. Raw official documents never cross the product envelope.

## Authentication split

`auth login` is the sole interactive delegated command. It requires all three
configured streams to be terminal-backed files (not only character devices),
removes ambient credential variables, writes/deletes a random non-secret
sentinel via the same cross-platform keyring library pinned by official glab,
and clears CI storage mode.

The login child runs behind a terminal-preserving monitored boundary: direct
human-terminal stdin plus PTY stdout/stderr on macOS/Linux, and ConPTY with a
fixed-buffer input relay on Windows. All three child descriptors therefore pass
real terminal checks. The combined terminal output is relayed to parent stderr
while a fixed-size window detects the exact pinned plaintext-fallback warning;
malformed or more than 8 MiB of interactive output fails closed. Warning,
monitor, cancellation, child-exit, and relay-drain states are reconciled before
success, so a warning cannot race a zero exit. Input bytes are not logged or
copied to product output. Login follows caller/process cancellation instead of
the ordinary 30-second request deadline; its output and teardown bounds remain
enforced. An unavailable secure store or warning cancels login, and no official
credential/config file is opened by gl-axi.

Product stdout remains one parseable envelope. Data commands always have stdin
closed and prompts disabled. `auth status` does not expose `--show-token`; its
child output is discarded and only a normalized boolean/host result crosses
the boundary.

The native lane retains its own noninteractive environment/keyring model. Its
stdin import is transactional: validation occurs first, keyring state is saved,
config is atomically written, and a config failure restores the prior keyring
entry. Private MR files are opened with no-follow semantics and validated from
the descriptor to prevent path-swap reads.

## Issue edit: best-effort guarded mutation

Product `issue edit --auth-source native` supports title, description, and label
deltas with drift checks and bounded reconciliation. After validating private
input, it opens the shared `productnative` client once and uses its selected
credential and configured API/web authority for the entire operation. The
feature adapter selects only its closed routes; it owns no credential resolver
or HTTP policy. Native and official identities are not assumed equivalent.
Omitting the selector preserves delegated preview/no-op validation and returns
`native_auth_required` for live changes before any PUT. No fallback occurs. Its parser requires an explicit host and nested project,
canonical positive IID and issue URL, `opened` or `closed` expected state, exact
RFC 3339 `updated_at`, and at least one title, description, or label request.
It validates these values before target or child discovery. Content comes only
from descriptor-validated private regular non-symlink files at the established
title and description bounds. A title cannot be blank; an empty description
represents a clear. Slash-leading description lines are refused, including
inside code blocks, because GitLab's update service interprets quick actions.
The effective existing description is checked before a title/label-only write.

Label arguments are repeatable and comma-free. The command rejects empty,
duplicate, case-colliding, overlapping, missing, ambiguous, or case-substituted
names. It consumes every bounded page of project and inherited labels, resolves
requested names to numeric identities, repeats that complete lookup after the
second issue read, and rejects identity drift. Add/remove semantics preserve
unrelated labels without sending a replacement set.

The state machine is:

1. resolve and validate project numeric ID, exact full path, and canonical URL;
2. read the exact issue and bind global ID, project ID, IID, URL, state,
   `updated_at`, title, description, and the complete issue-label set;
3. resolve requested labels when needed, read the exact issue again, then repeat
   label resolution and reject stale issue evidence, snapshot drift, or label
   identity drift;
4. return `preview` for `--dry-run` or `unchanged` for an exact no-op, with zero
   mutation;
5. send one fixed PUT to the validated numeric project ID and issue IID with
   only changed title/description and comma-separated label deltas;
6. validate the response and read the exact canonical issue once. Require bound
   identity/state, intended title/body/labels, and a non-regressing timestamp.
   Re-resolve all requested label identities in one bounded catalog;
7. return `updated` or `reconciled_update` with `observed_applied` only after
   verification. An intelligible wrong-identity or wrong-poststate response
   remains ambiguous even if a later read matches. Lost/malformed responses may
   reconcile by canonical observation. Any unproven result returns
   `ambiguous_update` with an `ambiguous`/`unknown` receipt. Never retry or roll back.

GitLab accepts no atomic expected revision and writes labels by name, not ID.
A residual check/write race remains: concurrent edits can be overwritten and
renamed/deleted labels can be reused or recreated by `add_labels`. Separate reads
cannot prove the absence of concurrent writes. Receipts explicitly disclose
`best_effort` and the race; `observed_applied` proves observed state, not which
actor applied it. No-op fields are not sent and unrelated labels are never replaced.

Receipts include bound identity, caller evidence, ordered changes, and requested
label IDs. Native private-text evidence is always byte counts and SHA-256 digests,
including previews or rejected payloads; unverified proposed content cannot bypass
the native response credential scanner by appearing verbatim in a receipt.
The default delegated preview retains its bounded values/digests. The
[receipt schema](../schema/ux-v1/issue-edit.schema.json) owns action-dependent
fields, including timestamp presence. Phase and response budgets are pinned in
[the consumer contract](../contracts/issue-edit/v2.json); caller cancellation
remains effective throughout.

`contracts/issue-edit/v2.json` supersedes the historical validation-only v1.
Persisted native config and self-managed mapping on the shipped Windows CLI
remain unproven. Assignees, milestones, attachments, and GitHub organization types remain explicit
parity gaps; GitLab incident/task types are not treated as equivalents. Creation,
comments, state changes, hierarchy and boards are separate contracts.

## Typed issue creation, notes and state observations

`contracts/issue-writes/v1.json` is separate from issue-edit concurrency guarantees.
`commands_issue_write.go` requires explicit native opt-in and validates private
content before opening one shared native client. Nonblank create and plain notes
send one fixed in-memory payload after caller-bound numeric identity and configured
web-URL validation. The same credential serves all requests, without official-profile
fallback or account-equivalence assumptions. Create and note never search for
reconciliation or retry automatically. Direct response identity and content must
match exactly; unconfirmed writes remain ambiguous.

State actions retain identity and expected-state preflight observations but have
no mutation dispatch or readback path. Already-matching states return `unchanged`;
actual transitions return `unsupported` with a `refused` receipt and zero attempts.
Existing descriptions are not inspected for slash lines or normalization. This
prevents provider content sanitization even when a concurrent writer changes the
description after preflight. It does not provide state-transition parity or an
atomic precondition. Blank and whitespace-only create descriptions are also
temporarily refused before credential resolution because default templates can
execute unrequested quick actions. See the
[temporary parity gaps](../contracts/issue-writes/review-blockers.md).

Original private-file bounds precede normalization. Titles strip surrounding ASCII
whitespace; new descriptions/notes remove carriage returns and trailing ASCII
whitespace. New quick-action-shaped lines are denied before credential resolution.
Canonical request hashes and exact response comparisons retain ordinary newline
files without weakening provider evidence. State hashes represent requested intent
only. The receipt schema is owned by `IssueWriteSchema` and emitted alongside help
and skills by `cmd/gen-product`.

Preflight/mutation budgets remain 10/20 seconds inside the 45-second operation
lifetime. Fixed reads need no pagination. The shared native client owns both the
2 MiB response bound and 8 MiB aggregate budget. Receipts keep
`atomic_precondition=false` and `retry_safe=false`.

## MR ensure: bounded create/update write

Product `mr ensure` / `mr create-or-update` uses official authentication but
preserves native ensure semantics:

1. fetch and validate exact project identity;
2. perform bounded all-page lookup by open source/target branch;
3. fail on more than one match;
4. replay identical content without a write;
5. update only title/description for one differing match;
6. recheck immediately before create;
7. place the fixed JSON body in a private mode-0600 temporary file;
8. perform exactly one POST or PUT through the fixed adapter; and
9. after an unvalidated result, perform at most one bounded read-only
   reconciliation—branch lookup after create or canonical exact-IID MR view
   after update—never blind retry.

An update reconciles only when the canonical MR retains the pre-write global
ID, IID, project ownership/destination, URL, source/base branches, and head SHA
and exactly matches the requested title/description. The adapter accepts either
the prior REST `sha` shape or official `diff_refs.head_sha`; dual values must
match, and it never invents a missing head. A create still reconciles
through the bounded exact-branch lookup; after a verified empty result,
recognized HTTP rejections retain only their bounded status category.
Transport, overflow, timeout, incomplete identity, drift, and malformed or
otherwise unverifiable results remain ambiguous.

## Guarded MR merge: immediate squash write

Product `mr merge` exists only for the pinned Firstmate consumer contract under
`contracts/firstmate/`. Its parser requires an explicit host/repository,
canonical MR URL, canonical positive IID, exact reviewed source and target
branches, reviewed lowercase 40/64-hex head, authority enum, and `--squash`
before target or delegate construction. Alternate strategies, auto-merge,
rebase, source deletion, messages, aliases, and generic
fields remain security denials. Nested namespaces and explicit host ports are
supported; official profiles whose web base has an additional relative path are
outside this first consumer contract and fail exact URL validation.

The state machine uses the official profile without reading its credential:

1. validate exact project identity and require provider-side successful-pipeline
   and resolved-discussion policies;
2. read the exact same-project MR with merge-status recheck and require the
   caller-bound URL, IID, source branch, target branch, and head;
3. reject draft, conflict, unresolved, auto-merge, explicit per-merge
   source-removal intent, unknown, or nonmergeable states;
4. require the provider-designated head pipeline at the expected SHA to be
   `success`, then consume every bounded page of non-retried jobs and trigger
   bridges with fail-closed status/identity checks;
5. prepare a mode-0600 body containing only `sha`, `squash:true`,
   `should_remove_source_branch:false`, and `auto_merge:false`, then re-read the
   canonical MR as the final provider operation before mutation and require the
   expected source branch, target branch, head, and pipeline;
6. execute one fixed merge PUT under a 15-second phase budget, never retry it;
7. validate exact merged identity, expected branches, attribution, squash
   commit, strategy, and pipeline; and
8. after any unvalidated mutation outcome, perform at most one bounded MR GET
   that must retain the caller's expected branches before proving success.

GitLab's `force_remove_source_branch` is a persisted/default preference, not
immutable merge intent. It is accepted because the fixed body explicitly sends
`should_remove_source_branch:false`, which overrides that preference;
`should_remove_source_branch:true` remains a refusal both before mutation and
in the merged postcondition.

An exact postcondition yields `merged`, `already_merged`, or
`reconciled_merged`. A definite framed rejection is returned only after the MR
is still open; transport, timeout, malformed output, cancellation after the PUT
boundary, identity drift, or an unproved result returns `ambiguous_merge`
(exit 6). The 45-second outer budget is partitioned into at most 20 seconds of
preflight, 15 seconds for the mutation, and 10 seconds for reconciliation. No
path issues a second PUT.

`gl-axi` owns provider truth and one mutation. The pinned contract records
that Firstmate owns task metadata, durable expected source/target branches and
head, canonical URL, and captain/standing-yolo authority. This stage does not
modify or integrate Firstmate. See the
[provider-write boundary](security.md#provider-write-boundary) for the supported
write families and denials.

[CI-variable operations](ci-variables.md) require explicit native selection and use one shared
`productnative.Client` for their complete operation. Feature-owned numeric-project
routes and exact scope filters never use the official profile. `internal/civariable`
removes values/descriptions at each inventory read boundary, retaining only
metadata and non-serialized private equality results for unhidden entries.
Hidden entries use exact observable metadata guards and report unavailable value
verification. Complete bounded inventories establish absence or the applicable
prestate; mutation success also requires provider acknowledgment. Help and owned schemas are generated
through `go run ./cmd/gen-product`.

## Native authority and CI semantics

The direct v1 lane keeps the stronger explicit authority model. Non-secret
config binds a logical host/Git hosts to complete HTTPS API and web bases.
`gitlab.com` has a built-in mapping; every private host, alternate API host,
port, CA, or relative-URL installation must be explicit. Project metadata and
returned URLs must match before operations. Fork MRs remain outside v1.

Native CI takes one complete snapshot. Every jobs page is required; MR SHA,
head-pipeline SHA, and available local HEAD must agree before success is
reported. Unknown/manual/allowed-failure states retain fail-closed
normalization. The external no-mistakes consumer owns polling.

## Local setup and update

`setup hooks` computes every target before writing, refuses symlink/non-regular
config, preserves unrelated JSON/TOML, writes atomically, and rolls back earlier
files on failure. It installs the canonical `gl-axi` skill plus Claude
Code/Codex SessionStart hooks that invoke the selected canonical or compatibility
executable; it performs no auth and does not remove an existing legacy skill.

Release binaries embed an Ed25519 update public key. Release CI first proves the
private signing secret matches the separately configured public key, publishes
the public key plus signed checksums, and never supplies the private key to
platform build jobs. `update --check` fetches only the fixed HTTPS manifest,
verifies its canonical signature, and validates exactly one platform artifact. A human-only
apply selects exactly one platform artifact, enforces the 128 MiB custody bound,
verifies signed size/SHA-256 and the name-specific candidate standalone
handshake, detects a path swap, and uses same-directory rename/rollback.
Canonical `gl-axi` and compatibility `glab-axi` assets use separate signed
manifests so existing updaters remain valid. Development builds,
symlink/package-managed installs, and Windows self-replacement fail closed.

## Output contracts

- `glab-axi/v1` remains byte/field compatible; its command data schemas are
  under `schema/v1/`.
- `glab-axi/ux-v1` has a separate envelope and one closed data schema per
  product command under `schema/ux-v1/`.
- TOON and JSON use the same normalized fields and deterministic ordering.
- Issue-edit receipt handling is described in
  [the issue-edit architecture](#issue-edit-best-effort-guarded-mutation).
  `issueEditTextValue` and `issueEditLabelSetValue` in
  `internal/product/commands_issue_edit.go` own evidence thresholds and hash
  encoding; `issueEditReceipt` applies the native text-confidentiality policy.
- Shared output bounds and pointers to tighter command-specific budgets are
  in the [security model](security.md#hard-limits).
- Errors never include causes, server HTML, headers, cookies, tokens, proxy URLs,
  official config paths, or raw child stderr.
