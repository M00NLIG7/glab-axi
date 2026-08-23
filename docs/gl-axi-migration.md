# `gl-axi` identity migration

Captain-approved scope B makes `gl-axi` canonical while retaining `glab-axi`
as an explicit compatibility alias with no removal date. The GitHub repository
and PR base remain `github.com/M00NLIG7/glab-axi`.

## Surface inventory

| Surface | Canonical state | Compatibility behavior |
|---|---|---|
| Product command | `gl-axi` | `glab-axi` ships and accepts the same grammar |
| Go package/module | `module gl-axi`; internal imports use `gl-axi/...` | Frozen wire identifiers are not Go import paths |
| Source/build entry point | `cmd/gl-axi` | `cmd/glab-axi` is a tested product alias; `cmd/glab-compat` remains the separate test-only executable named `glab` |
| Version handshake | `gl-axi VERSION (contract glab-axi/v1)` | The alias retains `glab-axi VERSION (contract glab-axi/v1)` |
| Wire schemas | Existing `glab-axi/v1`, `glab-axi/ux-v1`, and schema filenames | Intentionally unchanged to avoid a protocol break |
| Native config | `GL_AXI_CONFIG`, `GL_AXI_CONFIG_DIR` | `GLAB_AXI_*` aliases remain; the established `glab-axi/config.json` path and `glab-axi/config-v1` schema are shared |
| Native environment token | `GL_AXI_TOKEN` | `GLAB_AXI_TOKEN` remains; disagreeing values fail closed |
| Credential storage | Canonical command uses the existing scoped keyring item | Namespace remains `glab-axi/...`; no credential is copied, parsed, or exported |
| Agent integration | Generated `skills/gl-axi/SKILL.md`; setup installs the `gl-axi` skill | A generated `skills/glab-axi` compatibility skill remains, setup does not remove existing copies, and hooks using the alias remain valid |
| Local build | `dist/gl-axi` | `make build` also produces `dist/glab-axi`; test-only `dist/test-only/glab` remains separate |
| Release/install | `gl-axi_*` raw binaries/archives, public-key file, and signed manifest | Parallel `glab-axi_*` assets and a legacy-schema signed manifest keep existing installers/updaters functional; packages contain both product names |
| CI | Builds and checks canonical plus alias handshakes; offline official-glab contract uses `GL_AXI_OFFICIAL_GLAB_TEST_BINARY` | The old test variable remains accepted |
| Release credentials | `GL_AXI_UPDATE_SIGNING_KEY` and `GL_AXI_UPDATE_PUBLIC_KEY` | Existing `GLAB_AXI_*` GitHub secret/variable names are fallbacks |
| Firstmate/no-mistakes callers | New configuration should pin the `gl-axi` path/hash | Existing absolute `glab-axi` custody and versioned `glab-axi/*` consumer fixtures remain valid |
| Repository | No rename: `github.com/M00NLIG7/glab-axi` | Clone URLs, release authority, and PR base stay unchanged |

## Migration risks and controls

- **Caller flag day:** avoided by shipping both executable names indefinitely.
- **Wire/schema break:** avoided by retaining all existing contract identifiers.
- **Credential loss or exposure:** avoided by sharing the existing keyring
  namespace and config path; migration never reads or exports credential data.
- **Ambiguous environment configuration:** canonical and compatibility config
  paths or tokens must agree when both are populated.
- **Wrong self-update identity:** canonical and compatibility assets have
  separate signed manifests and exact name-specific candidate handshakes.
- **Packaging drift:** CI and release jobs build, size-check, inspect, package,
  and version-check both binaries; archives carry both names and both skills.
- **Confusing `glab-axi` with official `glab`:** the compatibility alias remains
  explicitly named and documented; the separate executable named `glab` is
  still test-only and never released.
