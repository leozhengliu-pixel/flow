#!/usr/bin/env bash
# Refresh scripts/ci/go-test-timings.json from recent successful CI runs on main.
#
#   update-go-test-timings.sh [RUN_ID...]
#
# Every push to main uploads the artifact "go-test-timings" (job "API test
# timings"): the seconds each top-level Go test took in that run, per package.
# This script downloads the artifact of the newest TIMING_RUNS (default 3)
# successful main runs that still have one, or of the RUN_IDs given, and rewrites
# the entries of every package found in them in scripts/ci/go-test-timings.json
# with each test's median time. One run alone is noisy (a test can take 130 s on
# one runner and 235 s on another), so the median over several runs is what the
# shard packing is based on. A test missing from some runs uses the runs it is in.
# Packages that are not in the artifacts keep their old entries.
#
# The file is only written. Committing it is a manual step:
#
#   scripts/ci/update-go-test-timings.sh
#   git diff --stat scripts/ci/go-test-timings.json
#   git commit -am "ci: refresh Go test timings"
#
# go-test-shard.sh packs the tests onto shards by these numbers, so a refresh
# moves tests between shards; the "API vet and shard check" job re-verifies that
# every test still runs exactly once. Refresh every few weeks, or when the shard
# times drift apart. Needs gh (logged in), jq and the repository's origin remote.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
target=$root/scripts/ci/go-test-timings.json
artifact=go-test-timings
wanted=${TIMING_RUNS:-3}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Download the artifact of one run into $work/runs/<run>.json.
download() {
  local run=$1
  mkdir -p "$work/runs" "$work/download"
  rm -rf "$work/download/$run"
  gh run download "$run" --name "$artifact" --dir "$work/download/$run" > /dev/null 2>&1 || return 1
  [ -s "$work/download/$run/go-test-timings.json" ] || return 1
  jq -e 'type == "object" and length > 0 and all(.[]; type == "object")' \
    "$work/download/$run/go-test-timings.json" > /dev/null || return 1
  mv "$work/download/$run/go-test-timings.json" "$work/runs/$run.json"
}

runs=()
if [ "$#" -ge 1 ]; then
  for run in "$@"; do
    download "$run" || {
      echo "run $run has no usable $artifact artifact (expired, or its tests did not all pass)" >&2
      exit 1
    }
    runs+=("$run")
  done
else
  for candidate in $(gh run list --workflow ci.yml --branch main --event push --status success \
    --limit 40 --json databaseId --jq '.[].databaseId'); do
    if download "$candidate"; then
      runs+=("$candidate")
      [ "${#runs[@]}" -ge "$wanted" ] && break
    fi
  done
  if [ "${#runs[@]}" -eq 0 ]; then
    echo "no successful main run in the last 40 has a $artifact artifact" >&2
    exit 1
  fi
fi

old=$work/old.json
if [ -s "$target" ]; then cp "$target" "$old"; else echo '{}' > "$old"; fi

# Per package and test: the median over the runs, rounded to 10 ms.
jq -S -s '
  def median: sort | length as $n
    | if $n % 2 == 1 then .[($n - 1) / 2] else (.[$n / 2 - 1] + .[$n / 2]) / 2 end;
  .[0] as $old
  | (.[1:]
      | map(to_entries[] | .key as $pkg | .value | to_entries[] | {pkg: $pkg, test: .key, seconds: .value})
      | group_by(.pkg + " " + .test)
      | map({pkg: .[0].pkg, test: .[0].test, seconds: ((map(.seconds) | median) * 100 | round / 100)})
      | group_by(.pkg)
      | map({key: .[0].pkg, value: (map({key: .test, value: .seconds}) | from_entries)})
      | from_entries) as $new
  | $old + $new
' "$old" "$work"/runs/*.json > "$work/new.json"

mv "$work/new.json" "$target"

echo "updated $target from run(s): ${runs[*]}"
jq -r 'to_entries[] | "\(.key): \(.value | length) tests, \(.value | add | . * 10 | round / 10) s total"' "$target"
