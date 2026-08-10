# glab-axi

`glab-axi` is a bounded GitLab experience for humans and agents. The v0.2
rework combines two deliberately separate backends in one Go executable:

- a product-facing lane delegates a closed, version-tested allowlist of safe
  reads and human login to **official `glab` 1.112.0 (`816e3a52`)**; and
- the frozen native `glab-axi/v1` lane performs the proven MR/CI automation
  contract directly and remains fully standalone for no-mistakes custody.

This is not `exec glab "$@"`. Unknown commands and prohibited mutations fail
before executable discovery, credential resolution, child execution, or HTTP.
Every delegated result is bounded and normalized into `glab-axi/ux-v1`; raw
official-glab JSON and stderr never pass through.

> **Activation status:** v0.2 is a release candidate. Do not replace an
> installed provisional v0.1.0 until no-mistakes, genuine CI, human onboarding,
> distribution, and separately approved read-only provider acceptance pass.

## Product commands

```text
glab-axi                               # current-project dashboard

glab-axi auth login [--hostname H]    # human TTY only
glab-axi auth status [--hostname H]

glab-axi issue list|view
glab-axi mr list|view|checks|diff
glab-axi mr ensure                     # only provider write
glab-axi mr create-or-update           # same ensure semantics
glab-axi pipeline list|view
glab-axi job list|view|trace
glab-axi release list|view
glab-axi repo list|view
glab-axi label list
glab-axi search issues|mrs|repos|commits|code

glab-axi setup hooks
glab-axi update [--check]
```

Use current Git context or command-first `-R/--repo namespace/project` and
`--hostname host`; space and equals forms are accepted. A `gitlab.com` remote
may supply both defaults. Any self-managed remote must be paired with explicit
`--hostname` or `GITLAB_HOST` authority so an untrusted remote cannot select
where an environment credential is sent. `--limit` never raises hard limits. TOON is default; `--format json` selects the versioned JSON
contract. Top-level, parent, and leaf help are local and do not probe auth or
execute official `glab`. See the generated [command reference](docs/command-reference.md).

The permanent denial boundary includes generic API, merge, approve, comments or
notes, close/reopen/delete, repository writes, release/label writes,
secrets/variables, and pipeline/job mutation. GitHub-only concepts are not
invented: GitLab uses MRs, pipelines, jobs, and repositories/projects.

## Authentication

Product login delegates only `glab auth login --hostname HOST` to checksum-
pinned official `glab` 1.112.0 (`816e3a52`). It requires a real human TTY.
Before execution, `glab-axi` probes the same OS keyring implementation with a
random non-secret sentinel; it also aborts on the pinned upstream
plaintext-fallback warning. Login strips ambient GitLab token/job-token
variables so a human flow cannot silently persist a headless credential. There
is no `--token`, `--stdin`, `--insecure-storage`, device, or browser flag on the
`glab-axi auth login` surface. A bounded PTY/ConPTY relay keeps all three child
streams terminal-backed while monitoring the exact plaintext warning. The
human prompt follows explicit caller/process cancellation rather than the
30-second noninteractive operation deadline; relay shutdown and output remain
bounded. Official interactive text is relayed to terminal stderr so stdout
remains one valid `glab-axi/ux-v1` envelope. An independently configured
official-glab profile is an external human trust decision; `glab-axi` never
parses, reads, exports, or copies its credentials.

Approved `GITLAB_TOKEN`, `GITLAB_ACCESS_TOKEN`, or `OAUTH_TOKEN` environment
credentials remain available to official `glab` for headless product reads.
The native v1 lane separately retains its ambiguity-checked environment/keyring
resolver and stdin-only import. No lane falls back to the other credential
store. See [authentication](docs/authentication.md).

## Standalone native contract

Existing no-mistakes forms and bytes remain native, including:

```text
glab-axi auth status --host H --repo P --format json
glab-axi mr ensure ... --host H --repo P --format json
glab-axi mr view IID --host H --repo P --format json
glab-axi ci status|jobs|trace ... --host H --repo P --format json
```

The preferred explicit alias is:

```text
glab-axi --contract glab-axi/v1 <existing argv>
```

The old unnamespaced forms remain accepted. Contract routing happens before
PATH lookup, so all v1 commands work with official `glab` absent. `--version`
continues to emit the custody handshake:

```text
glab-axi 0.2.0 (contract glab-axi/v1)
```

The separately compiled `cmd/glab-compat` executable named `glab` exists only
as a test artifact for installed no-mistakes v1.45.4. It is not built by the
normal product target, packaged, installed, or placed on PATH.

## Build and validation

Requires Go 1.23 or newer. Tests use only local processes and TLS fake servers;
they require no GitLab account and never contact a live GitLab API.

```sh
make test
make race
make vet
make build                    # dist/glab-axi only
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

CI additionally downloads (without installing) the public upstream
`glab_1.112.0_linux_amd64.tar.gz`, verifies its pinned SHA-256, and executes only
its version/help contract. The authoritative local evidence and MIT license are
under [`contracts/official-glab/v1.112.0`](contracts/official-glab/v1.112.0/).

## Distribution and updates

The initial supported package channel is the repository's tagged GitHub
Releases. A release is not valid until a maintainer configures the private
`GLAB_AXI_UPDATE_SIGNING_KEY` secret and independently published
`GLAB_AXI_UPDATE_PUBLIC_KEY` repository variable; release CI proves they match.
It builds only `glab-axi` for Linux/Windows amd64 and macOS amd64/arm64. The
legacy executable named `glab` is never an asset.

Each release contains raw one-file executables, archives, build metadata,
`checksums.txt`, its detached Ed25519 signature, the public key, and an
Ed25519-signed update manifest. For a fresh install:

1. download one tagged archive plus `checksums.txt`, `checksums.txt.sig`, and
   `glab-axi-update-public-key.txt` from that same release;
2. compare the public key with the captain-pinned value obtained independently;
3. verify the detached signature with an Ed25519-capable approved verifier,
   then verify the archive's SHA-256 entry;
4. extract `glab-axi` into a private user-owned executable directory on PATH;
5. verify `glab-axi --version` and the recorded release hash before setup/auth.

Do not treat a key downloaded only beside the artifact as its own trust root.
Release packages carry exact third-party notices/licenses and the generated
Agent Skill. No install script, release, or current installation is changed by
building this repository.

A release signing public key is injected into release binaries; development
builds intentionally refuse self-update. `update --check` verifies the signed
manifest and platform artifact metadata but never installs. Applying an update
requires a real human TTY, verifies signature, size, SHA-256, and the candidate
v1 handshake, then replaces a regular user-managed executable atomically with
rollback. Package-managed/symlinked installs and Windows self-replacement are
refused and must use the signed package channel.

`setup hooks` installs the generated Agent Skill and bounded dashboard
SessionStart hooks for Claude Code and Codex. It does not authenticate. Setup is
transactional, preserves unrelated configuration, and refuses symlink targets.

## Design and security

- [Architecture](docs/architecture.md)
- [Authentication](docs/authentication.md)
- [Security model](docs/security.md)
- [no-mistakes compatibility](docs/no-mistakes-compat.md)
- [Activation acceptance](docs/acceptance.md)
- [`glab-axi/ux-v1` schema](schema/glab-axi-ux-v1.schema.json)
- [`glab-axi/v1` schema](schema/glab-axi-v1.schema.json)
