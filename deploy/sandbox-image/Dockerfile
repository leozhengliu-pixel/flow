# syntax=docker/dockerfile:1
#
# Flow coding sandbox image (docs/coding-sessions-design.md §16, track C).
# Build from the repository root:
#   docker build -f deploy/sandbox-image/Dockerfile -t flow-sandbox .
#
# No credentials or agent configuration are baked in: model traffic goes to
# Flow's LLM proxy with the per-sandbox token, git/gh/glab get short-lived
# tokens from Flow at runtime, and commits are signed by Flow (/sign).
# Every download is pinned and checked against a SHA-256.

# ── runner bundle (architecture independent) ────────────────────────────────
FROM --platform=$BUILDPLATFORM node:22.23.3-bookworm-slim AS runner-build
WORKDIR /src/sandbox-runner
COPY sandbox-runner/package.json sandbox-runner/package-lock.json sandbox-runner/.npmrc ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund --fetch-retries=5 --fetch-timeout=60000
COPY sandbox-runner/ ./
RUN npm run typecheck && npm run build

# ── sandbox image ────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

ARG TARGETARCH
# Pinned tool versions (bump together with the recorded fixtures in
# sandbox-runner/test/fixtures; see docs/coding-sessions-design.md §25.4).
ARG NODE_VERSION=22.23.3
ARG NODE_SHA256_AMD64=df450af89261115ef9f9e3830c3eeb2cc9213b63c720b1af623cb5dcbe2e02de
ARG NODE_SHA256_ARM64=a44aeb94849a299b22df10b9e622ec2f605c2183501bc40590705131de7c740f
ARG MISE_VERSION=2026.9.0
ARG MISE_SHA256_AMD64=c9b089f2be1db4d4262eacaf367e480e7a328848adcc37d9d1612996902485d0
ARG MISE_SHA256_ARM64=0b5c4586353010378d9caca62c95166588c3dc4e310eb2d276aac1068397faec
ARG GH_VERSION=2.101.0
ARG GH_SHA256_AMD64=f876a3b87bf67c94f773d17becca4dc7340b056dab901473a9260ee2a73e237b
ARG GH_SHA256_ARM64=9aec87f9a011b1521556b06cb003776e7e214144c8efd2144924a28d90c23057
ARG GLAB_VERSION=1.119.0
ARG GLAB_SHA256_AMD64=95a9ca105518dec7f70ee403d98501402a5fda154c406f4f4cd6696928d3a098
ARG GLAB_SHA256_ARM64=70987a8bab6970d0d042d73d0f06b9d56ecb9abb88c7137c742f1c11b19350e0
ARG CLAUDE_CODE_VERSION=2.1.289
ARG CODEX_VERSION=0.160.0
ARG OPENCODE_VERSION=1.18.34

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Base packages. Debian bookworm package versions are fixed by the release.
# hadolint ignore=DL3008
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl fd-find git git-lfs jq less make \
      openssh-client procps python3 python3-pip python3-venv ripgrep tar tini \
      unzip xz-utils zstd && \
    ln -s /usr/bin/fdfind /usr/local/bin/fd && \
    rm -rf /var/lib/apt/lists/*

# Node.js (runner and agent CLIs), mise, gh and glab from pinned releases.
# The temporary directory is created and removed within this one layer.
# hadolint ignore=DL3003
RUN case "${TARGETARCH}" in \
      amd64) node_arch=x64; node_sha="${NODE_SHA256_AMD64}"; mise_arch=x64; mise_sha="${MISE_SHA256_AMD64}"; gh_sha="${GH_SHA256_AMD64}"; glab_sha="${GLAB_SHA256_AMD64}" ;; \
      arm64) node_arch=arm64; node_sha="${NODE_SHA256_ARM64}"; mise_arch=arm64; mise_sha="${MISE_SHA256_ARM64}"; gh_sha="${GH_SHA256_ARM64}"; glab_sha="${GLAB_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    tmp="$(mktemp -d)" && cd "${tmp}" && \
    curl -fsSLo node.tar.xz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" && \
    echo "${node_sha}  node.tar.xz" | sha256sum -c - && \
    tar -xJf node.tar.xz -C /usr/local --strip-components=1 --no-same-owner \
      --exclude='*/CHANGELOG.md' --exclude='*/README.md' --exclude='*/LICENSE' && \
    curl -fsSLo mise "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-${mise_arch}" && \
    echo "${mise_sha}  mise" | sha256sum -c - && \
    install -m 0755 mise /usr/local/bin/mise && \
    curl -fsSLo gh.deb "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.deb" && \
    echo "${gh_sha}  gh.deb" | sha256sum -c - && \
    curl -fsSLo glab.deb "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${TARGETARCH}.deb" && \
    echo "${glab_sha}  glab.deb" | sha256sum -c - && \
    dpkg -i gh.deb glab.deb && \
    cd / && rm -rf "${tmp}" && \
    node --version && mise --version && gh --version && glab --version

# Agent CLIs at pinned versions (the runner's harness adapters and fixtures
# are verified against exactly these).
RUN npm install --global --no-audit --no-fund --omit=dev \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@openai/codex@${CODEX_VERSION}" \
      "opencode-ai@${OPENCODE_VERSION}" && \
    npm cache clean --force && \
    claude --version && codex --version && opencode --version

# git defaults for every user; credentials and signing are configured per
# repository by the runner at runtime.
RUN git lfs install --system --skip-repo && \
    git config --system init.defaultBranch main && \
    git config --system advice.detachedHead false && \
    git config --system safe.directory /workspace/repo

# Non-root user (uid/gid 1000, matching the Pod securityContext).
RUN groupadd --gid 1000 flow && \
    useradd --uid 1000 --gid 1000 --create-home --home-dir /home/flow --shell /bin/bash flow && \
    install -d -o 1000 -g 1000 -m 0755 /workspace /opt/flow /opt/flow/bin /opt/flow/runner

COPY --from=runner-build /src/sandbox-runner/dist/flow-runner.mjs /opt/flow/runner/flow-runner.mjs
COPY deploy/sandbox-image/bin/ /opt/flow/bin/

# Browser testing layer (track I owns install-browser.sh). The glob always
# matches verify-image.sh, so the build works before that script lands.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
COPY deploy/sandbox-image/*.sh /opt/flow/image/
RUN chmod 0755 /opt/flow/bin/* /opt/flow/image/*.sh && \
    if [ -f /opt/flow/image/install-browser.sh ]; then /opt/flow/image/install-browser.sh; fi

ENV HOME=/home/flow \
    PATH=/opt/flow/bin:/home/flow/.local/share/mise/shims:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    FLOW_SANDBOX=1 \
    LINEAR_SANDBOX=1 \
    FLOW_TOKEN_FILE=/var/run/flow/token \
    DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    MISE_TRUSTED_CONFIG_PATHS=/workspace/repo \
    LANG=C.UTF-8

USER 1000:1000
WORKDIR /workspace
RUN /opt/flow/image/verify-image.sh

ENTRYPOINT ["tini", "--", "flow-runner"]
CMD ["session"]
