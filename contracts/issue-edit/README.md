# Guarded issue-edit consumer contract

`v2.json` pins best-effort guarded editing of title, description/body, and exact
label additions/removals. It supersedes the historical validation-only `v1.json`,
retained as evidence of the previous surface. The reference comparison is pinned
to gh-axi `2bffd9a5b60ded64d6c9851683b27a480173a7ee`, `editIssue`.

Explicit host/project, IID, canonical URL, expected state and timestamp bind two
preflight issue snapshots. Private content files and complete bounded label
catalogs protect inputs. One PUT targets the validated numeric project ID and
IID, with only changed fields; label deltas never replace the whole set. No-op
and dry-run perform validation but never mutate. Description slash-leading lines
are refused conservatively because GitLab interprets quick actions during update.

One canonical post-read plus requested label identity verification establishes
observed postconditions. Success is `updated` or `reconciled_update` with
`observed_applied`. Failed verification is `ambiguous_update` with a bounded
`ambiguous`/`unknown` receipt. There is no retry or rollback under any outcome.

GitLab cannot enforce an atomic expected revision or numeric label identity for
this route. Concurrent edits can race after checks; deleted or renamed labels
can be reused or recreated by name. Postchecks cannot prevent those effects or
prove exclusive authorship. Help, schema and all receipts disclose this residual
race. The provider semantics and fixed argv evidence are in
`../official-glab/v1.112.0/issue-edit-provider.json` and `capabilities.json`.

Assignees, milestones, attachments and GitHub organization issue types remain
explicit parity gaps, not supported or silently emulated features. Other issue
write families have separate contracts. No frozen native `glab-axi/v1` behavior
changes. Both `gl-axi` and `glab-axi` expose the same `glab-axi/ux-v1` command.
