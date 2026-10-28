#!/usr/bin/env bash
# Split the tests of one Go package across CI shards.
#
#   go-test-shard.sh run   PACKAGE INDEX TOTAL   print the -run regex for shard INDEX (1-based)
#   go-test-shard.sh list  PACKAGE INDEX TOTAL   print the test names of shard INDEX, one per line
#   go-test-shard.sh check PACKAGE TOTAL         fail unless the shards partition the package's tests
#   go-test-shard.sh plan  PACKAGE TOTAL         print each shard's test count and predicted seconds
#
# Run it from the Go module root (api/). PACKAGE is an import path or a relative
# path such as ./cmd/server. Extra flags for `go test -list` come from
# GO_TEST_LIST_FLAGS; pass the flags the real run uses (for example -race) so the
# test binary is compiled once and served from the build cache afterwards.
#
# How a test gets its shard
#
# scripts/ci/go-test-timings.json holds measured seconds per top-level test and
# package ({"./cmd/server": {"TestName": 1.23, ...}}; GO_TEST_TIMINGS points at
# another file). The tests listed there are packed greedily, longest first, each
# onto the shard with the least predicted time so far (ties go to the lowest
# shard; names break ties between equal times). Every entry of the file takes part
# in the packing, whether or not the test still exists, so adding, removing or
# renaming a test that is not listed moves no listed test; only a new timings file
# does. Each test counts for at least 50 ms so thousands of near-instant tests
# still spread out instead of piling onto one shard.
#
# A test missing from the file (new since the last refresh) goes to the shard
# given by a hash of its name, which depends on that name alone. Without timings
# for the package every test is placed that way. Refresh the file with
# update-go-test-timings.sh. Needs jq when a timings file exists.
#
# Only top-level Test*, Fuzz* and Example* functions are assigned; their subtests
# run with their parent. Benchmarks are not run by `go test` without -bench and are
# left out. A -run pattern longer than MAX_PATTERN_BYTES fails (the Linux limit
# for one argument is 128 KiB).
set -euo pipefail

MAX_PATTERN_BYTES=100000
timings_file=${GO_TEST_TIMINGS:-$(dirname "$0")/go-test-timings.json}

