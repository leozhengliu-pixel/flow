#!/usr/bin/env sh
# Offline tests for the Flow chart and the egress proxy entrypoint. Never
# talks to a cluster: only `helm lint` and `helm template`.
#
#   deploy/helm/flow/tests/run.sh           check
#   UPDATE_GOLDEN=1 deploy/helm/flow/tests/run.sh   regenerate golden files
set -eu

tests=$(cd "$(dirname "$0")" && pwd)
chart=$(dirname "$tests")
repo=$(cd "$chart/../../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# Isolate helm from any kubeconfig or repository cache on the machine.
export KUBECONFIG=/dev/null HELM_CACHE_HOME="$work/cache" HELM_CONFIG_HOME="$work/config" HELM_DATA_HOME="$work/data"

status=0
for scenario in "$tests"/scenarios/*.yaml; do
  name=$(basename "$scenario" .yaml)
  helm lint --strict "$chart" -f "$scenario" --namespace flow >"$work/lint.log" 2>&1 || { cat "$work/lint.log"; echo "FAIL lint $name"; status=1; continue; }
  helm template flow "$chart" -f "$scenario" --namespace flow >"$work/$name.yaml" 2>"$work/template.log" || { cat "$work/template.log"; echo "FAIL template $name"; status=1; continue; }
  if [ "${UPDATE_GOLDEN:-}" = 1 ]; then
    cp "$work/$name.yaml" "$tests/golden/$name.yaml"
  elif ! diff -u -B -I "checksum/" "$tests/golden/$name.yaml" "$work/$name.yaml" >"$work/diff.log"; then
    head -80 "$work/diff.log"; echo "FAIL golden $name (UPDATE_GOLDEN=1 to accept)"; status=1
  fi
  echo "ok   $name"
done

for scenario in "$tests"/invalid/*.yaml; do
  name=$(basename "$scenario" .yaml)
  if helm template flow "$chart" -f "$scenario" --namespace flow >/dev/null 2>&1; then
    echo "FAIL invalid/$name rendered but must be rejected"; status=1
  else
    echo "ok   invalid/$name rejected"
  fi
done

python3 -I "$tests/check.py" "$work" "$repo" || status=1
sh "$tests/egress-entrypoint-test.sh" "$repo" || status=1
exit $status
