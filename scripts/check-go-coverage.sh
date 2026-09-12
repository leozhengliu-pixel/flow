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

# Keep the public MCP contract from regressing behind the aggregate API score.
awk -v required="${MCP_COVERAGE_MIN:-75}" '
  $1 ~ /cmd\/server\/(mcp|mcp_tools|mcp_tools_write|oauth_mcp_auth)\.go:/ {
    statements += $2
    if ($3 > 0) covered += $2
  }
  END {
    if (!statements) { print "MCP coverage is missing from the profile"; exit 1 }
    actual = 100 * covered / statements
    printf "MCP/OAuth statement coverage: %.1f%% (required %.1f%%)\n", actual, required
    exit(actual + 0.0001 < required)
  }
' "$profile"