usage() {
  sed -n '2,6p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# All runnable top-level test names of a package, sorted and unique.
all_tests() {
  local package=$1
  # shellcheck disable=SC2086
  go test ${GO_TEST_LIST_FLAGS:-} -list '^(Test|Fuzz|Example)' "$package" |
    grep -E '^(Test|Fuzz|Example)[A-Za-z0-9_]*$' |
    LC_ALL=C sort -u
}

# Timings are keyed by the package path relative to the module ("./cmd/server").
# An import path such as flow/api/cmd/server is mapped onto that.
timings_key() {
  local package=$1 module
  case $package in
    . | ./*) printf '%s\n' "$package" ;;
    *)
      module=$(go list -m)
      printf '.%s\n' "${package#"$module"}"
      ;;
  esac
}

# Write the package's timings to $work/weights as "<name> <milliseconds>" lines,
# longest first and by name among equals. The file is empty without timings.
load_weights() {
  local package=$1 key
  : > "$work/weights"
  [ -s "$timings_file" ] || return 0
  key=$(timings_key "$package")
  jq -r --arg key "$key" '(.[$key] // {}) | to_entries[] | "\(.key) \(.value * 1000 | round)"' "$timings_file" |
    LC_ALL=C sort -k2,2nr -k1,1 > "$work/weights"
}

# Print "<shard> <name> <ms>" for every test read from stdin; <ms> is the
# predicted time in milliseconds, or -1 for a test without a timing. The hash is
# a plain polynomial over the characters of the name so it is the same under mawk,
# gawk and BSD awk. Integer milliseconds keep the packing identical everywhere.
assign() {
  local total=$1 weights=""
  if [ -s "$work/weights" ]; then
    weights=$work/weights
  fi
  awk -v total="$total" -v weights="$weights" '
    BEGIN {
      chars = "_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
      for (s = 1; s <= total; s++) load[s] = 0
      if (weights != "") {
        # The weights file is sorted: longest first, then by name.
        while ((getline line < weights) > 0) {
          split(line, f, " ")
          best = 1
          for (s = 2; s <= total; s++) if (load[s] < load[best]) best = s
          shard[f[1]] = best
          ms[f[1]] = f[2]
          load[best] += (f[2] < 50 ? 50 : f[2])
        }
        close(weights)
      }
    }
    {
      if ($0 in shard) {
        print shard[$0], $0, ms[$0]
        next
      }
      h = 7
      for (i = 1; i <= length($0); i++) {
        h = (h * 131 + index(chars, substr($0, i, 1))) % 2147483629
      }
      print (h % total) + 1, $0, -1
    }
  '
}

# Names of shard $1 from the assignment lines on stdin.
names_of() {
  awk -v want="$1" '$1 == want { print $2 }'
}

# The -run regex for the names on stdin.
regex_of() {
  printf '^(%s)$' "$(paste -sd'|' -)"
}

validate_shard() {
  case ${1:-}${2:-} in
    '' | *[!0-9]*) usage ;;
  esac
  if [ "$1" -lt 1 ] || [ "$1" -gt "$2" ]; then
    echo "shard index $1 is outside 1..$2" >&2
    exit 2
  fi
}

command=${1:-}
case $command in
  run | list)
    [ "$#" -eq 4 ] || usage
    package=$2 index=$3 total=$4
    validate_shard "$index" "$total"
    load_weights "$package"
    all_tests "$package" | assign "$total" | names_of "$index" > "$work/names"
    if [ ! -s "$work/names" ]; then
      echo "shard $index/$total of $package has no tests" >&2
      exit 1
    fi
    if [ "$command" = list ]; then
      cat "$work/names"
    else
      regex=$(regex_of < "$work/names")
      if [ "${#regex}" -gt "$MAX_PATTERN_BYTES" ]; then
        echo "shard $index/$total of $package: -run pattern is ${#regex} bytes, over the limit of $MAX_PATTERN_BYTES" >&2
        exit 1
      fi
      printf '%s\n' "$regex"
    fi
    ;;
  plan)
    [ "$#" -eq 3 ] || usage
    package=$2 total=$3
    validate_shard 1 "$total"
    load_weights "$package"
    all_tests "$package" | assign "$total" > "$work/assigned"
    if [ ! -s "$work/assigned" ]; then
      echo "$package has no tests" >&2
      exit 1
    fi
    awk -v total="$total" '
      { n[$1]++; if ($3 >= 0) { known[$1]++; sum[$1] += $3; all += $3 } else unknown[$1]++ }
      END {
        for (s = 1; s <= total; s++) {
          printf "shard %d/%d: %4d tests (%d without timing), predicted %8.1f s\n", s, total, n[s], unknown[s], sum[s] / 1000
          if (sum[s] > max) max = sum[s]
          if (min == "" || sum[s] < min) min = sum[s]
        }
        printf "total %.1f s, ideal %.1f s per shard, slowest %.1f s, fastest %.1f s\n", all / 1000, all / 1000 / total, max / 1000, min / 1000
      }
    ' "$work/assigned"
    ;;
  check)
    [ "$#" -eq 3 ] || usage
    package=$2 total=$3
    validate_shard 1 "$total"
    load_weights "$package"
    all_tests "$package" > "$work/all"
    if [ ! -s "$work/all" ]; then
      echo "$package has no tests" >&2
      exit 1
    fi
    if [ -s "$work/weights" ]; then
      listed=$(wc -l < "$work/weights" | tr -d ' ')
      stale=$(awk 'NR == FNR { have[$1]; next } !($1 in have)' "$work/all" "$work/weights" | wc -l | tr -d ' ')
      echo "timings: $listed tests listed for $package, $stale of them no longer exist"
    else
      echo "timings: none for $package, assigning by name hash"
    fi
    assign "$total" < "$work/all" > "$work/assigned"
    : > "$work/union"
    all_tests_count=$(wc -l < "$work/all" | tr -d ' ')
    for index in $(seq 1 "$total"); do
      names_of "$index" < "$work/assigned" > "$work/shard-$index"
      cat "$work/shard-$index" >> "$work/union"
      regex=$(regex_of < "$work/shard-$index")
      echo "shard $index/$total: $(wc -l < "$work/shard-$index" | tr -d ' ') of $all_tests_count tests, -run pattern ${#regex} bytes"
      if [ "${#regex}" -gt "$MAX_PATTERN_BYTES" ]; then
        echo "shard $index -run pattern is over the limit of $MAX_PATTERN_BYTES bytes" >&2
        exit 1
      fi
      # Each shard's pattern must select exactly the shard's tests and nothing else.
      # `go test -list` applies the pattern the same way -run does.
      if [ -n "${CHECK_PATTERNS:-}" ]; then
        # shellcheck disable=SC2086
        go test ${GO_TEST_LIST_FLAGS:-} -list "$regex" "$package" |
          grep -E '^(Test|Fuzz|Example)[A-Za-z0-9_]*$' | LC_ALL=C sort > "$work/selected-$index"
        if ! diff -u "$work/shard-$index" "$work/selected-$index" > /dev/null; then
          echo "shard $index pattern selects a different set than the shard list" >&2
          diff -u "$work/shard-$index" "$work/selected-$index" >&2 || true
          exit 1
        fi
      fi
    done
    LC_ALL=C sort "$work/union" > "$work/union.sorted"
    if ! diff -u "$work/all" "$work/union.sorted"; then
      echo "the union of the $total shards differs from the full test list of $package" >&2
      exit 1
    fi
    if [ -n "$(LC_ALL=C sort "$work/union" | uniq -d)" ]; then
      echo "tests assigned to more than one shard:" >&2
      LC_ALL=C sort "$work/union" | uniq -d >&2
      exit 1
    fi
    echo "OK: $total shards cover all $(wc -l < "$work/all" | tr -d ' ') tests of $package exactly once"
    ;;
  *)
    usage
    ;;
esac
