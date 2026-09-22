# Guarded project administration

`repo create`, `repo edit`, and `repo fork` are GitLab-native, provider-only
counterparts of the reference repository metadata operations. They are
consequential writes, not a grant to administer any live resource. Invoke them
only with explicit authorization for the exact account, host, destination and
change. `--allow-project-admin` records that opt-in; it does not obtain approval.

The closed consumer contract is [`contracts/repo-admin/v1.json`](../contracts/repo-admin/v1.json).
It pins the reference revision, GitLab REST v4 routes and field evidence from
GitLab v19.0.0, and the explicit product-native boundary. This is a field
contract, not a claim that every server version or permission level supplies
the required evidence.
Missing or unknown required settings fail closed. Native `glab-axi/v1` is
unchanged; both product executable names accept the new operations.

`--auth-source native` is required for these writes and for the administration
snapshot read. It reuses existing native environment/keyring resolution and
configured host/API/web mapping. Native and official-glab accounts may differ;
there is no credential export, profile parsing, fallback or new store. One
client pins one credential and authority for all preflights, mutation and
reconciliation. All redirects are refused before a second request, including
same-origin cross-path mutation replay. Ordinary `repo view` remains delegated
by default; native selection there is restricted to `--admin-snapshot`.

Windows shipped-binary persisted-native-config and self-managed authority
mapping remain unproven. These commands do not establish complete Windows
support or change its permission/authentication model.

## Explicit destinations and identities

All writes require `--auth-source native`, `--hostname`, `-R`, `--expected-user-id`,
`--expected-username`, `--namespace-id`, `--namespace-kind user|group`, and
`--allow-project-admin`. There is no account, host, owner or namespace fallback.
A namespace ID is **not** a user ID. A personal namespace must exactly match the
expected username. Group and nested subgroup full paths remain distinct.

For create/edit, `-R namespace/project` is the destination. For fork, `-R` is
the source; both `--expected-source-id` and `--destination namespace/project`
are required. Source and destination must differ and are on the same explicit
host. Returned project ID, full path, root-path HTTPS URL and namespace must
match the configured native web authority, including any configured web prefix.
No fuzzy namespace search or paginated destination inference occurs.

The effective account (`GET /user`) and exact namespace (`GET /namespaces/:id`)
are checked twice. Project edits and fork sources have adjacent identity and
settings rechecks. Creation/fork destinations must return HTTP 404 in two
exact-path reads before the POST. A permission error, an untrusted error body,
existing project, or ambiguity never selects a different destination.

## Create

```text
gl-axi repo create --auth-source native -R team/sub/project --hostname gitlab.example.invalid \
  --namespace-id 21 --namespace-kind group \
  --expected-user-id 7 --expected-username tester \
  --visibility private --allow-project-admin
```

Visibility is required, with exactly `private`, `internal`, or `public`. Omission
fails before a child runs; there is no inherited visibility or silent public
default. Internal visibility remains subject to GitLab instance/namespace
policy. The private example above is illustrative, not live-resource authority.

Creation sends `namespace_id`, exact `name`/`path`, `visibility`,
`initialize_with_readme:false`, and optionally `description`. It creates an
empty project, not a source import or template copy. Descriptions enter via
`--description-file`: absolute, regular, non-symlink, private mode-0600 file,
maximum 2,000 UTF-8 bytes. An empty file explicitly clears the description.

No merge policy setting is sent. Instance/group defaults apply to a new
project; observed pipeline/discussion/skipped-pipeline, merge-method and squash
settings are included in the receipt, not silently replaced with tool defaults.
No branch, CI, permission, or account settings are configured.

## Edit and non-atomic concurrency

Obtain read-only prestate with:

```text
gl-axi repo view --auth-source native -R team/sub/project --hostname gitlab.example.invalid \
  --admin-snapshot --format json
```

Store only `data.admin_snapshot` as JSON in an absolute private mode-0600 file.
The closed snapshot schema is
[`repo-admin.schema.json#/$defs/project`](../schema/ux-v1/repo-admin.schema.json).
It contains exact project/namespace identity, `updated_at`, description,
visibility, default branch, issues/wiki access levels, and observed merge
settings. The snapshot is complete, not field-truncated; an unavailable field
prevents an edit. Provider null descriptions/default branches normalize to
empty strings, and null `allow_merge_on_skipped_pipeline` normalizes to false.

```text
gl-axi repo edit --auth-source native -R team/sub/project --hostname gitlab.example.invalid \
  --namespace-id 21 --namespace-kind group \
  --expected-user-id 7 --expected-username tester \
  --expected-state-file /absolute/private/prestate.json \
  --description-file /absolute/private/description.txt \
  --allow-project-admin --accept-non-atomic
```

