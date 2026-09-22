# Ordinary MR writes

Reference: [gh-axi `pr` source at 2bffd9a5b60ded64d6c9851683b27a480173a7ee](https://github.com/kunchenguid/gh-axi/blob/2bffd9a5b60ded64d6c9851683b27a480173a7ee/src/commands/pr.ts).
The destination preserves GitLab terminology and stronger bounded identity checks, not GitHub review objects or arbitrary flags.

## Pinned provider evidence

The existing backend is official glab 1.112.0 at `816e3a52411aba73d90237859fdc6ecbc86bd169`, using client-go/v2 2.53.0. The fixed `api --method --hostname --input` argv is covered by `contracts/official-glab/v1.112.0/help.txt` and the offline version/help gate.

- [client-go v2.53.0 merge_requests.go](https://gitlab.com/gitlab-org/api/client-go/-/raw/v2.53.0/merge_requests.go): `CreateMergeRequestOptions` supports `assignee_ids`, `reviewer_ids`, `milestone_id`, and `labels`; `UpdateMergeRequestOptions` supports `state_event`, title, description, and replacement metadata, but **no expected revision or SHA**. The separate `AcceptMergeRequestOptions` has SHA, which must not be confused with ordinary update authority.
- [GitLab MR update API](https://docs.gitlab.com/api/merge_requests/#update-mr): `PUT projects/:id/merge_requests/:iid`, `state_event` is `close` or `reopen`. Assignees/reviewers are replacement collections. Labels are names and can implicitly create missing labels. These are not atomic numeric-label or revision preconditions.
- [client-go v2.53.0 notes.go](https://gitlab.com/gitlab-org/api/client-go/-/raw/v2.53.0/notes.go): `CreateMergeRequestNoteOptions` supports `body`, `created_at`, `internal`, and `merge_request_diff_head_sha`; POST `/projects/:id/merge_requests/:iid/notes` and GET the exact `/notes/:note_id` are separate operations. Only `body` is exposed here.
- [GitLab notes API](https://docs.gitlab.com/api/notes/#create-a-merge-request-note): notes are not inline reviews. An emoji-only body can create a reaction; quick actions can mutate other resources. `merge_request_diff_head_sha` is for the `/merge` quick action, **not a general note-head guard**. This implementation rejects quick-action-shaped lines, control/format characters, and emoji-only input before dependency work.
- [official glab update implementation at the pinned commit](https://gitlab.com/gitlab-org/cli/-/raw/816e3a52411aba73d90237859fdc6ecbc86bd169/internal/commands/mr/update/mr_update.go): `--draft` prepends `Draft: ` to the title; `--ready` strips draft/WIP title prefixes then assigns `l.Title`. Ready is a title replacement, not a provider-enforced conditional draft transition.

## Transport delivery gate

The actual pinned package currently fails
`TestPinnedOfficialGlabMRWriteRedirectAuthorityTLS`: a synthetic note POST that
receives HTTP 302 follows the redirect to a different HTTPS port and forwards the
runtime synthetic PRIVATE-TOKEN. This is a local TLS regression, not evidence of
real credential exposure. The official-package CI gate includes this regression.
These candidate writes must not be released until a supported transport contract
passes it. No proxy enforcement layer or credential extraction is introduced.

## Implemented contracts

`mr comment` (`mr note` alias) and `mr close` / `mr reopen` require explicit host/repository, canonical IID/URL, source/target branches, head, and observed opened/closed state. They reuse discussion project/MR/note identity validation. Both MR snapshots before mutation must agree, including base, head and updated-at. Forks and merged MRs are refused. Bounds: 20-second preflight, 15-second single mutation, 10-second readback within the 45-second outer/caller deadline; 2 MiB per response and 8 MiB total. Notes use absolute descriptor-validated private files, at most 128 KiB. Prose must contain letters; quick-action-shaped lines are refused even inside code blocks.

State events send only `state_event`. A no-op returns `unchanged` and zero mutation attempts. A post-write exact-IID read must prove the same identity/branches/base/head and desired state. The receipt says `observed`, not that this invocation exclusively caused the transition. The provider cannot atomically enforce the expected revision. That limitation is explicit in help and `provider_revision_enforced:false` receipts; expected and observed state are separate fields (unknown readback omits observed state); the existing guarded merge contract is unchanged.

Note success requires a valid ordinary note from this invocation's successful POST, followed by exact note-ID readback with identical body, attribution and resource identity. The body is never included in receipts. A lost or malformed response that loses trustworthy note identity stays `ambiguous_create`, even when another note has identical text. There is no latest-note/body/timestamp search and no retry. Readback failure, identity drift, cancellation or unproved state returns a bounded unknown receipt. A framed HTTP rejection plus intact target readback returns a rejected receipt.

`mr ensure` / `mr create-or-update` add bounded numeric `--assignee-id` and `--reviewer-id` selection (repeatable, at most 20 unique positive IDs), numeric `--milestone-id`, and `--draft`. Selection is creation-only. Provider responses and reconciliation must preserve the selected values. An existing branch-pair match, including a competing creator, must already have all selected metadata and exact title/body; otherwise the invocation refuses without PUT. No collection is merged with or copied from a potentially stale snapshot. No name-to-ID alias resolution or clear-all sentinel is accepted. The existing title/body-only ensure behavior remains available when no creation metadata is selected.

## Explicit residual gaps

- Exact-IID rich metadata edit and ready are pending. The pinned update endpoint has no expected revision/head, reviewer/assignee edits replace collections, and ready rewrites a title observed before the write. Exposing these as guarded edits would risk overwriting unseen concurrent metadata. No stub command claims success.
- Label selection is pending: name-only provider writes may implicitly create labels after a catalog rename/delete race. Numeric label identity is not enforceable by the pinned MR API.
- Numeric creation selectors do not claim username/milestone-name lookup parity. GitLab remains responsible for user/milestone availability, permissions and tier limits; a mismatching response is not success.
- Approval, request-changes, inline discussions/replies/resolution, attachments, cross-project authority, merge strategies, auto-merge, source deletion, rebase/revert and issue writes are not exposed by this increment.

Tests: `internal/product/mr_write_e2e_test.go` drives both executable names through local TLS protocol fixtures; `mr_write_test.go` covers private input, cancellation/budgets and creation-selection reconciliation. `internal/delegate/glab/mr_write_contract_test.go` pins argv and exercises the actual optional official package against synthetic TLS. No live GitLab mutation or credential store is involved.
