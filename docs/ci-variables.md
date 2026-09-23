# Guarded project CI/CD variables

These commands require explicit **`--auth-source native`**. One native credential
and configured authority cover every request in the complete operation. No
official-glab child/profile is consulted, and no fallback or account equivalence
is assumed. Existing command defaults and frozen native-v1 behavior are unchanged.
The shared native client refuses all automatic redirects, including same-origin
cross-path replay, before a second request. The underlying pinned official glab
has not been fixed: its synthetic redirect counterevidence remains separately
recorded in `TestPinnedOfficialGlabVariableRedirectCounterevidenceTLS`.

`secret list|set|delete` and `variable list|set|delete` are fixed typed commands,
not generic API forwarding. They require explicit `--hostname`, `-R`, and
`--scope`. Scope is the **literal GitLab environment_scope**, including `*`
or a pattern such as `review/*`; it is not a deployment environment lookup or
wildcard search. Duplicate keys in other scopes are independent resources.

## Confidentiality and platform semantics

Both list commands return **metadata only**, including key, exact scope, type,
masked, hidden, protected, raw, and class. Values, descriptions, hashes, and
value lengths are never returned. This is a deliberate confidentiality-driven
difference from `gh-axi variable list`, which displays ordinary variable values.

- `ordinary`: unmasked, unhidden, unprotected. Only `variable` operates on it.
- `protected`: unmasked, unhidden, restricted to protected refs. This is not a
  hidden secret. It is inventoried and deletable through `secret`, never through
  the ordinary-variable alias.
- `masked`: masked in job logs, not hidden in settings. Protection is reported
  separately. This is not equivalent to a hidden secret.
- `hidden`: masked and hidden in GitLab settings; protection is separate.
  **The pinned GitLab API returns `value:null`.** The old and resulting values
  cannot be verified. This does not make malicious CI code safe.

Unhidden provider value fields are compared only in transient private memory for
the selected key/scope and discarded at the inventory read boundary. Hidden,
null, missing, or malformed values never become value matches. Only safe
metadata and non-serialized exact-match booleans leave that boundary. Provider error
bodies, stderr, and unexpected fields are not raw-rendered. Go does not promise
cryptographic erasure of all heap copies; these commands do not claim protection
from a debugger or another process with access to this process's private memory.

## Version and availability

