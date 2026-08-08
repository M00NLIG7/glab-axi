# Official `glab` v1.112.0 adapter contract

This directory pins the only official GitLab CLI release accepted by the
`glab-axi/ux-v1` delegated backend.

The evidence was collected from the public, credential-free upstream release
and exact source tag. No GitLab account, config, token, or API data was used.

- release tag: `v1.112.0`
- annotated tag object: `a274f4c12f1ab92b155f09d2e0a9e106bcebb248`
- source commit and packaged version commit: `816e3a52411aba73d90237859fdc6ecbc86bd169`
- packaged macOS arm64 version output: `glab 1.112.0 (816e3a52)`
- license: MIT (`LICENSE.upstream`)

`help.txt` is the complete local `version` and relevant nested-help transcript
from the checksum-verified macOS arm64 release package. It was captured with an
empty temporary home and config, update checks disabled, and color disabled.
`upstream-checksums.txt` is the release's checksum asset.
`auth-storage-source.go.txt` contains the exact upstream source excerpts that
establish keyring probing and the documented plaintext fallback. The adapter
never parses official `glab` config or credentials.

`capabilities.json` is the implementation boundary. Public `glab-axi` input is
never appended to an upstream argv. Each adapter constructs one listed argv,
validates every substituted value, bounds child output, and normalizes it into
a command-specific `glab-axi/ux-v1` schema.

The Linux checksum in `capabilities.json` is also used by the offline upstream
contract job in CI. Updating official `glab` requires a new versioned directory,
fresh public-interface evidence, and adapter tests before changing the runtime
pin.
