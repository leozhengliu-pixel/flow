#!/usr/bin/env bash
# Run the known-failing tests of one scope and report which still fail.
#
#   run-known-failing.sh SCOPE      (from the Go module root, api/)
#
# SCOPE is race, plain, postgres or mysql (see scripts/ci/known-failing-tests.txt).
# race runs the tests with the race detector on SQLite, plain without it; postgres
# and mysql run them on the CI service databases. Each test runs alone, with a
# 10-minute go test timeout (and a hard stop a minute later), so a hang ends.
#
# Always exits 0: the job tracks the list and never blocks. It writes a summary
# table to $GITHUB_STEP_SUMMARY, prints the end of each failing test's output,
# and emits a warning annotation for each test that passes now, so its line (or
# scope) is removed from the list.
set -uo pipefail

scope=${1:-}
case $scope in
  race | plain | postgres | mysql) ;;
  *)
    echo "usage: run-known-failing.sh race|plain|postgres|mysql" >&2
    exit 2
    ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
list=$here/known-failing-tests.txt
summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

case $scope in
  postgres)
    export FLOW_TEST_DATABASE_DRIVER=postgres
    export FLOW_TEST_DATABASE_URL=${FLOW_TEST_DATABASE_URL:-postgres://flow:flow@127.0.0.1:5432/flow?sslmode=disable}
    ;;
  mysql)
    export FLOW_TEST_DATABASE_DRIVER=mysql
    export FLOW_TEST_DATABASE_URL=${FLOW_TEST_DATABASE_URL:-mysql://flow:flow@127.0.0.1:3306/flow}
    ;;
esac
build_flags=()
if [ "$scope" = race ]; then build_flags=(-race); fi

{
  echo "### Known failing tests: $scope"
  echo
  echo '| Package | Test | Result | Seconds |'
  echo '| --- | --- | --- | --- |'
} >> "$summary"

still=0 passing=0
for package in ./cmd/server ./internal/store; do
  mapfile -t tests < <("$here/go-test-shard.sh" known "$package" "$scope")
  [ "${#tests[@]}" -gt 0 ] || continue
  binary=$work/$(basename "$package").test
  if ! go test -c "${build_flags[@]}" -o "$binary" "$package"; then
    echo "::warning title=Known failing ($scope)::$package does not build; its known-failing tests were not run"
    for test in "${tests[@]}"; do
      echo "| $package | $test | not built | |" >> "$summary"
    done
    continue
  fi
  for test in "${tests[@]}"; do
    log=$work/$test.log
    started=$(date +%s)
    (cd "$package" && timeout 11m "$binary" -test.run "^${test}\$" -test.count=1 -test.timeout=10m -test.v) > "$log" 2>&1
    status=$?
    seconds=$(( $(date +%s) - started ))
    if [ "$status" -eq 0 ] && grep -q "^--- PASS: $test " "$log"; then
      result=passes
      passing=$((passing + 1))
      echo "::warning title=Known-failing test passes ($scope)::$package $test passes in scope $scope; remove that scope (or the line) from scripts/ci/known-failing-tests.txt"
    else
      if [ "$status" -eq 124 ] || grep -q '^panic: test timed out' "$log"; then result='still fails (timeout)'; else result='still fails'; fi
      still=$((still + 1))
      echo "::group::$package $test ($scope): $result after ${seconds}s"
      grep -E -- '^(--- FAIL|panic:|\s+\S+_test\.go:[0-9]+:)' "$log" | head -n 40
      tail -n 20 "$log"
      echo "::endgroup::"
    fi
    echo "| $package | $test | $result | $seconds |" >> "$summary"
  done
done
{
  echo
  echo "$still still fail, $passing pass now (scope $scope)."
} >> "$summary"
echo "$still still fail, $passing pass now (scope $scope)."
exit 0
