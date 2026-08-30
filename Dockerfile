# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install --global npm@10.9.2 --fetch-retries=5 --fetch-timeout=60000 && \
    npm ci --no-audit --prefer-offline --fetch-retries=5 --fetch-timeout=60000
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS api-build
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

FROM --platform=$BUILDPLATFORM alpine:3.22 AS certificates
RUN apk add --no-cache ca-certificates && \
    mkdir -p /out/data/uploads && \
    chown -R 65532:65532 /out/data

FROM scratch

WORKDIR /app

COPY --from=api-build /out/flow-api /app/flow-api
COPY --from=web-build /src/web/dist /app/web
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certificates --chown=65532:65532 /out/data /app/data

ENV FLOW_DATABASE_DRIVER=sqlite
ENV FLOW_DATABASE_PATH=/app/data/flow.db
ENV FLOW_STORAGE_DRIVER=local
ENV FLOW_STORAGE_LOCAL_PATH=/app/data/uploads
ENV FLOW_STATIC_PATH=/app/web
ENV FLOW_APP_URL=http://127.0.0.1:5173

EXPOSE 8080

USER 65532:65532

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/app/flow-api", "healthcheck"]

CMD ["/app/flow-api"]
