# Coding sessions: design

Status: design and skeleton landed on `feat/coding-sessions`. Every item in this document is in scope for this round
and is owned by an implementation track (§24). §23 describes the order in which tracks integrate. It does not defer
any scope.

The reference product's coding sessions let its agent write code in a cloud sandbox and open a draft PR. Flow does the same on
the operator's own infrastructure: Kubernetes Pods (or Docker for development and CI), using GitHub (through a GitHub
App) or GitLab (through the existing GitLab connection). The agent is Claude Code, Codex or an open-source harness.

Contents

1. Goals and reference parity
2. Architecture
3. Components
4. Session state machine
5. Reconcile loops and restart safety
6. Data model
7. API routes
8. Configuration
9. Kubernetes objects per sandbox
10. Snapshot strategies
11. Warm pool and regions
12. LLM proxy, model routing and reasoning effort
13. GitHub App
14. GitLab and glab
15. Triggers and PR/MR follow-ups
16. In-sandbox runner and activity mapping
17. Browser testing
18. Debug terminal, Configure/Fix with agent, .env import
19. Security model
20. Billing, metering and spend limits
21. Observability
22. Failure modes
23. Integration order
24. Work breakdown and file ownership
25. Open risks and decisions

---

## 1. Goals and reference parity

| Reference | Flow |
|---|---|
| Settings › AI & agents › Coding sessions (`/settings/ai/coding-sessions`) with Enable, Agent, Model, Require signed commits, Pin sandbox region, Environments (inline), Trusted external sources, Repository access for Loops | The same page (`settings/coding-sessions`). The separate `coding-environments` page is removed and redirects to it. The enable toggle stays the `coding-sessions` feature flag; policy fields extend `WorkspaceSettings.CodingAgentSettings` |
| Agent: Codex (default) / Claude Code / Open source | `harness`: `codex` (default) / `claudeCode` / `openSource`, plus `pi` (Pi, an addition). Legacy values `auto`→`codex` and `claude`→`claudeCode` are normalized on read |
| Model: Auto + specific | `model`: `auto` (default) or a catalog id (`FLOW_CODING_MODELS_*`). The first entry of each catalog is Auto's pick, and Auto falls back to the next entry |
| CodingEnvironment fields | `domain.CodingEnvironment` (§6). It adds `codeHost` (github or gitlab) and `hostName` (GHE or self-managed GitLab) |
| Secrets: encrypted, masked, immutable, setup-only | `coding_environment_secrets` (AES-GCM). There is no read API and no update. Secrets are served once per sandbox, during setup only |
| Sandbox detection variables | `FLOW_SANDBOX=1` always, `FLOW_SNAPSHOT=1` while building a snapshot |
| Debug terminal (temporary sandbox, 20 min), import .env, archive, Configure/Fix with agent | §18 |
| Snapshots with status, logs, refresh and cancel | §10 |
| AgentSession states and fields, sandboxes[], PRs, commitsByTurnActivityId | `domain.CodingSession` anchored to an `AgentTask` (§4, §6) |
| Activities thought/action/response/elicitation/error/prompt; signals stop/continue/auth/select | Reuses the AgentTask/AgentActivity protocol (§16). In issue activity a delegated session is a session card ("Flow started by …", the status row "Setting up owner/repo…" / "Finished" with the PR number, and Copy link, Unsubscribe from chat and Archive); its conversation shows in the session view |
| Error reason codes | `domain.CodingErrorCode`. The reference codes are kept verbatim; Flow adds sandbox codes (§4.4) |
| Triggers: delegate, chat or comment, Loops "Start a coding session", PR follow-ups, "fix this PR" | §15 |
| Billing: AI credits, sandbox runtime by time, spend limits | LLM proxy metering plus runtime metering, enforced against the existing `SpendLimitSettings` (§20) |

## 2. Architecture

```
                          Browser (web/)
   Settings › Coding sessions │ Issue / Agent chat │ Session view │ Review page
                              │ /api/coding/*, /api/issues/{id}/coding-session, WS terminal
┌─────────────────────────────▼──────────────────────────────────────────────────────────────┐
│ Flow API (api/cmd/server, one binary, N replicas)                                            │
│                                                                                              │
│  coding_sessions_api ── internal/coding (state machine, session reconciler, spend guard) ──┐ │
│  coding_environments_api ── snapshot reconciler ─────────────────────────────────────────┐ │ │
│  coding_triggers_api ◄── codehost webhooks (github_app.go, code_webhooks.go → GitLab)     │ │ │
│                                                                                           │ │ │
│  internal/sandbox ── sandbox reconciler, warm pool, orphan GC ── Provider ────────────┐   │ │ │
│      (kubernetes │ docker │ fake │ disabled)                                          │   │ │ │
│  internal/codehost ── github (GitHub App) │ gitlab (connection + project tokens)      │   │ │ │
│  internal/llmproxy ── /internal/llm-proxy/* ─► Anthropic / OpenAI / OSS upstream      │   │ │ │
│  coding_runtime_api ── /internal/coding-runtime/* (sandbox-token auth)                │   │ │ │
│  store (SQLite / PostgreSQL / MySQL): coding_* tables  ◄── source of truth ───────────┴───┴─┘ │
│  objectstore (local / s3): snapshot archives, harness state, logs, screenshots               │
└───────────────▲──────────────────────────────────────────┬───────────────────────────────────┘
                │ HTTPS: runtime API + LLM proxy only      │ client-go (GA APIs) / Docker API
                │ (optionally a dedicated port, §8)        ▼
┌───────────────┴──────────── namespace flow-sandboxes (tainted node pool) ───────────────────┐
│  Pod flow-cs-<sandbox>  (restricted PSS, runc | gVisor | Kata via runtimeClassName)          │
│   init: restore (archive download)    container: runner                                      │
│     flow-runner ─ spawns ─ claude -p --output-format stream-json │ codex exec --json │ opencode│
│     git (credential helper → Flow), flow-sign (signing → Flow), gh / glab (token from Flow)  │
│     Playwright Chromium (browser testing), mise toolchains                                   │
│   volumes: /workspace (PVC from VolumeSnapshot | emptyDir), /home/flow, /tmp, token (Secret) │
│  NetworkPolicy: default deny; egress only to DNS, Flow runtime endpoint, egress proxy        │
│  Deployment flow-egress (Squid) ─ domain allowlist + per-sandbox ACL (helper → Flow) ─► Internet│
└──────────────────────────────────────────────────────────────────────────────────────────────┘
       GitHub / GHE (App: installation tokens, PR API, webhooks) · GitLab (API, project tokens, hooks)
```

Data flow of one session (issue → draft PR):

1. A trigger (delegate, chat, comment, Loop, PR) calls `coding.Service.Start`. It writes an `AgentTask` anchor and a
   `coding_sessions` row (phase `queued`, or `awaitingApproval` for untrusted external sources).
2. The session reconciler checks spend and capacity, resolves the environment, the snapshot and the code host access,
   and creates a `coding_sandboxes` row with `desired=running`.
3. The sandbox reconciler mints a sandbox token and calls `Provider.StartSandbox`. The Pod starts, the restore init
   container unpacks the snapshot (archive strategy), or the PVC is cloned from a VolumeSnapshot.
4. The runner calls `GET /bootstrap`, runs setup (mise install and the prepare script, with secrets) when there is no
   snapshot, then runs the harness with the turn prompt. Events stream to `POST /events` and become AgentActivities.
5. At turn end the runner commits (signed through Flow when required), pushes `flow/<branch>` with a credential from
   `POST /credentials`, saves the harness state, and calls `POST /turns/{id}/complete`.
6. The session moves to `publishing`. Flow verifies the branch head through the code host, opens a draft PR/MR
   (idempotent), links it to the session (`externalUrls`, `coding_session_pull_requests`) and moves to `idle`
   (state `complete`).
7. Follow-ups (chat prompt, PR comment, "fix this PR") enqueue inbox turns. The warm sandbox runs them, or, after the
   idle TTL, a new sandbox restores the snapshot, checks out the branch and resumes the harness state.

## 3. Components

| Component | Location | Purpose |
|---|---|---|
| Domain types | `api/internal/domain/coding.go`, `CodingAgentSettings` in `models.go` | Sessions, environments, snapshots, sandboxes, PR links, error codes |
| Config | `api/internal/config/coding.go` | `FLOW_CODING_*`, `FLOW_GITHUB_APP_*` parsing, defaults and validation |
| Orchestration | `api/internal/coding` | Phase policies and transitions (`phases.go`), runner wire protocol (`protocol.go`), store interfaces (`store.go`), and (track B) service, session reconciler, spend guard, model routing |
| Sandbox | `api/internal/sandbox` | `Provider` interface, `Fake` and `Disabled` providers, registry, `Record`/`Store` for the sandbox reconciler, `SnapshotStrategy`. Subpackages `kubernetes`, `docker` |
| Code hosts | `api/internal/codehost` | `Host` interface, normalized webhooks, error mapping. Subpackages `github` (GitHub App) and `gitlab` |
| LLM proxy | `api/internal/llmproxy` | Token-authenticated per-session proxy with model enforcement and metering |
| HTTP | `api/cmd/server/coding_*.go`, `github_app.go` | Route files per track. `coding_runtime.go` is the single wiring point |
| Store | `api/internal/store/coding_schema.go` (+ `coding_*.go` per track) | Tables and indexes for all three databases, and store methods |
| Runner | `sandbox-runner/` (new, TypeScript, Node 22) | In-sandbox supervisor: harness adapters, event mapping, git, credential and sign shims, browser MCP tools |
| Sandbox image | `deploy/sandbox-image/` | Debian-based image: mise, git, gh, glab, claude, codex, opencode, pi, Playwright Chromium, common toolchains |
| Egress proxy | `deploy/egress-proxy/` | Squid config, allowlist, ACL helper (`flow-api egress-acl` subcommand) |
| Helm chart | `deploy/helm/flow/` | Flow, sandbox namespace, RBAC, NetworkPolicies, ResourceQuota/LimitRange, egress proxy, optional RuntimeClass |
| Web | `web/src/components/settings/coding-sessions/`, `web/src/components/coding-sessions/`, `web/src/types/coding-sessions.ts`, `web/src/lib/coding-sessions-api.ts` | Settings and environments, session UI, triggers |

## 4. Session state machine

### 4.1 States and phases

The user-facing **state** follows the reference: `pending, active, stopping, complete, awaitingInput, error, stale` (UI:
Starting, Working, Stopping…, Finished, Needs input, Errored, Stopped). The reconciler works on a finer **phase**. Each
phase maps to one state and has a timeout, a retry cap and an exhaustion error code. These come from
`coding.PhasePolicies` (skeleton, with tests). The timeouts below are the defaults.

| Phase | State | What happens | Timeout (env) | Retries | On timeout / exhaustion |
|---|---|---|---|---|---|
| `awaitingApproval` | awaitingInput | An untrusted external source waits for a member's approval | 7d `FLOW_CODING_APPROVAL_TIMEOUT` | 0 | `approvalExpired` |
| `queued` | pending | Spend check, global and workspace concurrency (`FLOW_CODING_MAX_SESSIONS`, `FLOW_CODING_WORKSPACE_SESSIONS`) | 30m `FLOW_CODING_QUEUE_TIMEOUT` | n/a (re-evaluated each tick) | `capacityUnavailable`; `quotaExceeded` immediately when over the limit |
| `provisioning` | pending | Resolve environment and policy repos, user access check, signing key check, pick snapshot, bind a warm sandbox or create a sandbox record (and a volume for volumesnapshot) | 5m `FLOW_CODING_PROVISION_TIMEOUT` | 3 `FLOW_CODING_PROVISION_RETRIES` | `sandboxStartFailed`, or the precise code (`noCodeAccess`, `repoNotInPolicy`, `missingCommitSigningKey`, `noGitHubConnection`, `githubTransientError`, …), no retry for non-transient |
| `starting` | pending | Pod scheduled, image pulled, restore init container, runner bootstrap | 10m `FLOW_CODING_START_TIMEOUT` | 2 `FLOW_CODING_START_RETRIES` (new sandbox id each time) | `imagePullFailed`, `unschedulable`, `sandboxStartFailed`, `snapshotFailed` (restore failed) |
| `setup` | active | No usable snapshot: clone, mise install, prepare script with secrets | 20m `FLOW_CODING_SETUP_TIMEOUT` | 0 | `environmentSetupFailed` |
| `running` | active | A harness turn runs. Heartbeats are expected every 15s | 60m `FLOW_CODING_TURN_TIMEOUT`; heartbeat 2m `FLOW_CODING_HEARTBEAT_TIMEOUT` | 0 (agent turns are not idempotent) | `turnTimeout`; `runnerLost` (heartbeats stop, pod running); `sandboxLost` (pod gone or evicted); `harnessFailed`; `quotaExceeded` (proxy 402) |
| `awaitingUser` | awaitingInput | The agent asked a question (elicitation, or a `select` signal for repository choice) | 7d (approval timeout) | 0 | `approvalExpired` |
| `publishing` | active | Verify the pushed head, create or update the draft PR/MR, link it, post the summary | 5m `FLOW_CODING_PUBLISH_TIMEOUT` | 5 `FLOW_CODING_PUBLISH_RETRIES` | `pullRequestFailed` / `pushFailed`. The session errors, but the branch stays and "Create PR" stays available |
| `idle` | complete | Turn done. The sandbox is kept warm for follow-ups | 15m `FLOW_CODING_IDLE_TTL` | n/a | Not an error: the sandbox is reclaimed and the state stays complete |
| `stopping` | stopping | Stop requested: the runner gets `stop` on heartbeat or inbox, SIGINT to the harness, WIP commit and push, sandbox drained | 2 × `FLOW_CODING_STOP_GRACE` (60s) | n/a | Force-delete the sandbox, then done (state stale) |
| `done` | complete / error / stale | Terminal until continued | none | n/a | n/a |

Whole-session cap: `FLOW_CODING_MAX_SESSION_DURATION` (4h) is the Pod's `activeDeadlineSeconds`, counted per sandbox.
The session reconciler also ends a session whose cumulative running time exceeds it, with `sessionTimeout`.

### 4.2 Transitions

```
awaitingApproval ─approve─► queued ─► provisioning ─► starting ─┬─► setup ─► running
                                                     ▲          └────────────► running   (snapshot restored)
                                                     └─ retry (new sandbox) ◄─ starting
running ─► awaitingUser ─answer─► running
running ─► publishing ─► idle ─follow-up─► running            (warm sandbox)
                          idle ─follow-up after TTL─► queued   (restore + checkout branch + resume harness)
any non-terminal ─stop─► stopping ─► done (stale)
any non-terminal ─error─► done (error)
done ─continue / restart / follow-up─► queued
```

`coding.CanTransition` enforces this. Every phase change is a CAS on `coding_sessions.version` that writes
`phase_started_at` and `phase_deadline` in the same update, so a crash between the two is impossible.

### 4.3 User actions

- **Stop** (`POST /stop`, or the AgentTask stop signal): sets `stop_requested`; the phase becomes `stopping`.
- **Follow-up** (`POST /prompt`, a chat message on the anchor, a PR comment): appends an inbox item of kind `turn`. In
  `idle` it is delivered to the warm runner (long-poll). In `done` or after the TTL the session is re-queued. While
  `running`, the turn waits until the current turn ends (FIFO, per session).
