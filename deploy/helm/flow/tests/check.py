"""Structural assertions over the rendered scenarios (run by run.sh).

Usage: python3 -I check.py <rendered-dir> <repo-root>
"""
import pathlib
import re
import sys

import yaml

rendered = pathlib.Path(sys.argv[1])
repo = pathlib.Path(sys.argv[2])
failures = []


def fail(scenario, message):
    failures.append(f"{scenario}: {message}")


def load(name):
    path = rendered / f"{name}.yaml"
    return [doc for doc in yaml.safe_load_all(path.read_text()) if doc]


def find(docs, kind, name=None):
    return [d for d in docs if d["kind"] == kind and (name is None or d["metadata"]["name"] == name)]


def flow_env(docs):
    env = {}
    for cm in find(docs, "ConfigMap", "flow"):
        env.update(cm.get("data") or {})
    for secret in find(docs, "Secret", "flow"):
        env.update(secret.get("stringData") or {})
    for deployment in find(docs, "Deployment", "flow"):
        for item in deployment["spec"]["template"]["spec"]["containers"][0].get("env", []):
            env[item["name"]] = item.get("value", "<from>")
    return env


def pod_specs(docs, namespace):
    for doc in docs:
        if doc["metadata"].get("namespace") != namespace:
            continue
        if doc["kind"] in ("Deployment", "DaemonSet", "StatefulSet"):
            yield doc["kind"] + "/" + doc["metadata"]["name"], doc["spec"]["template"]["spec"]
        elif doc["kind"] == "Pod":
            yield "Pod/" + doc["metadata"]["name"], doc["spec"]


# Exactly the Kubernetes provider's API calls (api/internal/sandbox/kubernetes).
EXPECTED_ROLE = {
    "pods": {"create", "get", "list", "delete"},
    "pods/log": {"get"},
    "pods/exec": {"create", "get"},
    "secrets": {"create", "get", "list", "delete", "patch"},
    "persistentvolumeclaims": {"create", "get", "list", "delete"},
    "namespaces": {"get"},
}

SCENARIOS = {
    # name: (runtime class env, host users, volumesnapshot, helm policies, flow policies, egress)
    "coding-runc-archive": ("", "true", False, True, False, True),
    "coding-gvisor-full": ("gvisor", "false", False, True, False, True),
    "coding-kata-volumesnapshot": ("kata", "true", True, False, True, True),
    "coding-netpol-off": ("", "true", False, False, False, False),
    "production-ha": ("", "true", False, True, False, True),
}

