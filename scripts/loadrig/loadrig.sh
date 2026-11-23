#!/bin/bash
# Load rig for activating FLOW_BOUNDED_METADATA: seed a realistic workspace, start Flow in a target
# topology with the flag off or on, drive a mixed workload and report flag off vs on.
#
#   scripts/loadrig/loadrig.sh build                      build bin/flow-server and bin/loadrig.test (-tags loadtest)
#   scripts/loadrig/loadrig.sh seed  <sqlite|pg2|mysql2>   seed one workspace (SCALE=1, 10, ...) and keep it as the seed copy
#   scripts/loadrig/loadrig.sh start <sqlite|pg2|mysql2> <default|on|off> start the replicas on a fresh copy of the seed
#   scripts/loadrig/loadrig.sh drive <label>               drive the started replicas (VUS, DURATION, ...)
#   scripts/loadrig/loadrig.sh stop                        stop the replicas (by PID)
#   scripts/loadrig/loadrig.sh pair  <sqlite|pg2|mysql2>   start off, drive, stop, start on, drive, stop, compare
#   scripts/loadrig/loadrig.sh smoke                       build, seed 1x and run a tiny pair on both topologies, then clean up
#   scripts/loadrig/loadrig.sh down                        stop everything the rig started and remove its containers and data
#
# Topologies: sqlite = one replica on SQLite; pg2 = two replicas on PostgreSQL (container loadrig-pg) with Redis
# coordination (container loadrig-redis when the redis:7-alpine image is present, else an in-memory Redis process);
# mysql2 = two replicas on MySQL 8.4 (container loadrig-mysql from the local mysql:8.4 image, 1.5 GiB) with the same
# Redis coordination. Each start copies the seed database into a fresh run database inside the container.
# The historical on/off profiles pin hydration, journaling and sync v2.
# The default profile leaves feature flags unset in the replica environment.
#
# Settings (environment): LOADRIG_HOME (~/.flow-local/live/loadrig), LOADRIG_PORT_BASE (47610), SCALE (1), VUS (10), DURATION (60s), WARMUP (5s),
# LOADRIG_API_PORT (port base, +1 for the second replica), LOADRIG_METRIC_PORT (port base+10), THINK (300ms), STREAMS (VUS/2), MIX (op=weight,...: only the listed ops run; a leading "default," keeps the other default weights), MODEL (fake|proxy|off; fake = local fake model, proxy = the
# shared 127.0.0.1:8787 proxy, deepseek-v4.1-flash), AGENT_EVERY (20s), AGENT_MAX (0 = no cap; proxy defaults to 6),
# FLOW_LOADRIG_SEARCH_QUERY (optional fixed search URL query), FLOW_LOADRIG_SYNC_REPLAY_AFTER (optional fixed cursor for single-transaction sync reads),
# FLOW_LOADRIG_ROTATE_READS (on rotates reads across replicas),
# LOADRIG_UPGRADE_SEED (1 creates the seed with the old defaults and no catalog coverage),
# GOMAXPROCS_REPLICA (4), GOMEMLIMIT_REPLICA (1536MiB), MAX_LOAD (20: refuse to start above this load average),
# LOADRIG_MYSQL_MEMORY (1536m, Docker's memory limit for MySQL), LOADRIG_CONTAINER_PREFIX (loadrig),
# KEEP (1 keeps data after smoke), LOADRIG_ALLOW_PULL (1 allows pulling a missing image), EXTRA_ENV ("NAME=value ..."
# added to every replica, e.g. EXTRA_ENV="FLOW_METADATA_CATALOG=shadow" for the flag-off replicas of a real rollout).
# Reports: $LOADRIG_HOME/reports/<label>.{md,json}, compare-<topology>.md, seed-<topology>.json.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOME_DIR="${LOADRIG_HOME:-$HOME/.flow-local/live/loadrig}"
BIN="$HOME_DIR/bin"
RUN="$HOME_DIR/run"
DATA="$HOME_DIR/data"
REPORTS="$HOME_DIR/reports"
HEAVY="$HOME/.flow-local/heavy.sh"
# LOADRIG_PORT_BASE moves every port (API base+0/1, metrics base+10/11, model base+81, Redis base+82,
# PostgreSQL base+23) so two rigs (each with its own LOADRIG_HOME) can run side by side; LOADRIG_API_PORT,
# LOADRIG_METRIC_PORT and the other LOADRIG_*_PORT settings override single ports.
PORT_BASE="${LOADRIG_PORT_BASE:-47610}"
PG_PORT="${LOADRIG_PG_PORT:-$((PORT_BASE + 23))}"
MYSQL_PORT="${LOADRIG_MYSQL_PORT:-$((PORT_BASE + 24))}"
REDIS_PORT="${LOADRIG_REDIS_PORT:-$((PORT_BASE + 82))}"
MODEL_PORT="${LOADRIG_MODEL_PORT:-$((PORT_BASE + 81))}"
API_PORT="${LOADRIG_API_PORT:-$PORT_BASE}"
METRIC_PORT="${LOADRIG_METRIC_PORT:-$((PORT_BASE + 10))}"
API_PORTS=("$API_PORT" "$((API_PORT + 1))")
METRIC_PORTS=("$METRIC_PORT" "$((METRIC_PORT + 1))")
CONTAINER_PREFIX="${LOADRIG_CONTAINER_PREFIX:-loadrig}"
PG_CONTAINER="$CONTAINER_PREFIX-pg"
MYSQL_CONTAINER="$CONTAINER_PREFIX-mysql"
REDIS_CONTAINER="$CONTAINER_PREFIX-redis"
MAX_LOAD="${MAX_LOAD:-20}"
mkdir -p "$BIN" "$RUN" "$DATA" "$REPORTS"
chmod 700 "$HOME_DIR"

