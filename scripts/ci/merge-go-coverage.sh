#!/usr/bin/env sh
# Merge Go coverage profiles into one.
#
#   merge-go-coverage.sh OUTPUT PROFILE...
#
# Every profile must use the same -covermode. Blocks that appear in more than one
# profile (the same package measured by several shards) are collapsed into a
# single line whose count is the sum of the inputs, so statement totals are not
# double counted by scripts that read the raw profile.
set -eu

if [ "$#" -lt 2 ]; then
  echo "usage: $0 OUTPUT PROFILE..." >&2
  exit 2
fi

output=$1
shift

mode=$(sed -n '1s/^mode: //p' "$1")
if [ -z "$mode" ]; then
  echo "$1 does not start with a mode line" >&2
  exit 1
fi

for profile in "$@"; do
  if [ "$(sed -n '1s/^mode: //p' "$profile")" != "$mode" ]; then
    echo "$profile uses a different coverage mode than $1 ($mode)" >&2
    exit 1
  fi
done

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# Each block line is "file:start.col,end.col numStatements count".
awk -v mode="$mode" '
  FNR == 1 { next }
  NF == 0 { next }
  {
    key = $1 " " $2
    if (!(key in count)) order[++n] = key
    if (mode == "set") {
      if ($3 > 0) count[key] = 1
      else if (!(key in count)) count[key] = 0
    } else {
      count[key] += $3
    }
  }
  END {
    for (i = 1; i <= n; i++) print order[i] " " count[order[i]]
  }
' "$@" | LC_ALL=C sort > "$tmp"

{
  echo "mode: $mode"
  cat "$tmp"
} > "$output"
