# syntax=docker/dockerfile:1

# The image currently deployed (or the last one published). Its web assets and
# the releases it carries are copied into /app/web-previous, so this image also
# serves the content-hashed files pages of earlier releases ask for during a
# rolling update. Unset: nothing is carried.
ARG PREVIOUS_RELEASE_IMAGE=no-previous-release
# How many earlier releases' assets to carry.
ARG CARRY_RELEASES=2

FROM --platform=$BUILDPLATFORM alpine:3.24 AS no-previous-release
RUN mkdir -p /app/web

FROM ${PREVIOUS_RELEASE_IMAGE} AS previous-release

FROM --platform=$BUILDPLATFORM node:26-alpine AS web-build
ARG VITE_PAGED_ISSUES=false
ENV VITE_PAGED_ISSUES=${VITE_PAGED_ISSUES}
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install --global npm@10.9.2 --fetch-retries=5 --fetch-timeout=60000 && \
    npm ci --no-audit --prefer-offline --fetch-retries=5 --fetch-timeout=60000
COPY web/ ./
# The Getting started issues embed the feature-tour GIFs; the build copies them to dist/tour/.
COPY docs/media/*.gif /src/docs/media/
RUN npm run build

# Manifest-driven (dist/asset-manifest.json, web/release-assets.ts): the
# previous image's own release, then the releases it carried, newest first.
FROM web-build AS release-assets
ARG CARRY_RELEASES
COPY --from=previous-release /app /previous
RUN node release-assets.ts carry --current dist --previous /previous --out /out/web-previous --keep "${CARRY_RELEASES}"

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS api-build
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG VCS_REF=unknown
WORKDIR /src/api
COPY api/go.mod api/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    if [ "$TARGETARCH" = "arm" ]; then \
      GOARM="${TARGETVARIANT#v}" CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${VCS_REF}" -o /out/flow-api ./cmd/server; \
    else \
      CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${VCS_REF}" -o /out/flow-api ./cmd/server; \
    fi

FROM --platform=$BUILDPLATFORM alpine:3.24 AS certificates
RUN apk add --no-cache ca-certificates && \
    mkdir -p /out/data/uploads && \
    chown -R 65532:65532 /out/data

FROM scratch

WORKDIR /app

COPY --from=api-build /out/flow-api /app/flow-api
COPY --from=web-build /src/web/dist /app/web
COPY --from=release-assets /out/web-previous /app/web-previous
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certificates --chown=65532:65532 /out/data /app/data

ENV FLOW_DATABASE_DRIVER=sqlite
ENV FLOW_DATABASE_PATH=/app/data/flow.db
ENV FLOW_STORAGE_DRIVER=local
ENV FLOW_STORAGE_LOCAL_PATH=/app/data/uploads
ENV FLOW_STATIC_PATH=/app/web
ENV FLOW_ASSET_PREVIOUS_PATH=/app/web-previous
ENV FLOW_APP_URL=http://127.0.0.1:5173

EXPOSE 8080

USER 65532:65532

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/flow-api", "healthcheck"]

CMD ["/app/flow-api"]
