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
| `--state` | `open`/`opened` (default), `closed`, `all`; MR also `merged`. GitLab `closed` excludes merged MRs. |
| `--label NAME` | Repeat up to 20 distinct exact names, requiring every label (AND). Commas, quotes/backslashes, control characters, padded names, leading dashes, and provider selector keywords are rejected rather than reinterpreted. |
| `--author USERNAME` | One explicit username, not `@me` or an expression. |
| `--assignee USERNAME` | One explicit username, not `@me` or a comma-separated set. |
| `--milestone TITLE` | One exact title. Like labels, special provider selectors (`None`, `Any`, `Upcoming`, `Started`) are rejected case-insensitively. |

Issues additionally accept `--sort created|updated` in descending order. The
pinned provider does not establish a comment-count sort; popularity is not an
alias for comment count.

MRs additionally accept `--source-branch BRANCH`, `--target-branch BRANCH`, and
mutually exclusive `--draft` / `--not-draft`. Branches must be valid Git branch
names. Returned MRs must match selected branches.

All flags accept space or equals values. Duplicate singleton flags, empty values,
unknown flags, and ambiguous input fail before official-glab child work. These
are typed selectors, not raw upstream argv or query-language passthrough.

```sh
gl-axi issue list -R group/project --state closed --label bug --author alice --sort updated
gl-axi mr list -R group/project --source-branch feature/topic --target-branch main --not-draft
```

The pinned official client resolves author/assignee usernames with bounded user
GET requests and sends their IDs to the project list. Milestone titles and label
names are encoded as query values. The official client's draft selector uses
GitLab's `wip=yes|no` query. Exact behavior is tested against a local TLS server
with synthetic credentials, without using a real account or profile.

## Optional fields and descriptions

`--fields FIELD,...` selects OPTIONAL normalized fields, not arbitrary GitLab
JSON keys. This differs from the reference's additive `--fields`: explicit
selection projects optional fields while keeping required identity/state fields.
Without this flag the existing default output shape is unchanged.

- Issue optional fields: `description`, `author`, `labels`, `created_at`, `updated_at`.
- MR optional fields: the above plus `base_sha`, `head_sha`, `head_pipeline`, `raw_merge_status`.
- Required identity/state fields always remain, including `iid`, `title`, `state`,
  `web_url`, and MR source/target branches, draft/conflict/merge status. Selection
  cannot hide an invalid returned identity, URL or branch.
- Selected absent optional values remain omitted, as in the existing resource
  schemas. Selection does not invent unavailable provider facts.
- Lists omit descriptions by default. Select `description` to include them.
- Views include descriptions by default. `--fields author,labels` omits the body.
- `--body-limit N` lowers the description cap to 0..131072 UTF-8 bytes. The
  default remains 131072. It requires a selected description (automatic on views
  without `--fields`). Zero omits the body, reporting truncation if nonempty.
  Truncation never splits UTF-8 or exceeds the requested bytes, including markers.

```sh
gl-axi issue list -R group/project --fields description,labels --body-limit 500 --limit 10 --format json
gl-axi mr view 42 -R group/project --fields description,head_sha --body-limit 4096 --format json
```

No schema widening is needed: these fields are already optional in the closed
`schema/ux-v1/resources.schema.json` contracts. `meta.complete` describes item-set
pagination, while `meta.truncated` also reports field cuts (`field_limit`). Explicit
omission through field selection is not truncation. Existing page/display-limit
reasons take precedence over field truncation.

## Bounds and compatibility

All pages retain identical filters and page width. The display limit remains
1..1000, at most 100 items/page and 10 pages, with 2 MiB JSON/page and 8 MiB total
provider output and a 30-second read deadline. Exact-limit lists probe one further
page when necessary; the hard page limit never claims completeness. There is no
unbounded `--full`, arbitrary `--json`, jq, raw API or new provider mutation.

Host/project/resource identity is checked before projection, including exact IID
for views. The existing root-path target contract does not accept a different
nested project merely because its path ends with the requested namespace.

Canonical `gl-axi` and the tested `glab-axi` executable alias share these behaviors.
Frozen native `glab-axi/v1` routing, schemas and consumer contracts are unchanged.
The dashboard keeps its existing preview defaults.

Remaining differences include issue comments, optional milestone/closed/merged
metadata enrichment, GitHub review state, broader search and other forge workflows.
MR discussion evidence already ships as `mr discussions`; this slice neither
reimplements it nor claims approvals are discussions. Existing issue edit remains
validation-only. Distribution, live installation and separate CI repair are not
part of this read increment.