# The connector secret key seals webhook signing secrets; seed and replicas share it. Generated once, never printed.
if [ ! -f "$HOME_DIR/secrets.env" ]; then
  (umask 077; printf 'FLOW_CONNECTOR_SECRET_KEY=%s\n' "$(openssl rand -base64 32)" > "$HOME_DIR/secrets.env")
fi
set -a; . "$HOME_DIR/secrets.env"; set +a

log() { printf '[loadrig %s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

check_load() {
  local load
  load=$(uptime | sed -E 's/.*load averages?: *//' | awk '{print $1}' | tr -d ,)
  log "load average $load (limit $MAX_LOAD)"
  awk -v l="$load" -v m="$MAX_LOAD" 'BEGIN{exit !(l>m)}' && die "load average $load is above $MAX_LOAD; not starting"
  return 0
}

port_free() { ! lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1; }

# PID files hold only processes this rig started; stop_pid never kills by name.
stop_pid() {
  local file="$1" pid
  [ -f "$file" ] || return 0
  pid=$(cat "$file")
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 40); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
    if kill -0 "$pid" 2>/dev/null; then log "PID $pid did not stop; sending KILL"; kill -KILL "$pid" 2>/dev/null || true; fi
  fi
  rm -f "$file"
}

cmd_build() {
  log "building flow-server and loadrig.test (-tags loadtest) through heavy.sh"
  (cd "$ROOT/api" && "$HEAVY" go build -tags loadtest -o "$BIN/flow-server" ./cmd/server)
  (cd "$ROOT/api" && "$HEAVY" go test -tags loadtest -c -o "$BIN/loadrig.test" ./cmd/server)
  git -C "$ROOT" rev-parse --short HEAD > "$BIN/commit"
}

ensure_built() { [ -x "$BIN/flow-server" ] && [ -x "$BIN/loadrig.test" ] || cmd_build; }

# ---- PostgreSQL and Redis -------------------------------------------------------------------------------------------
pg_secret() {
  if [ ! -f "$HOME_DIR/pg.env" ]; then
    umask 077
    printf 'POSTGRES_USER=flow\nPOSTGRES_PASSWORD=%s\nPOSTGRES_DB=flow_seed\n' "$(openssl rand -hex 16)" > "$HOME_DIR/pg.env"
  fi
}

pg_url() { # database name -> URL (never printed)
  local password
  password=$(sed -n 's/^POSTGRES_PASSWORD=//p' "$HOME_DIR/pg.env")
  printf 'postgres://flow:%s@127.0.0.1:%s/%s?sslmode=disable' "$password" "$PG_PORT" "$1"
}

