# Local GitLab stacks

A stack is an ordered chain of existing local branches, optionally bound to
existing GitLab MRs targeting their predecessor. This foundation registers,
inspects and navigates stacks. It does not publish branches, create MRs, rebase or
merge. See [the versioned contract](../contracts/stacks/v1.md) for guarantees,
bounds and the remaining parity work.

## Register an existing chain

Run inside its local repository. In this example `main` is an ancestor of
`model`, and `model` is an ancestor of `api`. Create those branches separately
using your normal workflow before registration.

```sh
gl-axi stack init model api --base main --stack feature \
  --hostname gitlab.example -R team/project --allow-local-metadata

gl-axi stack view --stack feature --hostname gitlab.example -R team/project \
  --format json
```

The explicit host/project must match the local origin. Initialization writes
only a local metadata ref. It does not switch branches, modify the index or
working files, or change branch tips. Dirty files are preserved by registration.
Repeating the identical registration is harmless; a different chain is refused.
The metadata is local to the repository and shared by linked worktrees, with no
global Git configuration or tracked metadata files.

## Bind existing MRs

Suppose MR 11 is `model -> main`, and MR 12 is `api -> model`, both in this same
GitLab project and at the exact local source heads:

```sh
gl-axi stack link model api --base main --stack feature \
  --hostname gitlab.example -R team/project \
  --mr model=11 --mr api=12 --allow-local-metadata

gl-axi stack view --stack feature --hostname gitlab.example -R team/project --mrs
```

This observes existing MRs using pinned official glab authentication. It never
creates or retargets one. Wrong host/project, fork identity, source/target branch,
source head, closed/merged MR or detected drift prevents binding. It may also
initialize a new bound stack, but will not replace an existing chain/binding.

An MR targeting its predecessor is a native dependency representation, not
proof that GitLab enforces that merge order. The output is not a green-check,
approval or merge-readiness verdict. Unrequested MR state is `not_observed`;
`--mrs` on unbound branches reports incomplete observations.

## Navigate explicitly

Read the current local graph, then supply its exact current and destination
heads. For example, from `api` to `model`:

```sh
gl-axi stack down --stack feature --hostname gitlab.example -R team/project \
  --allow-checkout --expected-current api \
  --expected-head <full-api-object-id> --expected-target <full-model-object-id>
```

Replace the angle-bracket placeholders with actual full object IDs from the
view. `up` moves toward the top; `down` toward the base. An optional positive
integer selects the distance. `top` selects the last branch, `bottom` the first,
`trunk` the base. `checkout BRANCH` selects any member or the base explicitly.
None wraps around the chain or fetches/creates missing branches.

Navigation refuses detached HEAD, dirty/staged/untracked/ignored files,
submodules, custom filters, sparse/partial checkouts, hidden index entries and
active Git operations. It never stashes, cleans, forces or discards work. All
existing branch tips, including unpublished commits, are preserved. Git refuses
a branch already checked out in another worktree. Checkout hooks are not run.

Unlike metadata publication, switching the working tree is not an atomic
transaction with arbitrary concurrent Git commands. If another command races,
or cancellation occurs after the attempt, the tool may report uncertainty.
Inspect the current branch and files before retrying; it never guesses a rollback.

## Stale graphs, limits and recovery

`view` can report missing or diverged branches without changing anything. Local
actions and live MR binding refuse stale graphs. This increment does not
repair/rebase/sync them automatically. `--limit` can return a partial display;
inspect `meta.complete`, `meta.truncated` and each node's status. There are at most
32 branches per stack and bounded local/provider reads. A view is not a global
inventory of every stack or MR in the project.

Metadata publication verifies object IDs and ordinary-ref identity while Git's
prepared transaction holds the relevant locks. A detected concurrent change
aborts before publication. An interrupted response can leave a successfully
published record without a confirmed receipt; re-read before replaying the
identical operation. Do not overwrite metadata or remove locks to force progress.
A retained local stack lock means an operation may still be active or was
interrupted; inspect before manual recovery. No public installation, server-side
stack service or Git extension is needed.

`glab-axi stack ...` remains the tested executable alias. Legacy native-v1
commands do not gain stack mutation authority.
