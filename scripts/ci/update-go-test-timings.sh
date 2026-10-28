#!/usr/bin/env bash
# Refresh scripts/ci/go-test-timings.json from the latest successful CI run on main.
#
#   update-go-test-timings.sh [RUN_ID]
#
# Every push to main uploads the artifact "go-test-timings" (job "API test
# timings"): the seconds each top-level Go test took in that run, per package.
# This script downloads the artifact of the newest successful main run that still
# has one (or of RUN_ID), and replaces the entries of every package found in it
# in scripts/ci/go-test-timings.json. Packages that are not in the artifact keep
# their old entries. The file is written; committing it is a manual step:
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

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

download() {
  local run=$1
  rm -rf "$work/artifact"
  gh run download "$run" --name "$artifact" --dir "$work/artifact" > /dev/null 2>&1 &&
    [ -s "$work/artifact/go-test-timings.json" ]
}

if [ "$#" -ge 1 ]; then
  run=$1
  download "$run" || {
    echo "run $run has no $artifact artifact (expired, or the run did not finish its tests)" >&2
    exit 1
  }
else
  run=""
  for candidate in $(gh run list --workflow ci.yml --branch main --event push --status success \
    --limit 30 --json databaseId --jq '.[].databaseId'); do
    if download "$candidate"; then
      run=$candidate
      break
    fi
  done
  if [ -z "$run" ]; then
    echo "no successful main run in the last 30 has a $artifact artifact" >&2
    exit 1
  fi
fi

if ! jq -e 'type == "object" and length > 0 and all(.[]; type == "object")' \
  "$work/artifact/go-test-timings.json" > /dev/null; then
  echo "the $artifact artifact of run $run is not a non-empty package-to-timings object" >&2
  exit 1
fi

old=$work/old.json
if [ -s "$target" ]; then cp "$target" "$old"; else echo '{}' > "$old"; fi

# Packages in the artifact replace the old entries; the others stay.
jq -S -s '
  .[0] as $old | .[1] as $new
  | reduce ($new | keys[]) as $pkg ($old; .[$pkg] = $new[$pkg])
' "$old" "$work/artifact/go-test-timings.json" > "$work/new.json"

mv "$work/new.json" "$target"

echo "updated $target from run $run"
jq -r 'to_entries[] | "\(.key): \(.value | length) tests, \(.value | add | . * 10 | round / 10) s total"' "$target"
