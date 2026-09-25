# Stack operations deliberately strip GIT_* overrides. Apply test
# isolation at the executable boundary, not in the product guard.
tools="${RUNNER_TEMP}/isolated-git"
mkdir -p "${tools}"
{
  printf '%s\n' '#!/bin/bash' \
    'export GIT_CONFIG_NOSYSTEM=1' \
    'export GIT_CONFIG_GLOBAL=/dev/null'
  printf 'exec %q "$@"\n' "$(command -v git)"
} > "${tools}/git"
chmod +x "${tools}/git"
printf '%s\n' "${tools}" >> "${GITHUB_PATH}"