Supported settings are `--description-file`, `--visibility`,
`--default-branch`, `--issues-access-level` and `--wiki-access-level`.
GitLab feature access has three values: `disabled`, `private` (members only),
and `enabled` (everyone with project access). These are deliberately not
collapsed into GitHub's enable booleans. Default-branch changes select an
existing short branch name; they never create or push a branch.

The caller snapshot must match the first project read, and the second read
must match the first. An exact no-op returns `unchanged` without PUT. A changed
request uses the pinned numeric project ID and sends only requested settings.
All unrequested supported settings and the observed merge defaults must still
match in the canonical postcondition read. No repair PUT follows a mismatch.

GitLab does **not** enforce an expected project revision. Neither repeated
reads nor `--accept-non-atomic` eliminate the race after the last preflight.
A concurrent edit can be overwritten, and provider-side project/namespace
state or account permissions can change after a check. The selected native
credential stays fixed throughout the operation. These residuals are disclosed
in every receipt. Postcondition success proves observed desired state, not exclusive
attribution or a lock. A failed postcondition can occur after a consequential
change was already applied. Do not blindly retry.

## Asynchronous fork

```text
gl-axi repo fork --auth-source native -R upstream/project --hostname gitlab.example.invalid \
  --expected-source-id 101 --destination team/sub/fork \
  --namespace-id 21 --namespace-kind group \
  --expected-user-id 7 --expected-username tester \
  --visibility private --allow-project-admin --wait-seconds 10
```

Forking copies provider repository contents asynchronously, unlike empty
project creation. Only namespace, name/path, visibility and optional
description are set. No follow-up settings change, local clone, remote edit,
push or pipeline mutation is performed.

Receipts distinguish:

| Outcome | Meaning |
|---|---|
| `accepted` | Canonical project observed with `none` or `scheduled`; not ready. |
| `in_progress` | Canonical `started`; not ready. |
| `completed` | Canonical `finished` and exact source project ID/path/URL proven. |
| `failed` | Provider import failed; the destination project may remain. No cleanup is attempted. |
| `ambiguous` | Identity, metadata, transport or unknown status prevents proof. No retry is attempted. |

The default wait is zero: one canonical postcondition read follows the POST.
`--wait-seconds 0..20` adds bounded observation, at most ten canonical reads in
total, separated by at most one second. `meta.complete:false` and a reason
accompany pending/timeout receipts, even when exit status is zero. Caller
cancellation after acceptance returns a cancellation receipt, not a claim that
the provider stopped. A polling deadline returns the last observed pending
state. Failed imports are exit 8; ambiguity is exit 6.

Later read-only `repo view --admin-snapshot --auth-source native` reports
`import_status` and, when
present, validated `forked_from_project`. Match the recorded destination ID
and exact source ID/path/URL before treating `finished` as this fork's
completion. Do not issue a second fork as a status check.

## Bounds, ambiguity and exclusions

There is at most one intended mutation, never a write retry. The outer deadline
is 45 seconds, with 15-second preflight, 10-second mutation and 20-second
postcondition phase caps. Responses are capped at 2 MiB each and 8 MiB across
the operation. Polling cannot lift these bounds. No paginated catalog is used.

An uncertain edit can reconcile from exact desired canonical settings. An
uncertain create/fork cannot attribute an observed destination to this attempt,
so it remains non-retryable ambiguity even if a matching project is visible.
A successful response with invalid identity never becomes success through a
later lookup. Error receipts are under `error.receipt.administration`; normal
receipts are under `data.administration`. Raw provider error bodies are omitted.

`--source`, `--push`, `--clone`, `--remote`, `--template`, generic API input,
merge protection changes, deletion, transfer, secrets, user/group permissions,
and CI mutation are not accepted. Local source/push/clone parity remains a
separate workflow, not an atomic side effect hidden in these receipts.

Tests use synthetic TLS servers and isolated CLI processes only. The feature
suite covers configured API/web mappings, single-credential full-operation
binding, required opt-in, no fallback, and native POST/PUT redirect refusal.
The official-package CI job retains `TestPinnedOfficialGlabRepoAdminRedirectEvidence`
as negative evidence of the unchanged dependency, not a claimed upstream fix.
`TestRepoAdminOperationsAreNotDelegated` protects the removed unsafe routes.
Run `go test ./...`, `go test -race ./...`, and `go vet ./...` when validation is
authorized. No production acceptance, installation or complete Windows support
is implied by these tests.
