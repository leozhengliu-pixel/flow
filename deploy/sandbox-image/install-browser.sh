#!/usr/bin/env bash
# Browser layer of the coding-session sandbox image (docs/coding-sessions-design.md §16, §17).
# Owner: track I. The rest of deploy/sandbox-image/ belongs to track C.
#
# Hook contract with track C's Dockerfile:
#   - Runs as root at build time, after Node.js (>= 20, the image ships 22) and npm are installed and
#     after the non-root user `flow` (uid 1000) exists (if the user does not exist yet, the smoke test
#     falls back to setpriv with uid/gid 1000):
#       COPY install-browser.sh /tmp/install-browser.sh
#       RUN /tmp/install-browser.sh && rm /tmp/install-browser.sh
#   - Root is needed only for the apt packages Chromium links against and for fonts. The browser only
#     ever runs as uid 1000; everything installed here is root-owned and read-only for that user.
#   - Results:
#       /opt/flow/playwright   pinned `playwright` npm package (the runner imports
#                              /opt/flow/playwright/node_modules/playwright/index.mjs)
#       /opt/ms-playwright     Chromium, Chromium headless shell and ffmpeg (video recording)
#       /etc/profile.d/flow-playwright.sh  exports PLAYWRIGHT_BROWSERS_PATH and FLOW_PLAYWRIGHT_MODULE
#     The Dockerfile should also set `ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright` so non-login
#     processes (the runner) see it; the runner defaults to that path anyway.
#   - Idempotent: running it again reinstalls the same pinned versions.
set -euo pipefail

PLAYWRIGHT_VERSION="1.49.1"
PLAYWRIGHT_PREFIX="/opt/flow/playwright"
PLAYWRIGHT_BROWSERS_PATH="/opt/ms-playwright"
SANDBOX_UID="1000"
SANDBOX_GID="1000"
export PLAYWRIGHT_BROWSERS_PATH
export DEBIAN_FRONTEND=noninteractive
# Never let the npm package download browsers on its own; the explicit install below does it.
export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

log() { printf 'install-browser: %s\n' "$*" >&2; }
fail() { log "error: $*"; exit 1; }

[ "$(id -u)" = "0" ] || fail "must run as root at image build time (apt packages and /opt)"
command -v node >/dev/null 2>&1 || fail "node is not installed; install Node.js 22 before running this script"
command -v npm >/dev/null 2>&1 || fail "npm is not installed; install Node.js 22 before running this script"
command -v npx >/dev/null 2>&1 || fail "npx is not installed"
node_major="$(node -p 'process.versions.node.split(".")[0]')"
[ "${node_major}" -ge 20 ] || fail "Node.js >= 20 is required (found $(node --version))"

log "installing fonts"
apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates \
  fonts-dejavu-core \
  fonts-liberation \
  fonts-noto-cjk \
  fonts-noto-color-emoji \
  fontconfig
fc-cache -f >/dev/null

log "installing playwright@${PLAYWRIGHT_VERSION} into ${PLAYWRIGHT_PREFIX}"
mkdir -p "${PLAYWRIGHT_PREFIX}" "${PLAYWRIGHT_BROWSERS_PATH}"
cat >"${PLAYWRIGHT_PREFIX}/package.json" <<JSON
{
  "name": "flow-sandbox-playwright",
  "private": true,
  "dependencies": {
    "playwright": "${PLAYWRIGHT_VERSION}"
  }
}
JSON
npm install --prefix "${PLAYWRIGHT_PREFIX}" --no-audit --no-fund --omit=dev --ignore-scripts --save-exact \
  "playwright@${PLAYWRIGHT_VERSION}"
installed="$(node -p "require('${PLAYWRIGHT_PREFIX}/node_modules/playwright/package.json').version")"
[ "${installed}" = "${PLAYWRIGHT_VERSION}" ] || fail "expected playwright ${PLAYWRIGHT_VERSION}, got ${installed}"