for name, (runtime, host_users, volumesnapshot, helm_policies, flow_policies, egress) in SCENARIOS.items():
    docs = load(name)
    env = flow_env(docs)
    if env.get("FLOW_CODING_K8S_RUNTIME_CLASS") != runtime:
        fail(name, f"runtime class env {env.get('FLOW_CODING_K8S_RUNTIME_CLASS')!r}")
    if env.get("FLOW_CODING_K8S_HOST_USERS") != host_users:
        fail(name, "host users env")
    if env.get("FLOW_CODING_K8S_NETWORK_POLICY") != str(flow_policies).lower():
        fail(name, "FLOW_CODING_K8S_NETWORK_POLICY")
    if env.get("FLOW_CODING_K8S_NAMESPACE") != "flow-sandboxes" or env.get("FLOW_CODING_K8S_FLOW_NAMESPACE") != "flow":
        fail(name, "namespaces")

    # RBAC: exactly the provider's verbs, only in the sandbox namespace.
    roles = find(docs, "Role")
    if len(roles) != 1 or roles[0]["metadata"]["namespace"] != "flow-sandboxes":
        fail(name, "expected one Role in flow-sandboxes")
    else:
        rules = {}
        for rule in roles[0]["rules"]:
            for resource in rule["resources"]:
                rules[resource] = set(rule["verbs"])
        expected = dict(EXPECTED_ROLE)
        if flow_policies:
            expected["networkpolicies"] = {"get", "create", "patch"}
        if volumesnapshot:
            expected["volumesnapshots"] = {"create", "get", "list", "delete"}
        if rules != expected:
            fail(name, f"Role rules {rules} != {expected}")
    for binding in find(docs, "RoleBinding"):
        subject = binding["subjects"][0]
        if subject["name"] != "flow" or subject["namespace"] != "flow":
            fail(name, "RoleBinding subject")
    for cluster_role in find(docs, "ClusterRole"):
        for rule in cluster_role["rules"]:
            if set(rule["verbs"]) - {"get", "list"}:
                fail(name, "ClusterRole must be read-only")
            if set(rule["resources"]) - {"runtimeclasses", "storageclasses", "volumesnapshotclasses"}:
                fail(name, f"ClusterRole resources {rule['resources']}")
            if "volumesnapshotclasses" in rule["resources"] and not volumesnapshot:
                fail(name, "volumesnapshotclasses without the strategy")

    policies = {p["metadata"]["name"] for p in find(docs, "NetworkPolicy") if p["metadata"]["namespace"] == "flow-sandboxes"}
    has_helm = {"flow-sandbox-default-deny", "flow-sandbox-egress"} <= policies
    if has_helm != helm_policies:
        fail(name, f"helm sandbox policies present={has_helm}")
    if helm_policies:
        deny = find(docs, "NetworkPolicy", "flow-sandbox-default-deny")[0]["spec"]
        if deny["podSelector"]["matchLabels"] != {"flow.dev/managed-by": "flow"} or set(deny["policyTypes"]) != {"Ingress", "Egress"}:
            fail(name, "default deny policy")
        egress_rules = find(docs, "NetworkPolicy", "flow-sandbox-egress")[0]["spec"]["egress"]
        ports = sorted(p["port"] for rule in egress_rules for p in rule["ports"])
        runtime_port = 8090 if name in ("coding-gvisor-full", "production-ha") else 8080
        if ports != sorted([53, 53, runtime_port] + ([3128] if egress else [])):
            fail(name, f"sandbox egress ports {ports}")
        for rule in egress_rules:
            for peer in rule["to"]:
                if "ipBlock" in peer:
                    fail(name, "sandboxes must not get ipBlock egress")

    proxies = find(docs, "Deployment", "flow-egress")
    if bool(proxies) != egress:
        fail(name, "egress proxy presence")
    if egress:
        if env.get("FLOW_CODING_EGRESS_PROXY_URL") != "http://flow-egress.flow-sandboxes.svc.cluster.local:3128":
            fail(name, "proxy url env")
        proxy_policies = find(docs, "NetworkPolicy", "flow-egress")
        if name != "coding-netpol-off" and proxy_policies:
            blocks = proxy_policies[0]["spec"]["egress"][-1]["to"][0]["ipBlock"]["except"]
            for cidr in ("169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"):
                if cidr not in blocks:
                    fail(name, f"proxy policy must exclude {cidr}")
        flow_acl = env.get("FLOW_CODING_EGRESS_ACL_SECRET")
        proxy_acl = [s for s in find(docs, "Secret", "flow-egress-acl")]
        if name == "production-ha":
            if flow_acl != "<from>" or proxy_acl:
                fail(name, "existing ACL secret must be referenced, not rendered")
        elif not proxy_acl or proxy_acl[0]["stringData"]["acl-secret"] != flow_acl:
            fail(name, "Flow and proxy ACL secrets differ")

    probes = find(docs, "Pod", "flow-netpol-probe")
    if bool(probes) != (name != "coding-netpol-off"):
        fail(name, "NetworkPolicy probe presence")
    elif probes and probes[0]["metadata"]["labels"].get("flow.dev/managed-by") != "flow":
        fail(name, "probe must carry the sandbox label so the policies apply")

    rc_objects = find(docs, "RuntimeClass")
    if name == "coding-gvisor-full":
        if not rc_objects or rc_objects[0]["handler"] != "runsc" or rc_objects[0]["metadata"]["name"] != "gvisor":
            fail(name, "gVisor RuntimeClass")
    elif rc_objects:
        fail(name, "unexpected RuntimeClass")

    if name != "coding-netpol-off":
        if not find(docs, "ResourceQuota") or not find(docs, "LimitRange"):
            fail(name, "ResourceQuota/LimitRange")
    namespace = find(docs, "Namespace", "flow-sandboxes")
    if not namespace or namespace[0]["metadata"]["labels"].get("pod-security.kubernetes.io/enforce") != "restricted":
        fail(name, "sandbox namespace must enforce restricted")

    # Every Pod the chart puts in the sandbox namespace passes the restricted
    # profile and declares limits (the ResourceQuota requires them).
    for ref, spec in pod_specs(docs, "flow-sandboxes"):
        if spec.get("automountServiceAccountToken") is not False:
            fail(name, f"{ref}: service account token mounted")
        if spec.get("hostNetwork") or spec.get("hostPID") or spec.get("hostIPC"):
            fail(name, f"{ref}: host namespaces")
        if any("hostPath" in v for v in spec.get("volumes", [])):
            fail(name, f"{ref}: hostPath")
        pod_sc = spec.get("securityContext", {})
        for container in spec.get("initContainers", []) + spec["containers"]:
            sc = container.get("securityContext", {})
            if not (sc.get("runAsNonRoot") or pod_sc.get("runAsNonRoot")):
                fail(name, f"{ref}/{container['name']}: runAsNonRoot")
            if sc.get("allowPrivilegeEscalation") is not False or sc.get("capabilities", {}).get("drop") != ["ALL"]:
                fail(name, f"{ref}/{container['name']}: privilege escalation or capabilities")
            if (sc.get("seccompProfile") or pod_sc.get("seccompProfile", {})).get("type") != "RuntimeDefault":
                fail(name, f"{ref}/{container['name']}: seccomp")
            limits = container.get("resources", {}).get("limits", {})
            if "cpu" not in limits or "memory" not in limits:
                fail(name, f"{ref}/{container['name']}: cpu and memory limits")
        if ref.startswith("DaemonSet/") or ref.startswith("Pod/"):
            if name != "coding-netpol-off" and spec.get("nodeSelector", {}) != {"flow.dev/pool": "sandbox"}:
                fail(name, f"{ref}: must run on the sandbox pool")