The contract pins [GitLab 17.6 project-variable API evidence](https://gitlab.com/gitlab-org/gitlab/-/blob/v17.6.0-ee/doc/api/project_level_variables.md)
and [17.6 variable security semantics](https://gitlab.com/gitlab-org/gitlab/-/blob/v17.6.0-ee/doc/ci/variables/index.md).
The pinned [API serializer](https://gitlab.com/gitlab-org/gitlab/-/blob/v17.6.0-ee/lib/api/entities/ci/variable.rb)
uses [VariableValue](https://gitlab.com/gitlab-org/gitlab/-/blob/v17.6.0-ee/app/models/ci/variable_value.rb)
to return null for hidden values.
Project variables are available on Free, Premium, and Ultimate. GitLab 17.4
introduced hidden variables behind a feature flag; 17.6 made them generally
available. These commands require a successful authenticated version read of
17.6.0 or newer and explicit `hidden`, `masked`, `protected`, and `raw` metadata.
Recognized version strings are `MAJOR.MINOR.PATCH`, optionally suffixed by
`-ee` or `-pre`. Newer prereleases such as `18.10.0-pre` are accepted;
`17.6.0-pre` is below the minimum.
Older/unknown versions, missing metadata, unavailable routes, and insufficient
permissions fail without a capability fallback. Ordinary variables have the
same floor so an older response cannot misclassify a hidden secret.

Mutations currently fail before credential/network work on Windows: the shared
private-file implementation verifies Unix mode bits but not Windows DACLs.
Persisted-native-config and self-managed authority mapping in the shipped Windows
binary also remain unproven, including for metadata reads. This increment does
not fix or claim general Windows authentication support, and changes no other
command's Windows behavior.

Only project-owned variables are covered, not group, instance, inherited,
pipeline, schedule, dotenv, or deployment environment administration. The
provider still enforces role, mask validation, and instance policy. No live
provider acceptance or production operation is implied by isolated tests.

## Input and exact prestate

```text
gl-axi secret list --auth-source native -R group/project --hostname gitlab.example --scope production --format json

gl-axi secret set DEPLOY_KEY --auth-source native -R group/project --hostname gitlab.example \
  --scope production --expected-project-id 101 \
  --expected-project-url https://gitlab.example/group/project \
  --expected-class absent --type env_var --protected true \
  --value-file /absolute/private/value --confirm
```

Create requires proven absence in that **exact** scope. Updating or deleting a
hidden entry requires all of these observable metadata guards:

```text
--expected-class hidden
--expected-type env_var
--expected-protected true
--expected-raw true
```

Hidden entries reject `--expected-value-file` because GitLab withholds their
values. These guards do not verify the old value or detect value-only drift.
For unhidden entries, the caller must also supply
`--expected-value-file /absolute/private/previous-value` and already possess the
old value. There is no command to reveal or export values and no exported
low-entropy value hash. `--confirm` authorizes only the selected operation; it is
not an auto-approval of arbitrary provider calls.

`--value-file -` explicitly selects piped stdin. Files must be absolute, private,
regular, and not final-component symlinks. Input is bounded to 1..10000 UTF-8
bytes without NUL; empty values are not supported. Bytes are exact: no newline
trimming. Hidden values need at least eight characters and no whitespace;
provider-specific additional mask constraints may still reject the request.
A terminal is never used to prompt for a value. No `--body`, `--value`, env-file,
inline JSON, or other argv value channel exists, even for ordinary variables.

`secret set` creates hidden+masked entries, or rotates existing hidden entries.
GitLab cannot turn an existing unhidden variable into a hidden one; this command
does not delete and recreate it or mislabel it. `variable set` creates/updates
only ordinary entries with `--protected false`. Existing type and protection
must be preserved. `env_var` and `file` are distinct types; no implicit type,
class, protection, key, or scope transition is allowed. Set always explicitly
uses `raw:true`, disabling expansion (an existing `raw:false` is an explicit
prestate guard, not a request to keep expansion). Descriptions are preserved by
omitting them from writes. `secret delete` can delete hidden, masked, or
protected entries with their exact metadata and, for unhidden entries, private
value prestate.

## Outcomes, races, and bounds

A mutation validates project ID/path/URL, completes an inventory, checks the
metadata prestate and any applicable private value, then repeats the complete
inventory and
project identity immediately before **one** POST/PUT/DELETE. Private request
bodies stay in process; no delegated temporary JSON file is needed. Writes use the
bound numeric project ID; update/delete always send the encoded exact
`filter[environment_scope]`. No provider default scope is trusted. After dispatch
one bounded inventory and project recheck observe the exact resulting metadata,
the resulting value when readable, or deletion absence.

GitLab offers neither a variable revision/CAS nor an immutable variable ID.
Preflight cannot prevent a concurrent change or delete/recreate between the
last check and the write. Hidden value-only drift cannot be detected even during
preflight. Receipts explicitly report `atomic_precondition:false`; they never
claim exclusive authorship, atomic value enforcement, or absence of concurrent
changes. Unhidden no-op set reports `precondition_observed` and makes no mutation.
Hidden set always attempts the requested mutation because equality with the
desired value cannot be checked.

Complete successful inventories, **never a 404 or an error string**, prove
absence. Success requires a complete provider response with the pinned success
status (201 for create, 200 for update, 204 for delete) and observable
reconciliation: exact metadata for hidden set, value plus metadata for unhidden
set, or exact absence for delete. A lost, incomplete, or unsuccessful response
never becomes success from reconciliation alone, even when metadata or absence
matches the requested result.

Receipts separate these facts:

- `provider_acknowledged`: whether the mutation returned a complete response
  with its pinned success status through the native transport.
- `reconciliation`: `metadata_observed`, `value_and_metadata_observed`,
  `absence_observed`, or `not_observed` for the requested postcondition.
- `value_verification`: `unavailable_hidden` for every hidden mutation;
  otherwise `matched` or `not_observed` for the desired set value or the
  preflight delete value. It never means an atomic value guard.

Successful mutations report `outcome:postcondition_observed`. A definite HTTP
provider rejection reports `outcome:rejected` only when reconciliation also
observes the original applicable guards: metadata for hidden entries, value
plus metadata for unhidden entries, or absence for create. A rejected hidden
mutation does not prove an unchanged hidden value. Other failures return
`ambiguous_variable` (exit 6) with a value-free receipt and
`mutation_attempted:true`. Observable evidence remains in ambiguous receipts
without implying success. A canceled/timed-out write is not retried; if the
caller deadline prevents reconciliation the outcome remains ambiguous.

Each inventory uses at most 10 pages of 100 records. A full final page cannot
prove completeness and fails closed. Each response is capped at 2 MiB and all
phases together at 8 MiB and 64 requests through the same native client;
read/write deadlines are 30/45 seconds and reconciliation
is at most 10 seconds within the caller deadline. `--limit` affects displayed
metadata only, never the completeness requirement for absence/prestate checks.
Duplicate exact key+scope records fail closed. This may refuse very large
projects rather than claim false completeness.

The shared native boundary also refuses selected authentication credential
material in request bodies or provider responses. There is no credential-extraction
exception for setting a CI variable to the current native authentication token.

Authoritative executable contracts: `contracts/ci-variables/v1.json`,
`internal/productnative`, and the generated
`schema/ux-v1/ci-variable-*.schema.json` files. Native synthetic TLS regressions
cover one credential over all phases, mapped API/web authority, exact scope,
private bodies, rejected/ambiguous outcomes, and no redirect or replay. Compiled
CLI tests cover both executable names and JSON/TOON. Separate pinned official-glab
counterevidence records its unsafe redirect behavior without exposing that path
as a product operation.
