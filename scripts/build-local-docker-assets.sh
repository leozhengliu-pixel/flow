#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_ARCH="$(docker version --format '{{.Server.Arch}}')"

case "$SERVER_ARCH" in
  amd64|x86_64)
    GOARCH_VALUE="amd64"
    ;;
  arm64|aarch64)
    GOARCH_VALUE="arm64"
    ;;
  *)
    echo "Unsupported Docker server architecture: $SERVER_ARCH" >&2
    exit 1
    ;;
esac

cd "$ROOT_DIR/web"
if [ ! -d node_modules ]; then
  npm ci
fi
npm run build

cd "$ROOT_DIR/api"
mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_VALUE" go build -o bin/flow-api ./cmd/server
