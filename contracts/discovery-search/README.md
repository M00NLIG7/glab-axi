# Discovery and search read contract

`v1.json` pins this slice to gh-axi revision
`2bffd9a5b60ded64d6c9851683b27a480173a7ee`, official glab 1.112.0,
client-go v2.53.0, and GitLab's v18.3.0 search route/state evidence.
The fixture contains exact source URLs and content digests, routes, field
allowlists, accepted selectors, and residuals. No live account data is evidence.

## Supported behavior

- `repo list [USER]` selects a **user**, never an inferred group.
  `--group FULL_PATH` selects a group, including nested GitLab namespaces.
  Group discovery excludes shared projects and includes descendants only with
  `--include-subgroups`. The returned namespace kind, path, ID, and repository
  identity must agree with the selector. Group identity is checked before listing.
- `--visibility public|internal|private` and `--archived` select
  provider visibility and archived projects. Unfiltered listing retains
  the existing official-profile command; filtered listing retains its owned-project
  default with `owned=true` before pagination. Explicit user and group selectors
  retain their selected namespace scope.
- `--language LANGUAGE` on host/user repository discovery maps only to
  `with_programming_language`: **uses this language**, not GitHub primary language.
  The group-project route has no pinned language filter and rejects it.
  Language names accept punctuation within 64 bytes of valid, non-control UTF-8
  text and are query-encoded. Digit-leading usernames are accepted; numeric IDs
  remain ambiguous and are rejected.
- Issue/MR search defaults to the existing project scope. Explicit `--scope host`
  ignores checkout context; `--group FULL_PATH` selects a group and descendants.
  These cannot be combined with a repository selector or each other. Search
  results carry positive project IDs checked against canonical project metadata;
  resource URLs must agree with host, project, type, and IID. Shared projects
  outside the selected group fail closed, rather than being silently dropped.
- Issue/MR `--state opened|closed|all` (MR also `merged`) maps to the search API
  state selector. Issue/MR/repository `--sort created` means `created_at` descending.
  Repository search supports group search and explicit user ownership. User or
  language selection uses the project-discovery search route, not a rewritten
  GitHub query. The native query remains required.
  Without user/language selectors, created sorting uses project-list routes
  with `order_by=created_at&sort=desc` before pagination, `archived=false`, and
  `search_namespaces=true` to retain basic project-search matching. Group searches
  use `include_subgroups=true&with_shared=false`. GitLab v18.3.0's
  `API::Helpers#project_finder_params_ce` passes namespace matching through to
  both project finders, including the group route; the fixture pins this behavior.
  The project-list route applies a three-character minimum for partial matching
  that basic search does not. This mapping rejects query terms shorter than three
  characters rather than changing their matching behavior. Standalone double-quoted
  phrases count as single terms under GitLab's pinned term rules: `"go cli"` is
  accepted, while `go cli` and `"go" cli` contain a short term and are rejected.
  Unsorted search remains available. The original query is sent unchanged.
- Returned URL authorities compare case-insensitively; project, group, and
  resource paths remain exact, including resource type and IID.

## Exact residuals, not equivalence claims

Search API evidence does **not** establish GitHub search label, assignee, author,
draft, review, stars-comparison, code-language, or arbitrary sort equivalence.
Those flags are rejected before child work. In particular commit author login
and author-date/committer-date sorting are not mapped to commit text search.
Commit/code search stays project-scoped; global/group variants require advanced
search/tier and additional identity contracts, and are not exposed in this slice.
Filter-only searches and a GitHub qualifier interpreter are not added. Existing
positional search text is GitLab-native, not a new raw API/query authority.

GitLab search availability depends on server configuration, edition, index, and
permissions. Forbidden, disabled-search, unavailable-tier, malformed-result,
rate-limit, and upstream errors remain structured failures with no scope fallback
and no fabricated empty success. Local protocol tests prove adapter behavior,
not availability on a live GitLab deployment.

## Executable evidence and bounds

- `internal/product/discovery_e2e_test.go` builds **both** executable names and
  exercises accepted selectors, unsupported/duplicate/malformed inputs before
  child execution, exact argv, nested namespaces, wrong owner/group/host/project,
  authority/path checks, default ownership before bounded results, quoted search
  terms, created ordering, retained filters across pages, page/display/field
  limits, 2 MiB page and 8 MiB total bounds, and controlled upstream errors.
- `internal/product/discovery_test.go` exercises public `Run` cancellation,
  inherited read deadlines, and the reproduced wrong-project repo-view regression.
- `internal/delegate/glab/discovery_test.go` checks closed request builders and
  executes the pinned official binary against isolated TLS fixtures. Supply
  `GL_AXI_OFFICIAL_GLAB_TEST_BINARY` to run the optional official-package tests.
  Test-only `ca_cert` configuration works on Darwin without touching system trust.

Lists retain the 30-item default, 1..1000 requested limit, stable <=100-item
pages, <=10 pages, 30-second operation deadline, and truthful field/display/page
truncation. Discovery and scoped-search identity lookups share the same 8 MiB
operation budget as the result pages. There is no unbounded output option, new
credential store, provider write, native-v1 change, or release/install claim.
