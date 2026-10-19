# Coding sessions: operations runbook

Day-to-day operations for coding sessions on Kubernetes. Commands assume the
release `flow` in namespace `flow` and sandboxes in `flow-sandboxes`; adjust
to your values. See the [operator guide](coding-sessions.md) and the
[security model](coding-sessions-security.md).

## How it fits together

Flow's database is the source of truth. Reconcilers converge the cluster to it:
the session reconciler (phases and timeouts), the sandbox reconciler (Pods,
Secrets, PVCs), the snapshot reconciler, the warm pool and orphan GC. They are
restart-safe: restarting Flow never touches running sandboxes, and Pods are
adopted by name when Flow comes back.

Every object Flow creates is labelled:

| Label | Value |
| --- | --- |
| `flow.dev/managed-by` | `flow` |
| `flow.dev/instance` | `FLOW_CODING_INSTANCE_ID` (default: the release name) |
| `flow.dev/kind` | `session`, `snapshot`, `debug` or `warm` |
| `flow.dev/sandbox-id`, `flow.dev/session-id` | ids from the session view |

Object names are deterministic: Pod `flow-cs-<sandboxId>`, token Secret
`flow-cs-<sandboxId>-token`, PVC `flow-ws-<volumeId>`, VolumeSnapshot
`flow-snap-<snapshotId>`.

Useful views:

```bash
kubectl -n flow-sandboxes get pods -l flow.dev/managed-by=flow -L flow.dev/kind,flow.dev/session-id,flow.dev/size
kubectl -n flow-sandboxes get events --sort-by=.lastTimestamp | tail -30
kubectl -n flow logs deploy/flow | grep -E 'coding|sandbox'      # session id, sandbox id and phase on every line
kubectl -n flow-sandboxes describe resourcequota flow-sandboxes
```

## Alerts

Flow exports OpenTelemetry metrics (meter `flow/coding`); scrape them through
an OTel Collector with the Prometheus exporter. Suggested alerts:

| Alert | Expression (PromQL, Collector naming) | Meaning |
| --- | --- | --- |
| Error rate per code | `sum by (code) (rate(flow_coding_session_errors_total[15m])) > 0.1` | A failure class is spiking; the code points at the section below. |
| Slow starts | `histogram_quantile(0.95, sum by (le) (rate(flow_coding_sandbox_start_seconds_bucket[30m]))) > 300` | Image pulls, scheduling or restores are slow. |
| Warm pool empty | `min by (size) (flow_coding_warm_pool_available) == 0` for 15m | Sessions pay full start latency; check quota and nodes. |
| Orphans deleted | `sum(increase(flow_coding_orphans_deleted_total[1h])) > 0` for 3h | Something keeps leaking objects (crash loops, a second installation with the same instance id). |
| LLM upstream errors | `sum(rate(flow_coding_llm_requests_total{status=~"5.."}[10m])) / sum(rate(flow_coding_llm_requests_total[10m])) > 0.05` | Model provider trouble; Auto sessions fall back, pinned ones fail with `modelUnavailable`. |
| Quota rejections | `sum(rate(flow_coding_session_errors_total{code="quotaExceeded"}[1h])) > 0` | Spend limits reached; expected occasionally. |
| Provider errors | `sum by (op) (rate(flow_coding_provider_errors_total{transient="false"}[15m])) > 0` | RBAC, admission or API server problems. |
| Egress proxy down | `kube_deployment_status_replicas_available{deployment="flow-egress"} < 1` | Sandboxes cannot install dependencies. |

## Stuck sessions

