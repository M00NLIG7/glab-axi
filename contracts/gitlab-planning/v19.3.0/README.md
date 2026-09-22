# GitLab-native planning contract

## Pinned evidence and compatibility

Provider baseline: GitLab EE `v19.3.0-ee`, annotated tag
`25979d16d37d05ffbbb34f8ee74d5431664499ae`, peeled source commit
`8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6`.
Official CLI remains `1.112.0` at
`816e3a52411aba73d90237859fdc6ecbc86bd169`.
Consumer: gh-axi `2bffd9a5b60ded64d6c9851683b27a480173a7ee`,
`src/commands/project.ts` and `src/commands/issue.ts`.

`consumer.json` pins the comparison, argv, data schemas, differences and limits.
`queries.json` pins all ten project/group query documents. Adapter tests compare
these exact documents and typed variable argv. Transport is the official
CLI's fixed `api graphql --method POST`, not a new credential path, arbitrary
GraphQL command, or direct HTTP client. POST is query transport; the board-issue
operation nevertheless has a real provider ordering side effect described below.

19.3 is the **schema baseline**, not an assertion about an unqueried server's
installed version. Compatible schemas can work on other releases, but there is
no blanket historical/future version guarantee. Unsupported schemas and
inaccessible/tier-gated resources produce typed errors, not stub success.

## Comparison and material differences

| Reference | Native contract | Differences and residuals |
|---|---|---|
| project list | board list | Project or group issue boards, explicit full path; not repositories or inferred GitHub owners. No board closed/archive lifecycle. |
| project view | board view | Core board metadata and bounded column/list definitions, including open/closed lists. Advanced board scope filters are applied by GitLab but not projected as editable field values. |
| project item-list | board issues | Requires exact board/list, scope, explicit host and per-invocation ordering acknowledgment. Provider applies board and list filters. Issues can occur in multiple lists; there is no independent membership object, draft item or board-item archive. This is not a pure-read equivalent. |
| project field-list | work-item fields | Fixed identity/state/type fields plus visible widget types for an exact work item. Widgets are type/tier-specific. CUSTOM_FIELDS can exist in GitLab EE; its presence is reported, but arbitrary custom-field definitions and values are not exposed by this increment. Do not count full custom-field parity. |
| issue subissue list | work-item hierarchy | Parent plus bounded authorized direct children from HIERARCHY. All states, depth exactly one. Not ordinary issue links, not a recursively complete tree. |

Group paths are distinct from project paths. `namespace(fullPath).workItem(iid)`
selects the item directly in that namespace, not a descendant project's equal
IID. Returned global IDs, full paths, URLs and IIDs are checked. Group board
issues must belong to a descendant project, never merely a matching suffix.
Cross-project/group hierarchy nodes retain their own validated namespace and
project identity on the same host.

Board entries and work items retain the provider's `workItemType.name`, such as
`Issue` or `Incident`. The board entry's `Issue` global ID identifies its model,
not its work-item type. Missing or malformed type names fail closed.

Completeness describes the provider-authorized connection only. An inaccessible
parent or hidden-only children are not reported as absent. Even a complete
visible child page cannot prove that additional inaccessible children do not
exist. Missing widgets and null connections are unavailable, not empty arrays.
Duplicate IDs, duplicate scoped IIDs, observed direct cycles, repeated/missing
cursors and identity changes fail closed. No recursive cycle-detection claim is
made beyond the returned parent/direct-child neighborhood. Pagination is not a
transactional snapshot under concurrent provider changes.

For planning results, `meta.complete` describes enumeration of the authorized
collection or field inventory. Text truncation independently sets
`meta.truncated`, so a complete collection can still contain shortened text.
The reason is `field_limit` unless a display or page limit also applies. Those
collection limits set `meta.complete` to false; consumers must inspect both flags.

## Explicit ordering opt-in

The GraphQL `BoardList.issues` field is **not mutation-free** at this baseline.
Its field extension calls
`Boards::Issues::ListService.initialize_relative_positions` after resolving
nodes. When the database is writable and the board is enabled, that method calls
`Issue.move_nulls_to_end(issues)`. Missing relative positions can be initialized;
when space is tight the relative-positioning implementation can shift existing
siblings too. Display-limit probes and siblings beyond the displayed issues can
therefore be affected. The operation does not provide a changed-record receipt,
expected-revision precondition, or transactional rollback.

`board issues` requires `--allow-ordering-initialization` on every invocation,
explicit `--hostname`, one exact `-R PROJECT` or `--group GROUP`, positive board
ID and positive `--list-id`. Both parser and adapter guard the acknowledgment.
Without it there is no child/provider work. `board list`, `board view` and
work-item commands cannot accept that flag and never select board issues.

