# Guarded issue-edit consumer contract

`v1.json` pins the captain-approved exact issue-edit validation slice. It
records caller evidence, private-file and label inputs, bounded provider reads,
preview and no-op receipts, and the deterministic live-request refusal that
keeps issue edit from becoming generic API authority.

GitLab's [documented issue PUT](https://docs.gitlab.com/api/issues/#update-an-issue)
accepts no expected issue revision and only label names, so it cannot
atomically bind the validated issue and requested numeric label identities. The
contract therefore permits zero issue mutations: `--dry-run`
returns a validated preview, an exact no-op returns `unchanged`, and every
non-no-op live request returns `safety_violation` with a bounded `refused`
receipt before any PUT.

The fixture is intentionally independent of the frozen `glab-axi/v1` native
contract and the Firstmate-only guarded merge contract. Both `gl-axi` and the
`glab-axi` compatibility executable expose this same product command through
the `glab-axi/ux-v1` envelope.
