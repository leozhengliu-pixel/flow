#!/usr/bin/env bash
# Run a list of top-level tests of one package so a hang cannot hide the rest.
#
#   go-test-resume.sh PACKAGE NAMES_FILE TIMEOUT [go test flags...]
#
# Runs `go test -v` on the tests named in NAMES_FILE (one per line) with
# -timeout TIMEOUT. When GO_TEST_BINARY names a precompiled test binary, that
# binary is run instead. When a timeout fires, the test that was running is
# reported as failed and the tests that had not started yet run in a further
# invocation, until every listed test has run. Exits non-zero if any invocation
# failed. Run it from the Go module root (api/).
set -uo pipefail

package=$1 names=$2 timeout=$3
shift 3
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
LC_ALL=C sort -u "$names" > "$work/remaining"
status=0 round=1
while [ -s "$work/remaining" ]; do
  pattern=$(printf '^(%s)$' "$(paste -sd'|' "$work/remaining")")
  echo "::group::go test $package, round $round: $(wc -l < "$work/remaining" | tr -d ' ') tests"
  if [ -n "${GO_TEST_BINARY:-}" ]; then
    (
      cd "$package"
      args=(
        -test.run "$pattern"
        -test.count=1
        -test.timeout="$timeout"
        -test.v
        "$@"
      )
      if command -v timeout >/dev/null 2>&1; then
        timeout --foreground "$timeout" "$GO_TEST_BINARY" "${args[@]}"
      else
        "$GO_TEST_BINARY" "${args[@]}"
      fi
    ) 2>&1 | tee "$work/log"
  else
    go test "$package" -run "$pattern" -count=1 -timeout "$timeout" -v "$@" 2>&1 | tee "$work/log"
  fi
  code=${PIPESTATUS[0]}
  echo "::endgroup::"
  [ "$code" -eq 0 ] && break
  status=$code
  if ! grep -q '^panic: test timed out after' "$work/log"; then
    break # ordinary failures do not stop go test: every test has run
  fi
  hung=$(awk '/^panic: test timed out after/ { getline; getline; sub(/^[[:space:]]+/, ""); sub(/[[:space:]].*/, ""); print; exit }' "$work/log")
  echo "::error title=Test timed out::$package $hung did not finish within $timeout; running the tests that had not started"
  grep -E '^=== RUN   [A-Za-z0-9_]+$' "$work/log" | awk '{ print $3 }' | LC_ALL=C sort -u > "$work/started"
  LC_ALL=C comm -23 "$work/remaining" "$work/started" > "$work/next"
  if cmp -s "$work/next" "$work/remaining"; then
    break # nothing started: do not loop
  fi
  mv "$work/next" "$work/remaining"
  round=$((round + 1))
done
exit "$status"