log "installing Chromium, its system dependencies and ffmpeg into ${PLAYWRIGHT_BROWSERS_PATH}"
# Use the CLI of the pinned package so browser builds match the library version exactly.
(
  cd "${PLAYWRIGHT_PREFIX}"
  unset PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD
  node node_modules/playwright/cli.js install --with-deps chromium ffmpeg
)
apt-get clean
rm -rf /var/lib/apt/lists/*

log "making the install read-only for the sandbox user"
chown -R root:root "${PLAYWRIGHT_PREFIX}" "${PLAYWRIGHT_BROWSERS_PATH}"
chmod -R u+rwX,go+rX,go-w "${PLAYWRIGHT_PREFIX}" "${PLAYWRIGHT_BROWSERS_PATH}"

cat >/etc/profile.d/flow-playwright.sh <<EOF
export PLAYWRIGHT_BROWSERS_PATH=${PLAYWRIGHT_BROWSERS_PATH}
export FLOW_PLAYWRIGHT_MODULE=${PLAYWRIGHT_PREFIX}/node_modules/playwright/index.mjs
EOF
chmod 0644 /etc/profile.d/flow-playwright.sh

# FLOW_BROWSER_SMOKE_TEST=skip skips the launch check: multi-arch builds set
# it for emulated (QEMU) targets, where Chromium cannot start reliably. The
# native build of the same Dockerfile still runs it.
if [ "${FLOW_BROWSER_SMOKE_TEST:-run}" = "skip" ]; then
  log "smoke test skipped (FLOW_BROWSER_SMOKE_TEST=skip, emulated build)"
else
  log "smoke test: headless Chromium as uid ${SANDBOX_UID}"
  smoke_dir="$(mktemp -d)"
  chmod 0777 "${smoke_dir}"
  cat >"${smoke_dir}/smoke.mjs" <<EOF
  import { chromium } from '${PLAYWRIGHT_PREFIX}/node_modules/playwright/index.mjs';
  // chromiumSandbox: false matches the runner: the Pod (non-root, no capabilities, seccomp, network
  // policy) is the sandbox boundary, and Chromium's own sandbox cannot start without user namespaces.
  const browser = await chromium.launch({ headless: true, chromiumSandbox: false, args: ['--disable-dev-shm-usage'] });
  try {
    const context = await browser.newContext({ recordVideo: { dir: '${smoke_dir}/video', size: { width: 320, height: 240 } } });
    const page = await context.newPage();
    await page.setContent('<h1>ok 中文 ✓</h1>');
    await page.screenshot({ path: '${smoke_dir}/shot.png' });
    await context.close();
  } finally {
    await browser.close();
  }
EOF
  chmod 0644 "${smoke_dir}/smoke.mjs"
  run_as_sandbox_user() {
    if id -u flow >/dev/null 2>&1 && [ "$(id -u flow)" = "${SANDBOX_UID}" ] && command -v runuser >/dev/null 2>&1; then
      runuser -u flow -- env HOME="${smoke_dir}" PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH}" "$@"
    elif command -v setpriv >/dev/null 2>&1; then
      setpriv --reuid="${SANDBOX_UID}" --regid="${SANDBOX_GID}" --clear-groups \
        env HOME="${smoke_dir}" PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH}" "$@"
    else
      fail "neither runuser (with user flow) nor setpriv is available for the non-root smoke test"
    fi
  }
  run_as_sandbox_user node "${smoke_dir}/smoke.mjs"
  [ -s "${smoke_dir}/shot.png" ] || fail "smoke test did not produce a screenshot"
  ls "${smoke_dir}"/video/*.webm >/dev/null 2>&1 || fail "smoke test did not produce a video (ffmpeg missing?)"
  rm -rf "${smoke_dir}"
fi

log "done: playwright ${PLAYWRIGHT_VERSION}, browsers in ${PLAYWRIGHT_BROWSERS_PATH}"
