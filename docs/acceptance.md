# Activation acceptance

The repository's automated fake/offline gates are necessary but do not
authorize replacing installed v0.1.0. Activation requires both tests below and
separate captain approval. Record release hash, operator, host/project, and
results privately; never put credentials or private provider data in a PR.

## Real human onboarding

Use a fresh disposable OS account/profile and captain-designated disposable
GitLab project (not Rune).

1. Obtain the signed release through the documented channel; compare its public
   key to the independently captain-pinned value, then verify the detached
   checksum signature, checksum, version, and one-file artifact.
2. Before auth, run top/parent/leaf help and dashboard. Missing context,
   official-glab dependency, and auth must produce actionable controlled errors
   without reading a credential or opening a browser.
3. Install checksum-pinned official `glab` 1.112.0 through an official channel.
4. In a real terminal, run `glab-axi auth login --hostname <test-host>` as the
   human. Do not drive this through an agent. Confirm a prompt may remain open
   beyond 30 seconds and that an explicit interrupt cancels it promptly.
5. Confirm the OS secure store is used. If unavailable, the AXI must abort before
   token acquisition; no plaintext official config is permitted. Repeat with an
   ambient synthetic token sentinel and prove login strips it before the child.
6. Confirm no token appears in argv, shell history, chat, logs, config output,
   AXI output, or test evidence.
7. Run auth status, dashboard, issue list/view, MR list/view/checks/diff,
   pipeline list/view, job list/view/trace, release list/view, repo list/view,
   label list, and bounded search against synthetic non-secret data.
8. Repeat target selection inside the checkout and with
   `-R namespace/project --hostname host`; authority must match. A self-managed
   checkout without explicit hostname/environment authority must fail before
   official-glab execution.
9. In the disposable project only, run MR ensure twice. The second invocation
   must replay/update the exact MR and never create a duplicate.
10. Run denied generic API, merge/comment/close, pipeline retry, repo create,
    release create, and label delete commands while auditing child/network
    execution; each exits 2 with no child/request.
11. Validate TOON and JSON against `glab-axi/ux-v1`, exits, no ANSI/pager/editor,
    completeness metadata, bounded trace/diff, setup idempotence, update check,
    and update refusal/rollback cases.
12. Exercise the supported subset on macOS, Linux, and Windows. Document
    official-login differences rather than hiding them.
13. Revoke/remove disposable credentials through the official human cleanup
    path.

Pass: a new human reaches authenticated, repository-scoped useful reads and
idempotent MR ensure without manually discovering API bases on the default host,
without an agent handling interactive credentials, and without plaintext
fallback.

## Safe read-only Rune MR/CI

This requires separate captain authorization for private metadata access and a
provisioned read credential. Do not create/update an MR, trigger/retry/cancel a
pipeline, alter Git/remotes, fetch new private source, or use a no-mistakes run
whose PR step can call `mr ensure`.

Preconditions:

- release-candidate hash and one-file custody hash recorded;
- Rune worktree clean and remotes recorded read-only;
- existing MR IID and expected host/API/web/project supplied out of band;
- credential injected from approved keyring or ephemeral environment, never
  argv/chat;
- private output destination and redaction review;
- trace omitted unless a synthetic/non-secret job is designated.

Allowed native commands:

```text
glab-axi auth status --host H --repo P --format json
glab-axi mr view IID --host H --repo P --format json
glab-axi ci status --mr IID --host H --repo P --format json
# ci jobs only for the returned pipeline ID if needed
# ci trace only for a pre-cleared synthetic/non-secret job
```

Assertions:

- network audit observes GET only;
- project/MR URLs match exact configured authority/project;
- IID, source/target, and head SHA match the known commit;
- head-pipeline SHA matches MR SHA; stale/mismatch is never green;
- every jobs page is accounted for and manual/allowed-failure states normalize
  correctly;
- JSON validates against `glab-axi/v1` (and product reads, if separately
  approved, against `glab-axi/ux-v1`);
- token/header/cookie/proxy sentinels are absent from argv/stdout/stderr/logs;
- no config, keyring, Git, remote, MR, pipeline, or repository state changes.

Pass: the release candidate represents the existing Rune MR/CI state with only
GET requests, exact authority, complete fail-closed CI, and no secret exposure.
