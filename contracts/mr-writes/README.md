# Ordinary MR writes

Reference: [gh-axi `pr` source at 2bffd9a5b60ded64d6c9851683b27a480173a7ee](https://github.com/kunchenguid/gh-axi/blob/2bffd9a5b60ded64d6c9851683b27a480173a7ee/src/commands/pr.ts).
The destination preserves GitLab terminology and stronger bounded identity checks, not GitHub review objects or arbitrary flags.

## Pinned provider evidence

Provider fields are pinned to client-go/v2 2.53.0 and the official glab 1.112.0 source at `816e3a52411aba73d90237859fdc6ecbc86bd169`. Runtime execution of these ordinary writes is explicit product-native HTTP, not delegated glab. Default legacy ensure/merge behavior and the official version/help gate remain unchanged.

- [client-go v2.53.0 merge_requests.go](https://gitlab.com/gitlab-org/api/client-go/-/raw/v2.53.0/merge_requests.go): `CreateMergeRequestOptions` supports `assignee_ids`, `reviewer_ids`, `milestone_id`, and `labels`; `UpdateMergeRequestOptions` supports `state_event`, title, description, and replacement metadata, but **no expected revision or SHA**. The separate `AcceptMergeRequestOptions` has SHA, which must not be confused with ordinary update authority.
- [GitLab MR update API](https://docs.gitlab.com/api/merge_requests/#update-mr): `PUT projects/:id/merge_requests/:iid`, `state_event` is `close` or `reopen`. Assignees/reviewers are replacement collections. Labels are names and can implicitly create missing labels. These are not atomic numeric-label or revision preconditions.
- [client-go v2.53.0 notes.go](https://gitlab.com/gitlab-org/api/client-go/-/raw/v2.53.0/notes.go): `CreateMergeRequestNoteOptions` supports `body`, `created_at`, `internal`, and `merge_request_diff_head_sha`; POST `/projects/:id/merge_requests/:iid/notes` and GET the exact `/notes/:note_id` are separate operations. Only `body` is exposed here.
- [GitLab notes API](https://docs.gitlab.com/api/notes/#create-a-merge-request-note): notes are not inline reviews. An emoji-only body can create a reaction; quick actions can mutate other resources. `merge_request_diff_head_sha` is for the `/merge` quick action, **not a general note-head guard**. This implementation rejects quick-action-shaped lines, control/format characters, and emoji-only input before dependency work.
- [official glab update implementation at the pinned commit](https://gitlab.com/gitlab-org/cli/-/raw/816e3a52411aba73d90237859fdc6ecbc86bd169/internal/commands/mr/update/mr_update.go): `--draft` prepends `Draft: ` to the title; `--ready` strips draft/WIP title prefixes then assigns `l.Title`. Ready is a title replacement, not a provider-enforced conditional draft transition.

## Explicit native authentication and transport

New note/state leaves require `--auth-source native`; creation metadata selection
on ensure also requires it. The shared selector and `openNative` are owned by
`internal/product/native.go`. One `internal/productnative.Client` covers the full
operation using existing native environment/keyring resolution and configured
host/API/web mapping. Native and official profiles may represent different
accounts. There is no credential export, profile parser, proxy enforcement,
automatic selection, or fallback. Native receipts report `backend:native` and no
upstream CLI version. Existing defaults and frozen native-v1 are unchanged.

`mr_native.go` maps only the typed MR operations onto that shared boundary,
reusing shipped ensure reconciliation and discussion/diff identity normalization.
All redirects and automatic retries are refused. Tests require zero second
requests across HTTPS origins and same-origin cross-path writes. The separate
`TestPinnedOfficialGlabMRWriteRedirectEvidenceTLS` pins negative dependency
evidence: the old official package forwards a runtime synthetic PRIVATE-TOKEN
after a note POST receives HTTP 302 to another HTTPS port. It does **not** claim
that upstream was fixed. No production operation or real credential is involved.

## Implemented contracts

`mr comment` (`mr note` alias) and `mr close` / `mr reopen` require `--auth-source native`, explicit host/repository, canonical IID and configured-web URL, source/target branches, head, and observed opened/closed state. They reuse discussion project/MR/note identity validation. Both MR snapshots before mutation must agree, including base, head and updated-at. Forks and merged MRs are refused. Bounds: 20-second preflight, 15-second single mutation, 10-second readback within the 45-second outer/caller deadline; 2 MiB per response and 8 MiB total. Notes use absolute descriptor-validated private files, at most 128 KiB. Prose must contain letters; quick-action-shaped lines are refused even inside code blocks.

State events send only `state_event`. A no-op returns `unchanged` and zero mutation attempts. A post-write exact-IID read must prove the same identity/branches/base/head and desired state. The receipt says `observed`, not that this invocation exclusively caused the transition. The provider cannot atomically enforce the expected revision. That limitation is explicit in help and `provider_revision_enforced:false` receipts; expected and observed state are separate fields (unknown readback omits observed state); the existing guarded merge contract is unchanged.

Note success requires a valid ordinary note from this invocation's successful POST, followed by exact note-ID readback with identical body, attribution and resource identity. The body is never included in receipts. A lost or malformed response that loses trustworthy note identity stays `ambiguous_create`, even when another note has identical text. There is no latest-note/body/timestamp search and no retry. Readback failure, identity drift, cancellation or unproved state returns a bounded unknown receipt. A recognized HTTP rejection plus intact target readback returns a rejected receipt.

`mr ensure` / `mr create-or-update` add bounded numeric `--assignee-id` and `--reviewer-id` selection (repeatable, at most 20 unique positive IDs), numeric `--milestone-id`, and `--draft`. Selection is creation-only and requires explicit native authentication. Native descriptions also reject quick actions. Provider responses and reconciliation must preserve the selected values. An existing branch-pair match, including a competing creator, must already have all selected metadata and exact title/body; otherwise the invocation refuses without PUT. No collection is merged with or copied from a potentially stale snapshot. No name-to-ID alias resolution or clear-all sentinel is accepted. The existing delegated title/body-only ensure behavior remains available when native selection is omitted. Explicit native ensure uses the same reconciliation algorithm for the complete operation without any official child.

## Explicit residual gaps

- Exact-IID rich metadata edit and ready are pending. The pinned update endpoint has no expected revision/head, reviewer/assignee edits replace collections, and ready rewrites a title observed before the write. Exposing these as guarded edits would risk overwriting unseen concurrent metadata. No stub command claims success.
- Label selection is pending: name-only provider writes may implicitly create labels after a catalog rename/delete race. Numeric label identity is not enforceable by the pinned MR API.
- Numeric creation selectors do not claim username/milestone-name lookup parity. GitLab remains responsible for user/milestone availability, permissions and tier limits; a mismatching response is not success.
- Approval, request-changes, inline discussions/replies/resolution, attachments, cross-project authority, merge strategies, auto-merge, source deletion, rebase/revert and issue writes are not exposed by this increment.

- Windows persisted-native-config and self-managed mapping remain unproven. The compiled native-MR tests do not claim general Windows authentication support or change its permission model.

Tests: `internal/product/mr_write_e2e_test.go` drives both executable names through real product-native TLS with synthetic config/CA/environment credentials. `mr_write_native_test.go` covers complete-operation identity, configured authority, redirects, no fallback and ambiguity. `mr_write_test.go` covers private input, cancellation/budgets and reused creation-selection reconciliation. `internal/delegate/glab/mr_write_contract_test.go` proves that ordinary MR operations remain undelegatable. No live GitLab mutation or real credential store is involved.