# Every FLOW_CODING_* / FLOW_GITHUB_APP_* variable the config reads is mapped.
config_source = (repo / "api/internal/config/coding.go").read_text()
variables = set(re.findall(r'"(FLOW_(?:CODING|GITHUB_APP)_[A-Z0-9_]+)"', config_source))
env = flow_env(load("coding-gvisor-full"))
missing = sorted(v for v in variables if v not in env and v + "_FILE" not in env)
if missing:
    fail("coding-gvisor-full", f"unmapped variables: {missing}")
expected_values = {
    "FLOW_CODING_IMAGE": "registry.example.com/flow-sandbox@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "FLOW_CODING_RUNTIME_ADDR": ":8090",
    "FLOW_CODING_RUNTIME_URL": "http://flow.flow.svc.cluster.local:8090",
    "FLOW_CODING_BROWSER": "off",
    "FLOW_CODING_WARM_POOL": "medium=1,small=2",
    "FLOW_CODING_SIZE_SMALL": "cpu=2,memory=4Gi,ephemeral=10Gi,pids=2048",
    "FLOW_CODING_K8S_NODE_SELECTOR": "flow.dev/pool=sandbox",
    "FLOW_CODING_K8S_TOLERATIONS": "flow.dev/pool=sandbox:NoSchedule",
    "FLOW_CODING_K8S_FLOW_POD_SELECTOR": "app.kubernetes.io/instance=flow,app.kubernetes.io/name=flow",
    "FLOW_CODING_K8S_PROXY_POD_SELECTOR": "app.kubernetes.io/instance=flow,app.kubernetes.io/name=flow-egress",
    "FLOW_CODING_MODEL_PRICES": '{"claude-sonnet-4-5":{"input":3,"output":15}}',
    "FLOW_CODING_LLM_OPENAI_WIRE_API": "chat",
    "FLOW_CODING_MODEL_INFO": '{"glm-5":{"contextWindow":200000,"label":"GLM-5"},"gpt-5-codex":{"description":"Multi-file changes","group":"frontier","label":"GPT-5 Codex"}}',
    "FLOW_CODING_LOCAL_COMMAND": "/opt/development/launcher",
    "FLOW_CODING_LOCAL_ROOT": "/tmp/development-sandboxes",
    "FLOW_CODING_EGRESS_ALLOWLIST": ".corp-mirror.example.net,pypi.internal.example.com",
    "FLOW_CODING_INSTANCE_ID": "flow-prod",
    "FLOW_CODING_MCP_SERVERS": "mcp-sourcebot-production,mcp-docs",
}
for key, want in expected_values.items():
    if env.get(key) != want:
        fail("coding-gvisor-full", f"{key}={env.get(key)!r}, want {want!r}")

