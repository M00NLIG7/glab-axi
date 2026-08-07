# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- `glab-axi` is intentionally narrow. Do not add commands or HTTP routes without a pinned consumer contract and fail-closed tests; generic API, merge/close/comment, repository writes, and pipeline mutations are out of scope.
- Run `go test ./...`, `go test -race ./...`, and `go vet ./...`; build locally with `make build`. Tests must use TLS fake servers only—never live GitLab or real credentials.
- `contracts/no-mistakes/v1.45.4.json` is the authoritative legacy argv boundary. A no-mistakes upgrade requires a new versioned fixture before parser changes.
- Keep credentials out of argv, config, output, logs, fixtures, and source. Token-import tests construct runtime sentinels and inject an in-memory keyring.
- Host/API/web binding, returned URL validation, pagination limits, replay reconciliation, and stale-pipeline normalization are security properties, not convenience behavior.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
