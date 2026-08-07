# Authentication

## Noninteractive sources

Credential resolution checks these environment names:

1. `GLAB_AXI_TOKEN`
2. `GITLAB_TOKEN`
3. `GITLAB_ACCESS_TOKEN`
4. `OAUTH_TOKEN`
5. the host/API-scoped OS keyring item

Empty values are ignored. If populated environment variables disagree, the
command fails rather than choosing an authority silently. `OAUTH_TOKEN` uses a
Bearer header; other sources use `PRIVATE-TOKEN`. Tokens are never accepted as
argv flags, URL/query values, config fields, or interactive input.

A project access token with project role Developer and scope `api` is the
recommended minimum for the complete lookup/create/update/MR/CI surface.
Read-only commands can use narrower authority, but `read_api` cannot create or
update MRs. SSH push authorization is unrelated to REST API authorization.

## Import

```sh
printf '%s\n' "$TOKEN" | glab-axi auth import \
  --host gitlab.example.invalid \
  --api-base https://gitlab.example.invalid/api/v4 \
  --web-base https://gitlab.example.invalid \
  --token-stdin
```

The command:

1. refuses character-device stdin;
2. reads one bounded printable token;
3. validates it with `GET /user` without displaying identity or token;
4. writes only host metadata to mode-0600 config; and
5. stores the value through the no-UI keyring interface.

On macOS the backend calls the Security framework directly, disables keychain
user interaction, and never places the secret in a child-process argument.
Platforms without a safe compiled keyring backend reject import; environment
credentials remain available. There is no plaintext fallback.

Use `--ca-bundle /absolute/file.pem` for an operator-approved private CA and
`--no-proxy` to disable environment proxy discovery for that host. There is no
insecure TLS option.

## Config

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

Config and its directory must be owned by the current user, private, regular,
and not symlinks. It contains no credential values.

## Operational rule

Do not paste tokens into chat, shell history, process arguments, fixtures, test
source, or config. Provision and validate live credentials only under the
separate authorization governing the target GitLab project.
