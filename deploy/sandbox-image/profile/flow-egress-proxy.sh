# Flow coding sandbox: egress proxy identity for login shells (debug
# terminals run `bash -l`). The container environment carries the proxy URL
# without credentials; the runner adds this sandbox's id and current token
# (read from the token file) the same way it does for every process it
# starts. Prints nothing without a proxy and never fails the shell.
if [ -n "${FLOW_SANDBOX_ID:-}" ] && [ -x /opt/flow/bin/flow-runner ]; then
  flow_proxy_env="$(/opt/flow/bin/flow-runner proxy-env 2>/dev/null)" && eval "$flow_proxy_env"
  unset flow_proxy_env
fi
