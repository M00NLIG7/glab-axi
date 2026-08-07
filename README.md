# glab-axi

`glab-axi` is a bounded, native GitLab MR/CI client for agents. It calls a
small allowlist of GitLab REST endpoints directly; it does not require or wrap
the upstream `glab` CLI.

The repository builds two executables:

- `glab-axi`: the native command-first interface. It emits bounded TOON by
  default and a versioned `glab-axi/v1` envelope with `--format json`.
- `glab`: a separately built compatibility adapter for the exact command
  contract used by no-mistakes v1.45.4. It is **not** the GitLab CLI.

## Scope

```text
glab-axi auth status --host HOST --repo GROUP/PROJECT
glab-axi auth import --host HOST --api-base URL --web-base URL --token-stdin
glab-axi mr ensure --host HOST --repo PROJECT --source BRANCH --target BRANCH \
  --title-file /private/file --description-file /private/file
glab-axi mr view IID --host HOST --repo PROJECT
glab-axi ci status --mr IID --host HOST --repo PROJECT
glab-axi ci jobs --pipeline-id ID --host HOST --repo PROJECT
glab-axi ci trace JOB_ID --host HOST --repo PROJECT
```

There are no merge, close, approve, delete, comment, repository-write,
pipeline-mutation, browser-login, token-argument, extension, GraphQL, or generic
API commands. Unsupported input exits before an HTTP request is made.

## Build and test

Requires Go 1.23 or newer. Builds are local; there is no install target.

```sh
go test ./...
go test -race ./...
go vet ./...
mkdir -p dist
go build -trimpath -o dist/glab-axi ./cmd/glab-axi
go build -trimpath -o dist/glab ./cmd/glab-compat
```

The tests use only deterministic TLS fake GitLab servers. They do not require a
GitLab account or credential and do not contact a live API.

## Authentication

Tokens are accepted only from noninteractive environment sources or the OS
keyring. Import accepts a token only through piped stdin and refuses a terminal.
No credential value is written to config or output. Private hosts require an
explicit full API base and web base. See [authentication](docs/authentication.md).

## Compatibility

The `glab` build accepts only the pinned no-mistakes v1.45.4 argv fixtures in
[`contracts/no-mistakes/v1.45.4.json`](contracts/no-mistakes/v1.45.4.json).
See [compatibility](docs/no-mistakes-compat.md).

## Design and security

- [Architecture](docs/architecture.md)
- [Authentication](docs/authentication.md)
- [no-mistakes compatibility](docs/no-mistakes-compat.md)
- [Security model](docs/security.md)

This release is intentionally not wired into a shared no-mistakes daemon. A
run-scoped native-client integration and separately authorized live validation
remain external boundaries.
