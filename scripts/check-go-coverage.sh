#!/usr/bin/env sh
set -eu

profile=${1:-coverage.out}
minimum=${GO_COVERAGE_MIN:-60}
total=$(go tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

if [ -z "$total" ]; then
  echo "Unable to read Go coverage from $profile" >&2
  exit 1
fi

awk -v actual="$total" -v required="$minimum" 'BEGIN {
  printf "Go statement coverage: %.1f%% (required %.1f%%)\n", actual, required
  exit(actual + 0.0001 < required)
}'