# OpenSandbox: no sandbox namespace or RBAC of Flow's own; the API key comes
# from a Secret; the egress proxy runs in the release namespace; every
# FLOW_CODING_OPENSANDBOX_* variable is mapped.
name = "coding-opensandbox"
docs = load(name)
env = flow_env(docs)
for kind in ("Role", "RoleBinding", "ClusterRole", "Namespace", "ResourceQuota", "LimitRange", "RuntimeClass"):
    if find(docs, kind):
        fail(name, f"unexpected {kind} with provider=opensandbox")
for doc in docs:
    if doc["metadata"].get("namespace", "flow") not in ("flow", "opensandbox"):
        fail(name, f"unexpected namespace for {doc['kind']}/{doc['metadata']['name']}")
if env.get("FLOW_CODING_PROVIDER") != "opensandbox" or env.get("FLOW_CODING_OPENSANDBOX_API_KEY") != "<from>":
    fail(name, "provider and API key from the Secret")
if "FLOW_CODING_OPENSANDBOX_API_KEY" in (find(docs, "Secret", "flow")[0].get("stringData") or {}):
    fail(name, "the API key must not be rendered into the chart's Secret")
if env.get("FLOW_CODING_RUNTIME_URL") != "http://flow.flow.svc.cluster.local:8090":
    fail(name, f"runtime url {env.get('FLOW_CODING_RUNTIME_URL')}")
if env.get("FLOW_CODING_EGRESS_PROXY_URL") != "http://flow-egress.flow.svc.cluster.local:3128":
    fail(name, "egress proxy must run in the release namespace")
if find(docs, "Deployment", "flow")[0]["spec"]["template"]["spec"]["automountServiceAccountToken"] is not False:
    fail(name, "Flow needs no Kubernetes API access with opensandbox")
source = (repo / "api/internal/config/coding_opensandbox.go").read_text()
for variable in sorted(set(re.findall(r'"(FLOW_CODING_OPENSANDBOX_[A-Z0-9_]+)"', source))):
    if variable not in env and variable != "FLOW_CODING_OPENSANDBOX_SIZE_":
        fail(name, f"unmapped variable {variable}")
fenced = find(docs, "NetworkPolicy", "flow-opensandbox-sandboxes")
if not fenced or fenced[0]["metadata"]["namespace"] != "opensandbox":
    fail(name, "sandbox NetworkPolicy in the OpenSandbox namespace")
else:
    spec = fenced[0]["spec"]
    if spec["podSelector"]["matchLabels"] != {"flow.dev/managed-by": "flow"}:
        fail(name, "sandbox policy selector")
    if spec["ingress"][0]["from"][0]["namespaceSelector"]["matchLabels"] != {"kubernetes.io/metadata.name": "opensandbox-system"}:
        fail(name, "sandbox ingress must come from the OpenSandbox server namespace")
    ports = sorted(p["port"] for rule in spec["egress"] for p in rule["ports"])
    if ports != [53, 53, 3128, 8090]:
        fail(name, f"sandbox egress ports {ports}")
proxy_policy = find(docs, "NetworkPolicy", "flow-egress")
if not proxy_policy or proxy_policy[0]["spec"]["ingress"][0]["from"][0]["namespaceSelector"]["matchLabels"] != {"kubernetes.io/metadata.name": "opensandbox"}:
    fail(name, "egress proxy must accept the OpenSandbox sandbox namespace")
flow_policy = find(docs, "NetworkPolicy", "flow")[0]["spec"]["ingress"]
if not any(peer.get("namespaceSelector", {}).get("matchLabels") == {"kubernetes.io/metadata.name": "opensandbox"} for rule in flow_policy for peer in rule.get("from", [])):
    fail(name, "Flow must accept the runtime port from the OpenSandbox namespace")

# Plain Flow renders no sandbox objects at all.
docs = load("default")
for doc in docs:
    if doc["metadata"].get("namespace", "flow") != "flow" or doc["kind"] in ("Role", "ClusterRole", "Namespace", "NetworkPolicy"):
        fail("default", f"unexpected {doc['kind']}/{doc['metadata']['name']}")
deployment = find(docs, "Deployment", "flow")[0]
if deployment["spec"]["strategy"]["type"] != "Recreate" or deployment["spec"]["template"]["spec"]["automountServiceAccountToken"] is not False:
    fail("default", "single-replica Deployment must Recreate and not mount the token")

docs = load("production-ha")
deployment = find(docs, "Deployment", "flow")[0]
if "replicas" in deployment["spec"] or deployment["spec"]["strategy"]["type"] != "RollingUpdate":
    fail("production-ha", "HPA-managed rolling Deployment")