Every phase has a deadline (see the timeouts in
[configuration.md](configuration.md#phase-timeouts-and-retries)), so a session
should never stay in one phase forever. If one looks stuck:

1. Find it: the session view shows the phase, the sandbox and its reason. Admins see the provider reason.
2. Check the sandbox Pod:
   ```bash
   kubectl -n flow-sandboxes get pod flow-cs-<sandboxId> -o wide
   kubectl -n flow-sandboxes describe pod flow-cs-<sandboxId>
   kubectl -n flow-sandboxes logs flow-cs-<sandboxId> -c runner --tail=200
   kubectl -n flow-sandboxes logs flow-cs-<sandboxId> -c restore      # archive restore
   ```
3. By phase:
   - **queued**: global or workspace concurrency is full (`FLOW_CODING_MAX_SESSIONS`, `FLOW_CODING_WORKSPACE_SESSIONS`) or a spend limit applies. It fails with `capacityUnavailable` after `FLOW_CODING_QUEUE_TIMEOUT`.
   - **starting**: `Pending` with `FailedScheduling` means taints, node selector, region or quota (see [Quota](#quota-and-capacity)); `ErrImagePull` means the image or pull secret; an init container failure means the archive restore (see [Snapshot failures](#snapshot-failures)).
   - **running** with no activity: the runner sends heartbeats every 15 s. If the Pod runs but heartbeats stopped, Flow marks the session `runnerLost` after `FLOW_CODING_HEARTBEAT_TIMEOUT`. Check that the sandbox can reach Flow (NetworkPolicy, `FLOW_CODING_RUNTIME_URL`).
   - **publishing**: the code host API is failing; Flow retries with backoff, then `pullRequestFailed` or `pushFailed`. The branch is kept and **Create PR** stays available.
   - **stopping**: the runner gets twice `FLOW_CODING_STOP_GRACE` to push work in progress, then the Pod is force-deleted.
4. Stop it from the UI (**Stop**) or the API (`POST /api/coding/sessions/{id}/stop`). Do not delete the Pod first: Flow would record `sandboxLost` rather than a clean stop. Deleting the Pod is safe, though, if the API is unavailable; the reconciler notices and ends the session.
5. If a session survives all of this, collect the Flow log lines for its id and file a bug: it means a reconcile step failed to set a deadline.

Flow restarts are harmless: leases expire after 30 s, reconcilers resume from the database, runners buffer events and retry, and heartbeat timeouts count from the process start so a Flow outage never marks healthy sessions `runnerLost`.

## Orphan cleanup

Orphan GC runs every `FLOW_CODING_GC_INTERVAL` (5 min). It lists objects
labelled with this installation's `flow.dev/instance` and deletes those with no
live database row, or whose row is terminal, once they are older than
`FLOW_CODING_ORPHAN_GRACE` (10 min). It also sweeps expired sandbox tokens,
GitLab project tokens older than a day, and archives of deleted snapshots.

To check what GC sees and clean up by hand (for example after restoring the
database from a backup older than the cluster state):

```bash
# Everything this installation owns, oldest first.
kubectl -n flow-sandboxes get pods,secrets,pvc -l flow.dev/managed-by=flow,flow.dev/instance=flow \
  --sort-by=.metadata.creationTimestamp
# Delete one sandbox completely (its token Secret is owner-referenced to the Pod).
kubectl -n flow-sandboxes delete pod flow-cs-<sandboxId>
kubectl -n flow-sandboxes delete pvc -l flow.dev/sandbox-id=<sandboxId>
```

Rules:

- Never reuse an instance id for two installations in one cluster: each would delete the other's sandboxes. The chart defaults `coding.instanceId` to the release name.
- The NetworkPolicy probe uses a different instance value on purpose, so GC ignores it.
- A sustained `flow_coding_orphans_deleted_total` means objects keep leaking. Typical causes: Flow crash-looping between Pod creation and the row update, two installations sharing an instance id, or manual changes to rows.
- GitLab project tokens named `flow-cs-<sessionId>` that survive more than a day: revoke them in the project's **Settings › Access tokens**; GC does the same on its next sweep when Flow can reach GitLab.

## Snapshot failures

The environment's snapshot list shows status, logs, **Refresh** and **Cancel**;
failed snapshots offer **Fix with agent**. Sessions fall back to inline setup
when no ready snapshot matches, so a failed snapshot slows sessions down but
does not block them.

| Symptom | Cause and fix |
| --- | --- |
| Build fails in the prepare script | Read the snapshot logs. Fix the script or the mise tools, or use **Fix with agent**. |
| Build times out | Raise `coding.snapshot.timeout`, use a larger size, or add caches to `.flow/snapshot-exclude`. |
| `archive too large` | Exclude caches (`.flow/snapshot-exclude`) or raise `coding.snapshot.archiveMaxBytes`. Check object storage free space and quotas. |
| Restore init container fails (`snapshotFailed`) | The archive is missing or corrupt, or object storage is unreachable from Flow. Flow retries once, then runs setup inline and rebuilds the snapshot. Check `kubectl logs ... -c restore` and Flow's object storage errors. |
| `volumesnapshot` stays not ready | `kubectl -n flow-sandboxes describe volumesnapshot flow-snap-<id>`: check the VolumeSnapshotClass, the CSI snapshotter sidecar and the snapshot controller. If the CSI driver cannot snapshot, switch to `archive`. |
| Secrets unreadable (`environmentSetupFailed`, "secret unreadable") | `FLOW_CODING_SECRET_KEY` changed. Restore the old key, or have environment owners re-add their secrets. |

Snapshots are retained per `coding.snapshot.retain` (2) and expire after
`coding.snapshot.ttl` (7 days); refresh happens automatically when the default
branch moved and the snapshot is older than `coding.snapshot.refreshInterval`.

## Quota and capacity

Capacity is bounded at three levels:

1. Flow's caps: `coding.maxSessions` (installation) and `coding.workspaceSessions` (per workspace). Sessions over the cap wait in `queued`.
2. The namespace ResourceQuota (`sandboxes.resourceQuota.hard`): pods, CPU, memory, ephemeral storage, PVCs. A Pod over quota is rejected at admission and the session fails with `capacityUnavailable` or `unschedulable` after the start timeout.
3. Node capacity of the sandbox pool: Pods stay `Pending` with `FailedScheduling`.

Size the quota to `maxSessions × largest size` plus the warm pool, snapshot
builds, debug terminals, the egress proxy replicas and the pre-pull DaemonSet:

```bash
kubectl -n flow-sandboxes describe resourcequota flow-sandboxes
kubectl -n flow-sandboxes get events --field-selector reason=FailedCreate
kubectl describe nodes -l flow.dev/pool=sandbox | grep -A6 'Allocated resources'
```

Spend limits (`quotaExceeded`) are separate: they come from **Settings ›
Usage & limits** and are enforced at session start, per LLM request and per
runtime increment.

## Rotate the GitHub App private key

GitHub allows several active private keys per App, so rotation has no downtime:

1. In the App's settings, **Generate a private key** (the old key stays valid).
2. Replace the Secret:
   ```bash
   kubectl -n flow create secret generic flow-github-app \
     --from-file=private-key.pem=./new.private-key.pem --dry-run=client -o yaml | kubectl apply -f -
   ```
   If the key is in `flow-env` as `FLOW_GITHUB_APP_PRIVATE_KEY` or in `coding.githubApp.privateKey`, update it there instead.
3. Restart Flow so it reads the new key: `kubectl -n flow rollout restart deploy/flow` (a `helm upgrade` that changes the chart's Secret restarts it automatically).
4. Check that installations still work: **Settings › Integrations › GitHub** lists them, and a new session can clone.
5. **Delete the old key** in the App settings.

Installation tokens already handed to sandboxes stay valid until they expire
(at most one hour); they are minted fresh on each git or CLI request.

Rotate the webhook secret the same way: set the new value in GitHub and in
`FLOW_GITHUB_APP_WEBHOOK_SECRET` within a few minutes of each other (GitHub
redelivers failed deliveries from the App's **Advanced** page).

Other secrets:

- **Egress ACL secret**: delete the chart's Secret `flow-egress-acl` in the sandbox namespace and run `helm upgrade`; a new value is generated for both Flow and the proxy, and both restart. During the restart, sandboxes may see proxy errors for up to a minute.
- **Model provider keys**: update `flow-env` and restart Flow. Sandboxes never see them.
- **`FLOW_CODING_SECRET_KEY`**: there is no in-place re-encryption. Changing it makes existing environment secrets and signing keys unreadable; owners must re-add them.

## OpenSandbox

With `coding.provider: opensandbox` Flow creates sandboxes through an
OpenSandbox lifecycle server (design §9.1) instead of creating Pods itself.
The chart then renders no sandbox namespace, Role, ResourceQuota, LimitRange,
RuntimeClass or sandbox NetworkPolicies, and Flow mounts no ServiceAccount
token: OpenSandbox owns the sandbox Pods. Tested against the API specs of
OpenSandbox `release-1.1.0`.

### In-cluster deployment

Flow and OpenSandbox in the same cluster (the expected setup):

```text
flow namespace                         opensandbox-system           opensandbox
┌────────────┐  /v1/sandboxes, proxy   ┌──────────────────┐  creates ┌──────────────────┐
│ Flow Pods  │ ──────────────────────▶ │ opensandbox-     │ ───────▶ │ sandbox Pods     │
│ :8080/8090 │                         │ server (:80)     │  execd   │ flow-runner,     │
└────────────┘ ◀─── runtime API, LLM proxy (FLOW_CODING_RUNTIME_URL) ─│ execd :44772     │
┌────────────┐ ◀──────────────── HTTP(S)_PROXY ────────────────────── └──────────────────┘
│ flow-egress│
└────────────┘
```

1. Install OpenSandbox (controller, CRDs, `opensandbox-server`) per its
   Kubernetes deployment guide, with an API key in a Secret. Note the server
   Service (default `opensandbox-server.opensandbox-system.svc.cluster.local:80`),
   the sandbox namespace (`[kubernetes].namespace`, default `opensandbox`) and
   whether the egress sidecar (`[egress]`) and a secure runtime
   (`[secure_runtime]`: gVisor/Kata) are configured. Raise
   `[server].max_sandbox_timeout_seconds` to at least `coding.opensandbox.expiryWindow`
   and `sandbox_create_timeout_seconds` enough for the first pull of the Flow
   sandbox image (several GB).
2. Copy the API key into a Secret in Flow's namespace (Secrets are namespaced):
   ```bash
   kubectl -n flow create secret generic opensandbox-api-key \
     --from-literal=api-key="$(kubectl -n opensandbox-system get secret opensandbox-api-key -o jsonpath='{.data.api-key}' | base64 -d)"
   ```
3. Values:
   ```yaml
   coding:
     enabled: true
     provider: opensandbox
     runtimePort: {enabled: true}      # sandboxes only need the runtime port
     # runtimeUrl: ""                  # default: Flow's in-cluster Service URL
     opensandbox:
       url: http://opensandbox-server.opensandbox-system.svc.cluster.local
       existingSecret: {name: opensandbox-api-key, key: api-key}
       sandboxNamespace: opensandbox
       serverNamespace: opensandbox-system
       egressPolicy: "proxy"           # only with OpenSandbox's egress sidecar
       networkPolicy: {enabled: true}  # see below
   networkPolicy:
     enabled: true                     # Flow accepts the runtime port from the sandbox namespace
   ```
   The sandbox → Flow callbacks (runtime API, LLM proxy, snapshot archives) use
   `FLOW_CODING_RUNTIME_URL`, which defaults to Flow's Service URL; the egress
   proxy runs in Flow's namespace and sandboxes get it as `HTTP(S)_PROXY`.
4. `helm upgrade`, then check **Settings → Coding → Diagnostics** (preflight:
   server reachable, API key accepted) and start a session.

### NetworkPolicies

- `networkPolicy.enabled` (Flow's own policy) admits the runtime port from
  `coding.opensandbox.sandboxNamespace` and, with the ACL helper, from the
  egress proxy.
- The egress proxy's policy admits only that namespace (and
  `coding.opensandbox.networkPolicy.podSelector`).
- `coding.opensandbox.networkPolicy.enabled` renders a policy **in OpenSandbox's
  sandbox namespace** for Pods labelled `flow.dev/managed-by: flow`: ingress only
  from the OpenSandbox server namespace (execd and the egress sidecar are
  reached through the server proxy), egress only to DNS, Flow's runtime port and
  the egress proxy. It relies on OpenSandbox copying sandbox metadata onto Pod
  labels — verify with
  `kubectl -n opensandbox get pods -l flow.dev/managed-by=flow` before enabling
  it. With `execdAccess: endpoint` (Flow calls execd directly), add Flow's
  namespace through `networkPolicy.extraIngressFrom`.
- The chart does not add a policy for the OpenSandbox server: in single-tenant
  mode its `/sandboxes/{id}/proxy/{port}` routes are not authenticated, so
  restrict who can reach it in the OpenSandbox installation (allow Flow's
  namespace and OpenSandbox's own components) and consider
  `coding.opensandbox.execdTokenSecret`.

### Operations

- Every OpenSandbox sandbox of this installation carries the metadata
  `flow.dev/managed-by=flow` and `flow.dev/instance=<instance id>`:
  ```bash
  curl -s -H "OPEN-SANDBOX-API-KEY: $KEY" \
    "$URL/v1/sandboxes?metadata=$(printf 'flow.dev/managed-by=flow&flow.dev/instance=flow' | jq -sRr @uri)" | jq '.items[] | {id, state: .status.state, sandbox: .metadata["flow.dev/sandbox-id"], expiresAt}'
  ```
  Orphan GC deletes those without a live row after `FLOW_CODING_ORPHAN_GRACE`;
  sandboxes of a Flow that is down expire within `expiryWindow`.
- `capacityUnavailable` comes from namespace quota (`KUBERNETES::QUOTA_EXCEEDED`,
  agent-sandbox provider) or exhausted Pools (HTTP 429). With the default
  BatchSandbox provider quota exhaustion surfaces as a create timeout
  (`sandboxStartFailed` after the retries).
- Runner output is the execd command log (`GET /command/{id}/logs`, kept 24 h
  after exit); Flow archives it on stop like on other providers.
- `kubectl -n opensandbox get batchsandboxes,pods -l flow.dev/sandbox-id=<id>`
  shows the workload of one Flow sandbox.

## Upgrades

1. Read the release notes for changes to the runner protocol, the egress-check protocol or values.
2. Upgrade Flow, the sandbox image and the egress proxy image together. The egress image defaults to the chart's appVersion; keep `coding.image` pinned to the matching sandbox image.
3. Render and diff before applying:
   ```bash
   helm template flow deploy/helm/flow -n flow -f values-prod.yaml > new.yaml
   helm get manifest flow -n flow > old.yaml
   diff -u old.yaml new.yaml | less
   helm upgrade flow deploy/helm/flow -n flow -f values-prod.yaml
   helm test flow -n flow
   ```
4. Running sessions are not interrupted: Flow restarts, reconcilers resume, runners reconnect. Sandboxes keep the image they started with; new sessions use the new image. Warm pool sandboxes are recycled within `coding.warmPoolMaxAge`.
5. With a single replica on SQLite or a ReadWriteOnce volume, the Deployment uses `Recreate`, so Flow is down for the restart (sandboxes keep running and buffer events). Use PostgreSQL or MySQL, Redis and S3 for rolling upgrades.
6. Snapshots whose `config_hash` changes (a new image is part of it) are rebuilt on demand; the first sessions after an image upgrade run setup inline.
7. Kubernetes upgrades: drain sandbox nodes only when they are idle, or accept that running sessions end with `sandboxLost` (the work up to the last push is kept, and **Restart** continues from the branch). Re-run `helm test` afterwards to confirm NetworkPolicy enforcement.

### Rollback

`helm rollback flow <revision> -n flow` restores the previous manifests.
Database migrations are additive, so rolling back Flow one release is safe;
sessions that started on the newer version continue or end with a runtime
error and can be restarted.
