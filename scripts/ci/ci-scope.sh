#!/usr/bin/env bash
# Emit the CI surfaces that a pull request needs.
#
# Main pushes, schedules, manual runs, workflow changes and unknown root-level
# changes run every surface. Pull requests with a known application-area change
# only run the jobs that can observe that area.
set -euo pipefail

event=${GITHUB_EVENT_NAME:-push}
ref=${GITHUB_REF:-}
base=${CI_SCOPE_BASE_SHA:-${GITHUB_BASE_SHA:-}}
head=${CI_SCOPE_HEAD_SHA:-${GITHUB_SHA:-HEAD}}

if [ "$event" != pull_request ] || [ "$ref" = refs/heads/main ]; then
  printf '%s\n' \
    'all=true' \
    'api=true' \
    'web=true' \
    'sdk=true' \
    'e2e=true' \
    'integration=true' \
    'dialect=true' \
    'packaging=true'
  exit 0
fi

if [ -z "$base" ]; then
  echo 'CI_SCOPE_BASE_SHA is required for pull requests' >&2
  exit 2
fi

all=false
api=false
web=false
sdk=false
e2e=false
integration=false
dialect=false
packaging=false

while IFS= read -r path; do
  case "$path" in
    .github/*|scripts/ci/*|scripts/check-go-coverage.sh|Dockerfile*|.dockerignore)
      all=true
      ;;
    api/*|go.mod|go.sum)
      api=true
      e2e=true
      integration=true
      dialect=true
      ;;
    web/*|scripts/generate-openapi.mjs|docs/openapi.json)
      web=true
      sdk=true
      e2e=true
      ;;
    sdk/typescript/*)
      sdk=true
      ;;
    deploy/*|sandbox-runner/*)
      packaging=true
      ;;
    docs/*|*.md)
      ;;
    *)
      all=true
      ;;
  esac
done < <(git diff --name-only "$base" "$head")

if [ "$all" = true ]; then
  api=true
  web=true
  sdk=true
  e2e=true
  integration=true
  dialect=true
  packaging=true
fi

printf 'all=%s\napi=%s\nweb=%s\nsdk=%s\ne2e=%s\nintegration=%s\ndialect=%s\npackaging=%s\n' \
  "$all" "$api" "$web" "$sdk" "$e2e" "$integration" "$dialect" "$packaging"
