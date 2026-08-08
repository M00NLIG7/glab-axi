# Architecture

## One executable, two non-fallback lanes

```text
cmd/glab-axi
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

The router recognizes the exact legacy automation forms before creating an
official-glab adapter. `--contract glab-axi/v1` is the explicit alias. Native
commands do not resolve PATH, inspect official config, emit update notices, or
fall back to product authentication. Product commands likewise do not read or
export the native keyring. The two credential stores are intentionally not
interoperated.

`cmd/glab-compat` imports the native typed core for one pinned legacy consumer.
It never spawns `glab-axi`, and normal product builds/releases never produce an
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
6. bounds stdout/stderr and operation time;
7. rejects malformed, prefixed, trailing, oversized, or non-UTF-8 output; and
8. maps child failures to controlled errors without rendering upstream stderr.

Most reads use official commands with documented JSON output. Operations for
which v1.112.0 has no safe dedicated JSON command—job detail/trace, bounded
search, and MR ensure—use internal fixed `glab api` routes. The public AXI has no
`api` command, endpoint/method/header/body authority, or passthrough. Every
fixed API argv is represented in the upstream capability fixture and exact-argv
tests.

List adapters request a one-item probe beyond the display limit, use at most 100
items/page and 10 pages, and never claim completeness when a display/provider
hard limit is reached. Normalizers keep documented fields only, normalize
unknown CI/merge states to pending-compatible values, validate HTTPS host and
repository URL paths, cap individual fields, and set backend/completeness/
truncation metadata. Raw official documents never cross the product envelope.

## Authentication split

`auth login` is the sole interactive delegated command. It checks all three
standard streams with a real terminal/console test (not only character-device
mode), removes ambient credential variables, writes/deletes a random non-secret sentinel via
the same cross-platform keyring library pinned by official glab, clears CI
storage mode, and watches stderr for the exact pinned plaintext-fallback
warning. An unavailable secure store or warning cancels login before accepting
a token. No official credential/config file is opened by glab-axi.

Official login text is relayed to the human terminal on stderr so final product
stdout remains one parseable envelope. Data commands always have stdin closed
and prompts disabled. `auth status` does not expose `--show-token`; its child output is discarded and only a normalized
boolean/host result crosses the boundary.

The native lane retains its own noninteractive environment/keyring model. Its
stdin import is transactional: validation occurs first, keyring state is saved,
config is atomically written, and a config failure restores the prior keyring
entry. Private MR files are opened with no-follow semantics and validated from
the descriptor to prevent path-swap reads.

## MR ensure: only provider write

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
9. reconcile every transport, malformed-success, or content ambiguity with GET
   only—never blind retry.

Same-project IDs, exact branches, state, returned host/repository URL, and
content are revalidated. All other provider mutations remain denied.

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
files on failure. It installs generated skills plus Claude Code/Codex
SessionStart hooks that invoke the bounded dashboard; it performs no auth.

Release binaries embed an Ed25519 update public key. Release CI first proves the
private signing secret matches the separately configured public key, publishes
the public key plus signed checksums, and never supplies the private key to
platform build jobs. `update --check` fetches only the fixed HTTPS manifest,
verifies its canonical signature, and validates exactly one platform artifact. A human-only
apply selects exactly one platform artifact, enforces the 128 MiB custody bound,
verifies signed size/SHA-256 and the candidate standalone handshake, detects a
path swap, and uses same-directory rename/rollback. Development builds,
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
