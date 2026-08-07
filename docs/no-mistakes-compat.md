# no-mistakes v1.45.4 compatibility

The binary built from `cmd/glab-compat` is normally written to `dist/glab`. Its
name exists only for a pinned consumer boundary. Help and version output state
that it is not upstream `glab`.

## Accepted argv

```text
glab auth status [--hostname HOST]
glab mr list --source-branch B [--target-branch B] --output json
glab mr create --source-branch B --target-branch B --title T --description D --yes
glab mr update IID_OR_URL --title T --description D --yes
glab mr view IID --output json
glab ci status --mr IID --output json
glab api --paginate projects/<encoded-configured-project>/pipelines/<id>/jobs
glab ci get --pipeline-id ID --output json --with-job-details
glab ci trace JOB_ID
```

Flags may be ordered differently, but duplicates, `--flag=value`, extra
positionals, unknown flags, and missing values fail with exit 2. The command
spelled `api` is matched to the configured project and anchored pipeline-jobs
route before the typed jobs method is called. It is not generic REST access.

The compatibility process infers host/project from the local `origin` remote,
then resolves that host through `glab-axi/config-v1`. The auth check with an
explicit hostname does not require a repository.

## Output contract

- auth status: no stdout on success;
- MR list: one JSON array with zero or one `{iid,web_url}` item;
- create/update: one `{iid,web_url}` object;
- view: one bounded object with MR state, URL, conflict/merge status, SHA, and
  head-pipeline metadata;
- CI status/get/API: one normalized JSON job array;
- trace: bounded, redacted raw UTF-8 bytes.

Errors are one controlled, redacted stderr line. Exit codes are documented in
[security](security.md). There are no prompts, pagers, ANSI, update notices, or
terminal-table parsing.

## Consumer limitations

no-mistakes v1.45.4 passes title and description in argv. The adapter cannot
remove that legacy exposure. A future direct `glab-axi/v1` integration must use
private files or stdin and a run-scoped absolute executable path.

The installed shared no-mistakes daemon does not inherit a task caller's PATH.
Do not expose this compatibility executable globally or treat a PATH prefix as
a safe run-scoped integration.

## Contract gate

`contracts/no-mistakes/v1.45.4.json` pins source module, version, commit, argv,
stdin, output shape, exit, and mutation classification. The test builds the
real executable, runs every vector against a TLS fake server, and verifies all
denied commands make zero requests. A no-mistakes upgrade must add a new pinned
contract before code accepts a new command.
