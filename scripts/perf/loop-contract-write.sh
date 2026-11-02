#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
heavy="$HOME/.flow-local/heavy.sh"
scratch=$(mktemp -d /tmp/codex-loop-contract-write.XXXXXX)
suffix="$(date +%s)-$$"
mysql="flow-codex-loop-contract-write-$suffix-mysql"
postgres="flow-codex-loop-contract-write-$suffix-postgres"
redis="flow-codex-loop-contract-write-$suffix-redis"
cleanup() {
  for container in "$mysql" "$postgres" "$redis"; do
    "$heavy" docker rm -f -v "$container" >>"$scratch/cleanup.log" 2>&1 || true
  done
}
trap cleanup EXIT
printf 'Contract write gate artifacts: %s\n' "$scratch"

# These credentials exist only in this run's throwaway containers.
password=$(openssl rand -hex 16)
"$heavy" docker run -d --name "$mysql" -p 127.0.0.1::3306 \
  -e MYSQL_ROOT_PASSWORD="$password" -e MYSQL_DATABASE=flow_contract \
  -e MYSQL_USER=flow -e MYSQL_PASSWORD="$password" mysql:8.4 \
  --general-log=ON --log-output=TABLE --innodb-buffer-pool-size=128M >"$scratch/mysql-container.log" 2>&1
"$heavy" docker run -d --name "$postgres" -p 127.0.0.1::5432 \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=flow_contract postgres:16-alpine >"$scratch/postgres-container.log" 2>&1
"$heavy" docker run -d --name "$redis" -p 127.0.0.1::6379 redis:7-alpine >"$scratch/redis-container.log" 2>&1
mysql_port=$("$heavy" docker port "$mysql" 3306/tcp | sed 's/.*://')
postgres_port=$("$heavy" docker port "$postgres" 5432/tcp | sed 's/.*://')
redis_port=$("$heavy" docker port "$redis" 6379/tcp | sed 's/.*://')
ready=false
for _ in {1..60}; do
  if "$heavy" docker exec -e MYSQL_PWD="$password" "$mysql" mysql -uroot -Nse 'SELECT 1' >"$scratch/mysql-ready.log" 2>&1; then
    ready=true
    break
  fi
  sleep 2
done
if [[ "$ready" != true ]]; then
  printf 'MySQL did not become ready; see %s\n' "$scratch/mysql-ready.log" >&2
  exit 1
fi

export GOMAXPROCS=2 GOMEMLIMIT=768MiB TMPDIR="$scratch"
export FLOW_TEST_REDIS_URL="redis://127.0.0.1:$redis_port"
export FLOW_TEST_DATABASE_DRIVER=mysql
export FLOW_TEST_DATABASE_URL="flow:$password@tcp(127.0.0.1:$mysql_port)/flow_contract?parseTime=true"
export FLOW_TEST_ADMIN_DATABASE_URL="root:$password@tcp(127.0.0.1:$mysql_port)/flow_contract?parseTime=true"
cd "$root/api"
"$heavy" go test -p 1 -c -o "$scratch/store.test" ./internal/store >"$scratch/store-build.log" 2>&1
"$heavy" go test -p 1 -c -o "$scratch/server.test" ./cmd/server >"$scratch/server-build.log" 2>&1
run_gate() {
  local label=$1
  shift
  if ! "$heavy" "$@" >"$scratch/$label.log" 2>&1; then
    rg 'FAIL|panic|\.go:[0-9]+:' "$scratch/$label.log" | tail -12 >&2 || true
    printf 'Gate failed: %s\n' "$scratch/$label.log" >&2
    return 1
  fi
  rg 'CONTRACT_(SEED|MEASURE|REPLICAS)' "$scratch/$label.log"
}
run_gate seed env FLOW_LOOP_CONTRACT_GATE=seed "$scratch/store.test" -test.run='^TestLoopContractScaleSeed$' -test.v
for mode in full narrow repeat stale; do
  run_gate "$mode" env FLOW_LOOP_CONTRACT_GATE="$mode" "$scratch/store.test" -test.run='^TestLoopContractScaleMeasure$' -test.v
done
export FLOW_LOOP_CONTRACT_REPLICAS=1
run_gate mysql-replicas "$scratch/server.test" -test.run='^TestLoopContractTwoReplicaGate$' -test.v
export FLOW_TEST_DATABASE_DRIVER=postgres
export FLOW_TEST_DATABASE_URL="postgres://postgres:$password@127.0.0.1:$postgres_port/flow_contract?sslmode=disable"
run_gate postgres-replicas "$scratch/server.test" -test.run='^TestLoopContractTwoReplicaGate$' -test.v
export FLOW_TEST_DATABASE_DRIVER=sqlite
export FLOW_TEST_DATABASE_URL="$scratch/replicas.db"
run_gate sqlite-replicas "$scratch/server.test" -test.run='^TestLoopContractTwoReplicaGate$' -test.v
