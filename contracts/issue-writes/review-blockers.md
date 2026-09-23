# Temporary issue parity gaps

State transitions and blank creation are temporarily refused to prevent provider
content/quick-action effects outside the authorized scope. Nonblank ordinary
creation, plain comments and the thin `note` alias remain supported. This boundary
does not establish full issue parity or resolve the provider mechanisms below.
Existing `issue edit` remains validation-only.

## State transitions

The pinned GitLab
[update service](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/issuable_base_service.rb#L177)
feeds the stored description through quick-action extraction even for a
`state_event`-only PUT. The
[interpreter](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/quick_actions/interpret_service.rb#L58)
subtracts existing command parameter changes but still returns stripped text.
Stored command removal is a collateral content edit, even without executing the
same stored label command again.

Two reads can both observe `keep`, followed by another writer storing
`keep\n/label ~bug`. The PUT strips that new command text and returns `keep`, so
matching preflight/response/readback snapshots cannot prevent or detect the edit.
The pinned [REST route](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/lib/api/issues.rb#L383)
offers neither an extraction bypass nor an atomic expected revision.

`TestIssueWritesApprovedStateBoundary` reproduced that mutation before correction.
Close/reopen now have no PUT path: actual transitions return `unsupported` and a
`refused` receipt with zero mutation attempts. Already-matching bound states return
`unchanged` from read-only observations. The previous existing-description guard
is removed: ordinary fenced `/usr/bin/env` content is inert under the pinned
[extractor](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/lib/gitlab/quick_actions/extractor.rb#L87)
and does not prohibit a read-only no-op. No existing content is filtered or
resubmitted, and no snapshot grants mutation authority.

## Blank creation

The pinned
[create service](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/services/issues/create_service.rb#L164)
substitutes a template for blank descriptions. Its
[service tests](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/spec/services/issues/create_service_spec.rb#L223)
cover template substitution and quick-action effects. A `/label ~bug` template can
add a label yet return an empty description, passing exact empty-body checks.
There is no supported template-bypass option in the pinned REST create parameters.

`TestIssueWritesApprovedBlankCreateBoundary` reproduced the unrequested label
mutation before correction. Normalized-empty and whitespace-only descriptions
now return `unsupported` before credentials or HTTP. This also temporarily refuses
otherwise ordinary blank/title-only creation without a template. Nonblank plain
creation remains supported, including with quick-action templates configured.
There is no filler body, template-preflight permission claim or second mutation.

## Normalization and preserved boundaries

Titles use the pinned
[issuable stripping declaration](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/models/concerns/issuable.rb#L140)
and [strip attribute implementation](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.0-ee/app/models/concerns/strip_attribute.rb#L29):
Ruby [String#strip](https://docs.ruby-lang.org/en/3.4/String.html#method-i-strip)
removes surrounding ASCII whitespace, preserving internal and Unicode spaces.
`TestIssueCreateProviderTitleNormalization` reproduced successful mutation followed
by `ambiguous_create` for ` new title \t\n` before correction. Canonical titles now
enter request hashing and exact response verification after original input limits.
New descriptions/notes retain the previously corrected carriage-return and trailing
ASCII whitespace normalization, without weakening quick-action denials.

Unused delegated issue-write production builders remain removed; dependency
characterization stays test-only. The single native client retains the 2 MiB
response bound and 8 MiB aggregate budget without duplicate handler accounting.
Source ancestry remains pinned in `v1.json`. No account-equivalence claim, replay,
redirect, alternate transport, credential export or new credential store is added.

Regression fixtures execute the public command interface against local TLS and
synthetic credentials. They model the cited provider paths and are not evidence
from a live GitLab installation. Tests assert zero forbidden mutations and retain
positive create/comment/no-op controls; unsafe effects are not success criteria.