pg_up() {
  pg_secret
  if docker container inspect --format '{{.State.Running}}' "$PG_CONTAINER" 2>/dev/null | grep -qx true; then return 0; fi
  docker image inspect postgres:17-alpine >/dev/null 2>&1 || [ "${LOADRIG_ALLOW_PULL:-0}" = 1 ] || die "postgres:17-alpine is not present locally (LOADRIG_ALLOW_PULL=1 to pull it)"
  port_free "$PG_PORT" || die "port $PG_PORT is in use"
  log "starting container $PG_CONTAINER (postgres:17-alpine, 1 GiB)"
  docker run -d --name "$PG_CONTAINER" --memory 1g --env-file "$HOME_DIR/pg.env" -p "127.0.0.1:$PG_PORT:5432" postgres:17-alpine \
    -c max_connections=200 -c shared_buffers=256MB -c synchronous_commit=off >/dev/null
  for _ in $(seq 1 60); do
    docker exec "$PG_CONTAINER" pg_isready -h 127.0.0.1 -U flow -d flow_seed >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "$PG_CONTAINER did not become ready"
}

pg_sql() { docker exec "$PG_CONTAINER" psql -v ON_ERROR_STOP=1 -q -U flow -d postgres -c "$1" >/dev/null; }

# ---- MySQL ----------------------------------------------------------------------------------------------------------
mysql_secret() {
  if [ ! -f "$HOME_DIR/mysql.env" ]; then
    umask 077
    printf 'MYSQL_ROOT_PASSWORD=%s\nMYSQL_USER=flow\nMYSQL_PASSWORD=%s\n' "$(openssl rand -hex 16)" "$(openssl rand -hex 16)" > "$HOME_DIR/mysql.env"
  fi
}

mysql_url() { # database name -> URL (never printed)
  local password
  password=$(sed -n 's/^MYSQL_PASSWORD=//p' "$HOME_DIR/mysql.env")
  printf 'mysql://flow:%s@127.0.0.1:%s/%s' "$password" "$MYSQL_PORT" "$1"
}

# mysql_root runs a command in the container as the MySQL root user (password from the env file, never printed).
mysql_root() { docker exec -e MYSQL_PWD="$(sed -n 's/^MYSQL_ROOT_PASSWORD=//p' "$HOME_DIR/mysql.env")" "$MYSQL_CONTAINER" "$@"; }

mysql_sql() { mysql_root mysql -uroot -e "$1" >/dev/null; }

mysql_up() {
  mysql_secret
  if docker container inspect --format '{{.State.Running}}' "$MYSQL_CONTAINER" 2>/dev/null | grep -qx true; then return 0; fi
  docker image inspect mysql:8.4 >/dev/null 2>&1 || [ "${LOADRIG_ALLOW_PULL:-0}" = 1 ] || die "mysql:8.4 is not present locally (LOADRIG_ALLOW_PULL=1 to pull it)"
  port_free "$MYSQL_PORT" || die "port $MYSQL_PORT is in use"
  local memory="${LOADRIG_MYSQL_MEMORY:-1536m}"
  log "starting container $MYSQL_CONTAINER (mysql:8.4, memory limit $memory)"
  # No binary log (a single server; triggers need no extra privilege) and a group-commit log flush, as pg2 runs with
  # synchronous_commit=off.
  docker run -d --name "$MYSQL_CONTAINER" --memory "$memory" --env-file "$HOME_DIR/mysql.env" -p "127.0.0.1:$MYSQL_PORT:3306" mysql:8.4 \
    --skip-log-bin --innodb-buffer-pool-size=768M --innodb-flush-log-at-trx-commit=2 --max-connections=300 \
    --max-allowed-packet=256M >/dev/null
  for _ in $(seq 1 90); do
    mysql_root mysql -h127.0.0.1 -uroot -Nse 'SELECT 1' >/dev/null 2>&1 && return 0
    sleep 1
  done
  die "$MYSQL_CONTAINER did not become ready"
}

# mysql_database recreates database name (empty) for the flow user.
mysql_database() {
  mysql_sql "DROP DATABASE IF EXISTS $1; CREATE DATABASE $1 CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci; GRANT ALL PRIVILEGES ON $1.* TO 'flow'@'%'; FLUSH PRIVILEGES"
}

