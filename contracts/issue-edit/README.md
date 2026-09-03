# Guarded issue-edit consumer contract

`v1.json` pins the captain-approved first issue-mutation slice. It records the
exact caller evidence, private-file and label surface, bounded provider state
machine, success data schema, and exclusions that keep issue edit from becoming
generic API authority.

The fixture is intentionally independent of the frozen `glab-axi/v1` native
contract and the Firstmate-only guarded merge contract. Both `gl-axi` and the
`glab-axi` compatibility executable expose this same product command through
the `glab-axi/ux-v1` envelope.
