# Coding sessions: operator guide

Coding sessions let Flow's agent write code in a sandbox and open a draft pull
request (GitHub) or merge request (GitLab). Each session runs in its own Pod in
a dedicated namespace. This guide covers installing and configuring them with
the Helm chart in `deploy/helm/flow`.

Related documents:

- [Security model](coding-sessions-security.md): what a sandbox can and cannot reach, and the risks of plain runc.
- [Operations runbook](coding-sessions-operations.md): stuck sessions, orphans, snapshots, quota, key rotation, upgrades.
- [Configuration reference](configuration.md#coding-sessions): every `FLOW_CODING_*` and `FLOW_GITHUB_APP_*` variable.
- [Design](coding-sessions-design.md): architecture, state machine and protocols.

## Prerequisites

| Requirement | Notes |
| --- | --- |
| Kubernetes 1.28 or newer | The chart uses only GA APIs (`apps/v1`, `networking.k8s.io/v1`, `policy/v1`, `autoscaling/v2`, `node.k8s.io/v1`). |
| A CNI that **enforces** NetworkPolicy | Calico, Cilium, Antrea, or a managed equivalent (GKE Dataplane V2, AKS with Azure/Calico policy, EKS with the VPC CNI network policy agent). Flannel alone does **not** enforce policies. Run the probe in [Verify NetworkPolicy enforcement](coding-sessions-security.md#verify-networkpolicy-enforcement) after installing. |
| A dedicated node pool for sandboxes (strongly recommended) | Labelled and tainted, with no cloud credentials. See [Dedicated node pool](#dedicated-node-pool). |
| Object storage | The default `archive` snapshot strategy stores workspace archives through `FLOW_STORAGE_DRIVER` (the data volume or S3). |
| Optional: a RuntimeClass | gVisor (`runsc`) or Kata Containers. Recommended, not required. Without one, sandboxes use runc and the [runc risks](coding-sessions-security.md#limits-of-plain-runc) apply. |
| Not required: user namespaces | `hostUsers: false` is optional (`sandboxes.hostUsers=false`, Kubernetes 1.33+ with a runtime that supports idmapped mounts). |
| Not required: VolumeSnapshot | Use the default `archive` strategy. `volumesnapshot` needs a CSI driver with snapshot support and the `snapshot.storage.k8s.io/v1` CRDs. |
| Model provider keys | Anthropic for Claude Code, OpenAI for Codex, or any OpenAI-compatible endpoint for OpenCode. Keys stay in Flow; sandboxes reach models only through Flow's LLM proxy. |
| A GitHub App and/or a GitLab connection | See [GitHub App setup](#github-app-setup) and [GitLab setup](#gitlab-setup). |

The images:

- `ghcr.io/leozhengliu-pixel/flow`: Flow (API and web).
- `ghcr.io/leozhengliu-pixel/flow-sandbox`: the sandbox image (runner, git, gh, glab, agent CLIs, Playwright).
- `ghcr.io/leozhengliu-pixel/flow-egress`: Squid plus the `flow-api egress-acl` helper, built from `deploy/egress-proxy/Dockerfile` (`docker build -f deploy/egress-proxy/Dockerfile .`). Its tag must match Flow's version.

Pin image digests in production (`image.digest`, `coding.image.digest`, `egressProxy.image.digest`).

## What the chart installs

In the release namespace (for example `flow`):

- the Flow Deployment, Service (plus an optional runtime port), optional Ingress, ConfigMap and Secret with every setting, data PVC or S3 storage, probes, PodDisruptionBudget and optional HPA;
- a ServiceAccount for Flow; optional bundled PostgreSQL, MySQL or Redis for evaluation.

In the sandbox namespace (`sandboxes.namespace.name`, default `flow-sandboxes`), when coding sessions are enabled with the kubernetes provider:

- the Namespace (optional) with `pod-security.kubernetes.io/enforce: restricted`;
- a Role and RoleBinding that give Flow's ServiceAccount exactly the provider's verbs in this namespace only (pods, pods/log, pods/exec, secrets, persistentvolumeclaims, resourcequotas, events, plus networkpolicies when Flow manages them and volumesnapshots for that strategy);
- an optional read-only ClusterRole for preflight (RuntimeClass, StorageClass, VolumeSnapshotClass);
- the `flow-sandbox` ServiceAccount (no RBAC, token never mounted);
- default-deny NetworkPolicies, a ResourceQuota and a LimitRange;
- the egress proxy Deployment, Service, PodDisruptionBudget and NetworkPolicy;
- an optional RuntimeClass object and an optional image pre-pull DaemonSet;
- a `helm test` Pod that verifies NetworkPolicy enforcement.

## Install

### 1. Prepare the node pool

```bash
kubectl label node <node> flow.dev/pool=sandbox
kubectl taint node <node> flow.dev/pool=sandbox:NoSchedule
```

These match the chart defaults (`sandboxes.nodeSelector` and `sandboxes.tolerations`). See [Dedicated node pool](#dedicated-node-pool) for the node-level settings.

### 2. Create secrets

Keep secrets out of values files. Create one Secret whose keys are variable names and reference it with `secrets.existingSecret`:

```bash
kubectl create namespace flow
kubectl -n flow create secret generic flow-env \
  --from-literal=FLOW_CONNECTOR_SECRET_KEY="$(openssl rand -base64 32)" \
  --from-literal=FLOW_CODING_LLM_ANTHROPIC_API_KEY=sk-ant-... \
  --from-literal=FLOW_CODING_LLM_OPENAI_API_KEY=sk-... \
  --from-literal=FLOW_GITHUB_APP_WEBHOOK_SECRET="$(openssl rand -hex 32)" \
  --from-literal=FLOW_GITHUB_APP_CLIENT_SECRET=...
kubectl -n flow create secret generic flow-github-app --from-file=private-key.pem=./flow-agent.private-key.pem
kubectl -n flow create secret generic flow-db --from-literal=url='postgres://flow:...@db.example.com:5432/flow?sslmode=require'
```

`FLOW_CODING_SECRET_KEY` falls back to `FLOW_CONNECTOR_SECRET_KEY`. Back this key up: losing it makes every environment secret and uploaded signing key unreadable.

The egress ACL secret is generated by the chart and kept across upgrades. To supply your own, create a Secret with the same name and key in **both** namespaces and set `egressProxy.aclHelper.existingSecret`.

### 3. Values

A production-shaped `values-prod.yaml`:

```yaml
image:
  digest: sha256:...
config:
  appUrl: https://flow.example.com
secrets:
  existingSecret: flow-env
database:
  driver: postgres
  existingSecret: {name: flow-db, key: url}
redis:
  mode: standalone
  url: rediss://redis.example.com:6380/0
storage:
  driver: s3
  persistence: {enabled: false}
  s3: {bucket: flow-uploads, region: eu-west-1, existingSecret: {name: flow-s3}}
replicaCount: 2
ingress:
  enabled: true
  className: nginx
  hosts: [{host: flow.example.com, paths: [{path: /, pathType: Prefix}]}]
  tls: [{secretName: flow-tls, hosts: [flow.example.com]}]

coding:
  enabled: true
  provider: kubernetes
  runtimePort: {enabled: false}
  image:
    digest: sha256:...
  llm:
    models:
      claudeCode: [claude-sonnet-4-5, claude-opus-4-1]
      codex: [gpt-5-codex, gpt-5]
    prices:
      claude-sonnet-4-5: {input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75}
  githubApp:
    appId: "123456"
    slug: flow-agent
    clientId: Iv1.0123456789abcdef
    privateKeySecret: {name: flow-github-app, key: private-key.pem}

sandboxes:
  runtimeClass: {name: ""}        # "" = runc; gvisor or kata when available
  resourceQuota:
    hard: {pods: "30", requests.cpu: "64", limits.cpu: "64", requests.memory: 512Gi, limits.memory: 512Gi,
           requests.ephemeral-storage: 1Ti, limits.ephemeral-storage: 1Ti, persistentvolumeclaims: "40",
           requests.storage: 1Ti, secrets: "200"}
egressProxy:
  allowlist: [.npm.pkg.github.com]
```

More than one Flow replica requires PostgreSQL or MySQL, Redis coordination and shared storage (S3 or a ReadWriteMany volume); the chart refuses to render otherwise. Without Redis, Flow is single-instance by design, and the coding GC and warm pool rely on the Redis coordination lock when there are several replicas.

### 4. Install and verify

```bash
helm lint deploy/helm/flow -f values-prod.yaml
helm template flow deploy/helm/flow -n flow -f values-prod.yaml > /tmp/flow.yaml   # review
helm upgrade --install flow deploy/helm/flow -n flow -f values-prod.yaml
kubectl -n flow rollout status deploy/flow
helm test flow -n flow                       # NetworkPolicy probe, must pass
kubectl -n flow logs deploy/flow | grep 'coding sessions preflight'
```

Then enable the `coding-sessions` feature for a workspace and open **Settings › Coding sessions**. `GET /api/coding/status` reports the provider, snapshot strategy, regions, sizes, code hosts and preflight results.

## Configuration

Every `FLOW_CODING_*` variable has a value under `coding:` or `sandboxes:`; the chart's `values.yaml` documents each one and [configuration.md](configuration.md#coding-sessions) lists their meaning. The chart sets these for you:

| Variable | Chart source |
| --- | --- |
| `FLOW_CODING_RUNTIME_URL` | `coding.runtimeUrl`, default `http://<release>.<ns>.svc.cluster.local:<port>` |
| `FLOW_CODING_K8S_NAMESPACE` | `sandboxes.namespace.name` |
| `FLOW_CODING_K8S_RUNTIME_CLASS` | `sandboxes.runtimeClass.name` (`runc` and empty both mean no RuntimeClass) |
| `FLOW_CODING_K8S_HOST_USERS` | `sandboxes.hostUsers` |
| `FLOW_CODING_K8S_NODE_SELECTOR` / `_TOLERATIONS` | `sandboxes.nodeSelector` / `sandboxes.tolerations` |
| `FLOW_CODING_K8S_NETWORK_POLICY` | `false` when the chart renders the policies (`sandboxes.networkPolicy.manager=helm`), `true` for `manager=flow` |
| `FLOW_CODING_K8S_FLOW_*`, `_PROXY_*` | The release namespace and the chart's Pod labels |
| `FLOW_CODING_EGRESS_PROXY_URL` | The chart's egress Service, or `coding.egress.proxyUrl` |
| `FLOW_CODING_EGRESS_ACL_SECRET` | Generated, or `egressProxy.aclHelper.existingSecret` |
| `FLOW_CODING_INSTANCE_ID` | `coding.instanceId`, default the release name |

Use `coding.extraEnv` for anything else (it is rendered last).

### Runtime choices

| `sandboxes.runtimeClass.name` | Kernel | Notes |
| --- | --- | --- |
| `""` or `runc` (default) | Shared host kernel | Works everywhere. Read [Limits of plain runc](coding-sessions-security.md#limits-of-plain-runc). |
| `gvisor` | gVisor user-space kernel (`runsc`) | GKE Sandbox, or containerd with the `runsc` shim. Set `create: true` to create the RuntimeClass (handler `runsc`). |
| `kata` | Lightweight VM per Pod | kata-deploy creates RuntimeClasses such as `kata-qemu`; use that name. Needs nested virtualization or bare metal. |

### Snapshot strategies

`archive` (default) works on any storage: the snapshot build streams a `tar.zst` of `/workspace` and `/home/flow` to Flow, which stores it under `coding.snapshot.archivePrefix`, and later sandboxes restore it in an init container. Archives can be several GB for large monorepos; `coding.snapshot.archiveMaxBytes` caps them.

`volumesnapshot` clones the workspace PVC from a CSI VolumeSnapshot. It needs the snapshot CRDs, a snapshot-capable CSI driver and PVC workspaces, and the chart adds the volumesnapshot RBAC rule. Warm sandboxes do not serve it.

### Dedicated node pool

Sandbox nodes should:

- run nothing but sandboxes (taint `flow.dev/pool=sandbox:NoSchedule`, label `flow.dev/pool=sandbox`);
- have **no cloud IAM role or instance profile**, or IMDSv2 with a hop limit of 1 (AWS), Workload Identity with the metadata server restricted (GKE), or no managed identity (AKS);
- set kubelet `podPidsLimit` (for example 16384) so a fork bomb stays inside its Pod;
- receive kernel updates promptly (they are the boundary under runc);
- optionally use a separate subnet whose route tables and security groups allow only the egress you expect.

The pre-pull DaemonSet (`sandboxes.prePull.enabled`) keeps the sandbox image on every pool node so sessions start without an image pull.

### Egress

Sandboxes have no direct Internet access. They reach Flow directly (runtime API and LLM proxy) and everything else through the egress proxy, which allows only:

- the built-in list (`deploy/egress-proxy/allowlist.default.txt`: GitHub, package registries, toolchain downloads) unless `egressProxy.defaultAllowlist=false`;
- `egressProxy.allowlist` (for example a private registry);
- the configured GitHub Enterprise and GitLab hosts, and Flow's own host;
- per environment, through the ACL helper: `extended` adds the environment's allowed domains, `open` allows any public host, and `none` allows only the environment's code host and Flow.

Model provider APIs are never allowlisted; models are reached through Flow. Private, loopback, link-local (cloud metadata) and CGNAT addresses are always denied, after DNS resolution.

## GitHub App setup

Coding sessions on GitHub need a GitHub App, because only an App can mint short-lived tokens scoped to one repository.

1. On GitHub (or GHES) go to **Settings › Developer settings › GitHub Apps › New GitHub App** (for an organization: **Organization settings › Developer settings**).
2. Set:
   - **Homepage URL**: `https://flow.example.com`
   - **Callback URL**: `https://flow.example.com/api/integrations/github-app/setup`, with **Request user authorization (OAuth) during installation** off. Enable **Expire user authorization tokens**.
   - **Setup URL**: `https://flow.example.com/api/integrations/github-app/setup`, with **Redirect on update** on.
   - **Webhook URL**: `https://flow.example.com/api/integrations/github-app/webhook`, active.
   - **Webhook secret**: the value of `FLOW_GITHUB_APP_WEBHOOK_SECRET` (`openssl rand -hex 32`).
3. Repository permissions: Contents **Read and write**, Pull requests **Read and write**, Metadata **Read**, Checks **Read**, Actions **Read**, Commit statuses **Read**, Issues **Read**. Add Workflows **Read and write** only if agents may edit `.github/workflows` (off by default).
4. Subscribe to events: Installation, Installation repositories (sent automatically), Pull request, Pull request review, Pull request review comment, Issue comment, Check suite, Check run, Push.
5. **Where can this GitHub App be installed?** Only on this account, unless several organizations share Flow.
6. Create the App. Note the **App ID**, the **slug** (from the App's URL), the **Client ID**, and generate a **client secret**.
7. **Generate a private key** and store it as a Secret (`flow-github-app`, key `private-key.pem`), then reference it with `coding.githubApp.privateKeySecret`. Put the webhook and client secrets in `flow-env`.
8. For GitHub Enterprise Server set `coding.githubApp.host: ghe.example.com` (the API URL is derived as `https://ghe.example.com/api/v3`). The GHES host is added to the egress allowlist automatically.
9. `helm upgrade`, then in Flow go to **Settings › Integrations › GitHub › Install GitHub App**, choose the repositories, and finish. Flow records the installation and returns to the coding settings.
10. Create an environment for a repository in **Settings › Coding sessions** and start a session from an issue.

Users who must have personal push access (the "require linked account" policy) link their GitHub account through the App's OAuth (Client ID and secret).

## GitLab setup

GitLab uses the workspace's existing GitLab connection (token or OAuth) on gitlab.com or a self-managed instance.

1. Add the connection in **Settings › Integrations › GitLab**. For the default `project-token` mode the connection token needs the `api` scope and the **Maintainer** role on each project, so Flow can create a project access token per session. On gitlab.com, project access tokens need Premium or Ultimate.
2. Choose the credential mode (`coding.gitlab.credentialMode`):
   - `project-token` (default): one Developer-level token per session, scoped to one project, revoked when the session ends (GitLab's minimum expiry is one day; GC revokes leftovers).
   - `oauth`: the workspace's GitLab OAuth access token (2-hour expiry). Reaches every project the OAuth user can access.
   - `connection`: the connection token itself. Long-lived and broad; Flow warns at startup and the settings page shows a banner. Avoid it.
3. Add `api` to `coding.gitlab.tokenScopes` only if agents should be able to post MR notes with `glab mr note`.
4. Configure the project webhook (Merge request, Comments, Pipeline events) to `https://flow.example.com/api/integrations/gitlab/webhook?workspace=<key>` with the connection's webhook secret, as for review sync.
5. Self-managed hosts are added to the sandbox egress allowlist automatically. If your GitLab uses a private CA, bake it into a derived sandbox image.

## Docker provider (development)

The docker provider runs sandboxes as containers on one machine. See the Compose `coding` profile in [configuration.md](configuration.md#compose-profiles). It needs the Docker socket, which is root-equivalent; never use it in production.

## Uninstall

`helm uninstall flow -n flow` removes Flow and, when the chart created it, the sandbox namespace with every running sandbox (set `sandboxes.namespace.keepOnUninstall=true` to keep it). The data PVC, bundled database Secrets and a chart-created RuntimeClass are kept (`helm.sh/resource-policy: keep`); delete them by hand if you no longer need them.
