# Coding sessions: security model

Coding sessions run code that nobody reviewed: the repository's own scripts,
its dependencies, and whatever the agent decides to execute. A malicious
repository, a malicious dependency, or a prompt injection hidden in an issue,
a PR comment or a web page can take full control of the sandbox user. This
document describes what that attacker can reach, for how long, and the residual
risks you accept, especially when sandboxes run on plain runc.

See the [operator guide](coding-sessions.md) for installation and the
[runbook](coding-sessions-operations.md) for day-to-day operations.

## Threat model

| Asset | Protection |
| --- | --- |
| Model provider keys | Never enter a sandbox. The harness talks to Flow's LLM proxy with the sandbox token; Flow adds the key upstream. |
| Code host credentials | GitHub installation tokens (1 hour, one repository, minimal permissions) or GitLab project tokens (one project, revoked at session end), fetched on demand through Flow. When the session's creator linked their own account, the git credential helper gets that member's short-lived token instead (GitHub App user token, 8 hours; GitLab OAuth token, 2 hours), only for the session's repository and only while the session runs. See [Member credentials](#member-credentials). |
| Commit signing keys | Stay in Flow. The sandbox asks Flow to sign (`/sign`). |
| Environment secrets | Served once per sandbox, only during setup, then unset. They can persist on disk or in snapshot archives if the prepare script writes them (the editor warns about this). |
| Other sandboxes, Flow, the cluster, the cloud account | Isolation, network policy, RBAC and node placement, described below. |

An attacker inside a sandbox has, for the sandbox's lifetime (at most
`FLOW_CODING_MAX_SESSION_DURATION`, 4 hours by default): the sandbox token
(only valid for this sandbox's runtime routes and the LLM proxy, revoked on
stop), a short-lived token for the one repository, network access to
allowlisted domains through the proxy, and the CPU, memory and disk of the
sandbox size.

## Controls

### Pod isolation

Every sandbox Pod runs with the `restricted` Pod Security profile, which the
namespace enforces (`pod-security.kubernetes.io/enforce: restricted`):

