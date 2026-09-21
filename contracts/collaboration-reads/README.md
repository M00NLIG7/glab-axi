# Collaboration reads v1

`v1.json` pins the bounded comparison to gh-axi
`2bffd9a5b60ded64d6c9851683b27a480173a7ee`. The provider route and field evidence is
public client-go v2.53.0 source, the client pinned by official glab 1.112.0.
Source hashes are recorded for reproducibility. No credential or live project
was used to establish the fixture.

## Public equivalents

- `gl-axi issue discussions IID`: GitLab discussion threads, including individual
  comments, replies and system notes. The existing MR discussion normalizer,
  pagination and nested/body bounds are reused, with `noteable_type=Issue` and
  exact global issue ID/project/IID validation. This is not a comment write.
- `gl-axi mr approvals IID`: assigned reviewers from the
  MR and the current approval summary from the fixed `/approvals` GET. This is
  not GitHub review-submission history. Existing `mr discussions` supplies
  inline/threaded notes; those notes are never interpreted as review votes.

Both leaves leave native `glab-axi/v1` and existing list/view defaults unchanged.
`glab-axi` remains an executable alias.

## Meaning and uncertainty

Approval `state` is `approved` or `not_approved` only when the provider supplies
an explicit boolean. Missing/null is `unknown`; zero remaining approvals and
an empty/nonempty approver list do not establish approval. The boolean is
edition-dependent, not a portable merge-policy decision. See the provider's
[approval API description](https://docs.gitlab.com/api/merge_request_approvals/#retrieve-approval-state-for-a-merge-request).
The deployed edition/license is not discovered, so `tier` is always `unknown`.
Approval rules, reviewer submission states, timestamps and historical reviews
are not exposed by this slice.

A definite HTTP 403 or 404 from the approval GET returns `availability=unavailable`
and incomplete metadata, with `access_denied` or `not_found_or_unsupported`.
These responses cannot distinguish entitlement, version, hidden resource or
permission. Other failures, including authentication/cancellation/malformed
responses, fail. Text mentioning a status without the pinned official error
framing is not sufficient for a definite unavailable result. Issue discussion
errors always fail rather than becoming empty comments. A complete empty array
means no visible discussions or assigned reviewers, not universal absence.

The shipped official `mr view` command internally attempts `/approval_state`
even for JSON and tolerates failure. Its result is not used here as tier or
approval evidence. This behavior is pinned in
[official source](https://gitlab.com/gitlab-org/cli/-/blob/816e3a52411aba73d90237859fdc6ecbc86bd169/internal/commands/mr/view/mr_view.go#L81-110).
No new arbitrary API or credential boundary is exposed.

## Bounds and evidence window

Issue limits count threads. MR approval limits apply independently to each
embedded user array; metadata count is the number of displayed approving users. All
records, including those beyond the display prefix, are validated before
trimming. Missing reviewer data is unknown, not an empty assignment set.
The hard bounds are pinned in `v1.json` and `internal/limits/limits.go`:
30-second whole-command deadline, 2 MiB per response, 8 MiB total response bytes
including identity reads, and bounded serialized output. Discussions use stable
page width, a limit+1 probe, ten-page ceiling, 1000 nested notes and shared
UTF-8-safe body bounds. Any discussion truncation makes completeness false.

Identity is checked before and after collection: canonical project path/URL and
numeric ID; issue global ID/IID/updated-at; MR source and target project,
branches, base/head and updated-at. Reviewer rechecks compare the full user
arrays, including undisplayed users. These checks detect observed changes but
are not an atomic provider snapshot or a merge-authorization receipt. Discussion
edits and approval changes are not guaranteed to advance MR/issue updated-at.

## Executable evidence

- `internal/product/collaboration*_test.go`: both built CLI entrypoints,
  real child argv, synthetic credentials, absent/denied/unknown/malformed
  responses, wrong identity/head, invalid-input zero-child behavior,
  pagination/probe/nested/body/operation bounds and cancellation.
- `internal/delegate/glab/collaboration_contract_test.go`: fixture-to-typed-argv
  checks and real pinned glab against an isolated TLS server. The latter runs
  when `GL_AXI_OFFICIAL_GLAB_TEST_BINARY` is supplied by the existing CI job.
  A fixture-only `ca_cert` configuration supports the synthetic CA on macOS as
  well as Linux; TLS verification is never disabled.
- Existing discussion, native compatibility, generated help and closed-schema
  checks remain part of `go test ./...`.

This is a collaboration-read increment, not a complete-parity, live-provider,
installation, reviewer-workflow or approval-rule-support claim.
