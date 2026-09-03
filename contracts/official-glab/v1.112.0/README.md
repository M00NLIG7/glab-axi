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

`capabilities.json` is the implementation boundary. Its `mr-view` entry also
pins response normalization: the official client's `diff_refs.head_sha` and
`diff_refs.base_sha` become canonical `sha` and `base_sha` only when valid, and
two supplied representations must match. Missing identity is never invented
and cannot prove a post-write result. Public `gl-axi` input is never appended
to an upstream argv. MR discussion evidence uses fixed project-identity and
paginated discussion GET routes and exposes no note mutation. The source-project
route accepts only the positive project ID returned by the bound MR. Each
adapter constructs one listed argv, validates every substituted value, bounds
child output, and normalizes it into a command-specific `glab-axi/ux-v1`
schema. The guarded merge entries pin four fixed reads and one fixed PUT; the
PUT consumes only a private four-key JSON file, is invoked once, and is never
delegated through
interactive `glab mr merge` behavior.

The Linux checksum in `capabilities.json` is also used by the offline upstream
contract job in CI. That job executes version/help plus isolated TLS fake-server
ensure, exact-MR-view normalization, and guarded-merge requests with synthetic
credentials; it never contacts a live GitLab API. Updating official `glab`
requires a new versioned directory, fresh public-interface evidence, and adapter
tests before changing the runtime pin.