A success includes `ordering`; a failure after delegation includes
`error.receipt.board_ordering`. Both identify the selected host/scope/board/list,
pinned provider schema/tier baseline, acknowledged possible effect, delegation
attempt count, and `may_have_occurred`. This is uncertainty disclosure, not a
claim that positions changed. Errors never automatically retry. No rollback or
reconciliation query is sent. A delegation attempt does not prove an HTTP
request reached GitLab (dependency failures can occur first), so the count is
intentionally named `delegate_attempts`.

Exact-revision side-effect sources:

- [BoardList type](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/board_list_type.rb)
- [IssuesConnectionExtension](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/lib/gitlab/graphql/board/issues_connection_extension.rb)
- [Issues ListService](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/services/boards/issues/list_service.rb)
- [RelativePositioning](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/models/concerns/relative_positioning.rb)

## Schema, scope and tier sources

- [Project boards docs](https://docs.gitlab.com/api/boards/) and
  [group boards docs](https://docs.gitlab.com/api/group_boards/): Free, Premium
  and Ultimate; advanced scope/list features and multiple group boards depend
  on tier. Their REST list APIs omit open/closed lists; this implementation uses
  the pinned GraphQL list connection instead.
- [Board type](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/board_type.rb),
  [BoardResolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/board_resolver.rb),
  [BoardsResolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/boards_resolver.rb),
  [BoardListsResolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/board_lists_resolver.rb):
  typed IDs and resource-parent binding; list reads explicitly disable creation
  of default lists.
- [BoardListIssuesResolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/board_list_issues_resolver.rb),
  [base list service](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/services/boards/base_items_list_service.rb),
  [EE list filters](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/ee/app/services/ee/boards/issues/list_service.rb):
  list/board selection and tier-dependent assignee/milestone/iteration/status
  columns remain provider-owned, not rewritten as guessed label membership.
- [Board issue type filter](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/services/boards/issues/list_service.rb),
  [incident definition](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/models/work_items/types_framework/system_defined/definitions/incident.rb),
  and [Issue type](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/issue_type.rb):
  incidents are board-eligible; `workItemType.name` preserves their provider type.
- [Namespace work-item resolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/namespaces/work_item_resolver.rb):
  exact namespace IID, with group lookups gated by the `epics` licensed feature.
  A null response cannot distinguish absence, denial and entitlement, so the
  typed `not_found` message explicitly lists those alternatives.
- [WorkItem type](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/work_item_type.rb),
  [widget interface](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/work_items/widget_interface.rb),
  [EE widgets](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/ee/app/graphql/ee/types/work_items/widget_interface.rb):
  fixed selected identity fields and visible widget inventory; no arbitrary
  GraphQL fields or custom-field editing authority.
- [Hierarchy widget](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/work_items/widgets/hierarchy_type.rb),
  [children resolver](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/work_items/children_resolver.rb),
  [hierarchy authorization](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/resolvers/work_items/hierarchy_resolver.rb):
  parent and connection of authorized direct children; no state filter means
  all states. Missing hierarchy widget is typed `unsupported`.
- [WorkItemState](https://gitlab.com/gitlab-org/gitlab/-/blob/8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6/app/graphql/types/work_item_state_enum.rb)
  returns OPEN/CLOSED, normalized to opened/closed without changing semantics.
- [Official glab API implementation](https://gitlab.com/gitlab-org/cli/-/blob/816e3a52411aba73d90237859fdc6ecbc86bd169/internal/commands/api/api.go)
  and `contracts/official-glab/v1.112.0/help.txt`: `query` is a fixed raw field;
  all other raw fields are string variables, and only bounded `first` uses
  numeric `--field`. No magic file/placeholder interpretation reaches caller
  strings, and official `--paginate` is never used.
  GraphQL error JSON is written to stdout before a nonzero exit. The adapter
  classifies bounded UTF-8 error documents before discarding failed-child output;
  the same extension-code classifier handles successful child exits. Provider
  messages and partial data are never emitted, and errors do not trigger retries.

## Validation ownership

`go test ./internal/delegate/glab -run TestPlanning` covers exact query/argv
fixtures and rejects malformed typed requests before execution.
`go test ./internal/product -run 'TestPlanning|TestBoard'` covers public CLI
normalization, scope confusion, null/absent/denied/tier-gated outcomes, bounds,
UTF-8 truncation, pagination/duplicates/cycles, cancellation and the ordering
consent/receipt boundary. Executable tests build both canonical and alias CLIs
and run only synthetic child-process protocol fixtures in isolated homes.
No live GitLab query, real credential, or production ordering change is used.

`go run ./cmd/gen-product` owns generated help, skills and planning schemas.
Full tests/race/vet and genuine CI remain release gates, not claims inferred
from this evidence fixture.
