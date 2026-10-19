#!/bin/sh
# Build-time smoke test of the sandbox image: every tool the runner relies on
# is present and runnable. Runs as the last build step (no network needed).
set -eu
fail=0
check() {
  if "$@" >/dev/null 2>&1; then
    printf 'ok   %s\n' "$*"
  else
    printf 'FAIL %s\n' "$*"
    fail=1
  fi
}
check /usr/local/bin/node --version
check git --version
check git lfs version
check test -x /usr/bin/ssh-keygen
check /usr/bin/gh --version
check /usr/bin/glab --version
check mise --version
check claude --version
check codex --version
check opencode --version
check rg --version
check zstd --version
check tar --zstd --version
check tini --version
check /opt/flow/bin/flow-runner version
for shim in flow-runner flow-git-credential flow-sign gh glab; do
  check test -x "/opt/flow/bin/$shim"
done
if [ -e /home/flow/.config/gh/hosts.yml ] || [ -e /home/flow/.config/glab-cli/config.yml ] || [ -e /home/flow/.flow/cli-env ]; then
  echo 'FAIL credentials must not be baked into the image'
  fail=1
fi
exit "$fail"
