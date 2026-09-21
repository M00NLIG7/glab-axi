# Official `glab` v1.112.0 adapter contract

Project CI-variable operations are not delegated through this contract. They
require the separate explicit native path in `../../ci-variables/v1.json`.
`TestPinnedOfficialGlabVariableRedirectCounterevidenceTLS` retains synthetic
negative evidence: the pinned upstream follows 301/302/303 with credentials and
replays DELETE on 307/308 to another target. This is not fixed upstream and
must not be represented as native/official profile equivalence.

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
schema. `snippet-reads.json` pins the authenticated identity and explicit
personal/project snippet reads, including locally filtered visibility and
inventory-bound file content. Its GitLab v18.0.0-ee and client-go v2.53.0 source
references establish routes and fields before typed adapter expansion. The
snippet E2E fixture uses both public CLI names and optional real official-glab
against verified local TLS with synthetic credentials only. Exact issue-edit
validation pins only project, issue, and label-catalog
GET routes. No issue content/label PUT is exposed because GitLab accepts no
expected issue revision and only label names. `issue-writes.json` and test-only
probes retain characterization evidence, not a supported delegated
issue-write backend: pinned glab follows 301/302/303 to another authority while
forwarding a synthetic Private-Token. New public issue writes instead require
explicit native selection under `contracts/issue-writes/`, without changing
existing default operations. No upstream transport fix is claimed. Guarded merge
pins four fixed reads and one fixed PUT; the PUT consumes only a private
four-key JSON file, is invoked once, and is never delegated through interactive
`glab mr merge` behavior.

CI read parity is pinned separately in `contracts/read-parity/ci-reads.json`.
`ci-list-source.go.txt` is the exact upstream `internal/commands/ci/list/list.go`
from this release; it proves typed ref/status/source/username/SHA translation
and the default `order_by=id&sort=desc`. `ci-reads-source.go.txt` contains exact
client-go/v2 v2.53.0 excerpts (the dependency pinned in this release's `go.mod`)
for pipeline IID, pipeline/job GET routes, job `scope[]`, and CI status/source
enums. Source artifacts were acquired from the public Go module proxy, not an
API/account. The fixture pins their SHA-256 digests. `TestPinnedOfficialGlabCIReadsTLS`
executes the pinned CLI against a local TLS fake for all five route shapes.
Filtered job reads are not used by guarded merge's complete jobs/bridges proof.

The Linux checksum in `capabilities.json` is also used by the offline upstream
contract job in CI. That job executes version/help plus isolated TLS fake-server
ensure, exact-MR-view normalization, pipeline/job selectors and trace reads,
read-only issue-edit validation, test-only issue-write characterization and
guarded-merge requests with synthetic credentials; it never contacts a live
GitLab API.
Updating official `glab` requires a new versioned directory, fresh
public-interface evidence, and adapter
tests before changing the runtime pin.
