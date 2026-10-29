#!/bin/bash
set -euo pipefail
export PATH="$HOME/.nvm/versions/node/v24.7.0/bin:$PATH"
cd "$(dirname "$0")/.."
mkdir -p /tmp/codex-worker-paths
scratch=$(mktemp -d /tmp/codex-worker-paths/session.XXXXXX)
proxy_pid=
cleanup() {
  if [ -n "$proxy_pid" ]; then
    kill "$proxy_pid" 2>/dev/null || true
    wait "$proxy_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM
if ! curl -s --max-time 2 -o /dev/null http://127.0.0.1:8787/v1/models; then
  PORT=8787 node "$HOME/.flow-local/opencode-go-proxy.mjs" >"$scratch/proxy.log" 2>&1 &
  proxy_pid=$!
  sleep 2
fi
export FLOW_AGENT_RUNTIME=worker FLOW_WORKER_REAL_TEST=1 FLOW_WORKER_REAL_SCRATCH="$scratch" TMPDIR="$scratch"
echo "Scratch logs: $scratch"
if [ "${1:-}" = "--verify" ]; then
  (
    cd agent-worker
    npm run typecheck
    npm test
  ) >"$scratch/worker-tests.log" 2>&1
  (
    cd api
    GOMAXPROCS=2 go test -p 1 ./cmd/server -run 'TestWorkerLoop|TestWorkerChat|TestWorkerThread|TestWorkerRunsUseTheWeb|TestLoopRunReplies' -count=1
    GOMAXPROCS=2 go vet -p 1 ./...
  ) >"$scratch/go-checks.log" 2>&1
  node scripts/generate-openapi.mjs --check
  (
    cd sdk/typescript
    node scripts/generate.mjs --check
  )
  echo "Worker tests, typecheck, targeted Go tests and go vet passed"
fi
cd api
if GOMAXPROCS=2 go test -p 1 ./cmd/server -run '^TestWorkerRuntimeRealPaths$' -count=1 -timeout=25m -v >"$scratch/api.log" 2>&1; then
  rg 'agent_worker_real_paths_test.go|PASS|^ok' "$scratch/api.log"
else
  tail -80 "$scratch/api.log"
  exit 1
fi
