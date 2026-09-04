# gl-axi

`gl-axi` is a bounded GitLab experience for humans and agents. The Go module,
primary executable, package, release artifacts, documentation, and generated
Agent Skill use the `gl-axi` identity. The repository remains
[`M00NLIG7/glab-axi`](https://github.com/M00NLIG7/glab-axi).

Two deliberately separate backends share one executable:

- a product-facing lane delegates a closed, version-tested allowlist of bounded
  reads, exact-identity issue-edit validation, two MR write contracts, and human
  login to **official `glab` 1.112.0 (`816e3a52`)**; and
- the frozen native `glab-axi/v1` lane performs the proven MR/CI automation
  contract directly and remains fully standalone for no-mistakes custody.

This is not `exec glab "$@"`. Unknown commands and out-of-scope mutation
shapes fail before executable discovery, credential resolution, child
execution, or HTTP. A declared issue-edit request performs bounded read-only
validation before refusing any non-no-op live change. Every delegated result
is bounded and normalized into the stable `glab-axi/ux-v1` wire contract; raw
official-glab JSON and stderr never pass
through.

> **Activation status:** v0.2 is a release candidate. Do not replace an
> installed provisional v0.1.0 until no-mistakes, genuine CI, human onboarding,
> distribution, and separately approved read-only provider acceptance pass.

## Product commands

```text
gl-axi                               # current-project dashboard

gl-axi auth login [--hostname H]    # human TTY only
gl-axi auth status [--hostname H]

gl-axi issue list|view
gl-axi issue edit IID ... --expected-url URL --expected-state STATE --expected-updated-at TIMESTAMP  # dry-run preview; live changes fail closed
gl-axi mr list|view|checks|diff|discussions
gl-axi mr ensure                     # bounded create/update write
gl-axi mr create-or-update           # same ensure semantics
gl-axi mr merge IID ... --squash     # guarded exact-head write
gl-axi pipeline list|view
gl-axi job list|view|trace
gl-axi release list|view
gl-axi repo list|view
gl-axi label list
gl-axi search issues|mrs|repos|commits|code

gl-axi setup hooks
gl-axi update [--check]
```

Use current Git context or command-first `-R/--repo namespace/project` and
`--hostname host`; space and equals forms are accepted. A `gitlab.com` remote
may supply both defaults. Any self-managed remote must be paired with explicit
`--hostname` or `GITLAB_HOST` authority so an untrusted remote cannot select
where an environment credential is sent. `--limit` never raises hard limits.
TOON is default; `--format json` selects the versioned JSON contract. Help is
local and does not probe authentication or execute official `glab`. See the
[generated command reference](docs/command-reference.md).

`mr discussions` gives agents bounded, read-only evidence for one merge request
without a browser. `--limit` counts discussion threads; provider thread and note
order is preserved. Each successful result binds the MR global ID and IID,
source/target branches, authoritative base/head SHAs, and canonical source and
target projects by numeric ID, full path, and validated URL. The MR and project
identities are read again after pagination, and an identity or `updated_at`
change rejects the snapshot instead of mixing observations. Forks are explicit
through `same_project` and distinct project identities.

Threads repeat target-project and MR IDs for citation and substitution safety,
and include stable discussion/note IDs, authors, timestamps, bodies, canonical
resolution state, resolver details, and supplied diff position metadata. An
omitted or `null` resolution is accepted only for a note explicitly marked
non-resolvable. The command never invents note URLs and never exposes a reply or
resolve operation. Pagination remains within 10 pages and 1,000 threads; nested
output keeps at most 1,000 notes, 128 KiB per body, and 2 MiB across bodies.
Any display, page, nested-record, or field truncation makes `meta.complete`
false with a machine-readable reason.

```sh
glab-axi mr discussions 42 -R group/project --hostname gitlab.com --limit 1000 --format json
```

```json
{
  "schema": "glab-axi/ux-v1",
  "ok": true,
  "data": {
    "discussions": [{
      "id": "thread-id",
      "target_project_id": 99,
      "merge_request_id": 7007,
      "merge_request_iid": 42,
      "individual_note": false,
      "resolvable": true,
      "resolved": false,
      "resolution_state": "unresolved",
      "notes": [{
        "id": 9001,
        "author": {"id": 17, "username": "reviewer", "name": "Reviewer"},
        "created_at": "2024-01-02T03:04:05Z",
        "updated_at": "2024-01-02T03:04:06Z",
        "body": "Please cover the empty case.",
        "system": false,
        "resolvable": true,
        "resolved": false
      }]
    }],
    "mr": {
      "id": 7007,
      "iid": 42,
      "project_id": 99,
      "web_url": "https://gitlab.com/group/project/-/merge_requests/42",
      "same_project": true,
      "source_project": {"id": 99, "full_path": "group/project", "web_url": "https://gitlab.com/group/project"},
      "target_project": {"id": 99, "full_path": "group/project", "web_url": "https://gitlab.com/group/project"},
      "source_branch": "feature",
      "target_branch": "main",
      "base_sha": "1123456789012345678901234567890123456789",
      "head_sha": "3123456789012345678901234567890123456789",
      "updated_at": "2024-02-03T04:05:06Z"
    }
  },
  "meta": {
    "backend": "official-glab",
    "host": "gitlab.com",
    "repo": "group/project",
    "complete": true,
    "truncated": false,
    "count": 1,
    "limit": 1000,
    "upstream_version": "1.112.0"
  }
}
```

Exact issue-edit validation requires explicit host/project plus the issue's
canonical URL, current `opened` or `closed` state, and exact `updated_at`. Title
and description enter only through private regular non-symlink files.
Repeatable `--add-label` and `--remove-label` values must resolve to one exact
stable numeric label identity in two complete bounded catalog reads; case
aliases, missing labels, duplicates, overlap, and identity drift fail closed.
The preview computes add/remove semantics so unrelated labels remain present.

`--dry-run` performs complete project, issue, stale-state, content, and label
validation and returns a bounded `preview` receipt. An exact no-op returns
`unchanged`. GitLab's issue PUT accepts no expected issue revision and only
label names, so it cannot atomically bind the validated issue and label
identities. Consequently,
every non-no-op live request returns `safety_violation` with a deterministic
`refused`/`not_applied` receipt under `error.receipt` before any PUT. The
official-glab adapter exposes no issue-write operation. The approved surface
and exclusions are pinned under
[`contracts/issue-edit`](contracts/issue-edit/).

Guarded merge requires an explicit host, nested project, canonical MR URL,
exact reviewed source and target branches, reviewed lowercase head SHA,
Firstmate authority class, and `--squash`. It requires provider-side
successful-pipeline and resolved-discussion policies, revalidates the
caller-bound branches immediately before mutation along with the exact MR and
all head-pipeline jobs/bridges, sends at most one fixed PUT with provider SHA
enforcement, and reconciles uncertain outcomes with one GET. It never delegates
`glab mr merge`, enables auto-merge, rebases, removes the source branch, or
accepts a custom message. The pinned Firstmate contract is
under [`contracts/firstmate`](contracts/firstmate/). Agents must not self-assert
`--authority` or bypass that lifecycle boundary.

The permanent denial boundary includes generic API, every live issue mutation,
issue creation, unguarded or alternate merge, approve,
comment/note/reply/resolve, merge-request and label-resource mutation,
close/reopen/delete, repository/release writes, secrets/variables, and
pipeline/job mutation. `issue edit --dry-run` is validation-only and changes no
labels or issue fields.

## `glab-axi` compatibility alias

`glab-axi` remains an explicit compatibility executable with no removal date.
It accepts the same commands while new installations and caller configuration
should use `gl-axi`. Release packages and `make build` contain both names.

Compatibility-sensitive identifiers remain unchanged so migration never
requires credential export or a flag day:

- `glab-axi/v1`, `glab-axi/ux-v1`, and `glab-axi/config-v1` remain the wire and
  storage schema identifiers;
- the existing user config location and OS-keyring namespace remain shared by
  both executable names;
- `GL_AXI_TOKEN`, `GL_AXI_CONFIG`, and `GL_AXI_CONFIG_DIR` are canonical, while
  `GLAB_AXI_TOKEN`, `GLAB_AXI_CONFIG`, and `GLAB_AXI_CONFIG_DIR` remain accepted;
  conflicting canonical and compatibility values fail closed; and
- the test-only executable named `glab` remains separate from both product
  names and is never distributed.

The version handshakes make the selected executable identity explicit while
retaining the frozen contract identifier:

```text
gl-axi 0.2.0 (contract glab-axi/v1)
glab-axi 0.2.0 (contract glab-axi/v1)
```

## Authentication

Product login delegates only `glab auth login --hostname HOST` to the pinned
official client and requires a real human TTY. Before execution, `gl-axi` probes
the same OS keyring implementation with a random non-secret sentinel and aborts
on the pinned upstream plaintext-fallback warning. Login strips ambient GitLab
token/job-token variables so a human flow cannot silently persist a headless
credential. There is no token, insecure-storage, device, or browser flag on the
product login surface.

A bounded PTY/ConPTY relay keeps all three child streams terminal-backed while
monitoring the exact warning. The human prompt follows caller/process
cancellation rather than the noninteractive deadline. Official interactive text
is relayed to terminal stderr so stdout remains one valid envelope. `gl-axi`
never parses, reads, exports, or copies official-glab credentials.

Approved `GITLAB_TOKEN`, `GITLAB_ACCESS_TOKEN`, or `OAUTH_TOKEN` values remain
available to official `glab` for headless product operations. The native lane
separately supports `GL_AXI_TOKEN` and its `GLAB_AXI_TOKEN` compatibility name,
with ambiguity checks and the shared keyring. See
[authentication](docs/authentication.md).

## Standalone native contract

Existing run-private forms remain native under either executable name:

```text
gl-axi auth status --host H --repo P --format json
gl-axi mr ensure ... --host H --repo P --format json
gl-axi mr view IID --host H --repo P --format json
gl-axi ci status|jobs|trace ... --host H --repo P --format json
gl-axi --contract glab-axi/v1 <existing argv>
```

Contract routing happens before PATH lookup, so these commands work with
official `glab` absent. The separately compiled `cmd/glab-compat` executable
named `glab` exists only as a test artifact for no-mistakes v1.45.4. It is not
built by the product target, packaged, installed, or placed on PATH.

## Build and validation

Requires Go 1.23 or newer. Tests use local processes and TLS fake servers; they
require no GitLab account and never mutate a live GitLab project.

```sh
make test
make race
make vet
make build                    # dist/gl-axi plus dist/glab-axi alias
make contract                 # pinned no-mistakes v1.45.4 fixture
make fake                     # typed fake-GitLab protocol gate
make compat                   # dist/test-only/glab, never distribute
```

The mandatory gate is:

```sh
go test ./...
go test -race ./...
go vet ./...
make build
```

CI additionally downloads without installing the checksum-pinned official
`glab` package and executes its version/help contract plus isolated TLS
fake-server ensure, exact-MR-view normalization, read-only issue-edit
validation, and guarded-merge contracts.
The authoritative evidence and MIT license are under
[`contracts/official-glab/v1.112.0`](contracts/official-glab/v1.112.0/).

## Distribution and updates

Tagged GitHub Releases remain in `github.com/M00NLIG7/glab-axi`. Release CI
accepts canonical `GL_AXI_UPDATE_SIGNING_KEY` and `GL_AXI_UPDATE_PUBLIC_KEY`
configuration while retaining the corresponding `GLAB_AXI_*` repository secret
and variable names as fallbacks. It builds canonical `gl-axi` and compatibility
`glab-axi` executables for Linux/Windows amd64 and macOS amd64/arm64. The legacy
test executable named `glab` is never an asset.

Each package contains both product executable names, build metadata, licenses,
documentation, schemas, and canonical plus compatibility Agent Skills. Both
canonical and compatibility archive names remain published. Fresh installs
should download and verify the `gl-axi_*` archive, `checksums.txt`, detached
signature, and `gl-axi-update-public-key.txt`, then place `gl-axi` in a private
user-owned directory on PATH and verify `gl-axi --version` before setup or
authentication. Existing automation may continue to verify and install the
parallel `glab-axi_*` assets.

A release signing public key is injected into release binaries; development
builds refuse self-update. Canonical and compatibility executables use separate
signed manifests and require a candidate with the matching version handshake.
`update --check` never installs. Applying an update requires a real human TTY,
verifies signature, size, SHA-256, and handshake, then replaces a regular
user-managed executable atomically with rollback. Package-managed/symlinked
installs and Windows self-replacement must use the signed package channel.

`setup hooks` installs the canonical `gl-axi` Agent Skill and bounded dashboard
SessionStart hooks for Claude Code and Codex. Invoking setup through the
compatibility executable keeps that hook command functional but does not remove
or rewrite an existing compatibility skill. Setup never authenticates, is
transactional, preserves unrelated configuration, and refuses symlink targets.

## Design and security

- [Architecture](docs/architecture.md)
- [Authentication](docs/authentication.md)
- [Security model](docs/security.md)
- [`gl-axi` identity migration](docs/gl-axi-migration.md)
- [no-mistakes compatibility](docs/no-mistakes-compat.md)
- [Activation acceptance](docs/acceptance.md)
- [`glab-axi/ux-v1` stable wire schema](schema/glab-axi-ux-v1.schema.json)
- [`glab-axi/v1` stable wire schema](schema/glab-axi-v1.schema.json)
