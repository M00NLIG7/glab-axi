# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- `gl-axi` is the canonical product/module/command; `glab-axi` remains a tested executable alias with no removal date, while the repository URL and `glab-axi/*` wire/config/keyring identifiers stay stable for compatibility. The product is intentionally narrow: do not add commands or HTTP routes without a pinned consumer contract and fail-closed tests; MR ensure and guarded immediate squash merge are the only provider writes, while generic API, alternate/unguarded merge, close/comment, repository writes, and pipeline mutations are out of scope.
- Product operations/login delegate only typed argv from `contracts/official-glab/v1.112.0/`; changing official glab requires a new evidence fixture and offline version/help gate. Never parse or export its credential store. Human login follows caller/process cancellation, not `limits.ShortOperation`; preserve the bounded PTY/ConPTY relay, terminal-backed child streams, and exact-warning monitor in `internal/delegate/glab`.
- Run `go test ./...`, `go test -race ./...`, and `go vet ./...`; build locally with `make build`. Provider tests must use TLS fake servers and synthetic credentials only—never live GitLab or real credentials.
- `contracts/no-mistakes/v1.45.4.json` is the authoritative legacy argv boundary; direct run-private automation remains standalone `glab-axi/v1`. A no-mistakes upgrade requires a new versioned fixture before parser changes.
- Keep credentials out of argv, config, output, logs, fixtures, and source. Token-import tests construct runtime sentinels and inject an in-memory keyring.
- Host/API/web binding, returned URL validation, pagination limits, replay reconciliation, and stale-pipeline normalization are security properties, not convenience behavior.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