- **Restart** (`POST /restart`): from `done`, re-queues with the same prompt history. `{useDefaultModel:true}` is
  The reference "Restart with default model": it sets `modelSelection=auto` (used after `modelUnavailable` or harness
  failures on a pinned model).
- **Approve** (`POST /approve`): moves `awaitingApproval` to `queued`, or `{reject:true}` ends it (state stale).
- **Answer** (an elicitation response on the anchor): an inbox turn of kind `followUp`; the phase goes back to
  `running`.

### 4.4 Error codes

All codes are in `domain.CodingErrorCode` and mirrored in `web/src/types/coding-sessions.ts`. The UI shows a message and
an action for each.

| Code | Meaning | UI action |
|---|---|---|
| `noCodeAccess` | The App installation or GitLab token cannot reach the repo | Open integration settings |
| `noGitHubConnection` / `noGitLabConnection` | No GitHub App installation / GitLab connection | Connect |
| `requiresGithubConnection` / `requiresGitlabConnection` | Policy requires the user's linked host account | Link account |
| `personalAccessDenied` | The user lacks push permission on the repo | n/a |
| `missingCommitSigningKey` | Signed commits are required and the user has no usable key | Upload signing key |
| `noPolicyRepos` / `repoNotInPolicy` | No environment, or the repo is outside the policy / Loop repository access | Create environment |
| `githubTransientError` / `gitlabTransientError` | Host API retries exhausted | Restart |
| `quotaExceeded` | Spend limit reached | Usage & limits |
| `permissionDenied` | Flow permission (guest, team access) | n/a |
| `approvalExpired`, `capacityUnavailable`, `sandboxDisabled`, `sandboxStartFailed`, `imagePullFailed`, `unschedulable` | Sandbox start problems | Restart; admins see the reason |
| `environmentSetupFailed`, `snapshotFailed` | Setup or snapshot failed | Fix with agent, View logs |
| `runnerLost`, `sandboxLost`, `turnTimeout`, `sessionTimeout`, `harnessFailed` | Runtime failures | Restart |
| `modelUnavailable` | The upstream rejected the model | Restart with default model |
| `pushFailed`, `pullRequestFailed`, `signingFailed` | Publishing failures | Create PR / Restart |
| `internal` | Bug | Restart |

## 5. Reconcile loops and restart safety

Flow's database is the source of truth. Three loops converge the world to it, plus GC and the warm pool. All loops
start from `server.runCoding` (`coding_runtime.go`).

1. **Session reconciler** (track B, `internal/coding/reconcile.go`). Every `FLOW_CODING_RECONCILE_INTERVAL` (5s), and
   immediately when woken by writes, it leases due sessions:
   `LeaseCodingSessions(owner, lease=30s, limit=50)` selects `phase NOT IN (done) AND next_reconcile_at <= now AND
   lease_until < now` and applies the same CAS pattern as `agent_runs` (FOR UPDATE SKIP LOCKED on PostgreSQL and
   MySQL 8, CAS on SQLite). For each session it runs a pure function
   `Step(session, sandboxes, observation, policy, now) → (mutation, []Action)`, executes the actions (each
   idempotent), writes the mutation with a version CAS, and releases with `next_reconcile_at`. Step never calls a
   provider directly. It creates or changes `coding_sandboxes` rows (`desired`) and reads their observed `phase`.
2. **Sandbox reconciler** (track A, `internal/sandbox/reconcile.go`). It leases `coding_sandboxes` rows whose desired
   and observed state differ, or whose observation is older than 15s. It calls `StartSandbox`, `Status` or
   `StopSandbox` and stores the observation (`SaveSandboxObservation`). Token minting happens here, right before
   StartSandbox (the token goes into a Secret owned by the Pod), so a restarted Flow can recreate a Pod with a fresh
   token. It persists sandbox logs to the object store on stop (`logs_key`), and records runtime usage every 5 min
   and at stop.
3. **Snapshot reconciler** (track K, `internal/coding/snapshots.go`). It drives `coding_environment_snapshots`:
   queued → building (creates a `snapshot` sandbox) → finishing (`SnapshotStrategy.Finish`) → ready/failed. It
   handles cancel, TTL expiry, retention (`FLOW_CODING_SNAPSHOT_RETAIN`) and auto-refresh when the default branch has
   moved and the snapshot is older than `FLOW_CODING_SNAPSHOT_REFRESH_INTERVAL`.
4. **Orphan GC** (track A). Every `FLOW_CODING_GC_INTERVAL` it runs `ListManaged`, which lists objects labelled
   `flow.dev/managed-by=flow,flow.dev/instance=$FLOW_CODING_INSTANCE_ID`. Objects without a live row
   (`KnownSandboxIDs`), or whose row is terminal, are deleted with `DeleteManaged` once older than
   `FLOW_CODING_ORPHAN_GRACE`. It also sweeps expired sandbox tokens, expired GitLab project tokens (§14) and
   archives of deleted snapshots.
5. **Warm pool** (track A). It keeps `FLOW_CODING_WARM_POOL` unbound sandboxes per size (and per region), and recycles
   them after `FLOW_CODING_WARM_POOL_MAX_AGE`.

Single-runner loops (GC and the warm pool) take a coordination lock (`coordination.Redis` lock) when Redis is enabled.
Without Redis Flow is single-instance by design (`config.Validate`), so a local mutex is enough. The leased loops are
safe on any number of replicas.

**Idempotency rules (all tracks):**

- Object names are deterministic: Pod `flow-cs-<sandboxId>`, PVC `flow-ws-<volumeId>`, Secret
  `flow-cs-<sandboxId>-token`, VolumeSnapshot `flow-snap-<snapshotId>`. Ids are 26-char lowercase ULIDs. "Already
  exists" counts as success and "not found" on delete counts as success.
- Every external side effect has a natural key: a PR/MR is looked up by head branch before it is created; commits are
  keyed by SHA; runner events carry client ids (`RunnerEvent.ID`, stored like `agent_run_keys`); inbox items have a
  per-session `seq`; session creation takes an `Idempotency-Key` (unique index).
- Retries use `coding.Backoff` (2s·2ⁿ, at most 1 min) and the per-phase caps in §4.1. Provider errors wrapped in
  `sandbox.ErrTransient` and code host `codehost.ErrTransient` count as retryable; everything else fails fast.

**Restart safety:**

- Flow stopping does not touch sandboxes. Runners keep working, buffer events (bounded at 8 MiB, oldest thought
  activities dropped first) and retry the runtime API with jittered backoff.
- When Flow comes back, leases expire and the loops resume from DB state. Heartbeat timeouts are measured from
  `max(last_heartbeat, process_start)`, so a Flow outage never marks healthy sessions `runnerLost`.
- Sandbox tokens are stored hashed, so they survive restarts. Pods created by a crashed Flow are adopted by name, and
  GC deletes anything without a row.
- No session state lives in memory. Waking channels (`AgentRunWake`-style) are only an optimization.

## 6. Data model

Flow's store is one `SQLiteStore` over SQLite, PostgreSQL and MySQL (`store.OpenDatabase`). Schema stages are
idempotent `ensure*` functions in `database.go`. The skeleton adds `ensureCodingSessions`
(`api/internal/store/coding_schema.go`), which creates every table below for all three dialects (BLOB, BYTEA or
LONGBLOB; MySQL index syntax handled like `ensureAgentRuns`). Timestamps are unix milliseconds. JSON documents live
in `data`, and the indexed columns are copies.

| Table | Key columns | Notes |
|---|---|---|
| `coding_environments` | `id`, `workspace_key`, `code_host`, `host_name`, `repository`, `active_repo_key` (NULL when archived), `version`, `data` | Unique `(workspace_key, active_repo_key)` = one active environment per repo |
| `coding_environment_secrets` | PK `(environment_id, name)`, `ciphertext` | AES-256-GCM with `FLOW_CODING_SECRET_KEY`, AAD = `envId\x00name`. No read or update API |
| `coding_environment_snapshots` | `id`, `environment_id`, `status`, `strategy`, `config_hash`, `storage_ref`, `logs_key`, `cancel_requested`, lease, `expires_at` | `config_hash` = SHA-256 of image + mise + prepare script + dependency mode + secret names + base ref |
| `coding_sessions` | `id`, `agent_session_id` (unique), `issue_id`, `environment_id`, `creator_id`, `source`, `idempotency_key` (unique), `state`, `phase`, `phase_started_at`, `phase_deadline`, `attempts`, `stop_requested`, `error_code`, lease, `next_reconcile_at`, `deadline_at`, `cost_micros`, `version`, `data` | `data` = `domain.CodingSession` (plan, summary, diffs, harness resume id, model resolution) |
| `coding_session_inbox` | PK `(session_id, seq)`, `kind`, `payload`, `delivered_at` | Turns and signals for the runner |
| `coding_sandboxes` | `id`, `session_id`, `snapshot_id`, `kind`, `provider`, `provider_ref`, `desired`, `phase`, `reason`, `size`, `region`, lease, `heartbeat_at`, `deadline_at`, `billed_seconds`, `logs_key`, `data` | Reference `sandboxes[]`. `data` = `sandbox.Record` extras (workspace source, env) |
| `coding_sandbox_tokens` | `token_hash` (SHA-256), `sandbox_id`, `session_id`, `setup_secrets_used`, `expires_at`, `revoked_at` | Prefix `fcs_`. TTL = sandbox deadline + `FLOW_CODING_TOKEN_GRACE` |
| `coding_session_pull_requests` | PK `(session_id, code_host, host_name, number)`, `repository`, `state`, `data` | `agentSessionToPullRequests`. The index on `(code_host, host_name, repository, number)` routes webhooks |
| `coding_session_commits` | PK `(session_id, sha)`, `turn_activity_id` | `commitsByTurnActivityId` |
| `coding_session_artifacts` | `id`, `session_id`, `object_key`, `data` | Screenshots (§17) |
| `coding_usage_records` | `id`, `workspace_key`, `session_id`, `sandbox_id`, `user_id`, `loop_id`, `kind` (llm or runtime), tokens, `runtime_seconds`, `size`, `cost_micros` | Spend windows are summed by `(workspace_key, user_id, created_at)` |
| `code_host_installations` | `id`, `workspace_key`, `code_host`, `host_name`, `installation_id`, `suspended_at`, `data` | GitHub App installations (one installation can serve several workspaces) |
| `coding_signing_keys` | `user_id`, `format` (ssh or openpgp), `ciphertext`, `fingerprint` | The private key, encrypted. Today `signing_keys.go` keeps only metadata; track E stores the material on upload |

The existing tables are reused: `application_agent_tasks` and `application_agent_activities` (the anchor and
activities), `connector_secrets` (GitLab tokens), `CodeReview` in workspace state (the PR list and "fix this PR").

**Legacy migration** (track K): at startup a data migration copies `CodingAgentSettings.Environments` (id, name,
repository, setupCommand → prepareScript, archived) into `coding_environments` with `codeHost` inferred from the
repository URL (default github / github.com), then clears the list. `CodingAgentSettings.Harness` values are
normalized on read.

Go types: `domain.CodingSession`, `CodingSandboxRecord`, `CodingEnvironment` (v2), `CodingEnvironmentSnapshot`,
`CodingEnvironmentProposal`, `SessionPullRequest`, `CodingCommit`, `CodingArtifact`, `CodingUsageRecord`,
`CodeHostInstallation`, `CodingErrorCode`. Store interfaces are in `internal/coding/store.go`, split per owning track
(`SessionStore`, `EnvironmentStore`, `ChangeRequestStore`, `ArtifactStore`, `UsageStore`, `SigningKeyStore`,
`sandbox.Store`). Track B adds `var _ coding.Store = (*store.SQLiteStore)(nil)` once all of them are implemented.

## 7. API routes

All routes are registered in the skeleton with 501 handlers. They are listed in `publicapi.InternalRoutes`, and
`docs/openapi.json` is regenerated (`node scripts/generate-openapi.mjs --check` passes). Each route file belongs to
one track (§24).

### 7.1 Web app (session cookie or API key, workspace-scoped)

| Method and path | Owner | Purpose |
|---|---|---|
| `GET /api/coding/status` | B | enabled, provider, snapshot strategy, regions, sizes, code hosts, preflight |
| `GET`, `PATCH /api/coding/settings` | B | Policy (admins write) |
| `GET /api/coding/models` | B | Catalog per harness, Auto pick, reasoning efforts |
| `GET /api/coding/usage` | B | Spend summary for Usage & limits |
| `GET`, `POST /api/coding/sessions` | B | List, or start (`Idempotency-Key`) |
| `GET /api/coding/sessions/{id}` | B | Session with sandboxes, PRs, commits, artifacts |
| `POST /api/coding/sessions/{id}/prompt`, `/stop`, `/restart`, `/approve`, `/pull-request` | B | Actions (§4.3) |
| `GET /api/coding/sessions/{id}/sandboxes/{sandboxId}/logs` | B | Live or archived logs |
| `GET /api/coding/sessions/{id}/artifacts` | I | Screenshots |
| `GET /api/coding/repositories` | K | Repo picker (`codeHost`, `hostName`, `q`, `cursor`) through `codehost.Host.ListRepositories` |
| `GET`, `POST /api/coding/environments`; `GET`, `PATCH /api/coding/environments/{id}` | K | CRUD (version CAS) |
| `POST /api/coding/environments/{id}/archive`, `/unarchive` | K | Archive |
| `POST /api/coding/environments/{id}/secrets`, `DELETE …/secrets/{name}` | K | Add (immutable) or delete |
| `POST /api/coding/environments/{id}/env-import` | K | Parse a .env into variables and secret suggestions |
| `GET`, `POST /api/coding/environments/{id}/snapshots` | K | History, or Refresh |
| `POST /api/coding/snapshots/{id}/cancel`, `GET /api/coding/snapshots/{id}/logs` | K | Cancel and logs (`?follow=true` streams) |
| `POST /api/coding/environments/{id}/configure-with-agent`, `/fix-with-agent`, `/proposal/apply` | K | Agent-assisted setup |
| `GET /api/coding/environments/{id}/terminal` (WebSocket) | K | Debug terminal |
| `POST /api/issues/{id}/coding-session` | L | Start or continue for an issue |
| `POST /api/reviews/{id}/coding-session` | L | From a PR/MR: `fix`, `followUp`, `rebase`, `lint`, `test` |
| `GET /api/integrations/github-app`, `POST …/install`, `GET …/setup`, `DELETE …/installations/{id}` | D | GitHub App |

The session's activities, plan and external URLs are read through the existing agent session endpoints
(`/api/agent-tasks/{id}` and the realtime events) using `agentSessionId`. No parallel activity API exists.

### 7.2 Runner-facing (sandbox token `Authorization: Bearer fcs_…`)

These paths are exempt from browser auth in `publicAuthPath` (done in the skeleton). Wire types are in
`internal/coding/protocol.go`.

