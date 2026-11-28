#!/usr/bin/env bash
# Run one shard from a precompiled Go test binary.
#
#   go-test-binary.sh BINARY PACKAGE RUN_PATTERN EVENTS COVERPROFILE TIMEOUT MODULE
#
# The binary is compiled with coverage flags by the CI build job when a
# COVERPROFILE path is supplied. The wrapper keeps the verbose test output for
# failures and emits the small JSON event stream needed by the existing timing
# and coverage aggregation steps. Set GO_TEST_EVENTS_APPEND=1 to add events to
# an existing stream.
set -uo pipefail

if [ "$#" -ne 7 ]; then
  echo "usage: $0 BINARY PACKAGE RUN_PATTERN EVENTS COVERPROFILE TIMEOUT MODULE" >&2
  exit 2
fi

binary=$1
package=$2
pattern=$3
events=$4
coverage=$5
timeout_value=$6
module=$7

mkdir -p "$(dirname "$events")"
if [ -n "$coverage" ]; then
  mkdir -p "$(dirname "$coverage")"
fi
if [ "${GO_TEST_EVENTS_APPEND:-0}" != 1 ]; then
  : > "$events"
fi
raw=$(mktemp)
trap 'rm -f "$raw"' EXIT

set +e
(
  cd "$package" || exit
  args=(
    -test.run "$pattern"
    -test.count=1
    -test.timeout="$timeout_value"
  )
  if [ -n "$coverage" ]; then
    args+=("-test.coverprofile=$coverage")
  fi
  args+=(-test.v)
  if command -v timeout >/dev/null 2>&1; then
    timeout --foreground "$timeout_value" "$binary" "${args[@]}"
  else
    "$binary" "${args[@]}"
  fi
) 2>&1 | tee "$raw"
status=${PIPESTATUS[0]}
set -e

relative_package=${package#./}
full_package="$module/$relative_package"
while IFS=$'\t' read -r test elapsed; do
  [ -n "$test" ] || continue
  jq -cn \
    --arg package "$full_package" \
    --arg test "$test" \
    --argjson elapsed "$elapsed" \
    '{Action: "pass", Package: $package, Test: $test, Elapsed: $elapsed}' >> "$events"
done < <(
  sed -nE \
    's/^--- PASS: ((Test|Fuzz|Example)[A-Za-z0-9_]*) \(([0-9]+([.][0-9]+)?)s\)$/\1\t\3/p' \
    "$raw"
)

exit "$status"