# mysql_copy copies the seed database into a fresh run database inside the container (no TEMPLATE in MySQL).
mysql_copy() {
  mysql_database flow_run
  mysql_root sh -c 'mysqldump -uroot --single-transaction --quick --triggers --routines --skip-lock-tables flow_seed | mysql -uroot flow_run' ||
    die "copying the MySQL seed failed"
}

redis_up() {
  if [ -f "$RUN/redis.pid" ] && kill -0 "$(cat "$RUN/redis.pid")" 2>/dev/null; then return 0; fi
  if docker container inspect --format '{{.State.Running}}' "$REDIS_CONTAINER" 2>/dev/null | grep -qx true; then return 0; fi
  port_free "$REDIS_PORT" || die "port $REDIS_PORT is in use"
  if docker image inspect redis:7-alpine >/dev/null 2>&1 || [ "${LOADRIG_ALLOW_PULL:-0}" = 1 ]; then
    log "starting container $REDIS_CONTAINER (redis:7-alpine, 256 MiB)"
    docker run -d --name "$REDIS_CONTAINER" --memory 256m -p "127.0.0.1:$REDIS_PORT:6379" redis:7-alpine >/dev/null
    echo docker > "$RUN/redis.kind"
  else
    log "redis:7-alpine is not present locally: using the in-memory Redis process (miniredis)"
    FLOW_LOADRIG_REDIS_ADDR="127.0.0.1:$REDIS_PORT" "$BIN/loadrig.test" -test.run '^TestLoadRigRedis$' -test.timeout 24h >"$RUN/redis.log" 2>&1 &
    echo $! > "$RUN/redis.pid"
    echo miniredis > "$RUN/redis.kind"
  fi
  for _ in $(seq 1 30); do port_free "$REDIS_PORT" || return 0; sleep 0.5; done
  die "Redis did not start"
}

model_up() {
  case "${MODEL:-fake}" in
    fake)
      if [ -f "$RUN/model.pid" ] && kill -0 "$(cat "$RUN/model.pid")" 2>/dev/null; then return 0; fi
      port_free "$MODEL_PORT" || die "port $MODEL_PORT is in use"
      FLOW_LOADRIG_FAKE_MODEL_ADDR="127.0.0.1:$MODEL_PORT" "$BIN/loadrig.test" -test.run '^TestLoadRigFakeModel$' -test.v -test.timeout 24h >"$RUN/model.log" 2>&1 &
      echo $! > "$RUN/model.pid"
      for _ in $(seq 1 30); do port_free "$MODEL_PORT" || return 0; sleep 0.3; done
      die "fake model did not start" ;;
    proxy) curl -s -m 3 -o /dev/null "http://127.0.0.1:8787/v1/models" || die "the shared model proxy on 127.0.0.1:8787 is not running (agents never start it)" ;;
    off) ;;
    *) die "MODEL must be fake, proxy or off" ;;
  esac
}

model_env() {
  case "${MODEL:-fake}" in
    fake) echo "FLOW_AGENT_ENABLED=true FLOW_AGENT_PROTOCOL=openai-chat-completions FLOW_AGENT_BASE_URL=http://127.0.0.1:$MODEL_PORT/v1 FLOW_AGENT_API_KEY=loadrig-fake FLOW_AGENT_MODEL=loadrig-fake" ;;
    proxy) echo "FLOW_AGENT_ENABLED=true FLOW_AGENT_PROTOCOL=openai-chat-completions FLOW_AGENT_BASE_URL=http://127.0.0.1:8787/v1 FLOW_AGENT_API_KEY=local-go-proxy FLOW_AGENT_MODEL=deepseek-v4.1-flash" ;;
    off) echo "FLOW_AGENT_ENABLED=false" ;;
  esac
}

