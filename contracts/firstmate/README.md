# Firstmate guarded-merge consumer contract

The versioned JSON fixture pins the Firstmate source commit inspected before the
provider's guarded merge was promoted. It records the only planned GitLab
invocation, required output/exits, forbidden flags, and the ownership split
between Firstmate policy and provider truth.

New Firstmate configuration should pin a released `gl-axi` path and hash. The
fixture's `glab_axi` key and `glab-axi/ux-v1` values remain stable consumer
identifiers; an existing caller may continue selecting the tested `glab-axi`
compatibility executable with no removal date. This directory is evidence for
the provider primitive, not an assertion that Firstmate already invokes it. A
separately reviewed Firstmate integration must satisfy this contract without a
plain-`glab` fallback.
