#!/usr/bin/env sh
# Renders the egress proxy's squid.conf with a stub squid binary and checks it.
set -eu
repo=$1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
printf '#!/bin/sh\necho "squid $*" > "%s/squid.args"\n' "$work" > "$work/bin/squid"
chmod +x "$work/bin/squid"
entry="$repo/deploy/egress-proxy/entrypoint.sh"
default_list="$repo/deploy/egress-proxy/allowlist.default.txt"
status=0
check() { if grep -q -- "$2" "$1"; then :; else echo "FAIL egress: '$2' missing in $(basename "$1")"; status=1; fi; }
absent() { if grep -q -- "$2" "$1"; then echo "FAIL egress: '$2' must not be in $(basename "$1")"; status=1; fi; }

# Helper mode.
printf 'extra.example.com\n.example.com\n' > "$work/extra.txt"
env -i PATH="$work/bin:/usr/bin:/bin" FLOW_EGRESS_RUNTIME_DIR="$work/helper" FLOW_EGRESS_DEFAULT_ALLOWLIST_FILE="$default_list" \
  FLOW_EGRESS_ALLOWLIST_FILE="$work/extra.txt" FLOW_CODING_EGRESS_ALLOWLIST='*.Corp.net,corp.net,a.corp.net' \
  FLOW_CODING_EGRESS_ACL_SECRET=s3cret FLOW_EGRESS_FLOW_URL=http://flow:8080 FLOW_EGRESS_BLOCK_CIDRS=203.0.113.0/24 sh "$entry"
conf="$work/helper/squid.conf"
check "$conf" "auth_param basic program $work/helper/flow-egress-acl auth --concurrency"
check "$conf" "external_acl_type flow_egress ttl=60"
check "$conf" "http_access deny to_metadata"
check "$conf" "169.254.0.0/16"
check "$conf" "203.0.113.0/24"
check "$conf" "http_access deny !sandbox_auth"
check "$conf" "http_access allow flow_egress_allowed"
absent "$conf" "http_access allow allowlist"
check "$work/helper/allowlist.txt" "^\.corp\.net$"
absent "$work/helper/allowlist.txt" "^corp\.net$"
absent "$work/helper/allowlist.txt" "^a\.corp\.net$"
absent "$work/helper/allowlist.txt" "^extra\.example\.com$"
check "$work/helper/allowlist.txt" "^registry\.npmjs\.org$"
check "$work/helper/flow-egress-acl" "FLOW_CODING_EGRESS_ACL_SECRET_FILE='$work/helper/acl-secret'"
absent "$work/helper/flow-egress-acl" "s3cret"
[ "$(cat "$work/helper/acl-secret")" = s3cret ] || { echo "FAIL egress: secret file"; status=1; }
check "$work/squid.args" "-N -f $conf"
# The http_access deny rules for private destinations come before any allow.
deny_line=$(grep -n 'http_access deny to_private' "$conf" | cut -d: -f1)
allow_line=$(grep -n 'http_access allow' "$conf" | head -1 | cut -d: -f1)
[ "$deny_line" -lt "$allow_line" ] || { echo "FAIL egress: private deny must precede allow"; status=1; }

# Static mode, default list off.
env -i PATH="$work/bin:/usr/bin:/bin" FLOW_EGRESS_RUNTIME_DIR="$work/static" FLOW_EGRESS_DEFAULT_ALLOWLIST_FILE="$default_list" \
  FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST=off FLOW_CODING_EGRESS_ALLOWLIST=only.example.org sh "$entry"
conf="$work/static/squid.conf"
check "$conf" "http_access allow allowlist"
absent "$conf" "auth_param"
absent "$work/static/allowlist.txt" "registry.npmjs.org"
check "$work/static/allowlist.txt" "^only\.example\.org$"

# Helper mode without a Flow URL must refuse to start.
if env -i PATH="$work/bin:/usr/bin:/bin" FLOW_EGRESS_RUNTIME_DIR="$work/bad" FLOW_EGRESS_DEFAULT_ALLOWLIST_FILE="$default_list" FLOW_CODING_EGRESS_ACL_SECRET=x sh "$entry" 2>/dev/null; then
  echo "FAIL egress: started without FLOW_EGRESS_FLOW_URL"; status=1
fi

[ $status = 0 ] && echo "ok   egress entrypoint"
exit $status
