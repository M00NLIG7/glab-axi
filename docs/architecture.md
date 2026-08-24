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
          `-> typed official-glab adapter (exactly 1.112.0 / 816e3a52)
               -> fixed argv builders
               -> bounded child execution
               -> typed normalization
               -> glab-axi/ux-v1 TOON/JSON
```

Both executable names enter the same router; only the version handshake retains
the selected name. The router recognizes the exact legacy automation forms
before creating an official-glab adapter. `--contract glab-axi/v1` is the explicit alias. Native
commands do not resolve PATH, inspect official config, emit update notices, or
fall back to product authentication. Product commands likewise do not read or
export the native keyring. The two credential stores are intentionally not
interoperated.

`cmd/glab-compat` imports the native typed core for one pinned legacy consumer.
It never spawns `gl-axi`, and normal product builds/releases never produce an
executable named `glab`.

## Product command registry

`internal/product/registry.go` is executable policy, not only documentation. It
owns every command path, usage, repository requirement, accepted command flag,
backend, output schema, and write classification. Top/parent/leaf help, the
Agent Skill, and `docs/command-reference.md` derive from that registry.

Parsing is command-first and fail-closed. Only declared global flags are
accepted; `-R`, `--repo`, and long flags support space or equals forms. Duplicate
aliases, unknown flags, NUL/newline values, excess positionals, and undeclared
subcommands fail before target resolution. Permanent denial names have a
separate `security_boundary` error and never construct a child process.

Host precedence is explicit `--hostname`, `GITLAB_HOST`, an exact
`gitlab.com` origin, then `gitlab.com`. Repository precedence is explicit
`-R/--repo`, then local origin. A self-managed origin may supply the repository
only after explicit hostname/environment authority; an arbitrary SSH remote
never chooses where an approved environment credential is sent. Git context
never exposes a native credential or changes the native API authority mapping.

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
   `diff_refs.head_sha` with the REST-shaped `sha` field, requiring equality
   when both are present and refusing invalid, missing, or conflicting identity
   during post-write proof; and
9. maps child failures to controlled errors without rendering upstream stderr.

Most reads use official commands with documented JSON output. Operations for
which v1.112.0 has no safe dedicated JSON command—job detail/trace, bounded
search, MR ensure, and guarded MR merge—use internal fixed `glab api` routes.
The public AXI has no `api` command, endpoint/method/header/body authority, or
passthrough. Every fixed API argv is represented in the upstream capability
fixture and exact-argv tests. Guarded merge callers cannot choose any route,
method, query, header, or body field.

List adapters request a one-item probe beyond the display limit, use at most 100
items/page and 10 pages, and never claim completeness when a display/provider
hard limit is reached. Normalizers keep documented fields only, normalize
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
canonical MR URL, canonical positive IID, reviewed lowercase 40/64-hex head,
authority enum, and `--squash` before target or delegate construction. Alternate
strategies, auto-merge, rebase, source deletion, messages, aliases, and generic
fields remain security denials. Nested namespaces and explicit host ports are
supported; official profiles whose web base has an additional relative path are
outside this first consumer contract and fail exact URL validation.

The state machine uses the official profile without reading its credential:

1. validate exact project identity and require provider-side successful-pipeline
   and resolved-discussion policies;
2. read the exact same-project MR with merge-status recheck and bind URL/IID/head;
3. reject draft, conflict, unresolved, auto-merge, explicit per-merge
   source-removal intent, unknown, or nonmergeable states;
4. require the provider-designated head pipeline at the expected SHA to be
   `success`, then consume every bounded page of non-retried jobs and trigger
   bridges with fail-closed status/identity checks;
5. always re-read the MR adjacent to mutation and require the same pipeline;
6. write a mode-0600 body containing only `sha`, `squash:true`,
   `should_remove_source_branch:false`, and `auto_merge:false`;
7. execute one fixed merge PUT under a 15-second phase budget, never retry it;
8. validate exact merged identity, attribution, squash commit, strategy and
   pipeline; and
9. after any unvalidated mutation outcome, perform at most one bounded MR GET.

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
that Firstmate owns task metadata, durable expected head, canonical URL, and
captain/standing-yolo authority. This stage does not modify or integrate
Firstmate. All other provider mutations remain denied.

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
- Output is capped at 8 MiB; traces are a redacted 256 KiB tail; diffs are 1 MiB.
- Errors never include causes, server HTML, headers, cookies, tokens, proxy URLs,
  official config paths, or raw child stderr.