| Method and path | Owner | Purpose |
|---|---|---|
| `GET /internal/coding-runtime/bootstrap[?wait=60s]` | B | `BootstrapResponse`. A warm sandbox gets 204 until it is bound |
| `GET /internal/coding-runtime/setup-secrets` | B | `SetupSecretsResponse`, once per sandbox, only in phase setup (or snapshot build) |
| `POST /internal/coding-runtime/heartbeat` | B | `HeartbeatRequest` → `{stop}` |
| `POST /internal/coding-runtime/events` | B | `EventsRequest` (≤100 events) |
| `GET /internal/coding-runtime/inbox?after=&wait=25s` | B | `InboxResponse` |
| `POST /internal/coding-runtime/turns/{turnId}/complete` | B | `TurnCompleteRequest` |
| `POST /internal/coding-runtime/credentials` | B (calls `codehost.Host.MintCredential`) | `CredentialRequest` → `CredentialResponse` |
| `GET`, `PUT /internal/coding-runtime/harness-state` | B | Harness session files (tar.zst, ≤64 MiB) in the object store |
| `PUT`, `GET /internal/coding-runtime/snapshots/{id}/archive` | K | Archive upload and restore download (streamed, `FLOW_CODING_ARCHIVE_MAX_BYTES`) |
| `POST /internal/coding-runtime/snapshots/{id}/logs`, `/complete` | K | Snapshot logs and result |
| `POST /internal/coding-runtime/environment-proposal` | K | Configure/Fix with agent result |
| `POST /internal/coding-runtime/sign` | E | `SignRequest` → `SignResponse` |
| `POST /internal/coding-runtime/artifacts` | I | Multipart screenshot upload |
| `POST /internal/coding-runtime/mcp/{id}` | B | MCP gateway: streamable-HTTP JSON-RPC (`initialize`, `ping`, `tools/list`, `tools/call`) relayed to an approved workspace MCP connector with its server-side credential; `GET`/`DELETE` answer 405 (no server stream, stateless). Listed in `BootstrapResponse.mcpServers`. See [security](coding-sessions-security.md#mcp-servers) |
| `POST /internal/coding-runtime/egress-check` | H | Squid ACL helper. Authenticated with `FLOW_CODING_EGRESS_ACL_SECRET`, not a sandbox token |
| `POST /internal/llm-proxy/anthropic/v1/messages[/count_tokens]` | E | Claude Code |
| `POST /internal/llm-proxy/openai/v1/responses`, `…/chat/completions`, `GET …/models` | E | Codex |
| `POST /internal/llm-proxy/opensource/v1/chat/completions`, `…/responses`, `GET …/models` | E | Open source, Pi |

The LLM proxy accepts the sandbox token as `Authorization: Bearer` (OpenAI, and Claude Code with
`ANTHROPIC_AUTH_TOKEN`) or as `x-api-key`. A token is valid only while its sandbox is running and the session is not
terminal.

### 7.3 Webhooks

- `POST /api/integrations/github-app/webhook` (D). It is public through the existing `/api/integrations/*/webhook`
  rule and verified with `X-Hub-Signature-256` and `FLOW_GITHUB_APP_WEBHOOK_SECRET`.
- GitLab keeps `POST /api/integrations/gitlab/webhook` (`code_webhooks.go`). Track J adds one call that hands verified
  Merge Request, Note and Pipeline hooks to `codehost/gitlab.ParseWebhook` and then to `s.dispatchCodingWebhook` (L).

## 8. Configuration

All variables are parsed in `api/internal/config/coding.go` (with tests). `_FILE` variants work for secrets, as
elsewhere in Flow. `FLOW_CODING_ENABLED=false` (the default) keeps everything off, and validation is strict only when
it is enabled.

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_ENABLED` | `false` | Turns on the runtime (workspaces also need the `coding-sessions` flag) |
| `FLOW_CODING_PROVIDER` | `disabled` | `disabled` \| `fake` \| `docker` \| `kubernetes` |
| `FLOW_CODING_RUNTIME_URL` | `FLOW_APP_URL` | URL sandboxes use to reach Flow (e.g. `http://flow.flow.svc:8090`) |
| `FLOW_CODING_RUNTIME_ADDR` | empty | Optional second listener serving only `/internal/coding-runtime/*` and `/internal/llm-proxy/*` |
| `FLOW_CODING_INSTANCE_ID` | `flow` | `flow.dev/instance` label value used for GC scoping |
| `FLOW_CODING_SECRET_KEY` | `FLOW_CONNECTOR_SECRET_KEY` | base64 32-byte AES key for secrets, signing keys and token material |
| `FLOW_CODING_IMAGE` | `ghcr.io/leozhengliu-pixel/flow-sandbox:latest` (pin a digest in production) | Sandbox image |
| `FLOW_CODING_IMAGE_PULL_POLICY` | `IfNotPresent` | |
| `FLOW_CODING_IMAGE_PULL_SECRETS` | empty | CSV of Secret names in the sandbox namespace |
| `FLOW_CODING_BRANCH_PREFIX` | `flow/` | Branch prefix. The runner may push only `prefix*` branches, or the PR head for PR follow-ups |
| `FLOW_CODING_PR_MENTION` | `@flow` | PR/MR comment trigger |
| `FLOW_CODING_BROWSER` | on | `off` disables browser testing |
| `FLOW_CODING_DEBUG_TERMINAL` | on | `off` disables the debug terminal |
| `FLOW_CODING_ARTIFACT_MAX_BYTES` | `10485760` | Per screenshot |
| `FLOW_CODING_MAX_SESSIONS` | `20` | Concurrent sessions per Flow installation |
| `FLOW_CODING_WORKSPACE_SESSIONS` | `5` | Concurrent sessions per workspace |
| `FLOW_CODING_RECONCILE_INTERVAL` | `5s` | |
| `FLOW_CODING_GC_INTERVAL` | `5m` | |
| `FLOW_CODING_ORPHAN_GRACE` | `10m` | |
| `FLOW_CODING_SIZE_SMALL` | `cpu=1,memory=8Gi,ephemeral=20Gi,pids=4096` | |
| `FLOW_CODING_SIZE_MEDIUM` | `cpu=2,memory=16Gi,ephemeral=40Gi,pids=8192` | |
| `FLOW_CODING_SIZE_LARGE` | `cpu=4,memory=32Gi,ephemeral=80Gi,pids=16384` | |
| `FLOW_CODING_WORKSPACE_SIZE` | `20Gi` | PVC size |
| `FLOW_CODING_CPU_REQUEST_RATIO` | `1` | requests = limits × ratio (1 = Guaranteed QoS) |
| `FLOW_CODING_REGIONS` | empty | CSV of region names. Empty means no region pinning |
| `FLOW_CODING_REGION_LABEL` | `topology.kubernetes.io/region` | Node label matched by region pinning |
| `FLOW_CODING_DEFAULT_REGION` | empty | |
| `FLOW_CODING_WARM_POOL` | empty | e.g. `small=2,medium=1` |
| `FLOW_CODING_WARM_POOL_MAX_AGE` | `1h` | |
| `FLOW_CODING_QUEUE_TIMEOUT` | `30m` | §4.1 |
| `FLOW_CODING_APPROVAL_TIMEOUT` | `168h` | |
| `FLOW_CODING_PROVISION_TIMEOUT` | `5m` | |
| `FLOW_CODING_START_TIMEOUT` | `10m` | |
| `FLOW_CODING_SETUP_TIMEOUT` | `20m` | |
| `FLOW_CODING_TURN_TIMEOUT` | `1h` | |
| `FLOW_CODING_HEARTBEAT_TIMEOUT` | `2m` | |
| `FLOW_CODING_PUBLISH_TIMEOUT` | `5m` | |
| `FLOW_CODING_IDLE_TTL` | `15m` | |
| `FLOW_CODING_STOP_GRACE` | `1m` | |
| `FLOW_CODING_MAX_SESSION_DURATION` | `4h` | `activeDeadlineSeconds` |
| `FLOW_CODING_DEBUG_TERMINAL_TTL` | `20m` | |
| `FLOW_CODING_TOKEN_GRACE` | `5m` | Sandbox token lifetime beyond the deadline |
| `FLOW_CODING_PROVISION_RETRIES` / `_START_RETRIES` / `_PUBLISH_RETRIES` / `_SNAPSHOT_RETRIES` | `3` / `2` / `5` / `1` | |
| `FLOW_CODING_SNAPSHOT_STRATEGY` | `archive` | `archive` \| `volumesnapshot` |
| `FLOW_CODING_VOLUME_SNAPSHOT_CLASS` | empty (cluster default) | |
| `FLOW_CODING_ARCHIVE_PREFIX` | `coding-snapshots` | Object key prefix in `FLOW_STORAGE_DRIVER` |
| `FLOW_CODING_ARCHIVE_MAX_BYTES` | `10737418240` | |
| `FLOW_CODING_SNAPSHOT_TIMEOUT` | `30m` | |
| `FLOW_CODING_SNAPSHOT_TTL` | `168h` | |
| `FLOW_CODING_SNAPSHOT_RETAIN` | `2` | Ready snapshots kept per environment |
| `FLOW_CODING_SNAPSHOT_REFRESH_INTERVAL` | `24h` | |
| `FLOW_CODING_K8S_KUBECONFIG` | empty (in-cluster) | |
| `FLOW_CODING_K8S_NAMESPACE` | `flow-sandboxes` | |
| `FLOW_CODING_K8S_RUNTIME_CLASS` | empty (runc) | e.g. `gvisor`, `kata` |
| `FLOW_CODING_K8S_HOST_USERS` | `true` | `false` sets `hostUsers: false` (user namespaces) |
| `FLOW_CODING_K8S_STORAGE_CLASS` | empty (default class) | |
| `FLOW_CODING_K8S_WORKSPACE_VOLUME` | `auto` | `auto` (PVC for volumesnapshot, emptyDir for archive) \| `pvc` \| `emptyDir` |
| `FLOW_CODING_K8S_NODE_SELECTOR` | empty | `k=v,k2=v2` (e.g. `flow.dev/pool=sandbox`) |
| `FLOW_CODING_K8S_TOLERATIONS` | empty | CSV in taint syntax `key[=value]:Effect` |
| `FLOW_CODING_K8S_SERVICE_ACCOUNT` | `flow-sandbox` | A ServiceAccount with no RBAC (the token is not mounted anyway) |
| `FLOW_CODING_K8S_PRIORITY_CLASS` | empty | |
| `FLOW_CODING_K8S_EXTRA_LABELS` / `_EXTRA_ANNOTATIONS` | empty | `k=v` CSV |
| `FLOW_CODING_K8S_NETWORK_POLICY` | `true` | Flow ensures the sandbox NetworkPolicies itself (set `false` when Helm or ops manage them) |
| `FLOW_CODING_K8S_FLOW_NAMESPACE` / `_FLOW_POD_SELECTOR` | `flow` / `app.kubernetes.io/name=flow` | NetworkPolicy peer for the Flow runtime |
| `FLOW_CODING_K8S_PROXY_NAMESPACE` / `_PROXY_POD_SELECTOR` | `flow-sandboxes` / `app.kubernetes.io/name=flow-egress` | NetworkPolicy peer for the proxy |
| `FLOW_CODING_K8S_QPS` / `_BURST` | `20` / `40` | client-go rate limits |
| `FLOW_CODING_DOCKER_HOST` | `unix:///var/run/docker.sock` | |
| `FLOW_CODING_DOCKER_NETWORK` | `flow-sandbox` | An internal network (no default route) shared with the proxy |
| `FLOW_CODING_DOCKER_RUNTIME` | empty | e.g. `runsc` |
| `FLOW_CODING_EGRESS_PROXY_URL` | empty | e.g. `http://flow-egress.flow-sandboxes.svc:3128`. Set as `HTTP(S)_PROXY` |
| `FLOW_CODING_EGRESS_ALLOWLIST` | empty | Extra domains (`.example.com` matches subdomains) |
| `FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST` | on | `off` drops the built-in registry list (§19) |
| `FLOW_CODING_EGRESS_NO_PROXY` | empty | Added to `NO_PROXY` (the Flow runtime host is always included) |
| `FLOW_CODING_EGRESS_ACL_SECRET` | empty | Shared secret for the Squid ACL helper |
| `FLOW_CODING_LLM_ANTHROPIC_BASE_URL` / `_API_KEY` | `https://api.anthropic.com` / empty | Upstream for Claude Code |
| `FLOW_CODING_LLM_OPENAI_BASE_URL` / `_API_KEY` | `https://api.openai.com/v1` / empty | Upstream for Codex |
| `FLOW_CODING_LLM_OPENSOURCE_BASE_URL` / `_API_KEY` | empty | OpenAI-compatible upstream (vLLM, Ollama, OpenRouter, …) |
| `FLOW_CODING_MODELS_CLAUDE_CODE` | `claude-sonnet-4-5,claude-opus-4-1` | The first entry is Auto |
| `FLOW_CODING_MODELS_CODEX` | `gpt-5-codex,gpt-5` | |
| `FLOW_CODING_MODELS_OPEN_SOURCE` | empty | |
| `FLOW_CODING_MODELS_PI` | empty | Pi; served by the open-source upstream |
| `FLOW_CODING_MODEL_PRICES` | empty | JSON `{"model":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}}`, USD per million tokens |
| `FLOW_CODING_MODEL_INFO` | empty | JSON per-model metadata `{"model":{"label","group","description","tier","contextWindow"}}`; `contextWindow` (integer 1024..10000000, null or left out = unset) becomes Codex `model_context_window` and the Pi model's `contextWindow` (configuration.md, Codex context window) |
| `FLOW_CODING_LLM_TIMEOUT` | `10m` | Per proxied request |
| `FLOW_CODING_RUNTIME_PRICES` | `small=0.10,medium=0.20,large=0.40` | USD per sandbox-hour |
| `FLOW_CODING_GITLAB_CREDENTIAL_MODE` | `project-token` | `project-token` \| `oauth` \| `connection` (§14) |
| `FLOW_CODING_GITLAB_TOKEN_SCOPES` | `read_repository,write_repository,read_api` | Scopes of minted project tokens (add `api` to let `glab mr note` write) |
| `FLOW_GITHUB_APP_ID` / `_SLUG` | empty | GitHub App |
| `FLOW_GITHUB_APP_CLIENT_ID` / `_CLIENT_SECRET` | empty | User-to-server OAuth (personal access checks) |
| `FLOW_GITHUB_APP_PRIVATE_KEY` | empty | PEM (use `_FILE`) |
| `FLOW_GITHUB_APP_WEBHOOK_SECRET` | empty | |
| `FLOW_GITHUB_APP_HOST` | `github.com` | GitHub Enterprise Server host |
| `FLOW_GITHUB_APP_API_URL` | derived | `https://api.github.com` or `https://<host>/api/v3` |

The GitHub App variables use `FLOW_GITHUB_APP_*` rather than `FLOW_CODING_*` because the App also serves PR sync for
the whole workspace, not only coding sessions. Track H documents every variable in `docs/configuration.md`.

## 9. Kubernetes objects per sandbox

Only GA APIs, through client-go: `core/v1` (Pod, PVC, Secret), `networking.k8s.io/v1` (NetworkPolicy), and
`snapshot.storage.k8s.io/v1` (GA since external-snapshotter v4, but a CRD) through the dynamic client, only when the
strategy is `volumesnapshot`. Flow creates bare Pods with `restartPolicy: Never`. It uses no Jobs because the
reconciler is the controller: retries need new tokens and new ids, and Job backoff would fight it.

Per sandbox:

| Object | Name | Lifetime |
|---|---|---|
| Secret (type Opaque, key `token`) | `flow-cs-<id>-token` | Created first, then owner-referenced to the Pod after the Pod exists, so it is GC'd with the Pod |
| PVC (volumesnapshot or `pvc` mode) | `flow-ws-<volumeId>` | `dataSource` VolumeSnapshot when restored. Deleted on stop unless kept for a snapshot build |
| Pod | `flow-cs-<id>` | `activeDeadlineSeconds` = session or debug TTL |
| VolumeSnapshot (builds only) | `flow-snap-<snapshotId>` | Until superseded or expired |
| NetworkPolicy (namespace-wide, when `FLOW_CODING_K8S_NETWORK_POLICY=true`) | `flow-sandbox-default-deny`, `flow-sandbox-egress` | Ensured at startup (idempotent) |

Pod spec (the contract for track A):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: flow-cs-<id>
  namespace: flow-sandboxes
  labels: {flow.dev/managed-by: flow, flow.dev/instance: <id>, flow.dev/kind: session, flow.dev/sandbox-id: <id>,
           flow.dev/session-id: <sid>, flow.dev/workspace: <hash>, flow.dev/size: medium, flow.dev/region: <r>}
spec:
  runtimeClassName: <FLOW_CODING_K8S_RUNTIME_CLASS or omitted>
  hostUsers: <omitted when true; false when FLOW_CODING_K8S_HOST_USERS=false>
  automountServiceAccountToken: false
  serviceAccountName: flow-sandbox
  enableServiceLinks: false
  restartPolicy: Never
  activeDeadlineSeconds: 14400
  terminationGracePeriodSeconds: 60
  priorityClassName: <optional>
  nodeSelector: {<FLOW_CODING_K8S_NODE_SELECTOR>}
  affinity: {nodeAffinity: requiredDuringScheduling: <region label In [region]> when pinned}
  tolerations: [<FLOW_CODING_K8S_TOLERATIONS>]
  imagePullSecrets: [...]
  securityContext:
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
    fsGroup: 1000
    fsGroupChangePolicy: OnRootMismatch
    seccompProfile: {type: RuntimeDefault}
  initContainers:            # archive strategy only
  - name: restore
    image: <FLOW_CODING_IMAGE>
    command: [flow-runner, restore]
    env: [FLOW_RUNTIME_URL, FLOW_TOKEN_FILE, FLOW_SANDBOX_ID, FLOW_RUNNER_MODE=restore]
    securityContext: &sc {allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities: {drop: [ALL]}}
    volumeMounts: [workspace, home, token(ro), tmp]
  containers:
  - name: runner
    image: <FLOW_CODING_IMAGE>
    command: [flow-runner, session]          # snapshot | debug | warm
    env:
      FLOW_RUNTIME_URL, FLOW_TOKEN_FILE=/var/run/flow/token, FLOW_SANDBOX_ID, FLOW_RUNNER_MODE,
      FLOW_SANDBOX=1, (FLOW_SNAPSHOT=1 while snapshotting),
      HTTP_PROXY/HTTPS_PROXY/http_proxy/https_proxy=<FLOW_CODING_EGRESS_PROXY_URL>, NO_PROXY=<flow host,…>,
      HOME=/home/flow
    resources: {limits: {cpu, memory, ephemeral-storage}, requests: limits × ratio}
    securityContext: *sc
    volumeMounts:
      - {name: workspace, mountPath: /workspace}
      - {name: home, mountPath: /home/flow}
      - {name: tmp, mountPath: /tmp}
      - {name: token, mountPath: /var/run/flow, readOnly: true}
  volumes:
    - workspace: PVC flow-ws-<vol> | emptyDir {sizeLimit: FLOW_CODING_WORKSPACE_SIZE}
    - home: emptyDir {sizeLimit: 10Gi}        # mise installs land here and go into the archive
    - tmp: emptyDir {medium: "", sizeLimit: 4Gi}
    - token: secret {secretName: flow-cs-<id>-token, defaultMode: 0400}
```

PID limits are not a Pod field. Track A enforces them at three layers: (a) Helm's kubelet guidance for
`podPidsLimit` on the sandbox node pool; (b) `LimitRange` and `ResourceQuota` (Helm); (c) the runner sets
`ulimit -u` per size (`SandboxSize.PIDs`) before spawning the harness. The docker provider uses `PidsLimit`.

Namespace objects (Helm or Flow-ensured): label `pod-security.kubernetes.io/enforce: restricted`; a ResourceQuota
(pods, cpu, memory, ephemeral-storage, PVCs); a LimitRange; NetworkPolicies:

```yaml
# default deny
podSelector: {matchLabels: {flow.dev/managed-by: flow}}
policyTypes: [Ingress, Egress]
---
# egress allow
podSelector: {matchLabels: {flow.dev/managed-by: flow}}
egress:
- to: [{namespaceSelector: {kubernetes.io/metadata.name: kube-system}, podSelector: {k8s-app: kube-dns}}]
  ports: [{port: 53, protocol: UDP}, {port: 53, protocol: TCP}]
- to: [{namespaceSelector: {kubernetes.io/metadata.name: <FLOW_NAMESPACE>}, podSelector: <FLOW_POD_SELECTOR>}]
  ports: [{port: <runtime port>}]
- to: [{namespaceSelector: …<PROXY_NAMESPACE>, podSelector: <PROXY_POD_SELECTOR>}]
  ports: [{port: 3128}]
ingress: []   # pod exec goes through the API server and kubelet, so it needs no ingress rule
```

Flow's RBAC (Helm `Role` in the sandbox namespace only): pods (create, get, list, delete), pods/log (get),
pods/exec (create, get), secrets (create, get, list, delete, patch), persistentvolumeclaims (create, get, list, delete),
namespaces (get, the sandbox namespace only), networkpolicies (get, create, patch) when Flow manages them, and volumesnapshots in
`snapshot.storage.k8s.io` (create, get, list, delete) when that strategy is on. Flow needs no ClusterRole. Preflight
uses `SelfSubjectAccessReview` and reads RuntimeClass, StorageClass and VolumeSnapshotClass when it is allowed to
(those checks become warnings otherwise).

The docker provider maps the same spec to one container (`--user 1000`, `--cap-drop ALL`, `--security-opt
no-new-privileges`, `--read-only` plus tmpfs and volumes, `--pids-limit`, `--memory`, `--cpus`, `--runtime`), a
named volume per workspace, the token as a bind-mounted 0400 file from a Flow temp dir, and the internal network
`FLOW_CODING_DOCKER_NETWORK` (no default route) that also holds the egress proxy and Flow. `Exec` uses the Docker exec
API with TTY.

### 9.1 OpenSandbox provider

`FLOW_CODING_PROVIDER=opensandbox` (`api/internal/sandbox/opensandbox`) runs
sandboxes through an [OpenSandbox](https://github.com/alibaba/OpenSandbox)
lifecycle server, typically in the same cluster as Flow.

**Pinned spec.** Implemented from the public specs at tag `release-1.1.0`
(published 2026-09-21, commit `b1a29cf9`; the newest release at least two
weeks old when this was written; the repository now lives at
`opensandbox-group/OpenSandbox`):

- Lifecycle API `specs/sandbox-lifecycle.yml`, `info.version` 0.1.0, base path `/v1`, header `OPEN-SANDBOX-API-KEY`;
- execd API `specs/execd-api.yaml`, `info.version` 1.0.0, header `X-EXECD-ACCESS-TOKEN`, port 44772;
- execd PTY sessions `components/execd/PTY.md` (not in the OpenAPI spec);
- server behaviour read from `server/opensandbox_server/` (error codes in `services/constants.py`, BatchSandbox status mapping, unauthenticated proxy routes in `middleware/auth.py`) and the Go SDK (`sdks/sandbox/go`: SSE parsing, exit codes from `error.evalue`);
- deployment from `docs/deployment/index.md` (Service `opensandbox-server` in `opensandbox-system`, port 80; sandbox Pods in namespace `opensandbox`).

Source: `https://github.com/alibaba/OpenSandbox/tree/release-1.1.0/specs`.
Differences from the original assumptions: there is no per-request
RuntimeClass or `hostUsers` (gVisor/Kata/Firecracker are server-side
`[secure_runtime]`), creation is synchronous (the server waits for the Pod),
sandbox ids are server-generated, and the PTY API is execd's, not the
lifecycle server's.

**Mapping.**

| Provider method | OpenSandbox |
|---|---|
| `StartSandbox` | Look up by metadata (`GET /sandboxes?metadata=flow.dev/managed-by=flow&flow.dev/instance=…&flow.dev/sandbox-id=…`); if absent, `POST /sandboxes` in the background (image = Flow sandbox image, entrypoint `tail -f /dev/null`, `env` = runner env with `FLOW_TOKEN_FILE` = `FLOW_CODING_OPENSANDBOX_TOKEN_PATH`, `resourceLimits` cpu/memory[/ephemeral-storage], `resourceRequests` from `FLOW_CODING_CPU_REQUEST_RATIO`, `metadata` = the `flow.dev/*` labels + `flow.dev/deadline`, `timeout` ≤ expiry window, optional `networkPolicy`, `extensions.poolRef`, `image.auth`). Then wait for execd `/ping`, upload the token (`POST /files/upload`, 0600, chown to the runner uid) and run `flow-runner <mode>` as a background `POST /command` (uid/gid 1000, cwd `/workspace`); the command id is stored as metadata `flow.dev/runner` (`PATCH /sandboxes/{id}/metadata`). Returns `pending` until then. |
| `Status` | Sandbox state + runner command status (`GET /command/status/{id}`): Pending/Allocated → starting (POOL_CAPACITY → pending/QuotaExceeded); Running/Paused + runner running → running; runner exit 0 → succeeded, 86 with restore → failed/InitFailed, other → failed/ExitNonZero, unknown command → failed/Evicted; Failed → failed with ImagePullFailed/Unschedulable/QuotaExceeded/ExitNonZero/StartFailed from reason and message; Stopping → failed/Evicted; Terminated → missing (DeadlineExceeded for TTL expiry); Running without a runner and no launch in flight → missing, so the reconciler restarts it (adoption with a fresh token). Also renews the expiry. |
| Create errors | 403 `KUBERNETES::QUOTA_EXCEEDED` and 429 `POOL_CAPACITY_EXHAUSTED` → `ErrQuota` (capacityUnavailable, no attempts spent); image-pull messages → `ErrImagePull`; 5xx/409/timeouts → `ErrTransient`; 400/401/403 → permanent (startFailed). Reported by the next `StartSandbox`. |
| `StopSandbox` | `kill -TERM` the runner (pid file) via execd, record `flow.dev/stop-requested`, `DELETE /sandboxes/{id}` once the runner exited or `TerminationGrace` passed; forced stops and runner-less sandboxes are deleted at once; 404 is success. |
| `Exec` | TTY: `POST /pty` + WebSocket `/pty/{id}/ws` (stdin `0x00`, output `0x01`/`0x03`, resize JSON frames, `exit` frame), `setpriv` to the runner uid when execd is root, `DELETE /pty/{id}` afterwards. Otherwise a foreground `POST /command` streaming stdout/stderr; stdin is uploaded to a temp file and redirected. |
| `Logs` | `GET /command/{runner}/logs` (cursor-based; `Follow` polls until the runner exits). |
| `ListManaged` / `DeleteManaged` | List by metadata (`flow.dev/managed-by`, `flow.dev/instance`, kind/session/sandbox filters) → objects of type `sandbox`; delete by OpenSandbox id after checking the instance label. |
| Volumes, VolumeSnapshots | `ErrUnsupported`: the archive strategy is used; restore runs inline before the runner (`flow-runner restore`, exit 86). OpenSandbox's own snapshots (`POST /sandboxes/{id}/snapshots`, image commit to a registry) and pause/resume are not used yet: Flow's snapshot contract is per-workspace archives, and pause would need a contract change. |
| Warm pool | Flow's warm pool (warm-kind sandboxes) works unchanged; `FLOW_CODING_OPENSANDBOX_POOLS` can additionally route a size to an OpenSandbox Pool (`extensions.poolRef`). |
| Egress | Flow's egress proxy via `HTTP(S)_PROXY` as on every provider, plus optionally (`FLOW_CODING_OPENSANDBOX_EGRESS_POLICY=proxy`) an OpenSandbox `networkPolicy` that denies everything except the Flow runtime host and the proxy host (domains only). |
| `Preflight` | `GET /health`, an authenticated list (API key), runtime URL sanity, runtime/egress warnings. |

**Idempotency and restarts.** Creates run in a goroutine bounded by
`FLOW_CODING_OPENSANDBOX_CREATE_TIMEOUT`; while one is in flight the same
process reports `pending` and never creates twice. After a crash or a lease
hand-over the metadata lookup finds the sandbox; if two exist for one Flow id
the one with a runner (then the oldest) wins and the others are deleted. A
lost create response is recovered by a lookup. The runner wrapper creates
`<token dir>/launched` atomically, so a relaunch after a crash between
launching and recording the command id waits for the first runner instead of
starting a second one.

**Expiry.** `timeout = min(FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW, deadline +
stop grace + 10m)`; `Status` renews (`POST /renew-expiration`) when less than
half the window is left, never past `flow.dev/deadline`. Orphans of a Flow that
is gone expire within the window; orphan GC deletes those of a running Flow.

**Security notes.** The token is never an env var (OpenSandbox stores create
`env` in its CRs); it is uploaded through execd. In single-tenant mode the
lifecycle server does not authenticate `/sandboxes/{id}/proxy/{port}` routes,
so anyone who can reach the server can reach execd: restrict ingress to the
server with a NetworkPolicy and consider an execd access token.

## 10. Snapshot strategies

A snapshot is the repo at `baseSha` after mise install and the prepare script (secrets are available while it is
built). Sessions restore the latest ready snapshot whose `config_hash` matches; otherwise they run setup inline and,
on success, trigger a background snapshot build.

**archive** (default; works on every CSI and on Docker):

1. Build: a `snapshot` sandbox with emptyDir workspace and home. The runner clones, installs and prepares, then
   streams `tar --zstd` of `/workspace` and `/home/flow` (excluding caches listed in
   `.flow/snapshot-exclude` plus defaults such as `~/.cache/ms-playwright`, which is already in the image) to
   `PUT /internal/coding-runtime/snapshots/{id}/archive`. Flow streams it to `objectstore.Put` under
   `<FLOW_CODING_ARCHIVE_PREFIX>-<workspace hash>-<snapshot>.tar.zst` (flat object keys), computing size and SHA-256 and enforcing
   `FLOW_CODING_ARCHIVE_MAX_BYTES`. Archives never pass through the sandbox's egress proxy because they go to Flow
   directly.
2. Restore: the `restore` init container GETs the archive with the sandbox token and unpacks it into the fresh
   emptyDir or PVC. A failed or corrupt download fails the init container, which maps to `snapshotFailed`; the
   reconciler retries once and then falls back to inline setup.
3. Secrets: an archive contains whatever the prepare script wrote to disk. The editor warns about this (as the reference
   does), and the runner unsets secret env vars before archiving.

**volumesnapshot** (requires CSI snapshot support and the `snapshot.storage.k8s.io/v1` CRDs):

1. Build: the reconciler creates a PVC `flow-ws-<vol>` and a build sandbox that mounts it. After the runner reports
   success and exits, Flow creates `VolumeSnapshot flow-snap-<id>` (`volumeSnapshotClassName` =
   `FLOW_CODING_VOLUME_SNAPSHOT_CLASS`), waits for `readyToUse`, then deletes the build PVC.
2. Restore: `RestoreWorkspace` creates a PVC with `dataSource: {kind: VolumeSnapshot, name: flow-snap-<id>}`.
3. `/home/flow` is also on the PVC (subPath `home`), so mise installs are captured.

Operations, the same for both: Refresh (`POST …/snapshots`) supersedes queued builds. Cancel sets `cancel_requested`;
the reconciler stops the build sandbox and marks the snapshot canceled. Logs are posted by the runner line by line
(`POST …/logs`, appended to an object-store log `logs_key`) and streamed to the UI. Failed snapshots offer "Fix with
agent". Retention keeps `FLOW_CODING_SNAPSHOT_RETAIN` ready snapshots and deletes older ones through
`SnapshotStrategy.Delete`, idempotently.

The user's cluster lacks VolumeSnapshot support, so it uses `archive`, which is the default. Validation rejects
`volumesnapshot` with emptyDir workspaces or with the docker provider.

## 11. Warm pool and regions

`FLOW_CODING_WARM_POOL=small=2,medium=1` keeps unbound sandboxes per size (and per region when regions are
configured). A warm sandbox runs `flow-runner warm`: it has a token with the `warm` scope that can only call
`GET /bootstrap?wait=60s`, so it holds no secrets, no repo access and no LLM access until it is bound.

Binding: in `provisioning`, the session reconciler calls `BindWarmSandbox(size, region, sessionID)`, a CAS on an
unbound row. The token's scope is upgraded in the DB (same token), and the next long-poll returns the session
bootstrap. The runner then restores the archive itself (the same code as the init container) or clones fresh.

Limits: warm sandboxes serve the archive strategy and fresh clones. Sessions using `volumesnapshot` need the PVC at Pod
creation and skip the pool. Pool sandboxes are recycled after `FLOW_CODING_WARM_POOL_MAX_AGE` and count toward the
ResourceQuota. Their idle time is an operator cost and is not billed to users.

Regions: `FLOW_CODING_REGIONS` lists names. A Pod is pinned with required node affinity
`FLOW_CODING_REGION_LABEL In [region]`. The workspace setting "Pin sandbox region" (`pinnedRegion`) wins; otherwise
`FLOW_CODING_DEFAULT_REGION` applies, otherwise any region. The region is recorded on the sandbox and the session.
The docker provider ignores regions, and `/api/coding/status` reports `regions: []`.

## 12. LLM proxy, model routing and reasoning effort

No model API key enters a sandbox. The harness talks to Flow:

| Harness | Configuration written by the runner |
|---|---|
| Claude Code | `ANTHROPIC_BASE_URL={runtime}/internal/llm-proxy/anthropic`, `ANTHROPIC_AUTH_TOKEN=<sandbox token>`, `ANTHROPIC_MODEL=<model>`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `DISABLE_AUTOUPDATER=1`, `DISABLE_TELEMETRY=1` |
| Codex | `~/.codex/config.toml`: `model_provider="flow"`, `[model_providers.flow] base_url="{runtime}/internal/llm-proxy/openai/v1" env_key="FLOW_LLM_TOKEN" wire_api="responses"`, `model=<model>`, `model_reasoning_effort=<effort>`, and `model_context_window=<n>` only when bootstrap `harness.contextWindow` is set (`FLOW_CODING_MODEL_INFO` `contextWindow`, Codex sessions only; rewritten before every turn, resumed turns included; `model_auto_compact_token_limit` is never written) |
| Open source (OpenCode) | `opencode.json` provider `flow` (OpenAI-compatible) pointing at `{runtime}/internal/llm-proxy/opensource/v1` with the sandbox token |
| Pi | `PI_CODING_AGENT_DIR=~/.pi/agent`: `models.json` provider `flow` (`api: openai-completions`, `baseUrl` `{runtime}/internal/llm-proxy/opensource/v1`, `apiKey: "$FLOW_LLM_TOKEN"`, compat `supportsStore:false`, `supportsDeveloperRole:false`, `maxTokensField:"max_tokens"`; the model gets `reasoning:true` only with a reasoning effort and `contextWindow` from bootstrap `harness.contextWindow`), `mcp.json`, `settings.json` (`defaultProjectTrust:"never"`) and `APPEND_SYSTEM.md` (guidance). `PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0` |

The proxy (track E, `internal/llmproxy`) does the following:

1. It authenticates the token (`Authenticator`), which resolves the caller: workspace, session, user, loop and
   resolved model.
2. It checks spend (`Meter.CheckSpend`, using a 30s cached sum). Over the limit, it answers 402 with
   `{"error":{"type":"quota_exceeded"}}`; the runner maps that to `quotaExceeded`.
3. It enforces the model. Requests naming another model are rewritten to the session's model. A fixed allowlist
   covers internal small models: Claude Code's background `haiku` calls are rewritten to the cheapest configured
   Claude model, never refused, so the harness keeps working.
4. It injects the upstream key, forwards the request, and streams SSE back unbuffered.
5. It meters usage from the stream: Anthropic `message_start.usage` plus `message_delta.usage`; OpenAI Responses
   `response.completed.usage`; chat completions `usage` (the proxy forces `stream_options.include_usage`). Then it
   calls `RecordUsage` with the cost from `FLOW_CODING_MODEL_PRICES`. Unknown prices are recorded at 0 with a
   warning metric.
6. It limits request bodies to 32 MiB and uses `FLOW_CODING_LLM_TIMEOUT`.

**Auto routing:** at provisioning time `auto` classifies the task (the session title and its first prompt) as small,
standard or large and picks the first catalog model of that tier (`FLOW_CODING_MODEL_INFO` `tier`, built-in tiers for
well-known models, otherwise standard), falling back to the nearest tier. The pick is stored in `CodingSession.Model`,
the reason in `modelReason` (shown in Session details), and the next more capable catalog model in `escalationModel`
("Start over with <model>": `POST /api/coding/sessions/{id}/restart` with `{"model":"<id>"}` runs the first prompt
again with that model). A routed session keeps its model across sandboxes. When the upstream answers model-not-found, overloaded (529) or 503 on an Auto session, the
proxy retries the request once with the next catalog model and records the switch as a thought activity ("Switched to
X"). A pinned model fails with `modelUnavailable` instead, and the UI offers "Restart with default model".

**Reasoning effort** (workspace setting, environment override): Codex `model_reasoning_effort` low, medium or high.
Claude Code `MAX_THINKING_TOKENS` 4k, 16k or 32k. Open source sends `reasoning_effort` when the model catalog marks it
as supported. Pi gets `--thinking low|medium|high` (sent upstream as `reasoning_effort`); with no effort the model is
declared without reasoning and Pi sends no reasoning field.

## 13. GitHub App

Configuration: `FLOW_GITHUB_APP_*` (§8). Required App permissions: Contents RW, Pull requests RW, Metadata R,
Checks R, Actions R, Commit statuses R, Issues R (for links), Workflows RW (only if agents may edit
`.github/workflows`; off by default). Events: installation, installation_repositories, pull_request,
pull_request_review, pull_request_review_comment, pull_request_review_thread (review thread resolution for the
Reviews sync), issue_comment, check_suite, check_run, push. Webhook URL:
`{FLOW_APP_URL}/api/integrations/github-app/webhook`. Setup URL: `/api/integrations/github-app/setup`.

**Install flow:** Settings › Integrations › GitHub › "Install GitHub App" calls `POST /install`, which returns
`https://{host}/apps/{slug}/installations/new?state=<HMAC(workspace, user, nonce, exp)>`. GitHub redirects to
`/setup?installation_id=&setup_action=install&state=`. Flow verifies the state, which must belong to the signed-in
user, so this route requires the browser session and checks the state's workspace explicitly. It fetches
`GET /app/installations/{id}` with an App JWT, upserts `code_host_installations`, and redirects to the coding
settings. Installation events from webhooks keep the rows current (suspend, delete, repository changes).

**Existing installation:** when the App is already installed on the account and the admin changes nothing, GitHub
shows the installation's settings and sends no setup redirect. An admin who has linked their own GitHub account then
sees the installations that account can access (`GET /available-installations`, from `GET /user/installations` with
the member's user-to-server token) and links one with `POST /installations {installationId}`; Flow checks the id is
in that list and reads it again with the App JWT before upserting the row.

**Tokens:** the App JWT is RS256, `iat=now-60s`, `exp=now+9m`, `iss=app id`. Installation tokens come from
`POST /app/installations/{id}/access_tokens` with `repositories:[name]` and the minimum permissions for the purpose
(git and CLI: contents write, pull_requests write, checks read, actions read, metadata read). They are cached in
memory per (installation, repo, permissions) until 5 min before expiry and never persisted. The runner gets them
through `POST /credentials`. Expiry is 1h, so long sessions refresh transparently because git asks the helper again.

**Personal access** (`personalAccessDenied` and `requiresGithubConnection`): when the policy requires it, the
requesting user must have linked GitHub (App user-to-server OAuth with `FLOW_GITHUB_APP_CLIENT_ID`, a member account
in `code_host_accounts`), and `GET /repos/{o}/{r}/collaborators/{login}/permission` must return write or admin.

**PRs:** in `publishing`, Flow calls `BranchHead` to verify the runner's reported SHA, then
`FindChangeRequest(head)` and, if there is none, `POST /repos/{o}/{r}/pulls {draft:true, head, base, title, body}`.
The body holds the session summary, `Fixes <ISSUE-ID>` / the Flow issue link, before/after screenshots (signed Flow
URLs, §17) and a "Session" link. Flow then calls `UpsertSessionPullRequest`, adds the PR to `externalUrls` on the
anchor (so the existing agent session UI shows it), and adds a response activity. The existing `CodeReview` sync
consumes the same webhooks (D routes App PR events into the current review applier), so App-created PRs appear under
Reviews and in the issue's PR list without OAuth or PAT.

**Commit identity:** without a linked account the author is the requesting user (Flow name and primary email) and
the committer is the App bot `<slug>[bot] <appid+slug[bot]@users.noreply.github.com>`, or the user when commits are
signed with the user's key (GitHub verifies the signature against the committer). The trailer is
`Co-authored-by: Flow <bot email>`.

**Pushing as the member.** When the session's creator linked their GitHub account (Settings › Account › Connected
accounts; App user-to-server OAuth with PKCE, 8-hour tokens refreshed by Flow, stored sealed in connector secrets,
see `code_host_accounts.go`), the session acts as that member end to end:

- the branch is `<username>/<identifier>-<title>-<suffix>` (the member's Flow username), and the bootstrap's
  `repo.branchPrefix` is that prefix so the runner's push guard allows it;
- author and committer are the member: their workspace display name and
  `<id>+<login>@users.noreply.<host>`, unsigned unless signed commits are on (no bot trailer);
- pushes go through Flow's git relay: the bootstrap's `repo.pushUrl` is
  `<runtime>/internal/coding-runtime/git/<host>/<owner>/<repo>.git`, which the runner sets as origin's push URL
  (fetches keep using `cloneUrl`). For that URL the git credential helper gets only an opaque placeholder (`fgr_…`,
  derived from the sandbox token, stored next to it and revoked with it); the relay (`coding_git_relay.go`,
  `coding.GitRelay`) accepts only the three smart-HTTP endpoints of the session's repository, swaps the placeholder
  for the member's user token (`coding.MemberCodeHost.MemberGitCredential`; any token the member linked, including a
  non-expiring OAuth token, since the relay is its only consumer) and forwards to
  `https://<host>/<owner>/<repo>.git/…`. The member's token never enters the sandbox. Straight to the host, the
  helper gets the installation token, and so does `{purpose:"cli"}` (written to `~/.flow/cli-env`);
- Flow opens the draft PR with the member's token (`CreateChangeRequestInput.ActAs`); a token GitHub rejects falls
  back to the App. `SessionPullRequest.openedAs` / `openedVia` record which;
- the App bot only posts Flow's back-link comments.

The member's token is looked up from the session's creator, which comes from the sandbox token, never from the
request; a finished session gets `410 Gone`. The relay serves only while the session runs. Without a link the
session pushes straight to the host with the installation token, `CodingSession.pushIdentity.via` is `workspace`, and the
session suggests linking the account. Details: [security model](coding-sessions-security.md#member-credentials).

**Webhooks to follow-ups:** `ParseWebhook` verifies `X-Hub-Signature-256` and normalizes the delivery
(`codehost.WebhookEvent`). `github_app.go` stores deliveries idempotently (by `X-GitHub-Delivery`, reusing the
webhook dedupe used by `code_webhooks.go`) and calls `s.dispatchCodingWebhook` (L).

The OAuth and PAT GitHub integration (`integration_oauth.go`, `code_webhooks.go`) stays unchanged for PR sync.
Coding sessions require the App, because only it can mint repository-scoped short-lived tokens.

## 14. GitLab and glab

GitLab is first-class: environments pick `codeHost=gitlab` and a host (gitlab.com or self-managed) from the
workspace's GitLab connections (`gitlab_integration.go` token connections, `integration_oauth.go` OAuth).

**Sandbox credentials** (`FLOW_CODING_GITLAB_CREDENTIAL_MODE`):

| Mode | How | Pros | Cons |
|---|---|---|---|
| `project-token` (default) | At provisioning Flow calls `POST /projects/:id/access_tokens` with the connection token: name `flow-cs-<sessionId>`, `access_level=30` (Developer), `scopes=FLOW_CODING_GITLAB_TOKEN_SCOPES`, `expires_at=tomorrow`. Flow revokes it (`DELETE /projects/:id/access_tokens/:token_id`) when the session ends; GC sweeps `flow-cs-*` tokens older than a day | Scoped to one project; least privilege; revocable; a bot user is visible in the audit log | The connection token needs `api` scope and Maintainer on the project. GitLab's minimum expiry is one calendar day, so Flow's revocation is what makes it short-lived. On gitlab.com project access tokens need Premium or Ultimate. The token is still alive if Flow dies before revoking (GC covers it, at most about 1 day) |
| `oauth` | The workspace's GitLab OAuth access token (2h expiry, refreshed by Flow through `oauth/{id}/refresh`) is handed to the runner | Short-lived; no paid tier | User-scoped, not project-scoped: it reaches every project the OAuth user can access |
| `connection` | The connection's own token is handed to the runner | Works everywhere | Long-lived and broad, which breaks the no-long-lived-secrets rule. Off unless chosen explicitly; startup logs a warning and the settings page shows a red banner |

Group and deploy tokens were rejected: deploy tokens cannot use the API (no `glab` and no CI status), and group
tokens are broader than one project.

**Members' GitLab accounts.** A member may link their own account on an instance the workspace connected (OAuth
through an application the admin registered on that instance, or a personal access token with `read_user` and
`api`). Sessions then push and open the MR as the member like on GitHub (§13): branch `<username>/…`, author and
committer the member's display name with their GitLab commit email or `<id>-<username>@users.noreply.<host>`, and the
MR opened with the member's token. Pushes go through Flow's git relay (§13), which adds the member's token server
side: an OAuth token (2 hours, refreshed by Flow) as `oauth2:<token>`, a personal access token as
`<gitlab username>:<token>`. Neither ever enters the sandbox, the bootstrap, a credential-helper answer or the glab
CLI env; the sandbox's project token (the mode above) is used only for fetches and glab. The member's username also satisfies the personal access check
(`GET /projects/:id/members/all/:user_id`, Developer or higher).

**glab and gh in the sandbox:** the image installs both. Neither is ever configured at build time. The runner fetches
the credential (`POST /credentials {purpose:"cli"}`) and exports the returned `CLIEnv` only into the harness
environment: `GITLAB_TOKEN`, `GITLAB_HOST`, `GLAB_CONFIG_DIR=/home/flow/.config/glab-cli` (with `glab config set -g
host` written at runtime for self-managed hosts) or `GH_TOKEN`, `GH_HOST`. It refreshes them before expiry by
restarting nothing: it rewrites a small `~/.flow/cli-env` and wrappers `gh` and `glab` in `/opt/flow/bin` (earlier on
`PATH`), which re-source it on every call. The harness guidance tells the agent it may use `glab ci status/view`,
`glab mr view`, `glab mr note` (when the scope allows) and `gh pr view/checks`, but it must not create PRs or MRs
itself.

**Decision: Flow creates the MR/PR itself through the API.** Creating it in Flow is reliable and idempotent (head
branch lookup), always links the MR to the session, survives harness quirks, and does not need `api` scope in the
sandbox token. Agents use the CLIs for reading CI and comments, and for replying when the scope permits it.

**MR flow** (mirrors §13): the draft MR is `POST /projects/:id/merge_requests` with title `Draft: <title>`,
`source_branch`, `target_branch`, `description`, and `remove_source_branch=true`; lookup is
`GET /merge_requests?source_branch=&state=opened`. Merge Request, Note and Pipeline hooks reach `code_webhooks.go`
(verified with the existing `X-Gitlab-Token` check). Track J's single additive call hands them to
`codehost/gitlab.ParseWebhook` and then to `dispatchCodingWebhook`. "Fix this MR" uses the same review route (L),
because GitLab MRs already sync into `CodeReview`. Pipeline status is visible to the agent through `glab ci status`
and is summarized in follow-up prompts from Pipeline hooks.

**Egress:** `codehost.Host.EgressHosts` returns the configured GitLab hosts (and the GHE host). The egress ACL adds
them to every sandbox's allowlist (§19).

## 15. Triggers and PR/MR follow-ups

All triggers call `coding.Service.Start(ctx, StartInput{Source, SourceID, IssueID, Prompt, EnvironmentID?,
Harness?, Model?, CreatorID, LoopID?})` (track B). Track L owns the trigger glue:

- **Delegate an issue** to the built-in Flow agent: the existing delegation creates the AgentTask. The Flow agent
  (chat runtime) gets a `start_coding_session` tool (L adds it to the agent tool groups and to `flow_mcp_tools.json`).
  It decides, as the reference agent does, when code changes are needed, and calls the tool. The tool reuses the existing
  task as the anchor. The UI also offers an explicit "Start coding session" (`POST /api/issues/{id}/coding-session`).
- **Agent chat**: the same tool from chat. Without an issue, the anchor is an AgentTask with `ResourceType="agentChat"`.
- **Comment**: `@Flow` in an issue comment prompts the agent, which then decides like a delegation.
- **Repository selection**: an explicit environment wins. Otherwise: the single active environment; or the issue's
  linked PRs' repos; or the team's last-used environment. If it is still ambiguous, the session goes to
  `awaitingUser` with a `select` elicitation listing environments (the reference `select` signal).
- **Loops**: the "Autofix bugs" template's "Start a coding session" action calls the same tool from a loop run.
  Allowed only when `loopRepositoryAccess` is `all`, or `selected` and the environment is in `loopRepositories`
  (otherwise `repoNotInPolicy`). Spend is charged to `loopLimit` and `loopOverrides`.
- **Trusted external sources**: issues created by integrations (Slack, Intercom, email intake, Sentry, GitHub or
  GitLab issue sync, API apps) or comments by external users count as external. Unless the source is in
  `trustedExternalSources`, the session starts in `awaitingApproval`; team members get an inbox notification, and any
  member can approve or reject.
- **PR/MR follow-ups** (`dispatchCodingWebhook`):
  - A comment or review comment on a session-linked PR/MR mentioning `FLOW_CODING_PR_MENTION`, or a "changes
    requested" review on a Flow-authored PR, becomes an inbox turn (`prComment`) carrying the comment, the file and
    line, and the diff hunk.
  - A mention on an unlinked PR/MR (same repo, not a fork, environment exists) creates a new session with
    `source=pullRequest` on the PR head branch (`existingBranch=true`).
  - The commenter must map to a Flow member (linked host identity, or the commit email); otherwise the comment is
    treated as an external source.
  - A merged or closed PR updates `SessionPullRequest.state`. A merged PR ends an idle session.
  - CI failure on a session PR shows a "Fix failing checks" action. Flow never retries automatically.
- **"Fix this PR"** and quick actions on the review page (`POST /api/reviews/{id}/coding-session`): `fix` (failing
  checks and unresolved review threads), `rebase` (rebase on base and push with `--force-with-lease`, allowed only on
  `flow/` branches or with explicit confirmation), `lint`, `test`, `followUp` (free prompt).

## 16. In-sandbox runner and activity mapping

`sandbox-runner/` (track C) is a TypeScript (Node 22) program bundled to `/opt/flow/runner/flow-runner.mjs`, with
shims in `/opt/flow/bin`: `flow-runner`, `flow-git-credential`, `flow-sign`, and `gh`/`glab` wrappers.

**Lifecycle (session mode):**

1. Read the token from `FLOW_TOKEN_FILE` and call `GET /bootstrap`, then start the heartbeat (15s).
2. Prepare the workspace:
   - If `restored`, `git fetch` and check out the branch (an existing branch, or a new one from `baseRef`).
   - Otherwise clone `cloneUrl`, using `git config credential.helper "/opt/flow/bin/flow-git-credential"` with
     `credential.useHttpPath=true`.
   - Without a snapshot, run setup: write the mise files into `$HOME`, set `mise.toml` tools, run `mise install`,
     fetch the setup secrets, and run the prepare script with the secrets and plain env.
   - Dependency management `custom-and-generated` also runs detected installers (npm/pnpm/yarn ci, `pip install -r`,
     `go mod download`, `bundle install`, `cargo fetch`) when the prepare script did not.
   - Then unset the secrets.
3. Configure git: author and committer identity, `commit.gpgsign` with `gpg.format=ssh` and
   `gpg.ssh.program=/opt/flow/bin/flow-sign` (openpgp: `gpg.program=flow-sign`), and `user.signingkey` set to the
   public key.
4. Run the turn. Spawn the harness (table below) in its own process group, with `ulimit -u`, the turn timeout and
   `cwd=/workspace/repo`. Parse JSONL stdout and map it to `RunnerEvent`s (batched every 250 ms or every 20 events).
5. At turn end:
   - Run `git add -A`. If anything changed, commit with a message from the harness final summary (first line, at most
     72 characters, plus the trailer).
   - Push `branchName` (refuse branches outside `FLOW_CODING_BRANCH_PREFIX` unless `existingBranch`).
   - Compute `git diff --numstat base...HEAD`, then upload the harness state.
   - Call `POST /turns/{id}/complete`.
6. Idle: long-poll `/inbox` for the next turn or stop. On stop: SIGINT the harness, wait `stopGraceSeconds`, SIGKILL,
   commit and push WIP (`WIP: stopped by <user>`), complete with `stopped`, and exit 0.
7. Snapshot mode does steps 1 and 2, archives, calls complete, and exits. Debug mode sleeps until the deadline
   (`exec` sessions attach to it). Warm mode long-polls bootstrap. Restore mode downloads and unpacks.

**Harness commands** (the exact flags are pinned in `sandbox-runner/src/harness/*.ts` against the CLI versions
pinned in the image; track C verifies them in its tests):

| Harness | First turn | Follow-up |
|---|---|---|
| Claude Code | `claude -p "<prompt>" --output-format stream-json --verbose --model <m> --permission-mode bypassPermissions --append-system-prompt "<guidance>" --mcp-config ~/.flow/mcp.json` | add `--resume <session_id>` (from the `system/init` event) |
| Codex | `codex exec --json --model <m> -c model_reasoning_effort=<e> --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check -C /workspace/repo "<prompt>"` (guidance in `AGENTS.override.md` written to `~/.codex`) | `codex exec resume <thread_id> --json "<prompt>"` |
| Open source | `opencode run --format json -m flow/<m> "<prompt>"` | `opencode run --session <id> …` |
| Pi | `pi --mode json --no-approve --provider flow --model <m> [--thinking <e>]` (prompt on stdin; guidance in `~/.pi/agent/APPEND_SYSTEM.md`) | add `--session <id>` (from the `session` header record) |

Both "bypass" flags are safe here because the Pod is the sandbox. Claude Code refuses `bypassPermissions` as root,
and the runner is uid 1000.

**Activity mapping** (runner → `RunnerEvent` → Flow `AgentActivity`; B maps events to activities and stores them
through the existing agent activity store, so realtime and `issue-agent-tasks.tsx` work):

| Source event | RunnerEvent | AgentActivity |
|---|---|---|
| Claude `system/init` | none (stores `session_id`, model) | none |
| Claude `assistant` → `thinking` block | `thought` (ephemeral) | thought, ephemeral |
| Claude `assistant` → `text` (not final) | `thought` | thought |
| Claude `assistant` → `tool_use` Bash | `action {action:"Ran command", parameter:cmd}`; result from the matching `user/tool_result` (exit code and last 4 KiB) | action |
| Claude `tool_use` Edit, Write, MultiEdit, NotebookEdit | `action {action:"Edited", parameter:path}` plus a `diff` event | action |
| Claude `tool_use` Read, Grep, Glob, LS | `action {action:"Read"/"Searched"}` (ephemeral) | action, ephemeral |
| Claude `tool_use` WebFetch, WebSearch | `action {action:"Fetched"/"Searched the web"}` | action |
| Claude `tool_use` Task | `action {action:"Delegated to subagent"}` | action |
| Claude `tool_use` TodoWrite | `plan` (pending→pending, in_progress→inProgress, completed→completed) | session plan replace |
| Claude `tool_use mcp__flow__ask_user` | `elicitation` | elicitation; phase awaitingUser |
| Claude `tool_use mcp__flow__*` (screenshot, proposal) | `action` | action |
| Claude `result` success | `response {body: result}` plus `usage` | response |
| Claude `result` error_max_turns / error_during_execution | `error {code: harnessFailed}` | error |
| Codex `thread.started` | none (stores `thread_id`) | none |
| Codex `item.*` reasoning | `thought` (ephemeral) | thought |
| Codex `item.completed` agent_message (not last) / last | `thought` / `response` | thought / response |
| Codex `item.started`/`item.completed` command_execution | `action {"Ran command", command, exit_code, aggregated_output tail}` | action (ephemeral while running, replaced on completion) |
| Codex `item.completed` file_change | `action {"Edited", paths}` plus `diff` | action |
| Codex `item.*` mcp_tool_call | `action {tool}` (an ask_user elicitation as with Claude) | action / elicitation |
| Codex `item.*` web_search | `action {"Searched the web", query}` | action |
| Codex `item.*` todo_list | `plan` | plan replace |
| Codex `turn.completed` | `usage` | none |
| Codex `turn.failed` / `error` | `error` | error |
| OpenCode `text` / `tool_use` / `step_finish` / `error` | thought or response / action / usage / error | as above |
| Pi `session` | none (stores the session `id`) | none |
| Pi `message_end` assistant: `thinking` / `text` (not final) / last `text` | `thought` (ephemeral) / `thought` / `response`; its `usage` (input + cacheRead + cacheWrite, output) summed into `usage` | as above |
| Pi `tool_execution_end` bash, edit/write, read/ls, grep/find, `mcp__flow__ask_user`, `mcp__flow__*`, `mcp__<server>__<tool>`, `tool_search` | `action` "Ran command" (exit code) / "Edited" plus `diff` / "Read" / "Searched" / `elicitation` / Flow tool action / `server.tool` / "Searched tools" (ephemeral) | as above |
| Pi `message_end` with `stopReason:"error"` left after the automatic retries (`auto_retry_end success:false`) | `error` | error |
| Runner phases (clone, setup, push, PR) | `status` / `action` ("Cloned repo", "Installed tools", "Pushed branch") | action |
| Flow follow-up turn | none (Flow writes a `prompt` activity when the inbox item is created) | prompt |

The signals `stop`, `continue`, `auth` and `select` from the AgentTask protocol map to inbox items. `auth` is used when
a code host credential fails (for example the user needs to link GitHub): the session goes to `awaitingUser` with an
auth elicitation.

**Runner-local MCP server** `flow` (stdio, registered in the Claude and Codex configs): `ask_user` (C),
`attach_screenshot` and `browser_*` (I), `propose_environment` (K). Each tool lives in its own file under
`sandbox-runner/src/mcp/tools/`. Pi reads MCP servers only from `~/.pi/agent/mcp.json` (`--no-approve`: a
repository's `.pi/mcp.json` never adds or changes servers). The `flow` server has `exposure: "direct"`, so its tools
are declared like built-in tools (Pi's default `codemode` exposure would hide them behind scripts). Workspace servers
behind the MCP gateway use `exposure: "deferred"` with their title as description and
`Authorization: Bearer ${FLOW_LLM_TOKEN}`: they are listed in the system prompt and the model loads their tools with
`tool_search`, which keeps large connector tool lists out of every request and does not hold the first prompt for
gateway connections.

**Base image** (`deploy/sandbox-image/`, track C; the browser layer is track I's):

- Base: `debian:bookworm-slim`. User `flow` (uid 1000). `/opt/flow`.
- Packages: git, git-lfs, openssh-client (ssh-keygen for verification), ca-certificates, curl, jq, ripgrep, fd, make,
  build-essential, python3, zstd, tar, unzip, procps, tini.
- Toolchains: mise (system-wide); default tools node 22, python 3.12, go and java via mise, with pre-warmed caches.
- Agent CLIs: claude, codex, opencode and pi at pinned versions. gh and glab from their release packages. Node 22 for the
  runner.
- Browser: Playwright plus Chromium with fonts, preinstalled to `/opt/ms-playwright` (`install-browser.sh`, track I).
- `ENTRYPOINT ["tini","--","flow-runner"]`. No credentials or config are baked in. It is built and pushed by
  `.github/workflows/container.yml` (an additive job owned by C).

## 17. Browser testing

Track I owns this end to end:

- Image: `deploy/sandbox-image/install-browser.sh` (Playwright and Chromium headless, plus fonts).
- Runner MCP tools: `browser_open(url)`, `browser_click`, `browser_type`, `browser_screenshot(label)`, and
  `attach_screenshot(path,label)` in `sandbox-runner/src/browser/` and `src/mcp/tools/browser.ts`. They use
  Playwright directly and run headless.
- Guidance (added to the bootstrap `guidance` by B, from text that I owns): when a change affects UI, start the app
  using the environment's guidance or the scripts in `package.json`, take a "before" screenshot on the base ref
  (`git stash` / worktree) and an "after" screenshot on the branch, and attach both.
- Upload: `POST /internal/coding-runtime/artifacts` (multipart) → objectstore `coding-artifacts/<ws>/<session>/<id>.png`,
  then `coding_session_artifacts`, plus an `action` activity with an inline image. PNG and JPEG only, at most
  `FLOW_CODING_ARTIFACT_MAX_BYTES`, and at most 20 per turn.
- PR/MR body: track D (and J) render a "Screenshots" section using long-lived signed Flow file URLs (the existing
  `/api/files/signed/` mechanism, 30-day expiry and renewed on PR updates). GitHub's API cannot upload images, and
  the signed URLs keep the images private to holders of the link.
- UI: `<CodingSessionArtifacts>` (before/after gallery), in I's directory and imported by G's session view.
- `FLOW_CODING_BROWSER=off` removes the tools and the guidance.

## 18. Debug terminal, Configure/Fix with agent, .env import

**Debug terminal** (K, WebSocket `GET /api/coding/environments/{id}/terminal`):

- Access: environment owners and workspace admins.
- Flow creates a `debug` sandbox (`activeDeadlineSeconds` = `FLOW_CODING_DEBUG_TERMINAL_TTL`, 20 min) restored from
  the latest ready snapshot, or a fresh clone, and waits for it to be running (progress frames).
- It then calls `Provider.Exec(TTY, ["bash","-l"])`. Binary frames carry TTY bytes; text frames carry JSON
  `{type:"resize",cols,rows}` and status messages.
- The connection closes when the sandbox deadline hits. A WS close stops the sandbox. It is audit-logged.
- No setup secrets are injected. "Run prepare script" runs it with secrets inside the same sandbox, and the UI warns
  that the user can then read the secrets.
- Uses `github.com/coder/websocket`, which is already a dependency. The UI uses xterm.js (`@xterm/xterm`), a new web
  dependency added by F.

**Configure with agent** (K): creates a `source=environmentSetup` session with a prompt template. The agent inspects
the repo, writes mise tools and a prepare script, runs them in its sandbox, iterates until they succeed, and calls
the `propose_environment` MCP tool. The proposal goes to `POST /internal/coding-runtime/environment-proposal` and is
stored as `pendingProposal`. The editor shows a diff with Apply or Discard (`/proposal/apply`). **Fix with agent**
does the same, seeded with the failed snapshot's logs and error. Proposals never contain secret values; they list
`missingSecrets` instead.

**Import from .env** (K plus F): the user pastes or drops a `.env`. `POST /env-import` parses it with dotenv rules
(quotes, `export`, comments, multiline) and classifies keys matching `*_KEY|*_SECRET|*_TOKEN|*PASSWORD*|*_DSN|*_URL`
with credentials as secrets, the rest as variables. The UI shows both lists to confirm, then saves them through the
normal PATCH and secrets endpoints. Nothing is stored by the import call itself.

**Archive** (K): `archive` sets `archived_at` and clears `active_repo_key`. Running sessions continue; new sessions
cannot pick the environment. `unarchive` fails with `ErrRepoInUse` if another active environment exists for the repo.

## 19. Security model

The threat model treats agent-run code as untrusted. A repository, a prompt injection (from issue text, a PR comment,
a web page or a dependency) or a malicious dependency can take full control of the sandbox user. The design limits
what that attacker can reach and for how long.

Controls:

1. **Isolation.** `runtimeClassName` when available (gVisor or Kata strongly recommended); `hostUsers:false` when
   available. With neither (this user's cluster), plain runc plus the restricted Pod Security profile applies:
   non-root uid 1000, `allowPrivilegeEscalation:false`, all capabilities dropped, seccomp RuntimeDefault, read-only
   root filesystem, no hostPath, hostNetwork or hostPID, `automountServiceAccountToken:false`, and
   `enableServiceLinks:false`. The namespace enforces `pod-security.kubernetes.io/enforce=restricted`.
2. **Dedicated, tainted node pool.** `FLOW_CODING_K8S_NODE_SELECTOR` and tolerations put sandboxes on nodes that run
   nothing else. Those nodes have no cloud IAM role (or IMDSv2 with hop limit 1), and kubelet `podPidsLimit` is set.
3. **Network.** Default-deny NetworkPolicy; egress only to DNS, the Flow runtime port and the egress proxy.
   169.254.169.254 and every other address are unreachable directly.
   - The proxy (Squid) allows `CONNECT` and `GET` only to allowlisted domains, and denies private, link-local and
     loopback destinations after DNS resolution (`acl to_private dst …`), so the proxy cannot be used for SSRF into
     the cluster.
   - Per-environment network modes (`default`, `extended`, `open`, `none`) are enforced through Squid
     `external_acl_type`. The helper (`flow-api egress-acl`, a subcommand of the API binary) reads the sandbox id and
     token from `Proxy-Authorization` (the runner sets `http://<sandboxId>:<token>@proxy:3128`) and calls
     `POST /internal/coding-runtime/egress-check`, with results cached for 60s.
   - Built-in allowlist (unless `FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST=off`): code hosts (github.com,
     api.github.com, codeload.github.com, objects.githubusercontent.com, raw.githubusercontent.com, ghcr.io, the GHE
     host, the configured GitLab hosts); registries (registry.npmjs.org, registry.yarnpkg.com, pypi.org,
     files.pythonhosted.org, proxy.golang.org, sum.golang.org, crates.io, static.crates.io, index.crates.io,
     rubygems.org, repo.maven.apache.org, repo1.maven.org, plugins.gradle.org, services.gradle.org, deb.debian.org);
     toolchains (mise.jdx.dev, mise-versions.jdx.dev, nodejs.org, go.dev, dl.google.com, static.rust-lang.org).
   - Model provider APIs are not allowed: models are reached only through Flow.
4. **No long-lived secrets in the sandbox.**
   - Sandbox token: scoped to one sandbox, and expires at the deadline plus grace. It is revoked on stop and stored
     hashed.
   - Model keys: stay in Flow (LLM proxy).
   - Code host: GitHub installation tokens (1h, one repo, minimal permissions); GitLab project tokens (one project,
     revoked at session end).
   - Signing keys: Flow signs (`/sign`), and the private key never leaves the API.
   - Environment secrets: one fetch per sandbox, only in setup, then unset.
   - The warm pool holds no capabilities until it is bound.
5. **Push guardrails.** Branch prefix enforcement in the runner (defense in depth only, because the token technically
   allows other pushes). Workflow file changes are blocked unless the App has the Workflows permission. Flow verifies
   the branch head before it opens a PR. PRs are always drafts, and humans merge.
6. **Resource limits.** CPU, memory and ephemeral-storage limits, emptyDir `sizeLimit`, PID limits,
   `activeDeadlineSeconds`, ResourceQuota, and the global and workspace concurrency caps.
7. **Flow API exposure.** The runtime API and LLM proxy can listen on a separate port (`FLOW_CODING_RUNTIME_ADDR`)
   that serves only sandbox routes, and the NetworkPolicy targets that port. Runtime routes check that the token's
   sandbox matches every id in the path.
8. **Audit.** Session start, approval, terminal use, secret add or delete, snapshot builds and PR creation are written
   to the workspace audit log.

**Limits with plain runc (state these plainly in `docs/coding-sessions-security.md`):**

- A kernel exploit from inside the container escapes to the node. Without gVisor or Kata, the node pool boundary is
  the real boundary, which is why that pool must be dedicated, have no cloud credentials, and receive prompt kernel
  patches.
- Shared-kernel side channels between concurrent sandboxes on one node are possible. Use one sandbox per node
  (pod anti-affinity, documented as optional) for sensitive tenants.
- During its life, the sandbox can use its code host token for anything that token allows (for example, push other
  branches of the same repo) and can exfiltrate data to allowlisted domains such as github.com. The allowlist reduces
  the risk but cannot remove it.
- Secrets used in setup can persist on disk and in archives if the prepare script writes them there.

## 20. Billing, metering and spend limits

- **LLM usage**: the proxy writes a `coding_usage_records` row with `kind=llm` per request, using the model, the
  token counts and `cost_micros` from `FLOW_CODING_MODEL_PRICES`.
- **Runtime**: the sandbox reconciler bills `kind=runtime` per sandbox: `runtime_seconds` from `started_at` to now or
  `ended_at`, in 5-minute increments (`billed_seconds` prevents double counting), priced with
  `FLOW_CODING_RUNTIME_PRICES[size]`. Snapshot builds and debug terminals are billed to the requesting user. The warm
  pool is not billed.
- **Attribution**: `user_id` is the session creator (the loop owner for loops), plus `loop_id`. The session's
  `cost_micros` is a running sum shown in the session view.
- **Enforcement** (`coding.SpendGuard`, track B): the existing `SpendLimitSettings` (workspace, user and loop limits
  with daily, weekly or monthly reset, plus overrides) is finally enforced. The window start comes from the reset
  settings. Checks run at:
  1. session creation and every turn start (the session fails at once with `quotaExceeded`);
  2. the LLM proxy, per request, with a 30s cached sum (402);
  3. each runtime increment (the sandbox is stopped and the session ends with `quotaExceeded`).
- **Credits**: Flow's limits are in USD (as the Usage & limits page stores them). "AI credits" in the UI are shown
  as USD amounts. No separate credit currency is introduced.
- **UI** (F): Usage & limits shows coding spend by user and loop (`GET /api/coding/usage`) next to the existing limit
  editors. The session view shows the cost.
- The `SpendGuard` interface is generic (`Check(ctx, ws, userID, loopID) error`), so chat and loops can adopt it.

## 21. Observability

Flow exports OpenTelemetry metrics (`telemetry.go`, OTLP). There is no Prometheus endpoint: operators scrape through
an OTel Collector with the Prometheus exporter. Instruments (meter `flow/coding`):

| Metric | Type | Labels |
|---|---|---|
| `flow_coding_sessions` | gauge | state, phase |
| `flow_coding_session_phase_seconds` | histogram | phase, outcome |
| `flow_coding_session_errors_total` | counter | code |
| `flow_coding_sandbox_start_seconds` | histogram | kind, size, warm (bool) |
| `flow_coding_sandboxes` | gauge | kind, phase |
| `flow_coding_warm_pool_available` | gauge | size, region |
| `flow_coding_snapshot_build_seconds` | histogram | strategy, status |
| `flow_coding_reconcile_seconds` | histogram | loop |
| `flow_coding_provider_errors_total` | counter | op, transient |
| `flow_coding_orphans_deleted_total` | counter | type |
| `flow_coding_llm_requests_total` | counter | upstream, status |
| `flow_coding_llm_tokens_total` | counter | model, direction |
| `flow_coding_cost_micros_total` | counter | kind |
| `flow_coding_codehost_requests_total` | counter | host, op, status |

Logs are structured (`log/slog`): session id, sandbox id and phase on every reconciler line. Runner stdout is the Pod
log. Flow archives it to the object store on stop (`logs_key`) and serves it at `…/sandboxes/{id}/logs`. Track H
writes the ops runbook (`docs/coding-sessions-operations.md`) with the alerts: error rate per code, start p95, warm
pool empty, orphans deleted > 0 sustained, LLM 5xx, and quota rejections.

## 22. Failure modes

| Failure | Detection | Handling | User-visible |
|---|---|---|---|
| Flow restarts mid-session | n/a | Leases expire; loops resume; runners buffer and retry; heartbeat timeout counted from process start | Nothing |
| Runner crashes, Pod still running | Heartbeats stop | After 2m, `runnerLost`; the sandbox is stopped | Errored, Restart |
| Pod evicted, OOMKilled or node lost | `Status` reports Failed/Missing with a reason | `sandboxLost` (OOM gives a hint to use a larger size); work up to the last push is kept | Errored, Restart (continues from the pushed branch) |
| Image pull failure | Pod waiting reason ErrImagePull/ImagePullBackOff | Retry ×2, then `imagePullFailed` | Errored, admin hint |
| Unschedulable (quota, taints, region) | PodScheduled=False / quota admission error | Wait until `starting` times out, then `unschedulable` or `capacityUnavailable` | Starting… then Errored |
| Restore archive missing or corrupt | Init container failed | One retry, then fall back to inline setup and rebuild the snapshot | Slower start |
| VolumeSnapshot not ready or failed | `readyToUse=false` / error | Snapshot failed; sessions use inline setup | Snapshot shows failed with logs |
| Prepare script fails | Runner reports setup error | `environmentSetupFailed` | Fix with agent |
| Harness exits non-zero / max turns | Result or turn.failed event | `harnessFailed` with message | Restart |
| Turn exceeds timeout | Phase deadline | SIGINT, WIP push, `turnTimeout` | Errored |
| Spend limit hit mid-turn | Proxy 402 | Harness fails; `quotaExceeded`; sandbox stopped | Usage & limits |
| Model unavailable | Upstream 404/400 model | Auto: fall back to the next model; pinned: `modelUnavailable` | Restart with default model |
| Code host down or 5xx | `codehost.ErrTransient` | Backoff retries per phase; then `githubTransientError` / `gitlabTransientError` | Restart |
| Installation removed or token revoked | 401/404 from the host | `noCodeAccess` / `noGitHubConnection` | Connect |
| Push rejected (protected branch, hook) | Runner push error | `pushFailed` with git stderr | Errored |
| PR creation fails (validation) | 422 | Retry ×5 for transient, else `pullRequestFailed`; the branch stays | Create PR |
| Webhook duplicated or reordered | Delivery id / state compare | Idempotent upsert; `updated_at` ordering | Nothing |
| Orphaned Pods or PVCs | GC listing | Deleted after grace | Nothing |
| GitLab project token not revoked (Flow down) | GC sweep by name and age | Revoked on the next sweep; expires within a day anyway | Nothing |
| Database unavailable | Store errors | Loops back off; runners retry; no provider actions without DB state | Stalls, no data loss |
| Egress proxy down | Runner network errors | Proxy runs 2 replicas with a PDB; runner surfaces clear errors | Errored (setup) or agent reports |
| Secrets key rotated or missing | Decrypt error | `environmentSetupFailed` with "secret unreadable"; admin must re-add | Errored |

## 23. Integration order

Integration order only; every track ships its full scope in this round.

1. **Contracts frozen (this commit).** Domain types, config, `Provider`/`Host`/`Store` interfaces, protocol, schema,
   routes and TS types. All tracks start in parallel against them; the fake provider and stub hosts let B, F, G, K
   and L build and test without a cluster.
2. **Core loop on Docker.** A (docker provider), B (service, reconcilers, runtime API), C (runner, image, Claude Code
   adapter), D (App, tokens, draft PR) and E (proxy) integrate first to reach issue → sandbox → Claude Code edit →
   push → draft PR on Docker with a fake model server in CI.
3. **Kubernetes.** A's kubernetes provider with the archive strategy, plus H's chart, NetworkPolicies and Squid,
   tested on kind with Calico (NetworkPolicy) in CI; volumesnapshot tested with the csi-hostpath driver.
4. **Breadth.** K (environments UI backend, snapshots, terminal, proposals), F and G (UI), J (GitLab end to end),
   L (all triggers and PR follow-ups), I (browser testing), Codex and open-source adapters (C), signing (E), warm
   pool and regions (A), metering and limits (B).
5. **Hardening.** Failure-injection tests (fake provider `FailNext`), restart tests (kill Flow mid-session), load
   test (`FLOW_CODING_MAX_SESSIONS` sessions), security review against §19, docs complete.

## 24. Work breakdown and file ownership

Rules for all tracks:

- Put new code in new files you own. Shared files may only receive the additive edits listed here, each a small block
  marked `// coding sessions (track X)`.
- Don't change the frozen contracts (`domain/coding.go`, `coding/protocol.go`, `coding/store.go`,
  `sandbox/sandbox.go`, `sandbox/store.go`, `sandbox/strategy.go`, `codehost/codehost.go`, `config/coding.go`,
  `web/src/types/coding-sessions.ts`) without telling the owning track (B for the coding and domain files, A for
  sandbox, D for codehost, H for config) and every consumer. Additive fields are fine; renames need coordination.
- New routes go into your own route file, and you add your lines to the `// Coding sessions` block of
  `publicapi.InternalRoutes`, then run `node scripts/generate-openapi.mjs`. Rebase conflicts in `docs/openapi.json`
  are resolved by regenerating it.
- Store methods go in `api/internal/store/coding_<area>.go`; schema changes go only in `coding_schema.go`
  (append-only; owner B, others send additions through B or append in their own clearly marked block).
- Before every push: `go build ./... && go vet ./... && go test ./...`, `npx tsc -b --noEmit && npm run lint`, and
  `node scripts/generate-openapi.mjs --check`.

### Track A: sandbox providers, sandbox reconciler, warm pool, GC

- **Owns:** `api/internal/sandbox/**` (except the frozen interface files, which it maintains as owner);
  `kubernetes/` (client-go, Pod/PVC/Secret/NetworkPolicy/VolumeSnapshot); `docker/` (Docker Engine API over HTTP,
  no SDK required); `reconcile.go` (sandbox reconciler); `warmpool.go`; `gc.go`; `strategy_archive.go`;
  `strategy_volumesnapshot.go`; `metrics.go`; and the tests. `api/internal/store/coding_sandboxes.go` (the
  `sandbox.Store` implementation: lease, observation, token mint, warm binding).
- **Additive:** `api/go.mod`/`go.sum` (k8s.io/client-go, k8s.io/api, k8s.io/apimachinery); `coding_runtime.go`
  `runCoding` (start the sandbox reconciler, GC and warm pool, one line each).
- **Contracts:** converges `coding_sandboxes` rows (B writes desired, A writes observed); `SnapshotStrategy` used by K;
  `Provider.Exec` used by K's terminal; runtime usage records written through `UsageStore` (B).

### Track B: session core (orchestration, state machine, runtime API, metering, limits)

- **Owns:** `api/internal/coding/**` except `snapshots.go` (K), `triggers*.go` (L) and `webhooks.go` (L). That
  includes `service.go` (`Start`, `Prompt`, `Stop`, `Restart`, `Approve`), `reconcile.go` (`Step` and the loop),
  `spend.go` (`SpendGuard`), `models.go` (Auto routing, reasoning effort), `bootstrap.go` (guidance assembly),
  `events.go` (RunnerEvent → AgentActivity), and `metrics.go`.
  `api/cmd/server/coding_runtime.go`, `coding_sessions_api.go`, `coding_runtime_api.go`.
  `api/internal/store/coding_sessions.go`, `coding_usage.go`, `coding_schema.go`.
- **Additive:** `application_agents.go` and `agent_tasks.go` (stop and prompt on the anchor route to the coding
  service when a coding session exists, one hook each); `settings_features.go` (normalize the new
  `CodingAgentSettings` fields).
- **Contracts:** `coding.Service` used by K, L and E; runtime protocol with C; `UsageStore` with A and E;
  `SpendGuard` with E; session JSON with G.

### Track C: in-sandbox runner, base image, harness adapters

- **Owns:** `sandbox-runner/**` (new TS package with its own `package.json`, tests run in CI as an additive job):
  `src/protocol.ts` (mirror of `coding/protocol.go`), `src/runtime-client.ts`, `src/session.ts`,
  `src/snapshot.ts`, `src/restore.ts`, `src/warm.ts`, `src/git/*` (clone, commit, push, credential helper, sign
  shim), `src/harness/{claude,codex,opencode,pi}.ts`, `src/mapping/*` (event → RunnerEvent, with fixture-driven
  tests), `src/mcp/server.ts`, `src/mcp/tools/ask-user.ts`, `src/cli-env.ts` (gh/glab).
  `deploy/sandbox-image/**` except `install-browser.sh`.
- **Additive:** `.github/workflows/ci.yml` (runner job), `.github/workflows/container.yml` (image build).
- **Contracts:** the runtime protocol (B); MCP tool registration interface for I and K (`registerTool(name, schema,
  handler)` in `src/mcp/server.ts`); the image runs `install-browser.sh` (I).

### Track D: GitHub App and the GitHub code host

- **Owns:** `api/internal/codehost/github/**`; `api/cmd/server/github_app.go`;
  `api/internal/store/coding_pull_requests.go` (`ChangeRequestStore`, shared with J and L through the same
  interface; D implements it).
- **Additive:** `code_webhooks.go` or the review applier (feed App PR events into the existing `CodeReview` sync,
  one function call); `web/src/components/settings/integrations*` (GitHub App install card, small block).
- **Contracts:** `codehost.Host` (B and K call it); `WebhookEvent` → `dispatchCodingWebhook` (L); the PR body
  includes the screenshot section from I's `coding.ArtifactMarkdown(artifacts)` helper.

### Track E: LLM proxy and commit signing

- **Owns:** `api/internal/llmproxy/**`; `api/cmd/server/coding_llm_proxy.go`; `api/internal/coding/signing.go`
  (SSHSIG and OpenPGP detached signatures); `api/internal/store/coding_signing_keys.go`.
- **Additive:** `signing_keys.go` (also store the encrypted private key on upload and delete it on removal);
  `go.mod` if an OpenPGP library is needed (`github.com/ProtonMail/go-crypto`).
- **Contracts:** `llmproxy.Authenticator` (backed by B's token store), `Meter` (B's `UsageStore` and `SpendGuard`);
  `/sign` protocol with C's `flow-sign` shim.

### Track F: front-end settings and environments

- **Owns:** `web/src/components/settings/coding-sessions/**` (the settings page, environment list and editor with code
  host and repository picker, mise tools/files/env editors, secrets UI (masked, add-only), .env import dialog,
  snapshot status/logs/refresh/cancel, debug terminal (xterm.js), archive, Configure/Fix with agent with the proposal
  diff, trusted sources, Loop repository access, region pin, signed commits toggle, model and reasoning pickers).
  Replaces `coding-agent-settings.tsx` (F deletes it and its CSS and test).
- **Additive:** `feature-settings.tsx` (render the new page; `coding-environments` redirects to `coding-sessions`);
  `app-routes.ts`, `route-info.ts`, `settings-access.ts` (redirect); `usage-settings.tsx` (coding spend block);
  `web/src/i18n/translations*.ts` (own new file `translations-coding-sessions.ts` plus its registration line);
  `web/package.json` (`@xterm/xterm`).
- **Contracts:** `web/src/lib/coding-sessions-api.ts` (shared with G; additive only); types file.

### Track G: front-end session UI and triggers

- **Owns:** `web/src/components/coding-sessions/**` except `artifacts/`: the session panel (state badge, phase
  progress, activities through the existing agent task renderer, plan, sandboxes with logs, PR/MR links with host
  icons, commits by turn, diff summary, cost, stop, follow-up composer, restart, restart with default model,
  approve/reject, create PR), the "Start coding session" menu items, and the review page "Fix this PR/MR" plus quick
  actions.
- **Additive:** `components/agent/issue-agent-tasks.tsx` (mount `<CodingSessionPanel>` when the task has a coding
  session); `components/agent/agent-chat-panel.tsx` (coding session card); issue delegate menu; reviews detail page
  (button); realtime handlers (a `codingSession` event refresh hook).
- **Contracts:** session JSON (B); `<CodingSessionArtifacts>` from I.

### Track H: Helm chart, egress proxy, docs

- **Owns:** `deploy/helm/flow/**` (Flow Deployment/StatefulSet, Service with optional runtime port, sandbox namespace,
  Role and RoleBinding limited to it, ServiceAccount `flow-sandbox` without RBAC, NetworkPolicies, ResourceQuota,
  LimitRange, optional RuntimeClass reference, PDBs, values for every `FLOW_CODING_*`); `deploy/egress-proxy/**`
  (Squid image or config, allowlist rendering, ACL helper wiring); `api/cmd/server/coding_egress.go` (egress-check
  endpoint, ACL helper subcommand logic in `egress_acl.go`); `docs/coding-sessions-security.md`,
  `docs/coding-sessions-operations.md`.
- **Additive:** `main.go` (`egress-acl` subcommand dispatch next to `healthcheck`, one `if`); `docs/configuration.md`
  (coding section); `docker-compose.yml` (a `coding` profile with Squid and the docker provider); README links.
- **Contracts:** the egress-check protocol with the Squid helper; `CodingEgressConfig`; network modes from
  `CodingEnvironment.Network`.

### Track I: browser testing and artifacts

- **Owns:** `deploy/sandbox-image/install-browser.sh`; `sandbox-runner/src/browser/**` and
  `sandbox-runner/src/mcp/tools/browser.ts`; `api/cmd/server/coding_artifacts_api.go`;
  `api/internal/store/coding_artifacts.go`; `api/internal/coding/artifacts.go` (`ArtifactMarkdown`, the guidance
  text); `web/src/components/coding-sessions/artifacts/**`.
- **Contracts:** MCP registration (C); guidance text consumed by B's bootstrap; the artifact markdown consumed by D
  and J.

### Track J: GitLab code host

- **Owns:** `api/internal/codehost/gitlab/**` (project listing, project token mint and revoke, OAuth and connection
  modes, draft MR, notes, pipelines, webhook normalization, egress hosts); `api/cmd/server/gitlab_coding.go` (the
  dispatch glue from `code_webhooks.go`; project-token sweep for GC).
- **Additive:** `code_webhooks.go` (one call after verification for gitlab Merge Request, Note and Pipeline hooks);
  `gitlab_integration.go` (only if a helper must become exported to the codehost package; prefer passing it through
  `codehost.Dependencies`).
- **Contracts:** `codehost.Host`; `ChangeRequestStore` (D's implementation); `WebhookEvent` (L).

### Track K: environments and snapshots backend, debug terminal, agent-assisted setup

- **Owns:** `api/cmd/server/coding_environments_api.go` and `coding_terminal.go`; `api/internal/coding/snapshots.go`
  (snapshot reconciler), `envimport.go` (.env parser), `confighash.go`, `proposal.go`;
  `api/internal/store/coding_environments.go` (environments, secrets with AES-GCM, snapshots, legacy migration);
  `sandbox-runner/src/mcp/tools/propose-environment.ts`.
- **Additive:** `coding_runtime.go` `runCoding` (start the snapshot reconciler, one line).
- **Contracts:** `EnvironmentStore`; `SnapshotStrategy` (A); `coding.Service.Start` with
  `source=environmentSetup` (B).

### Track L: triggers and PR/MR follow-ups

- **Owns:** `api/cmd/server/coding_triggers_api.go`, `coding_agent_tool.go` (the `start_coding_session` agent and
  MCP tool, the Loop action), `coding_webhook_dispatch.go` (`dispatchCodingWebhook`); `api/internal/coding/triggers.go`
  (repository selection, trusted external source detection, approval notifications), `webhooks.go` (WebhookEvent →
  follow-up mapping, commenter identity).
- **Additive:** `agent_chat_tools.go` (a `coding` tool group entry); `mcp_tools*.go` and `flow_mcp_tools.json` (tool
  registration); `loop_templates.go` and the loop runtime (wire the "Start a coding session" action); notification
  kinds for approvals.
- **Contracts:** `coding.Service`; `codehost.WebhookEvent`; `ChangeRequestStore.SessionsForChangeRequest`.

### Shared-file edit map

| Shared file | Who edits | Edit |
|---|---|---|
| `api/cmd/server/main.go` | done in the skeleton; H adds one subcommand `if` | `coding` field, `initCoding`, `runCoding`, `registerCodingRoutes` |
| `api/cmd/server/auth.go` | done in the skeleton | `/internal/coding-runtime/`, `/internal/llm-proxy/` public prefixes |
| `api/cmd/server/coding_runtime.go` | B owns; A and K add one line each in `runCoding` | |
| `api/internal/publicapi/internal_routes.go` | any track adding routes, inside the coding block | |
| `api/internal/store/database.go` | done (`ensureCodingSessions`) | |
| `api/internal/store/coding_schema.go` | B owns; append-only | |
| `api/internal/domain/models.go` | done (`CodingAgentSettings` fields) | |
| `api/cmd/server/code_webhooks.go` | D (App PR sync hook), J (GitLab dispatch) | one call each |
| `web/src/types/flow.ts`, `web/src/lib/api.ts` | done (re-export lines) | |
| `web/src/components/settings/feature-settings.tsx` | F | |
| `web/src/components/agent/issue-agent-tasks.tsx` | G | |
| `docs/openapi.json` | generated | regenerate on conflict |

## 25. Open risks and decisions

1. **runc only on the user's cluster.** This is the largest residual risk (§19). Mitigations are mandatory in the
   chart (dedicated tainted pool, restricted PSS, NetworkPolicy, no SA token). The docs recommend gVisor where a
   future cluster allows it; the setting already exists.
2. **NetworkPolicy enforcement depends on the CNI.** Preflight cannot prove enforcement. H documents a probe Pod that
   tries to reach 169.254.169.254 and an outside host directly; it should fail.
3. **GitLab project access tokens** need Maintainer rights and, on gitlab.com, Premium. The `oauth` and `connection`
   fallbacks are weaker, documented and visible in the UI.
4. **CLI flag drift** (claude, codex, opencode, pi). The versions are pinned in the image and the adapters are tested
   against recorded JSONL fixtures. Upgrades go through a fixture refresh.
5. **Open-source harness = OpenCode.** This is an interpretation of the reference "Open source" option (an open-source
   agent plus any OpenAI-compatible model). The harness interface makes swapping it a contained change in C.
6. **Signed commits.** Flow signs on the user's behalf with their uploaded key, which keeps the key out of the sandbox
   but gives Flow custody of the private key (encrypted at rest). Existing uploaded keys have no stored material, so
   users must re-upload them (prompted by `missingCommitSigningKey`). OpenPGP signing needs a new dependency.
7. **Screenshot URLs in PRs** are signed and long-lived links to Flow. Anyone holding a link can view the image until
   it expires. This is accepted for parity, and a setting can drop images from PR bodies.
8. **Archive size and time** for large monorepos (node_modules plus toolchains). The size cap and exclude file apply.
   Volumesnapshot is the faster option where the CSI supports it.
9. **Harness state across sandboxes** (resume after the idle TTL) depends on the harness session file formats. If a
   resume fails, the runner starts a fresh harness session with a summary of previous turns (from activities) in the
   prompt.
10. **Multi-replica Flow without Redis** is not supported, matching Flow's existing rule. The leased loops would work,
    but GC and the warm pool rely on the coordination lock.
11. **Model catalog defaults** (`claude-sonnet-4-5`, `gpt-5-codex`, …) are placeholders that operators override.
    Pricing defaults are empty (cost 0) until configured, and a metric flags unpriced models.