- non-root uid 1000, `allowPrivilegeEscalation: false`, every capability dropped, seccomp `RuntimeDefault`;
- read-only root filesystem with size-limited emptyDir volumes for `/workspace`, `/home/flow` and `/tmp`;
- no hostPath, hostNetwork, hostPID or hostIPC;
- `automountServiceAccountToken: false` and a ServiceAccount (`flow-sandbox`) with no RBAC;
- `enableServiceLinks: false`, so no cluster Service addresses leak into the environment;
- CPU, memory and ephemeral-storage limits, `activeDeadlineSeconds`, and PID limits (kubelet `podPidsLimit` plus the runner's `ulimit -u`);
- optional `runtimeClassName` (gVisor or Kata) and `hostUsers: false`.

### Network

- `flow-sandbox-default-deny` denies all ingress and egress for Pods labelled `flow.dev/managed-by=flow`.
- `flow-sandbox-egress` allows only DNS (kube-dns), the Flow runtime port and the egress proxy port. Sandboxes never get an `ipBlock` rule, so the cloud metadata address (169.254.169.254), node IPs, the API server and every other address are unreachable directly.
- `flow-sandbox-namespace-default-deny` isolates every other Pod in the namespace unless a policy allows it.
- The egress proxy (Squid) accepts connections only from sandboxes and allows only `CONNECT` to port 443 and plain HTTP on port 80, to allowlisted domains. It denies private, loopback, link-local, CGNAT and reserved ranges **after DNS resolution**, so an allowlisted name that resolves into the cluster cannot be used for SSRF. Its own NetworkPolicy allows egress only to DNS, Flow and public addresses on 80/443.
- Per-environment network modes (`default`, `extended`, `open`, `none`) are enforced by the proxy's ACL helper, which asks Flow (`POST /internal/coding-runtime/egress-check`, authenticated with `FLOW_CODING_EGRESS_ACL_SECRET`) with the sandbox's proxy credentials. A token that is invalid, expired, revoked, or belongs to another sandbox is denied. Unbound warm sandboxes are denied all egress. Decisions are cached for 60 seconds, so revocation takes effect within a minute.
- Model provider APIs are never on the allowlist.

### RBAC

Flow's ServiceAccount gets a Role in the sandbox namespace only, with the
verbs the provider uses: pods (create, get, list, delete), pods/log (get),
pods/exec (create, get), secrets (create, get, list, delete, patch),
persistentvolumeclaims (create, get, list, delete), namespaces (get, the
sandbox namespace only), networkpolicies (get, create, patch) only when Flow
manages them, and volumesnapshots only for that strategy. The only
cluster-scoped grant is an optional read-only ClusterRole for preflight
(RuntimeClass, StorageClass, VolumeSnapshotClass). Flow cannot touch any
other namespace.

Note that `pods/exec` and `secrets` in the sandbox namespace are powerful:
whoever controls Flow's ServiceAccount can read sandbox tokens and exec into
sandboxes. Treat the Flow Pod and its namespace as sensitive.

### Push guardrails

The runner pushes only branches under `FLOW_CODING_BRANCH_PREFIX`, or under
the member's own prefix (`<username>/`) for a session that pushes as its
creator (defense in depth: the repository token itself allows more). Flow verifies the pushed head
before opening the PR or MR, PRs are always drafts, and humans merge.
Workflow file changes fail unless the GitHub App has the Workflows permission.

### Agent permissions inside the sandbox

The harnesses run with their own permission prompts and sandboxes turned off,
by design: Claude Code with `--permission-mode bypassPermissions`, Codex with
`--dangerously-bypass-approvals-and-sandbox`, and OpenCode with
`permission: {edit, bash, webfetch: allow}`. A coding session is unattended:
nobody is there to answer "may I run `npm test`?", and the harnesses' built-in
sandboxes (Seatbelt, Landlock, bubblewrap) either do not work in an
unprivileged, read-only-root container or would only duplicate the controls
above while breaking builds.

This is safe only because the sandbox itself is the boundary, and only while
all of the following hold:

- **Isolation.** The agent runs as uid 1000 in a restricted Pod (or container)
  with no capabilities, no privilege escalation, no host access and no service
  account token. Everything it can touch is the throwaway workspace and home
  volumes of this one sandbox.
- **Egress.** It can reach only Flow's runtime API and the allowlisted domains
  through the egress proxy, filtered per environment network mode; never the
  cluster, node, metadata or private addresses.
- **Credentials.** It never holds a model provider key (the LLM proxy adds it),
  a signing key (Flow signs), or a long-lived code host token: only the
  sandbox token, revoked at stop, and a short-lived token for the one
  repository (the workspace's, or the creator's own), fetched on demand.
  Environment secrets are served once, during setup only.
- **Review.** Its only durable output is a pushed branch under
  `FLOW_CODING_BRANCH_PREFIX` and a draft PR or MR that a human reviews and
  merges.

If you weaken any of these (for example a `none`-isolation runtime on shared
nodes, an `open` network mode, a broad GitHub App permission set, or
`FLOW_CODING_GITLAB_CREDENTIAL_MODE=connection`), the bypass flags give the
agent, and any prompt injection it reads, that extra reach without asking.

### Member credentials

A session started by (or on behalf of) a member who linked their GitHub or
GitLab account in Settings › Account › Connected accounts pushes and opens
its PR/MR as that member: the branch uses the member's prefix
(`<username>/<identifier>-<title>-<suffix>`), commits are authored and
committed by the member (their display name and host email: GitHub
`<id>+<login>@users.noreply.github.com`, GitLab the account's commit email or
`<id>-<username>@users.noreply.<host>`), and Flow opens the PR/MR with the
member's token. The bot identity only posts Flow's back-link comments.

The member's credential follows the same model as the workspace's:

- **On demand, never at rest in the sandbox.** It is not in the bootstrap,
  the environment or any file. Git's credential helper asks Flow
  (`POST /internal/coding-runtime/credentials`, authenticated with the sandbox
  token) for each fetch and push and hands the answer to git in memory. The
  gh/glab CLI env, which the runner writes to `~/.flow/cli-env`, always gets
  the workspace credential, never the member's.
- **One member, one repository.** The session (and so the member) comes from
  the sandbox token, not from the request; another member's token is never
  looked up. The helper's host and path must be the session's repository.
- **Short-lived only.** Only tokens that expire within hours enter a
  sandbox: GitHub App user-to-server tokens (8 hours, refreshed by Flow) and
  GitLab OAuth tokens (2 hours). A GitLab personal access token or a
  non-expiring GitHub OAuth token never reaches a sandbox; such sessions push
  with the workspace credential while Flow still opens the PR/MR as the member
  server side.
- **Stops at session end.** A finished session's sandbox token gets nothing
  (the runtime API answers `410 Gone`), and unlinking the account or a revoked
  grant stops serving immediately. Flow cannot revoke a GitHub user token on
  its own without revoking the member's whole authorization, so a token copied
  out of a sandbox stays valid until it expires (at most 8 hours).
- **Fallback.** Without a linked account (or with only a long-lived token)
  the session uses the installation / project token as before, with the
  member still the commit author, and the session shows "Link your GitHub
  account to push as yourself".

Residual risk: a member's user token is not narrowed to one repository the
way an installation token is. Code in the sandbox that calls the credential
helper itself can obtain it and use it, until it expires, for anything the
member and the GitHub App (or the GitLab `api` scope) allow. Members who do
not want that can leave their account unlinked; sessions then push with the
repository-scoped workspace token.

## Limits of plain runc

When `sandboxes.runtimeClass.name` is empty (runc) and `hostUsers` is true,
sandboxes share the node's kernel with nothing between them but namespaces,
cgroups, seccomp and dropped capabilities. Be clear about what that means:

- **A kernel exploit escapes to the node.** A local privilege escalation in the Linux kernel (several are found every year) turns sandbox code into root on the node. From there the attacker can read every other Pod's memory and volumes on that node, the kubelet's credentials, and anything the node can reach on the network. Under runc, **the node pool is the real security boundary.**
- **Side channels between sandboxes.** Concurrent sandboxes on one node share CPU caches and the kernel. Cross-sandbox leaks through side channels are possible.
- **What the sandbox can do legitimately is still exposure.** During its life the sandbox can use its repository token for anything that token allows (for example, push other branches of the same repository) and can send data to allowlisted domains such as github.com or a package registry. The allowlist narrows exfiltration; it cannot remove it.
- **Setup secrets.** Anything the prepare script writes to disk can end up in the snapshot archive and in later sandboxes of the same environment.

### Mitigations you must apply with runc

1. **Dedicated, tainted nodes.** Keep the defaults `sandboxes.nodeSelector: {flow.dev/pool: sandbox}` and the matching toleration, and taint those nodes so nothing else schedules there. Never run Flow, databases, ingress controllers or other tenants on them.
2. **No cloud credentials on sandbox nodes.** No IAM role or instance profile, no node service account with project access, no managed identity. On AWS use IMDSv2 with `HttpPutResponseHopLimit=1`; on GKE enable Workload Identity / the GKE metadata server; on Azure disable IMDS access from Pods or use a pool without identity. The NetworkPolicy already blocks 169.254.169.254, but after a node escape only the node's own configuration protects the account.
3. **Metadata blocking at every layer.** The NetworkPolicy, the proxy's `to_metadata` and `to_private` rules, the proxy's own NetworkPolicy, and Flow's egress check all deny link-local and metadata addresses. Keep `egressProxy.blockCIDRs` updated if your cluster uses public address space for Pods, Services or nodes.
4. **Patch kernels promptly** on the sandbox pool, and prefer node images that auto-update.
5. **Limit blast radius on the node:** kubelet `podPidsLimit`, the ResourceQuota and LimitRange from the chart, and no hostPath or privileged workloads (DaemonSets for logging or monitoring on the sandbox pool should be minimal and run without host access where possible).
6. **One sandbox per node for sensitive tenants** (optional): run large nodes with one sandbox each by sizing, or add Pod anti-affinity through `FLOW_CODING_K8S_EXTRA_LABELS` and a scheduler profile.
7. **Verify NetworkPolicy enforcement** after every CNI or cluster upgrade (below).
8. **Move to gVisor or Kata** when the cluster allows it: set `sandboxes.runtimeClass.name`. Nothing else changes.

## Verify NetworkPolicy enforcement

Preflight cannot prove that the CNI enforces NetworkPolicies: a cluster with
Flannel alone accepts the objects and ignores them. Verify with a probe Pod
that carries the sandbox label, so the sandbox policies apply to it.

The chart ships it as a Helm test:

```bash
helm test flow -n flow --logs
```

It runs `curlimages/curl` in the sandbox namespace on the sandbox pool and
passes only if:

| Check | Expected |
| --- | --- |
| `http://169.254.169.254/latest/meta-data/` directly | blocked |
| `http://metadata.google.internal/` directly | blocked |
| `https://kubernetes.default.svc/version` directly | blocked |
| `https://example.com/` and `https://1.1.1.1/` directly | blocked |
| Flow's runtime URL directly | reachable (any HTTP answer) |
| `https://github.com/` through the proxy with invalid credentials | refused |
| `http://169.254.169.254/` through the proxy | refused |

To run the same probe by hand (for example without Helm):

```bash
kubectl -n flow-sandboxes run netpol-probe --rm -it --restart=Never \
  --image=curlimages/curl:8.11.1 \
  --labels=flow.dev/managed-by=flow,flow.dev/instance=netpol-probe \
  --overrides='{"spec":{"automountServiceAccountToken":false,"nodeSelector":{"flow.dev/pool":"sandbox"},
    "tolerations":[{"key":"flow.dev/pool","operator":"Equal","value":"sandbox","effect":"NoSchedule"}],
    "securityContext":{"runAsNonRoot":true,"runAsUser":1000,"seccompProfile":{"type":"RuntimeDefault"}},
    "containers":[{"name":"netpol-probe","image":"curlimages/curl:8.11.1","command":["sh"],"stdin":true,"tty":true,
      "resources":{"limits":{"cpu":"200m","memory":"64Mi"}},
      "securityContext":{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"capabilities":{"drop":["ALL"]}}}]}}'
# inside the Pod:
curl -m 5 http://169.254.169.254/latest/meta-data/     # must time out
curl -m 5 https://example.com/                          # must time out
curl -m 5 http://flow.flow.svc.cluster.local:8080/internal/coding-runtime/bootstrap   # must answer (401)
```

The `flow.dev/instance` value differs from Flow's instance id, so orphan GC
never deletes the probe. If any "blocked" request succeeds, stop: sandboxes
are not isolated. Fix the CNI (install Calico or Cilium, or enable the managed
policy engine) before enabling coding sessions for users.

## Audit

Flow writes session start, approval, debug terminal use, secret add or delete,
snapshot builds and PR creation to the workspace audit log. Sandbox logs are
archived to object storage on stop. The proxy logs every request with the
sandbox id to stdout (`%un` in the access log).
