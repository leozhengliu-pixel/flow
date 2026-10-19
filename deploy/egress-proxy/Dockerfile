# syntax=docker/dockerfile:1
#
# Flow coding-session egress proxy: Squid with a domain allowlist and the
# `flow-api egress-acl` helper that applies per-environment network modes.
# Build from the repository root:
#
#   docker build -f deploy/egress-proxy/Dockerfile -t flow-egress .
#
# The helper is the Flow API binary itself, built from the same source, so the
# proxy and Flow always speak the same egress-check protocol.

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS helper-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags="-s -w" -o /out/flow-api ./cmd/server

FROM alpine:3.24
# The alpine base tag pins the package set; per-package pins would break on
# every alpine point release.
# hadolint ignore=DL3018
RUN apk add --no-cache squid ca-certificates tini && \
    mkdir -p /etc/flow-egress /var/cache/squid /var/log/squid /var/run/squid && \
    chown -R 1000:1000 /var/cache/squid /var/log/squid /var/run/squid
COPY --from=helper-build /out/flow-api /usr/local/bin/flow-api
COPY deploy/egress-proxy/allowlist.default.txt /etc/flow-egress/allowlist.default.txt
COPY deploy/egress-proxy/entrypoint.sh /usr/local/bin/flow-egress
RUN chmod 0755 /usr/local/bin/flow-egress /usr/local/bin/flow-api

# Squid runs as an unprivileged user and keeps no disk cache, so the container
# works with a read-only root filesystem and a writable /tmp.
USER 1000:1000
EXPOSE 3128
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/flow-egress"]
