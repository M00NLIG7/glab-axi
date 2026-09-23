# Typed issue writes

`v1.json` pins the executable consumer grammar and bounded UX-v1 receipt.
`provider-v1.json` pins the GitLab REST routes, request/response fields and
semantic evidence. Issue create, comment/note and close/reopen require explicit
`--auth-source native`, host, project, numeric identities and canonical URL.
They reuse `internal/product/native.go` and `internal/productnative` for one
existing-native environment/keyring identity throughout the complete operation.
There is no official-profile fallback or account-equivalence assumption.
Existing read/merge/ensure defaults and frozen native-v1 are unchanged.

## Delegated dependency evidence

Pinned official `glab` 1.112.0 follows HTTP 301/302/303 for issue-create POST,
issue-note POST and issue-state PUT to a different HTTPS authority as GET,
forwarding the synthetic `Private-Token` in the local characterization fixture.
307/308 do not redirect these private-input requests in that fixture. The
upstream behavior is NOT fixed or used as the public issue-write backend.
`TestPinnedOfficialGlabIssueWritesRedirectCharacterization` preserves that
negative evidence. The native acceptance tests instead require zero redirected
requests for every status, including same-origin cross-path redirects.
No production service or real credential is involved.

## Scope and private input

Coverage is ordinary issue creation, plain issue notes (`comment`/`note`) and
individual reversible close/reopen requests, not full reference issue parity.
Title/body content enters through descriptor-validated private files before
credential resolution. Request JSON stays in memory. Numeric project routes
prevent a mutable project path from selecting a replacement project; exact
web URLs are bound to the configured native web origin and path prefix.
Quick-action-shaped lines are rejected even inside Markdown code fences.
After the private-file size and quick-action checks, new description/note bodies
lose carriage returns and trailing ASCII space, tab, newline, vertical tab and
form feed, matching the pinned provider extractor. Leading/internal whitespace
and Unicode spaces are preserved. The request hash and exact response comparison
use this canonical content.
Labels, assignment, milestone, custom types, attachments, delete/move, lock,
hierarchy and bundled comment+close are not included.

## Receipts and concurrency

- Create/comment accept only direct response evidence for the exact resource
  and content. They never look up the latest note or an issue by title to claim
  authorship or idempotency.
- Close/reopen take `--expected-state opened|closed`. This is a preflight
  observation, not a server-enforced precondition. Two reads detect observed
  drift but cannot prevent another writer in the remaining window. Only
  `state_event` is submitted; there is no atomic expected revision.
  Before PUT, existing title/description evidence must match across those reads;
  descriptions with slash-leading lines or provider-sensitive whitespace are
  refused. Existing content is never resubmitted or normalized by the client.
- An accepted state response plus exact readback returns `state_observed`, not
  exclusive authorship. Response content must match the preflight observation;
  later different state or content is `conflict`.
- Matching preflight state returns `unchanged` without a mutation.
- A definite HTTP rejection reports `rejected`. Otherwise an unconfirmed,
  canceled, oversized, malformed or redirected mutation outcome is ambiguous.
  State readback cannot promote an unconfirmed response to success. A new
  invocation is a new attempt. Never retry blindly.
- `mutation_attempts` counts zero or one intended native transfer, not proof of
  transmission or provider application. `mutation_response`, `postcondition`
  and the compact typed request's SHA-256 provide bounded evidence without
  private content or raw provider errors. `atomic_precondition` and `retry_safe`
  are always false.
- GitLab `closed`/`opened` does not model GitHub `completed`/`not_planned` close
  reasons. Comments and state events remain separate invocations.

Existing `issue edit` remains validation-only in this increment, including its
label-name races and missing expected revision. Existing-content and label
changes are outside the authorized issue-write scope. However, raced description
sanitization (R1) and default-template effects on blank creates (R2) remain
[unresolved provider blockers](review-blockers.md); observed-content checks do
not establish absence of collateral effects. Windows persisted-native-config/self-managed
mapping remains unproven; this increment does not claim or implement general
Windows authentication support.

## Executable evidence

- `TestPinnedIssueWritesConsumerContract`: public grammar, excluded fields,
  independent invocations and distinct direct-response identities.
- `TestIssueWritesNativeContractExecutableAliases`: both built executable names
  against local TLS with private config/CA and synthetic environment credentials;
  success, rejection, lost/malformed responses, target mismatch, no-op and
  no-child/no-network input denials. Official glab is a child-invocation trap.
- Other `TestIssueWritesNativeContract*`: one environment/keyring identity for
  the full sequence, native unavailability without fallback, configured API/web
  mapping, every redirect and ambiguous state readback.
- `TestIssueWrite*`, `TestIssueState*` and the provider-field fixture: payload,
  drift, response bounds, cancellation, missing evidence and one-attempt tests.
- `TestIssueWriteProviderContentNormalization` and
  `TestIssueStateProviderExistingDescription`: ordinary file normalization and
  pre-mutation refusal of descriptions that the provider would rewrite.
- `TestIssueWriteProviderUnresolvedCollateralCharacterization`: negative
  evidence for the remaining template/concurrent-content blockers, not acceptance.
- Official-glab fixture tests characterize only the pinned dependency; they do
  not substitute for native product validation.

There are no list/search/paginated requests in this contract. Phase deadlines
are 10/20/10 seconds inside the native 45-second lifetime. JSON response and
aggregate bounds remain 2 MiB and 8 MiB.