# ---- Seed -----------------------------------------------------------------------------------------------------------
db_env() { # topology phase(seed|run) -> FLOW_DATABASE_* assignments
  case "$1" in
    sqlite) if [ "$2" = seed ]; then echo "FLOW_DATABASE_DRIVER=sqlite FLOW_DATABASE_PATH=$DATA/sqlite-seed/flow.db"; else echo "FLOW_DATABASE_DRIVER=sqlite FLOW_DATABASE_PATH=$RUN/sqlite/flow.db"; fi ;;
    pg2) if [ "$2" = seed ]; then echo "FLOW_DATABASE_DRIVER=postgres FLOW_DATABASE_URL=$(pg_url flow_seed)"; else echo "FLOW_DATABASE_DRIVER=postgres FLOW_DATABASE_URL=$(pg_url flow_run)"; fi ;;
    mysql2) if [ "$2" = seed ]; then echo "FLOW_DATABASE_DRIVER=mysql FLOW_DATABASE_URL=$(mysql_url flow_seed)"; else echo "FLOW_DATABASE_DRIVER=mysql FLOW_DATABASE_URL=$(mysql_url flow_run)"; fi ;;
    *) die "topology must be sqlite, pg2 or mysql2" ;;
  esac
}

cmd_seed() {
  local topology="$1" scale="${SCALE:-1}"
  ensure_built
  check_load
  if [ "$topology" = pg2 ]; then
    pg_up
    pg_sql "DROP DATABASE IF EXISTS flow_run" ; pg_sql "DROP DATABASE IF EXISTS flow_seed" ; pg_sql "CREATE DATABASE flow_seed"
  elif [ "$topology" = mysql2 ]; then
    mysql_up
    mysql_sql "DROP DATABASE IF EXISTS flow_run"; mysql_database flow_seed
  else
    rm -rf "$DATA/sqlite-seed"; mkdir -p "$DATA/sqlite-seed"
  fi
  mkdir -p "$DATA/$topology-out"; rm -f "$DATA/$topology-out/fixture.json"
  log "seeding $topology at scale $scale"
  # shellcheck disable=SC2046
  (cd "$HOME_DIR" && env $(db_env "$topology" seed) FLOW_LOADRIG_SEED=1 FLOW_LOADRIG_UPGRADE_SEED="${LOADRIG_UPGRADE_SEED:-0}" FLOW_LOADRIG_OUT="$DATA/$topology-out" FLOW_LOADRIG_SCALE="$scale" \
    FLOW_RECORD_HYDRATION=on FLOW_RECORD_JOURNAL=on FLOW_SYNC_V2=on FLOW_WORKSPACE_TUTORIAL=off \
    GOMAXPROCS="${GOMAXPROCS_SEED:-4}" GOMEMLIMIT="${GOMEMLIMIT_SEED:-3GiB}" \
    "$BIN/loadrig.test" -test.run '^TestLoadRigSeed$' -test.v -test.timeout 4h) 2>&1 | tee "$REPORTS/seed-$topology.log" | grep -E '^(=== RUN|--- |\s+loadrig_seed_test.go|PASS|FAIL|ok|panic)' | cut -c1-300 >&2 || true
  [ -f "$DATA/$topology-out/fixture.json" ] || die "seed failed"
  cp "$DATA/$topology-out/seed.json" "$REPORTS/seed-$topology.json"
  log "seed report: $REPORTS/seed-$topology.json"
}

