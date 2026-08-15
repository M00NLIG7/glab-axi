# Firstmate guarded-merge consumer contract

The versioned JSON fixture pins the Firstmate source commit inspected before
`glab-axi mr merge` was promoted. It records the only planned GitLab invocation,
required output/exits, forbidden flags, and the ownership split between
Firstmate policy and `glab-axi` provider truth.

This directory is evidence for the `glab-axi` provider primitive, not an
assertion that Firstmate already invokes it. Stage 1A intentionally leaves
Firstmate unchanged; a separately reviewed Firstmate integration must pin a
released `glab-axi` version/hash and satisfy this contract without a plain-`glab`
fallback.
