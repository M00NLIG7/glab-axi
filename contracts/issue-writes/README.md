# Typed issue writes

`v1.json` pins the executable consumer grammar and bounded UX-v1 receipt.
`provider-v1.json` pins the GitLab REST routes, fields and semantic evidence.
Create, comment/note and close/reopen require explicit `--auth-source native`,
host, project, numeric identities and canonical URL. They reuse one existing-native
environment/keyring identity through `internal/product/native.go` and
`internal/productnative`, without official-profile fallback or account-equivalence
assumptions. Existing defaults and frozen native-v1 are unchanged.

## Scope and temporary gaps

Supported mutations are nonblank ordinary issue creation and plain issue notes;
`note` remains a thin alias for `comment`. This is not full reference issue parity.
Close/reopen transitions temporarily return `unsupported` with zero mutation
attempts. Already-matching caller-bound states return read-only `unchanged`
observations. Title-only, normalized-empty and whitespace-only creation is also
temporarily unavailable, even for projects without a default template. The
[provider evidence](review-blockers.md) explains these boundaries. No filler,
compensating mutation or collateral-operation authority is introduced.

Private title/body files are descriptor-validated and bounded before credentials.
Request JSON stays in memory. Numeric mutation routes and exact configured web
URLs bind the selected project and issue identities. After original file bounds,
titles strip surrounding ASCII whitespace, preserving internal and Unicode spaces.
New descriptions/notes reject quick-action-shaped lines even in code fences, then
remove carriage returns and trailing ASCII whitespace. Leading/internal body
whitespace and Unicode spaces remain unchanged. Hashes and exact response checks
use this canonical content. Existing issue content is never filtered, normalized
or resubmitted by state actions; fenced code permits read-only no-ops.

Labels, assignment, milestones, custom types, attachments, delete/move, lock,
hierarchy, GitHub close reasons and bundled comment+close are outside this contract.
Existing `issue edit` remains validation-only. Windows persisted-native-config/
self-managed mapping remains unproven.

## Receipts and concurrency

- Create/comment accept only direct response evidence for exact identity and
  content. They never search by title or latest note to claim authorship or replay.
- Close/reopen take `--expected-state opened|closed`. Identity/state drift fails
  preflight. Matching requested state returns `unchanged`; otherwise the
  `unsupported` error carries `outcome=refused`. Both have zero mutation attempts,
  `mutation_response=not_attempted` and `postcondition=preflight`. These are
  observations, not atomic preconditions or exclusive authorship.
- Definite HTTP rejection of create/note reports `rejected`. Unconfirmed,
  canceled, oversized, malformed or redirected outcomes remain `ambiguous_create`.
  Another invocation is a new attempt and may duplicate the resource.
- `mutation_attempts` counts zero or one intended transfer, not proof of receipt
  or application. `requested_sha256` hashes canonical requested JSON; for state
  refusals/no-ops it describes intent only, with no payload transmitted.
  `atomic_precondition` and `retry_safe` are always false. No private content or
  raw provider errors appear in receipts.

## Executable evidence

- `TestPinnedIssueWritesConsumerContract`: public grammar, excluded fields,
  independent create/note invocations, distinct identities and state refusals.
- `TestIssueWritesNativeContractExecutableAliases`: both built executable names
  against local TLS, isolated config/CA and synthetic credentials; create/note
  success, response failures, normalization, state refusals/no-ops, blank denials
  and zero official-child invocations.
- Other `TestIssueWritesNativeContract*`: one environment/keyring credential,
  native unavailability without fallback, API/web mapping and redirect refusal.
- `TestIssueWritesApprovedStateBoundary`: zero mutations despite concurrent
  description insertion, with ordinary, empty, command, fenced and CRLF content.
- `TestIssueWritesApprovedBlankCreateBoundary`: zero template effects for blank
  inputs; nonblank creation works with absent, ordinary and quick-action templates.
- `TestIssueCreateProviderTitleNormalization` and
  `TestIssueWriteProviderContentNormalization`: provider normalization, original
  bounds, canonical request hashes and exact response evidence.
- Other issue-write tests cover response fields, drift, bounds, cancellation,
  quick-action denials and one-attempt semantics.

There are no list/search/paginated requests. Preflight/mutation budgets are 10/20
seconds inside the 45-second operation lifetime. JSON response and aggregate
bounds remain 2 MiB and 8 MiB. `cmd/gen-product` owns generated help, skills and
the issue-write receipt schema.

## Delegated dependency evidence

Pinned official `glab` 1.112.0 follows HTTP 301/302/303 for issue-create POST,
issue-note POST and issue-state PUT to another HTTPS authority as GET, forwarding
the synthetic `Private-Token` in the local characterization fixture. 307/308 do not
redirect those requests in that fixture. The upstream behavior is not fixed or
used for public issue writes. `TestPinnedOfficialGlabIssueWritesRedirectCharacterization`
preserves test-only negative evidence. Native acceptance requires zero redirected
requests, including same-origin cross-path redirects. No live provider or real
credential is involved.
