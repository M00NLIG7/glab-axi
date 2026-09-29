# Issue and MR read selection

This increment compares source/help/tests at
[`gh-axi` 2bffd9a5b60ded64d6c9851683b27a480173a7ee](https://github.com/kunchenguid/gh-axi/tree/2bffd9a5b60ded64d6c9851683b27a480173a7ee),
not an installed package. It improves existing issue/MR list/view commands; it
is not a complete forge-parity claim. The executable conformance vectors are in
`contracts/read-parity/gh-axi-2bffd9a5b60ded64d6c9851683b27a480173a7ee.json`.

## Typed list filters

Both `issue list` and `mr list` accept:

| Flag | Contract |
| --- | --- |
| `--state` | `open` (default; returned state remains `opened`), `closed`, `all`; MR also `merged`. GitLab `closed` excludes merged MRs. |
| `--label NAME` | Repeat up to 20 distinct exact names, requiring every label (AND). Commas, double quotes/backslashes, control characters, padded names, leading dashes, and provider selector keywords are rejected rather than reinterpreted. |
| `--author USERNAME` | One explicit username, not `@me` or an expression. |
| `--assignee USERNAME` | One explicit username, not `@me` or a comma-separated set. |

Issues additionally accept `--milestone TITLE` for one exact title. Provider
selectors (`None`, `Any`, `Upcoming`, `Started`, `#upcoming`, `#started`,
`No Milestone`, `Any Milestone`) are rejected case-insensitively before dependency
or network work. MRs do not accept a milestone filter.

Issues also accept `--sort created|updated` in descending order. The
pinned provider does not establish a comment-count sort; popularity is not an
alias for comment count.

MRs additionally accept `--source-branch BRANCH`, `--target-branch BRANCH`, and
`--draft` to select draft MRs. Omitting `--draft` includes both draft and
non-draft MRs. Branches must be valid Git branch names. Returned MRs must match selected branches.

Value-taking flags accept space or equals forms. `--draft` is a bare boolean
flag and accepts no value, including `=true` or `=false`. Duplicate singleton
flags, empty values, unknown flags, and ambiguous input fail before official-glab
child work. These are typed selectors, not raw upstream argv or query-language
passthrough.

```sh
gl-axi issue list -R group/project --state closed --label bug --author alice --sort updated
gl-axi mr list -R group/project --source-branch feature/topic --target-branch main --draft
```

The pinned official client resolves author/assignee usernames with bounded user
GET requests and sends their IDs to the project list. Milestone titles and label
names are encoded as query values. The official client's draft selector uses
GitLab's `wip=yes` query. Exact behavior is tested against a local TLS server
with synthetic credentials, without using a real account or profile.

## Optional fields and descriptions

`--fields FIELD,...` adds declared normalized fields to lists, retaining every
default field. Only `description` changes inclusion; the other supported names
retain their existing conditional output without extra provider reads. Arbitrary
GitLab JSON keys are not accepted, and views do not accept `--fields`. Each
command's local `--help` lists its supported field names.

- Default fields always remain, including `iid`, `title`, `state`,
  `web_url`, and MR source/target branches, draft/conflict/merge status. Selection
  cannot hide an invalid returned identity, URL or branch.
- Selected absent optional values remain omitted, as in the existing resource
  schemas. Selection does not invent unavailable provider facts.
- Lists omit descriptions by default. Select `description` to include them.
- Views include descriptions by default and retain their existing output shape.
- Included descriptions retain the fixed 131072-byte UTF-8 cap. Truncation never
  splits UTF-8 or exceeds that cap, including the truncation marker.

```sh
gl-axi issue list -R group/project --fields description,labels --limit 10 --format json
gl-axi mr view 42 -R group/project --format json
```

No schema widening is needed: these fields are already optional in the closed
`schema/ux-v1/resources.schema.json` contracts. `meta.complete` describes item-set
pagination, while `meta.truncated` also reports field cuts (`field_limit`).
Existing page/display-limit reasons take precedence over field truncation.

## Capability matrix

| Capability | Pinned reference | This increment |
| --- | --- | --- |
| State | `--state open\|closed\|all` | Equivalent issue states; GitLab MR `closed` excludes `merged`, with a separate `--state merged` selector |
| Labels, author, assignee | Repeated labels and usernames | Typed AND labels and explicit usernames; no `@me` selector |
| Issue milestone | `--milestone TITLE` | Exact title; provider-special selectors refused |
| Issue order | `created`, `updated`, `comments` | Descending created/updated; comment-count order remains a gap |
| PR/MR branches | `--base`, `--head` | Native `--target-branch`, `--source-branch`, with returned-branch binding |
| List descriptions | Additive `--fields body` | Additive `--fields description`, capped at 131072 UTF-8 bytes |
| Default view body | Truncated preview | Existing default body shape, capped at 131072 UTF-8 bytes |
| Full view body | `--full` disables body truncation | Not implemented; the fixed safety cap always applies |
| Draft-only PR/MR list | `--draft`; omission is unfiltered | `--draft`; omission is unfiltered |

The fixture's `capabilities` rows execute both real CLI aliases, compare exact
provider argv, and assert repeatable output or a no-provider-work refusal for
residual gaps. Separate tests cover normalized schemas, byte and pagination
bounds, and the actual official client against synthetic TLS. This is
source-pinned conformance, not execution of the reference repository or live
GitLab acceptance.

The reference full-view option remains a parity gap. This increment does not
provide unbounded body output or caller-configurable byte limits.

## Bounds and compatibility

All pages retain identical filters and page width. The display limit remains
1..1000, at most 100 items/page and 10 pages, with a 30-second read deadline.
Shared JSON-page and serialized-output bounds, including overflow and
writer-failure behavior, are owned by the [security model](security.md#hard-limits).
Exact-limit lists probe one further page when necessary; the hard page limit
never claims completeness. There is no unbounded `--full`, arbitrary `--json`, jq,
raw API or new provider mutation.

Host/project/resource identity is checked before rendering, including exact IID
for views. Issue URLs accept only the exact project paths `/-/issues/IID` and
`/-/work_items/IID`; MR URLs accept only `/-/merge_requests/IID`. The existing root-path target contract does not accept a different
nested project merely because its path ends with the requested namespace.

Canonical `gl-axi` and the tested `glab-axi` executable alias share these behaviors.
Frozen native `glab-axi/v1` routing, schemas and consumer contracts are unchanged.
The dashboard keeps its existing preview defaults.

Remaining differences include optional milestone/closed/merged metadata
enrichment, GitHub review-submission history, broader search and other forge workflows.
Issue comment/discussion and MR reviewer/approval reads are covered by the
[collaboration-read contract](../contracts/collaboration-reads/README.md).
Existing issue writes and explicit-native best-effort edits retain their
separate contracts in
`contracts/issue-writes/v1.json` and `contracts/issue-edit/v2.json`.
Distribution, live installation and separate CI repair are not part of this read
increment.
