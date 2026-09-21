# Typed issue writes

`v1.json` pins the executable consumer grammar and its bounded UX-v1 receipt.
Provider routes, request/response fields and semantic evidence are pinned in
`../official-glab/v1.112.0/issue-writes.json` and `capabilities.json`.

Coverage is ordinary issue creation, plain issue notes (`comment`/`note`) and
individual reversible close/reopen requests. It is not full issue parity with
the reference. Title/body content must come from descriptor-validated private
files. Explicit host, project, numeric identity and canonical URLs are required.
GitLab quick-action-shaped lines are rejected before any child, even inside
Markdown code fences. Labels, assignment, milestone, custom types, attachments,
delete/move, lock, hierarchy and bundled comment+close are not included.

## Receipts and concurrency

- Create/comment accept only direct provider response evidence for the exact
  resource and content. They never look up the latest note, or an issue by title,
  to claim authorship or idempotency.
- Close/reopen take `--expected-state opened|closed`. This is a preflight
  observation, not a server-enforced precondition. Two reads detect observed
  drift, but cannot prevent another writer in the remaining window. The request
  only sets `state_event`; it cannot promise an atomic expected revision.
- A successful state response plus exact readback returns `state_observed`, not
  an exclusive authorship claim. A later different state is `conflict`.
- Already matching preflight state returns `unchanged` without a mutation.
- A framed definite HTTP rejection reports `rejected`; otherwise a lost,
  canceled, oversized, invalid or wrong-identity response is ambiguous. State
  readback may disclose the observed state, but cannot promote an unconfirmed
  response to success. A new invocation is a new attempt. Never retry blindly.
- The receipt reports `mutation_attempts` (zero or one delegation),
  `mutation_response`, `postcondition`, and a SHA-256 of the canonical compact
  JSON request. It never includes private content or raw provider errors.
  `atomic_precondition` and `retry_safe` are always false.
- GitLab `closed`/`opened` states do not model GitHub `completed`/`not_planned`
  close reasons. Comments and state events are separate invocations.

The existing `issue edit` contract remains validation-only, including its
label-name races and lack of an expected-revision field. These new commands
cannot mutate existing issue content or labels.

## Executable evidence

- `TestPinnedIssueWritesConsumerContract`: fixture-driven public grammar,
  excluded fields and independent invocation semantics.
- `TestIssueWritesExecutableAliasesEndToEnd`: both built executable names,
  exact/wrong targets, private inputs, success/rejection/lost/malformed response,
  no-op and no-child denial paths.
- `TestIssueWrite*` and `TestIssueState*`: payload/identity drift, content and
  response bounds, cancellation, ambiguity, single-attempt and cleanup tests.
- `TestIssueWritesPinnedProviderBuilders` and
  `TestPinnedOfficialGlabIssueWritesTLS`: authoritative provider fixtures,
  actual official-glab method/path/private JSON, synthetic TLS credentials,
  status classification, and no retry/redirect for every mutation.

No production resource or real credential is used. No list/search or paginated
read is required by this contract. Phase deadlines are 10/20/10 seconds inside
the existing 45-second operation cap; JSON page and aggregate byte caps remain
2 MiB and 8 MiB.
