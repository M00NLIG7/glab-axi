# no-mistakes compatibility

There are two different consumer generations. Neither uses the product-facing
official-glab backend.

## Direct run-private `glab-axi/v1` consumer

The accepted current no-mistakes integration selects one absolute
`--gitlab-client` path, copies that single executable into a private mode-0500
run directory, records SHA-256, and verifies it before each call. New caller
configuration should select `gl-axi` and require:

```text
gl-axi <version> (contract glab-axi/v1)
```

Existing configuration selecting the `glab-axi` compatibility executable keeps
its exact prior handshake, with no removal date:

```text
glab-axi <version> (contract glab-axi/v1)
```

It invokes native `auth status`, `mr ensure`, `mr view`, `ci status`, and
`ci trace` (plus jobs as needed) with explicit host/repository, private
mode-0600 title/description files, and `--format json`. The consumer strictly
decodes `glab-axi/v1`, validates project/MR/URL/SHA identity, treats selected
client failure as fatal, and removes custody at terminal cleanup.

The v0.2 router preserves these migration properties:

- old unnamespaced argv still routes native under either executable name;
- `--contract glab-axi/v1` is an additional explicit alias, not a required
  migration;
- v1 output/error fields remain unchanged, and each executable has a tested,
  name-specific handshake;
- official `glab` is never discovered or executed;
- one executable remains sufficient and below the 128 MiB limit;
- no prompt, ANSI, banner, product update notice, or delegated stderr appears;
- private-file semantics remain compatible and are now descriptor/no-follow;
- data schemas are pinned under `schema/v1/`.

A no-mistakes parser/argv cleanup must add a new versioned consumer fixture
before requiring the explicit contract flag or canonical executable name. The
`glab-axi` executable and old argv forms remain supported with no removal date.

## Installed no-mistakes v1.45.4 compatibility artifact

The binary built explicitly by `make compat` from `cmd/glab-compat` is written to
`dist/test-only/glab`. Its name exists only for the pinned installed
no-mistakes v1.45.4 boundary. Help/version state that it is not official glab.
The normal `make build`, release workflow, package archives, setup, and updater
never build, publish, install, suggest, or expose this executable.

### Accepted legacy argv

```text
glab auth status [--hostname HOST]
glab mr list --source-branch B [--target-branch B] --output json
glab mr create --source-branch B --target-branch B --title T --description D --yes
glab mr update IID_OR_URL --title T --description D --yes
glab mr view IID --output json
glab ci status --mr IID --output json
glab api --paginate projects/<encoded-configured-project>/pipelines/<id>/jobs
glab ci get --pipeline-id ID --output json --with-job-details
glab ci trace JOB_ID
```

Flags may be ordered differently, but duplicates, equals forms, extra
positionals, unknown flags, and missing values fail with exit 2. The spelling
`api` is anchored to the configured project's exact pipeline-jobs route before
the typed native jobs method runs; it is not generic REST authority.

The legacy process infers host/project from local origin, then resolves native
`glab-axi/config-v1`. Auth with explicit hostname does not require repository.
It emits bounded legacy JSON/raw trace shapes, no prompts/pagers/ANSI/update
notices, and one controlled stderr error line.

no-mistakes v1.45.4 puts MR title/description in argv; the adapter cannot remove
that consumer exposure. Use only in its isolated legacy boundary. Never put the
compatibility executable on shared PATH or use a PATH prefix as run-scoped
custody.

## Contract gates

`contracts/no-mistakes/v1.45.4.json` remains authoritative for the legacy
binary. `internal/compat` builds and executes every vector against TLS fake
GitLab, verifies response/exit/mutation classification, and asserts denied
commands make zero requests.

The direct native gate additionally executes the frozen v1 command suite with
no official `glab` on PATH, validates handshake/output schemas and one-file
copy behavior, and is exercised by the accepted no-mistakes repository's
native-host/custody tests. All provider tests use synthetic credentials and
local TLS servers only—never a live GitLab account.
