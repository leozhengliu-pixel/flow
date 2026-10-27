#!/usr/bin/env bash
# Split the tests of one Go package across CI shards.
#
#   go-test-shard.sh run   PACKAGE INDEX TOTAL   print the -run regex for shard INDEX (1-based)
#   go-test-shard.sh list  PACKAGE INDEX TOTAL   print the test names of shard INDEX, one per line
#   go-test-shard.sh check PACKAGE TOTAL         fail unless the shards partition the package's tests
#
# Run it from the Go module root (api/). PACKAGE is an import path or a relative
# path such as ./cmd/server. Extra flags for `go test -list` come from
# GO_TEST_LIST_FLAGS; pass the flags the real run uses (for example -race) so the
# test binary is compiled once and served from the build cache afterwards.
#
# A test is assigned to a shard by a hash of its name, so adding or removing a
# test never moves any other test between shards. Only top-level Test*, Fuzz* and
# Example* functions are assigned; their subtests run with their parent.
# Benchmarks are not run by `go test` without -bench and are left out.
set -euo pipefail

usage() {
  sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

# All runnable top-level test names of a package, sorted and unique.
all_tests() {
  local package=$1
  # shellcheck disable=SC2086
  go test ${GO_TEST_LIST_FLAGS:-} -list '^(Test|Fuzz|Example)' "$package" |
    grep -E '^(Test|Fuzz|Example)[A-Za-z0-9_]*$' |
    LC_ALL=C sort -u
}

# Print "<shard> <name>" for every test read from stdin. The hash is a plain
# polynomial over the characters of the name so it is the same under mawk,
# gawk and BSD awk.
assign() {
  local total=$1
  awk -v total="$total" '
    BEGIN {
      chars = "_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
    }
    {
      h = 7
      for (i = 1; i <= length($0); i++) {
        h = (h * 131 + index(chars, substr($0, i, 1))) % 2147483629
      }
      print (h % total) + 1, $0
    }
  '
}

shard_tests() {
  local package=$1 index=$2 total=$3
  all_tests "$package" | assign "$total" | awk -v want="$index" '$1 == want { print $2 }'
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
    names=$(shard_tests "$package" "$index" "$total")
    if [ -z "$names" ]; then
      echo "shard $index/$total of $package has no tests" >&2
      exit 1
    fi
    if [ "$command" = list ]; then
      printf '%s\n' "$names"
    else
      printf '^(%s)$\n' "$(printf '%s\n' "$names" | paste -sd'|' -)"
    fi
    ;;
  check)
    [ "$#" -eq 3 ] || usage
    package=$2 total=$3
    validate_shard 1 "$total"
    work=$(mktemp -d)
    trap 'rm -rf "$work"' EXIT
    all_tests "$package" > "$work/all"
    if [ ! -s "$work/all" ]; then
      echo "$package has no tests" >&2
      exit 1
    fi
    : > "$work/union"
    all_tests_count=$(wc -l < "$work/all" | tr -d ' ')
    for index in $(seq 1 "$total"); do
      # Reuse the one list instead of compiling the test binary once per shard.
      assign "$total" < "$work/all" | awk -v want="$index" '$1 == want { print $2 }' > "$work/shard-$index"
      cat "$work/shard-$index" >> "$work/union"
      regex=$(printf '^(%s)$' "$(paste -sd'|' - < "$work/shard-$index")")
      echo "shard $index/$total: $(wc -l < "$work/shard-$index" | tr -d ' ') of $all_tests_count tests, -run pattern ${#regex} bytes"
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
