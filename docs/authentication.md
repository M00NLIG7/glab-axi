# Authentication

`glab-axi` has two intentionally separate authentication lanes. There is no
automatic fallback, import, export, or token copy between them.

## Product lane: official glab profile

A human on a real terminal runs:

```sh
glab-axi auth login --hostname gitlab.com
```

The product accepts only `--hostname`; it does not expose official glab's token,
job-token, stdin, insecure-storage, web, device, API-protocol, or Git-protocol
flags. It delegates the exact argv below only after checking the official
binary/version and secure-store policy:

```text
glab auth login --hostname <validated-host>
```

Official `glab` v1.112.0 normally stores credentials in macOS Keychain, Windows
Credential Manager, or Linux Secret Service, but its documented fallback is a
plaintext config file when no keyring is available (and in CI). `glab-axi`
refuses that fallback:

1. all standard streams must be actual terminal files and pass a terminal
   ioctl/console check (a character device such as `/dev/null` is not
   sufficient);
2. CI/GitLab-CI storage mode and ambient GitLab token/job-token variables are
   removed for the child, so login cannot persist a headless credential;
3. a random, non-secret sentinel is written/deleted through the same
   cross-platform keyring library used by the pinned official release;
4. the child's terminal output is monitored for the exact pinned
   plaintext-fallback warning; and
5. the child is canceled if either secure-store check fails.

Login uses a terminal-backed relay rather than `os/exec` writer pipes. On
macOS/Linux the child inherits the human terminal for stdin and receives a PTY
for stdout/stderr. On Windows it runs in ConPTY while a fixed-buffer input relay
is active. Thus all three child descriptors remain terminals. Combined official
stdout/stderr is validated and relayed immediately to the human terminal on
stderr; stdout remains reserved for the final parseable `glab-axi/ux-v1`
envelope. Monitoring retains only a fixed warning window, rejects malformed or
more than 8 MiB of interactive output, suppresses output after the fallback
warning, and never records terminal input. The human prompt is not subject to
the 30-second noninteractive operation deadline: it runs until completion or
explicit caller/process cancellation, while cancellation and relay shutdown
remain bounded.

The sentinel uses a unique service/account and is not a credential. The probe
does not list or read keyring entries. `glab-axi` never opens official glab's
config, asks official glab to print a token, parses a credential source, or
copies a value into its native store.

A profile configured independently by a human with official `glab` is an
external trust decision. Product reads use it as official glab normally would.
`glab-axi auth status` delegates without `--show-token`, discards all child
text, and returns only normalized authentication state/host/backend metadata.

### Headless product reads

Official glab v1.112.0 gives these approved environment values precedence over
stored profiles:

- `GITLAB_TOKEN`
- `GITLAB_ACCESS_TOKEN`
- `OAUTH_TOKEN`

They remain available to noninteractive delegated reads, but are stripped from
`auth login`. Product data commands close stdin and disable prompts, pager,
editor, browser, debug HTTP, CI auto-login, color, and update checks. Do not put credentials in argv, shell
history, chat, fixtures, or logs.

### OAuth/device and private-host limitations

`glab-axi` intentionally does not promise a universal device flow. Official
glab's browser/device support depends on its pinned release, GitLab server
version (device flow requires GitLab 17.9+), host OAuth configuration, and an
appropriate client identity. Self-managed instances may disable OAuth, use a
relative URL, separate API host, private CA, or proxy.

A human who needs a special official-glab onboarding path may configure the
profile directly outside glab-axi, subject to organizational policy. A
`gitlab.com` checkout may supply product host/repository context; self-managed
checkout hosts require explicit `--hostname` or `GITLAB_HOST` authority. The AXI
will use that selected authenticated host but never expose insecure TLS/storage
flags. Native private-host REST remains separately configured as described
below.

## Native v1 lane: noninteractive credential

Native `glab-axi/v1` resolution checks:

1. `GLAB_AXI_TOKEN`
2. `GITLAB_TOKEN`
3. `GITLAB_ACCESS_TOKEN`
4. `OAUTH_TOKEN`
5. the host/API-scoped glab-axi OS keyring item

Empty values are ignored. If populated environment variables disagree, the
command fails rather than choosing silently. `OAUTH_TOKEN` uses a Bearer header;
other native values use `PRIVATE-TOKEN`. No native token is accepted in argv,
URL/query, config, or interactive input.

A project access token with project role Developer and `api` scope is the
recommended minimum for the full native MR ensure/CI surface. Read-only native
commands can use narrower authority; `read_api` cannot create/update an MR. SSH
push authorization is unrelated to REST API authority.

### Transactional native import

```sh
printf '%s\n' "$TOKEN" | glab-axi auth import \
  --host gitlab.example.invalid \
  --api-base https://gitlab.example.invalid/api/v4 \
  --web-base https://gitlab.example.invalid \
  --token-stdin
```

Import is part of the frozen native grammar, not the primary human onboarding
story. It:

1. refuses character-device stdin;
2. reads one bounded printable token;
3. validates it with native `GET /user` without displaying identity/value;
4. prepares validated non-secret host metadata;
5. records any previous keyring value;
6. stores the credential through the no-UI keyring interface;
7. atomically writes mode-0600 config; and
8. restores/deletes the keyring entry if config installation fails.

A keyring write failure leaves no config. An incomplete rollback is a safety
error rather than false success. On macOS+cgo, native keyring calls the Security
framework directly with authentication UI disabled. Platforms without a proven
no-UI native backend reject persistent import; environment credentials remain
available. There is no plaintext fallback.

## Native host config

Default config is the platform user-config directory under
`glab-axi/config.json`. Tests/automation may select an absolute file with
`GLAB_AXI_CONFIG` or an absolute directory with `GLAB_AXI_CONFIG_DIR`.

```json
{
  "schema": "glab-axi/config-v1",
  "hosts": {
    "gitlab.example.invalid": {
      "git_hosts": ["gitlab.example.invalid", "gitlab-ssh.example.invalid"],
      "api_base": "https://gitlab.example.invalid/gitlab/api/v4",
      "web_base": "https://gitlab.example.invalid/gitlab",
      "ca_bundle": "/absolute/path/company-ca.pem",
      "proxy_disabled": true
    }
  }
}
```

Config and directory must be owned by the current user, private, regular, and
not symlinks. Config contains no credential values. Native HTTPS requires TLS
1.2+, verifies hostnames, supports one explicit private CA bundle and explicit
proxy disable, and has no insecure mode. Product delegation uses the trust/TLS
settings of the human-controlled official profile and identifies its backend in
output; those properties are not falsely attributed to native validation.
