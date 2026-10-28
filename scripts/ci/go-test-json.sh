#!/usr/bin/env bash
# Helpers for `go test -json` output in CI. Needs jq.
#
#   go test -json ... | go-test-json.sh log          print a compact log (package-level lines only)
#   go-test-json.sh failures JSONL...                print the output of tests that did not pass
#   go-test-json.sh timings MODULE OUTPUT JSONL...   write {"<pkg>": {"<Test>": seconds}} for passed
#                                                    top-level tests; <pkg> is relative to MODULE
#                                                    ("./cmd/server")
#   go-test-json.sh merge OUTPUT TIMINGS...          merge timings files (later files win)
#
# `go test -json` prints one event per line. Raw, that is every line a test logs
# and thousands of RUN/PASS lines, so `log` keeps only what `go test` prints
# without -v (the "ok"/"FAIL" package lines, build errors, panics outside a test)
# and the job calls `failures` afterwards to show what the failing tests said.
# Lines that are not JSON (older toolchains print build errors that way) pass
# through unchanged.
set -euo pipefail

usage() {
  sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

command=${1:-}
[ "$#" -ge 1 ] || usage
shift

case $command in
  log)
    jq -R -j --unbuffered '
      . as $line
      | (try ($line | fromjson) catch {Action: "output", Output: ($line + "\n")})
      | select(.Action == "output" and .Test == null)
      | .Output
    '
    ;;
  failures)
    [ "$#" -ge 1 ] || usage
    cat "$@" | jq -R -n -j '
      reduce (inputs | (try fromjson catch empty) | select(.Test != null)) as $e ({};
        .[$e.Package + " " + $e.Test] |= (
          (. // {out: "", done: false})
          | if $e.Action == "output" then .out += $e.Output
            elif $e.Action == "pass" or $e.Action == "skip" then .done = true
            else . end))
      | to_entries[] | select(.value.done | not)
      | "=== " + .key + "\n" + .value.out
    '
    ;;
  timings)
    [ "$#" -ge 3 ] || usage
    module=$1 output=$2
    shift 2
    cat "$@" | jq -R -n -S --arg module "$module" '
      reduce (inputs | (try fromjson catch empty)
          | select(.Action == "pass" and .Test != null and (.Test | contains("/") | not))) as $e ({};
        .["." + ($e.Package | ltrimstr($module))][$e.Test] = (($e.Elapsed * 100 | round) / 100))
    ' > "$output"
    ;;
  merge)
    [ "$#" -ge 2 ] || usage
    output=$1
    shift
    jq -S -s 'reduce .[] as $f ({}; . * $f)' "$@" > "$output"
    ;;
  *)
    usage
    ;;
esac