# ---- Replicas -------------------------------------------------------------------------------------------------------
cmd_start() {
  local topology="$1" flag="$2" replicas=1
  local feature_env=()
  case "$flag" in
    on|off) feature_env=(FLOW_RECORD_HYDRATION=on FLOW_RECORD_JOURNAL=on FLOW_SYNC_V2=on FLOW_RECORD_SPOOL_MAX_BYTES=268435456 "FLOW_BOUNDED_METADATA=$flag") ;;
    default) ;;
    *) die "flag must be default, on or off" ;;
  esac
  ensure_built
  check_load
  [ -f "$DATA/$topology-out/fixture.json" ] || die "seed $topology first"
  cmd_stop
  model_up
  if [ "$topology" = pg2 ]; then
    replicas=2
    pg_up; redis_up
    pg_sql "DROP DATABASE IF EXISTS flow_run"; pg_sql "CREATE DATABASE flow_run TEMPLATE flow_seed"
  elif [ "$topology" = mysql2 ]; then
    replicas=2
    mysql_up; redis_up
    mysql_copy
  else
    rm -rf "$RUN/sqlite"; mkdir -p "$RUN/sqlite"
    for file in "$DATA/sqlite-seed"/flow.db*; do cp -c "$file" "$RUN/sqlite/" 2>/dev/null || cp "$file" "$RUN/sqlite/"; done
  fi
  local redis_env="FLOW_REDIS_MODE=disabled"
  [ "$replicas" = 2 ] && redis_env="FLOW_REDIS_MODE=standalone FLOW_REDIS_ADDRS=127.0.0.1:$REDIS_PORT FLOW_REDIS_PREFIX=loadrig"
  local bases="" metrics="" pids=""
  for index in $(seq 0 $((replicas - 1))); do
    local port=${API_PORTS[$index]} mport=${METRIC_PORTS[$index]}
    port_free "$port" || die "port $port is in use"
    mkdir -p "$RUN/uploads"
    # shellcheck disable=SC2046
    env $(db_env "$topology" run) $redis_env $(model_env) \
      FLOW_HTTP_ADDR="127.0.0.1:$port" FLOW_APP_URL="http://127.0.0.1:$port" FLOW_STORAGE_LOCAL_PATH="$RUN/uploads" FLOW_WORKSPACE_TUTORIAL=off \
      ${feature_env[@]+"${feature_env[@]}"} FLOW_LOADRIG_METRICS_ADDR="127.0.0.1:$mport" \
      GOMAXPROCS="${GOMAXPROCS_REPLICA:-4}" GOMEMLIMIT="${GOMEMLIMIT_REPLICA-1536MiB}" ${EXTRA_ENV:-} \
      "$BIN/flow-server" >"$RUN/replica-$index.log" 2>&1 &
    echo $! > "$RUN/replica-$index.pid"
    bases="$bases${bases:+,}http://127.0.0.1:$port"; metrics="$metrics${metrics:+,}http://127.0.0.1:$mport"; pids="$pids${pids:+,}$(cat "$RUN/replica-$index.pid")"
    local began=$SECONDS
    until curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/health" | grep -q 200; do
      kill -0 "$(cat "$RUN/replica-$index.pid")" 2>/dev/null || { tail -5 "$RUN/replica-$index.log" | cut -c1-300 >&2; die "replica $index exited during startup ($topology, flag $flag)"; }
      [ $((SECONDS - began)) -lt "${START_TIMEOUT:-600}" ] || die "replica $index did not become healthy"
      sleep 1
    done
    log "replica $index up in $((SECONDS - began))s (pid $(cat "$RUN/replica-$index.pid"), $topology, flag $flag, RSS $(ps -o rss= -p "$(cat "$RUN/replica-$index.pid")" | awk '{printf "%.0f MiB", $1/1024}'))"
    echo "$((SECONDS - began))" > "$RUN/replica-$index.startup"
  done
  printf 'TOPOLOGY=%s\nFLAG=%s\nBASES=%s\nMETRICS=%s\nPIDS=%s\n' "$topology" "$flag" "$bases" "$metrics" "$pids" > "$RUN/current.env"
}

cmd_stop() {
  for file in "$RUN"/replica-*.pid; do [ -e "$file" ] && stop_pid "$file"; done
  rm -f "$RUN/current.env"
}

