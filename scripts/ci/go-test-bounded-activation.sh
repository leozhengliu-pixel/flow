#!/bin/bash
# Use a disposable database, with FLOW_TEST_DATABASE_DRIVER and
# FLOW_TEST_DATABASE_URL for MySQL or PostgreSQL. Unset both for SQLite.
set -euo pipefail
cd "$(dirname "$0")/../../api"

export FLOW_TEST_BOUNDED_ACTIVATION=1
export FLOW_BOUNDED_METADATA=off FLOW_RECORD_HYDRATION=on FLOW_RECORD_JOURNAL=on
export FLOW_RECORD_PEER_SYNC=on FLOW_METADATA_CATALOG=off FLOW_SYNC_V2=on

suites=$(../scripts/ci/go-test-shard.sh selection bounded-activation ./cmd/server)
exec go test -p 1 ./cmd/server -run "$suites" -count=1 "$@"
