#!/usr/bin/env bash
# Print the CI matrices of the Go test jobs as GitHub Actions outputs.
#
#   go-test-matrix.sh MODE
#
# MODE is race (pushes to main and manual runs: the API shards run with the race
# detector) or plain (pull requests: no race detector, so far fewer shards). The
# shard counts come from scripts/ci/go-test-shards.json:
#
#   race / plain  shards of cmd/server and internal/store in the API test job
#   dialect       shards of the TestScaleP tests per package in each of the
#                 PostgreSQL and MySQL "Scale differentials" jobs
#
# Writes mode=, api= and dialect= lines (compact JSON matrices) to stdout.
set -euo pipefail

mode=${1:-}
case $mode in
  race | plain) ;;
  *)
    echo "usage: go-test-matrix.sh race|plain" >&2
    exit 2
    ;;
esac
config=$(dirname "$0")/go-test-shards.json

api=$(jq -c --arg mode "$mode" '
  def short: if . == "./cmd/server" then "server" else "store" end;
  { include: ([ .[$mode] | to_entries[] | .key as $package | .value as $total
      | range(1; $total + 1) as $shard
      | { name: "\($package | short) \($shard)/\($total)", profile: "\($package | short)-\($shard)",
          package: $package, shard: $shard, shards: $total } ]
    + [ { name: "other packages", profile: "other", package: "others", shard: 0, shards: 0 } ]) }
' "$config")

dialect=$(jq -c '
  . as $config | { include: [ ("postgres", "mysql") as $dialect
      | $config.dialect | to_entries[] | .key as $package | .value as $total
      | range(1; $total + 1) as $shard
      | { dialect: $dialect, package: $package, shard: $shard, shards: $total,
          name: "\($dialect), \($package | ltrimstr("./")) \($shard)/\($total)" } ] }
' "$config")

printf 'mode=%s\napi=%s\ndialect=%s\n' "$mode" "$api" "$dialect"
