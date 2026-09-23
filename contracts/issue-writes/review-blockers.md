# Remaining provider incompatibilities

R1 is mitigated for descriptions observed before mutation, but remains open
for concurrent description changes. R2 remains open. This increment does not
yet satisfy the required absence of collateral content/quick-action effects.
These are release blockers, not authority to perform the extra operations.

## R1: state updates sanitize stored content

The pinned GitLab
[update service](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/issuable_base_service.rb#L177)
feeds the existing description through quick-action extraction when a PUT
contains only `state_event`. Its
[interpreter](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/quick_actions/interpret_service.rb#L58)
subtracts the original commands' parameter changes, but still returns stripped
text. The reproduction must distinguish this content removal from executing
the same stored label command again.

`TestIssueStateProviderExistingDescription` failed before the guard: both close
and reopen changed `keep\n/label ~bug` to `keep` and returned `state_observed`.
Trailing newlines and carriage returns were also rewritten. Ordinary and empty
descriptions passed the control cases. The guard now rejects observed command
lines and normalization-sensitive descriptions before PUT. It also requires
observed content stability, checks response/readback content, and preserves
no-write no-ops. It never submits replacement description/title content.

The remaining counterexample is `state-description-race` in
`TestIssueWriteProviderUnresolvedCollateralCharacterization`: both preflight
reads see `keep`; another writer inserts `keep\n/label ~bug` before PUT; GitLab
strips the newly stored command and returns `keep`. The direct response and
readback match the original snapshots. They cannot detect that collateral edit,
and `state_observed` proves only the observed state/content. The receipt still
sets `atomic_precondition=false`.

The pinned [REST route](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/lib/api/issues.rb#L383)
offers neither an extraction-bypass parameter nor a conditional revision guard.
`updated_at` is a writable timestamp, not a precondition. Submitting the old
description would authorize an overwrite and does not fix the race. Fully
preventing this effect requires a supported provider mechanism or a separately
decided product boundary; the observed-content guard alone does not resolve R1.

## R2: blank creation invokes the default template

The pinned
[create service](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/issues/create_service.rb#L164)
substitutes a default template when the description is blank. Its
[service tests](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/spec/services/issues/create_service_spec.rb#L223)
cover both template substitution and its quick-action effects. The pinned REST
create parameters provide no template-bypass option.

The TLS CLI characterization reproduced these outcomes before fixes:

| Submitted description | Default template | Stored description | Labels added | CLI result |
| --- | --- | --- | --- | --- |
| Empty | Absent | Empty | None | `created` |
| Empty | `template body` | `template body` | None | `ambiguous_create` |
| Empty | `/label ~bug` | Empty | `bug` | `created` |
| `ordinary body` | `/label ~bug` | `ordinary body` | None | `created` |

A template preflight would be another raced observation, not prevention at
creation time. Rejecting every blank body would remove the valid first row;
filler text, a second mutation, or a made-up server parameter would change the
approved contract. Blank creation is retained pending the supervisor's concrete
boundary decision. The quick-action-template case still requires resolution.

## Completed corrections and evidence scope

- R3 canonicalizes new descriptions/comments with the pinned
  [extractor's rules](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/lib/gitlab/quick_actions/extractor.rb#L87):
  remove carriage returns and trailing ASCII whitespace. Input limits apply
  before normalization; leading/internal whitespace and Unicode spaces survive.
  Request hashes and exact response checks use the submitted canonical content.
- R4 removes unused production delegated issue-write builders and capability
  entries. Pinned official-client TLS/redirect probes are test-only; the unsafe
  upstream redirect behavior is not represented as fixed.
- R5 retains `note` as the existing thin alias for `comment`.
- R6 removes handler byte accounting; every request still goes through the same
  native client with a 2 MiB response bound and 8 MiB operation budget. Behavioral
  overflow tests remain in place.

Fixtures use local TLS and synthetic credentials only. They model the cited
provider paths; they are not evidence from a live GitLab installation. Passing
the explicitly named unresolved characterization test confirms the remaining
counterexamples and must not be interpreted as safety acceptance or waiver.