cmd_drive() {
  local label="$1"
  [ -f "$RUN/current.env" ] || die "start a topology first"
  # shellcheck disable=SC1091
  . "$RUN/current.env"
  check_load
  local agent_max="${AGENT_MAX:-0}"
  [ "${MODEL:-fake}" = proxy ] && agent_max="${AGENT_MAX:-6}"
  rm -f "$REPORTS/$label.md"
  log "driving $label: ${VUS:-10} VUs for ${DURATION:-60s} on $TOPOLOGY (flag $FLAG)"
  (cd "$HOME_DIR" && FLOW_LOADRIG_DRIVE=1 FLOW_LOADRIG_OUT="$REPORTS" FLOW_LOADRIG_LABEL="$label" FLOW_LOADRIG_FIXTURE="$DATA/$TOPOLOGY-out/fixture.json" \
    FLOW_LOADRIG_BASES="$BASES" FLOW_LOADRIG_METRICS="$METRICS" FLOW_LOADRIG_PIDS="$PIDS" FLOW_LOADRIG_TOPOLOGY="$TOPOLOGY" FLOW_LOADRIG_FLAG="$FLAG" \
    FLOW_LOADRIG_VUS="${VUS:-10}" FLOW_LOADRIG_DURATION="${DURATION:-60s}" FLOW_LOADRIG_WARMUP="${WARMUP:-5s}" FLOW_LOADRIG_THINK="${THINK:-300ms}" \
    FLOW_LOADRIG_STREAMS="${STREAMS:-}" FLOW_LOADRIG_MIX="${MIX:-}" FLOW_LOADRIG_MODEL="${MODEL:-fake}" FLOW_LOADRIG_AGENT_EVERY="${AGENT_EVERY:-20s}" \
    FLOW_LOADRIG_AGENT_MAX="$agent_max" GOMAXPROCS="${GOMAXPROCS_DRIVER:-2}" \
    "$BIN/loadrig.test" -test.run '^TestLoadRigDrive$' -test.v -test.timeout 6h) 2>&1 | tee "$REPORTS/$label.log" | grep -E '^(--- |\s+loadrig_drive_test.go|PASS|FAIL|panic)' | cut -c1-300 >&2 || true
  [ -f "$REPORTS/$label.md" ] || die "drive $label produced no report"
  for index in 0 1; do
    [ -f "$RUN/replica-$index.log" ] && cp "$RUN/replica-$index.log" "$REPORTS/$label.replica-$index.log"
  done
  log "report: $REPORTS/$label.md"
}

cmd_compare() {
  FLOW_LOADRIG_COMPARE_A="$1" FLOW_LOADRIG_COMPARE_B="$2" FLOW_LOADRIG_COMPARE_OUT="$3" "$BIN/loadrig.test" -test.run '^TestLoadRigCompare$' >/dev/null
  log "comparison: $3"
}

cmd_pair() {
  local topology="$1" tag="${TAG:-$1}"
  for flag in off on; do
    cmd_start "$topology" "$flag"
    cmd_drive "$tag-$flag"
    cmd_stop
  done
  cmd_compare "$REPORTS/$tag-off.json" "$REPORTS/$tag-on.json" "$REPORTS/compare-$tag.md"
}

cmd_down() {
  cmd_stop
  stop_pid "$RUN/model.pid"
  stop_pid "$RUN/redis.pid"
  local name id
  for name in "$PG_CONTAINER" "$MYSQL_CONTAINER" "$REDIS_CONTAINER"; do
    if id=$(docker container inspect --format '{{.Id}}' "$name" 2>/dev/null); then
      docker rm -f -v "$id" >/dev/null
      log "removed container $name"
    fi
  done
  rm -f "$RUN/redis.kind"
}

cmd_clean() {
  cmd_down
  rm -rf "$RUN" "$DATA" "$HOME_DIR/pg.env" "$HOME_DIR/mysql.env" "$HOME_DIR/secrets.env"
  log "removed rig data (binaries and reports kept)"
}

cmd_smoke() {
  export VUS="${VUS:-6}" DURATION="${DURATION:-40s}" WARMUP="${WARMUP:-5s}" AGENT_EVERY="${AGENT_EVERY:-10s}" SCALE="${SCALE:-1}"
  trap 'cmd_down' EXIT
  ensure_built
  for topology in sqlite pg2; do
    cmd_seed "$topology"
    TAG="smoke-$topology" cmd_pair "$topology"
  done
  trap - EXIT
  if [ "${KEEP:-0}" = 1 ]; then cmd_down; else cmd_clean; fi
}

case "${1:-help}" in
  build) cmd_build ;;
  seed) cmd_seed "${2:?topology}" ;;
  start) cmd_start "${2:?topology}" "${3:?flag default|on|off}" ;;
  drive) cmd_drive "${2:?label}" ;;
  stop) cmd_stop; stop_pid "$RUN/model.pid" ;;
  pair) cmd_pair "${2:?topology}" ;;
  compare) cmd_compare "${2:?a.json}" "${3:?b.json}" "${4:?out.md}" ;;
  smoke) cmd_smoke ;;
  down) cmd_down ;;
  clean) cmd_clean ;;
  *) sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
