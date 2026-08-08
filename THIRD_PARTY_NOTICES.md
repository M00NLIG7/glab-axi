# Third-party notices

The standalone `glab-axi` binary links the modules pinned in `go.mod`/`go.sum`:

| Module | Version | License | Purpose |
|---|---:|---|---|
| `github.com/zalando/go-keyring` | v0.2.8 | MIT | Non-secret secure-store availability probe matching official glab |
| `github.com/danieljoos/wincred` | v1.2.3 | MIT | Windows Credential Manager backend (transitive) |
| `github.com/godbus/dbus/v5` | v5.2.2 | BSD-2-Clause | Linux Secret Service transport (transitive) |
| `golang.org/x/sys` | v0.27.0 | BSD-3-Clause | Platform system interfaces (transitive) |
| `golang.org/x/term` | v0.26.0 | BSD-3-Clause | Real-terminal detection for human-only operations |

Exact license texts are distributed under `licenses/`.

Official GitLab CLI `glab` is an external runtime prerequisite, not linked or
bundled. The supported v1.112.0 release is MIT licensed; its exact upstream
license and package/source/help evidence are retained under
`contracts/official-glab/v1.112.0/`.