if not find(docs, "HorizontalPodAutoscaler") or not find(docs, "PodDisruptionBudget", "flow"):
    fail("production-ha", "HPA and PDB")
if find(docs, "PersistentVolumeClaim"):
    fail("production-ha", "no PVC with S3")
# Operator-trusted private endpoints and the enterprise CA bundle.
env = flow_env(docs)
for key, want in {
    "FLOW_TRUSTED_PRIVATE_ENDPOINTS": "hsb.haier.net,*.corp.example.com",
    "FLOW_TRUSTED_PRIVATE_NETWORKS": "10.218.0.0/16",
    "FLOW_TRUSTED_CA_FILE": "/var/run/flow/trusted-ca/ca.crt",
    "FLOW_CODING_MCP_SERVERS": "mcp-sourcebot-production",
}.items():
    if env.get(key) != want:
        fail("production-ha", f"{key}={env.get(key)!r}, want {want!r}")
pod = deployment["spec"]["template"]["spec"]
mounts = {m["name"]: m for m in pod["containers"][0]["volumeMounts"]}
volumes = {v["name"]: v for v in pod["volumes"]}
if mounts.get("trusted-ca", {}).get("mountPath") != "/var/run/flow/trusted-ca" or volumes.get("trusted-ca", {}).get("secret", {}).get("items") != [{"key": "ca-bundle.pem", "path": "ca.crt"}]:
    fail("production-ha", "enterprise CA bundle must be mounted from its Secret at /var/run/flow/trusted-ca/ca.crt")
default_env = flow_env(load("default"))
if any(key.startswith("FLOW_TRUSTED_") for key in default_env):
    fail("default", "no trusted private endpoints unless the operator lists them")
# Personal connections and review import: plain values in the ConfigMap, the
# Google client secret only in the Secret, the dev-only base URL never.
config_data = find(docs, "ConfigMap", "flow")[0].get("data") or {}
secret_data = find(docs, "Secret", "flow")[0].get("stringData") or {}
for key, want in {
    "FLOW_GOOGLE_OAUTH_CLIENT_ID": "google-client.apps.example.com",
    "FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL": "30m",
    "FLOW_NOTION_CONNECTION_URL": "https://www.notion.com/connections/flow",
    "FLOW_REVIEW_IMPORT": "on",
    "FLOW_REVIEW_IMPORT_INTERVAL": "15m",
}.items():
    if config_data.get(key) != want:
        fail("production-ha", f"ConfigMap {key}={config_data.get(key)!r}, want {want!r}")
if secret_data.get("FLOW_GOOGLE_OAUTH_CLIENT_SECRET") != "google-secret-value" or "FLOW_GOOGLE_OAUTH_CLIENT_SECRET" in config_data:
    fail("production-ha", "the Google OAuth client secret belongs in the Secret only")
for key in default_env:
    if key.startswith(("FLOW_GOOGLE_", "FLOW_NOTION_", "FLOW_REVIEW_IMPORT", "FLOW_DEV_", "FLOW_SECURITY_", "FLOW_TRUST_PROXY")):
        fail("default", f"{key} must not render unless configured (Flow's own defaults apply)")

# Security headers: the chart passes what the operator set and nothing else.
for key, want in {
    "FLOW_TRUST_PROXY_HEADERS": "true",
    "FLOW_TRUSTED_PROXIES": "10.0.0.0/8,fd00::/8",
    "FLOW_SECURITY_CSP": "enforce",
    "FLOW_SECURITY_CSP_REPORT_URI": "https://csp-reports.example.com/flow",
    "FLOW_SECURITY_FRAME_ANCESTORS": "'self' https://portal.example.com",
    "FLOW_SECURITY_HSTS_MAX_AGE": "15552000",
    "FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS": "true",
}.items():
    if config_data.get(key) != want:
        fail("production-ha", f"ConfigMap {key}={config_data.get(key)!r}, want {want!r}")
for key in ("FLOW_SECURITY_HEADERS", "FLOW_SECURITY_HSTS_PRELOAD", "FLOW_SECURITY_HEADERS_SKIP"):
    if key in config_data:
        fail("production-ha", f"{key} must not render unless configured")

if failures:
    for failure in failures:
        print("FAIL", failure)
    sys.exit(1)
print("ok   structural checks")
