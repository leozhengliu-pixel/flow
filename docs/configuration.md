# Deployment configuration

Flow uses environment variables for infrastructure and authentication choices.
Configuration is validated before migrations or the HTTP listener start. A
selected backend with missing required values causes a clear startup error.
Secrets may be supplied as `VARIABLE_FILE=/run/secrets/name`; the file value
takes precedence over `VARIABLE`.

## Logging

The API writes startup, request, integration, and error logs to stdout so
container runtimes and Kubernetes can collect them with `kubectl logs`. The
legacy `log.Printf` calls are also redirected to stdout during the migration to
structured logging.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `FLOW_LOG_FORMAT` | `text` | `text` for human-readable logs or `json` for log collectors. |
| `FLOW_SLOW_MUTATION_MS` | `500` | Log a `slow workspace mutation` warning with stage timings (lock wait, clone, load, mutate, persist, publish) when a workspace write, or the time it holds the workspace lock, exceeds this many milliseconds. `0` disables it. |

Every HTTP request receives an `X-Request-ID` response header. Clients may send
their own `X-Request-ID`; otherwise Flow generates one and includes it in the
request log record.

This follows the deployment model used by [Plane](https://developers.plane.so/self-hosting/govern/environment-variables),
[Outline](https://docs.getoutline.com/s/hosting/doc/docker-7pfeLP5a8t), and
[authentik](https://docs.goauthentik.io/install-config/configuration/). OTLP
settings use the standard [OpenTelemetry environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/).

## Database

`FLOW_DATABASE_DRIVER` accepts `sqlite`, `postgres`, or `mysql`. Migrations run
automatically at startup for all three drivers.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_DATABASE_DRIVER` | `sqlite` | SQL dialect and driver. |
| `FLOW_DATABASE_PATH` | `data/flow.db` | SQLite file path. |
| `FLOW_DATABASE_URL` | empty | Required for PostgreSQL and MySQL. |
| `FLOW_DATABASE_MAX_OPEN_CONNS` | driver default | `1` for SQLite, `20` otherwise. |
| `FLOW_DATABASE_MAX_IDLE_CONNS` | driver default | `1` for SQLite, `5` otherwise. |
| `FLOW_DATABASE_CONN_MAX_LIFETIME` | `30m` | Go duration for pooled connections. |
| `FLOW_WORKSPACE_STATE_MAX_BYTES` | `67108864` | Maximum serialized workspace state size. Mutations fail before exceeding this limit. |
| `FLOW_DATABASE_MAX_TRANSACTION_BYTES` | `33554432` | Cumulative SQL write text and parameter bytes per transaction. Exceeding the budget rolls back the transaction, even if a caller ignores the first error. Minimum 1 MiB. This is not a measurement of physical binlog bytes. |

### Database growth

Flow keeps history it no longer serves out of the database, or bounded:

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_DOMAIN_EVENT_RETENTION_DAYS` | `90` | Days the `domain_events` log keeps an event; `0` keeps every event. Values below `7` are raised to `7`, because the log is also the change log behind `/api/realtime/changes`. |
| `FLOW_CHANGE_LOG_MIN_ROWS` | `10000` | Newest changes per workspace that pruning always keeps, however old. `0` keeps only the age window. |
| `FLOW_TRASH_RETENTION_DAYS` | `30` | Days deleted items stay in "Recently deleted" before they are deleted permanently. Minimum 1. |
| `FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES` | `8192` | Agent replies and tool results larger than this are stored outside the database row, with a short summary left in the row. Minimum 1024. |
| `FLOW_CONTENT_CACHE_BYTES` | `134217728` | In-process cache of recently loaded content stored outside rows (document versions, agent replies). |
| `FLOW_SEARCH_INDEX_MAX_CHARS` | `20000` | Characters of each issue and document indexed for full-text search. Text past the limit is still stored and shown but is not found by search. `0` indexes everything. |

- **Domain events and the change log.** Every persisted change gets a
  per-workspace cursor (`seq`), allocated in the same transaction as the change
  from a counter row, so cursors are gap-free, ordered by commit and survive
  restarts; multi-node deployments share the counter through the database.
  `GET /api/realtime/changes?since=<cursor>&limit=N` returns the changes after a
  cursor (`events`, `next`, `head`, `hasMore`), filtered and projected for the
  caller exactly like the SSE stream. `resyncRequired: true` means the cursor is
  older than retention, ahead of the head, or unparseable: reload a snapshot and
  resume from `head`. `/api/bootstrap` and `/api/issue-records/bootstrap` send
  the cursor they were read at in the `X-Flow-Change-Cursor` header, SSE and
  socket events carry `seq`, and the SSE `connected` event carries the head as
  `cursor`; `Last-Event-ID`/`since` on the stream also accepts a cursor (a numeric `since` replays from the durable log, so it survives restarts and ring eviction). Replayed
  events carry the persisted payload (the mutation input), not the transient
  `entity` envelope; entities the viewer may read are re-projected, and events
  that SSE turns into `workspace.resync_required` do so here too. Presence,
  agent task and session state (`agent_task.updated` and streaming; refetch
  agent sessions after a resume, `agent_task.replied` is logged), document collaboration
  updates, `loop_run.*`, `notification.created` and recurring-issue signals are
  not persisted and keep their own channels (no `seq`). Pruning runs hourly in
  batches of 1,000 rows and keeps events newer than the retention window and
  each workspace's newest `FLOW_CHANGE_LOG_MIN_ROWS` changes. Events written by
  an older version during a rolling upgrade have no cursor and are not served.
  Webhooks are queued from the committed event and the audit log is stored
  separately.
- **Trash.** Expired entries are purged hourly by a background job (in batches
  of 200), not only when someone opens the app. Entries deleted before the
  window was shortened are purged at the new, shorter window.
- **Document versions and agent transcripts.** Each document version's body
  (content, rich-text data and editor state) is stored outside the document's
  row; the row keeps the version's author, time, size and a key. A version
  equal to the current document refers to it instead of repeating it; the
  newest version always stays in the row. Agent conversations store long
  replies and tool results the same way (user messages and tool arguments
  stay in the row). Deleted documents in the trash share their versions'
  stored bodies. With
  `FLOW_STORAGE_DRIVER=s3` the bodies are gzip-compressed objects under
  `FLOW_S3_CONTENT_PREFIX/<workspace>/document-revisions/<document>/<version>/…`
  (and `…/agent-transcripts/<conversation>/…`); otherwise they are kept
  gzip-compressed in the `metadata_blobs` table. Rows written by earlier
  versions are converted by a resumable background migration in small
  batches. Bodies nothing refers to any more are deleted after 24 hours.
- **Loading on demand.** Starting Flow or reloading a workspace reads none of
  these bodies. They are read, through the in-process cache, only where they
  are used: document history, comparing and restoring versions, responses
  that carry whole documents (the document API, MCP document tools, exports,
  webhook payloads and the event log's previous values), opening an agent
  conversation, and the history an agent reply is generated from. Bodies that
  retained domain events refer to are kept until those events are pruned.
  The workspace data and conversation lists
  carry older versions without their bodies (`_offloaded` names them) and
  partly offloaded conversations without their messages; the app loads them
  when a history or a conversation is opened.
- **Search.** Changing `FLOW_SEARCH_INDEX_MAX_CHARS` takes effect at the next
  start: lowering it truncates stored search text, raising it re-indexes.

MySQL and PostgreSQL reuse the space freed by pruning, but do not return it to
the operating system on their own. Run `OPTIMIZE TABLE domain_events,
workspace_metadata_records;` (MySQL) or `VACUUM FULL` (PostgreSQL) in a
maintenance window after the first cleanup to shrink the files. Use the
reports in `scripts/db-size-report/` to see where space goes
([Operations](operations.md#database-size)).

### Import writes and MySQL binary logs

Issue imports compare existing records and update only changed entities and index
entries. Replaying an identical batch does not write rows. Compatibility creation
preserves existing collection positions instead of renumbering every issue.
Label usage counts are not duplicated into stored issue payloads.
Display-only changes to referenced users, teams, statuses, projects and labels
are resolved from their authoritative records on reads. Persisted mutation events
contain the mutation input or changed fields; unchanged descriptions are not
copied into both current and previous event payloads. Webhook previous values
contain only changed fields.

Workspace metadata collections are persisted per entity or dictionary entry in
`workspace_metadata_records`. Project and initiative updates are stored per
update. CSV inputs and migration bundles have separate records, so changing a
job status does not rewrite the uploaded source. Completed CSV jobs discard
their source rows; incomplete jobs retain them for retry. Existing workspace JSON is
migrated automatically and reconstructed by the normal API; public bootstrap
response shapes remain unchanged. Back up the database before upgrading; an
older server that cannot read the new metadata records must not be started on
the migrated database. Stop old application writers during this upgrade; do not
run mixed old/new versions against the same database.

The row import storage API accepts at most 1000 records, 1 MiB per serialized
issue, and 16 MiB per batch. HTTP batch updates accept at most 1000 IDs and
deduplicate repeated IDs. CSV preview remains limited to 5000 rows and the
existing upload limit. A transaction-budget error means that the batch must be
split; committed batches remain intact. The limits bound individual operations,
not the total data a client can import over time.
An upgrade that exceeds the configured transaction budget fails without switching
the affected workspace to a partially migrated layout. Migrate that workspace
offline with a reviewed larger budget, then restore the operational limit.

For a MySQL 8.4 deployment, [deploy/mysql/binlog.cnf](../deploy/mysql/binlog.cnf)
provides an optional configuration with minimal row images, transaction
compression, a 256 MiB binlog transaction-cache ceiling and seven-day retention.
Install it through your database configuration management after checking that
replication and point-in-time recovery support the chosen image format,
compression and retention. Flow does not disable binary logging, weaken fsync,
alter global MySQL settings or purge existing logs.

The MySQL limits have different purposes: `max_binlog_size` rotates files and is
not a total disk quota; `max_binlog_cache_size` rejects oversized transactions;
`binlog_expire_logs_seconds` controls age-based expiry when automatic purge is
enabled. Even efficient writes can fill a disk during a sustained import if
retention and capacity are mismatched. Budget disk for measured binlog bytes per
imported row times the retained row volume, plus database/undo/temp space, and
pause the importer before the disk reserve is exhausted. Keep the retention
window longer than the required backup recovery period and replica outage lag.
See the [official MySQL binary log configuration reference](https://dev.mysql.com/doc/refman/8.4/en/replication-options-binary-log.html).

The opt-in regression test uses a **fresh, isolated** MySQL database and measures
actual `SHOW BINARY LOGS` byte increments with `ROW/FULL` enabled. It refuses a
database that already has tables. Run it with an account that has DDL rights on
that isolated database and `REPLICATION CLIENT`:

```sh
FLOW_TEST_WRITE_MYSQL_DSN='user:password@tcp(127.0.0.1:3306)/empty_test_database?parseTime=true' \
FLOW_ASSERT_WRITE_BOUNDS=1 \
go test ./cmd/server -run '^TestMySQLImportWriteAmplification$' -count=1 -v
```

Run from `api/`. CI also runs this check on its disposable MySQL service.

Examples:

```dotenv
FLOW_DATABASE_DRIVER=postgres
FLOW_DATABASE_URL=postgres://flow:secret@postgres:5432/flow?sslmode=require
```

```dotenv
FLOW_DATABASE_DRIVER=mysql
FLOW_DATABASE_URL=mysql://flow:secret@mysql:3306/flow?tls=true
```

`FLOW_DB_PATH` remains accepted as a fallback for existing SQLite deployments;
new deployments should use `FLOW_DATABASE_PATH`.

## New workspaces

A workspace created from the app starts with a "Getting started" tour: a
"Welcome to Flow" issue with one sub-issue per module (issues, planning,
documents, agent), each embedding its feature-tour clip, in the creator's
language (English or Simplified Chinese). The clips ship with the web build
(`docs/media/*.gif` is copied to `/tour/`), so the tour works offline and on
self-hosted deployments. Existing and imported workspaces never get it.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_WORKSPACE_TUTORIAL` | `on` | `off` (or `false`, `0`) creates new workspaces without the tutorial issues. API clients can also pass `"tutorial": false` to `POST /api/workspaces`. |

## Redis coordination and cluster mode

Redis is optional for a single Flow process and disabled by default. Enable it
when running multiple API replicas, and whenever the workspace is large enough
that SQL round-trips dominate (tens of thousands of teams or projects, or
millions of issues). Flow uses Redis for shared authentication rate limits,
cross-instance realtime events, shared presence, distributed workspace write
locks, **and a hot cache**:

- Issue records (`IssueRecord` / authorized detail) are cached by ID.
- Issue list pages, totals, and group counts are cached per query and viewer.
- Project directory pages are served from the in-memory snapshot when loaded,
  otherwise from a per-query cache.
- Workspace metadata collections (teams, projects, states, …) are stored as
  Redis hashes so replica reloads do not scan every SQL metadata row.

Writes bump a per-workspace generation so list caches drop immediately; the
mutated issue key is deleted so the next detail read refills from SQL. Cache
failures never fail the request: the store falls back to SQL.

A replica reloads the latest workspace metadata from Redis (and SQL on miss)
inside the distributed lock before applying a mutation, preventing one process
from overwriting another process's update.

Redis does not replace the primary database. Multi-instance mode requires
PostgreSQL or MySQL; startup rejects Redis with SQLite because a local SQLite
file is not a horizontally scalable source of truth.

Flow serializes mutations per workspace. Issues and discussion records are
stored separately from workspace metadata. Native issue operations update a
bounded set of rows; compatibility cascade operations can still load complete
collections. Monitor heap usage, query latency, and transaction sizes before
raising `FLOW_WORKSPACE_STATE_MAX_BYTES`. This setting does not cap the total
resident size of split metadata or issue collections.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_REDIS_MODE` | `disabled` | `disabled`, `standalone`, or `cluster`. |
| `FLOW_REDIS_URL` | empty | `redis://` or `rediss://` URL. In cluster mode, extra seed nodes can be supplied with repeated `addr` query parameters. |
| `FLOW_REDIS_ADDRS` | empty | Comma-separated standalone/cluster seed addresses when no URL is used. |
| `FLOW_REDIS_USERNAME` | empty | Redis ACL username. |
| `FLOW_REDIS_PASSWORD` | empty | Redis password; `_FILE` is supported. |
| `FLOW_REDIS_DB` | `0` | Logical DB for standalone mode. Must remain `0` for cluster mode. |
| `FLOW_REDIS_TLS` | `false` | Enable TLS when address-based configuration is used. Prefer `rediss://` with URL configuration. |

The HTTP endpoint must allow WebSocket upgrades on `/api/realtime/socket`.
Document collaboration works on one API instance without Redis. Enable Redis
standalone or cluster mode when multiple API instances must share Yjs updates,
Awareness messages, and workspace entity events.
| `FLOW_REDIS_PREFIX` | `flow` | Namespace for keys and channels. Use a unique value per environment. |
| `FLOW_REDIS_POOL_SIZE` | `40` | Maximum connections per process and cluster node. |
| `FLOW_REDIS_MIN_IDLE_CONNS` | `5` | Warm idle connections per process and cluster node. |
| `FLOW_REDIS_DIAL_TIMEOUT` | `5s` | Connection dial timeout. |
| `FLOW_REDIS_READ_TIMEOUT` | `3s` | Command read timeout. |
| `FLOW_REDIS_WRITE_TIMEOUT` | `3s` | Command write timeout. |
| `FLOW_REDIS_CONNECT_TIMEOUT` | `15s` | Startup probe deadline. |
| `FLOW_REDIS_LOCK_TTL` | `30s` | Distributed write lease, renewed while a mutation runs. |
| `FLOW_REDIS_LOCK_WAIT` | `5s` | Maximum wait to acquire a workspace write lock. |

## Container security

The published image runs as numeric UID/GID `65532`, exposes an internal health
check, and the Compose service uses a read-only root filesystem with all Linux
capabilities dropped. Only `/app/data` and `/tmp` are writable. When upgrading an
older named volume that was created by a root-running image, adjust ownership
once before starting the new image:

```bash
docker run --rm -v flow_flow-api-data:/data alpine:3.22 chown -R 65532:65532 /data
```

Development account tokens are disabled by default. Set
`FLOW_DEV_AUTH_TOKENS=true` only in an isolated local environment.
`FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS` is likewise development only and off by
default (see [Development only: loopback GitLab](#development-only-loopback-gitlab)).

Standalone example:

```dotenv
FLOW_DATABASE_DRIVER=postgres
FLOW_DATABASE_URL=postgres://flow:secret@postgres:5432/flow?sslmode=require
FLOW_REDIS_MODE=standalone
FLOW_REDIS_URL=rediss://default:secret@redis.example.com:6379/0
```

Cluster example:

```dotenv
FLOW_DATABASE_DRIVER=postgres
FLOW_DATABASE_URL=postgres://flow:secret@postgres:5432/flow?sslmode=require
FLOW_REDIS_MODE=cluster
FLOW_REDIS_ADDRS=redis-1:6379,redis-2:6379,redis-3:6379
FLOW_REDIS_PASSWORD_FILE=/run/secrets/redis_password
FLOW_REDIS_POOL_SIZE=80
```

Cluster keys that participate in a single Lua script or transaction use Redis
hash tags so they remain in one slot. The client follows cluster redirections
and maintains a connection pool per node.

## Trusted private endpoints

Flow makes server-side requests to URLs that workspace members, admins and
remote servers supply: MCP connectors, webhooks, identity providers,
self-hosted GitLab. By default every such request must be HTTPS to a
**public** address: Flow resolves the host and refuses the request unless
every DNS answer is a public unicast address, so a workspace cannot use Flow
to probe the internal network or a cloud metadata service.

An enterprise that runs these services on its intranet can open **specific**
private hosts. This is an operator decision, made in the deployment's
environment (or Helm values), never by workspace members or admins, and it
opens exactly the listed hosts on the listed networks to Flow's server-side
requests:

| Variable | Helm value | Meaning |
| --- | --- | --- |
| `FLOW_TRUSTED_PRIVATE_ENDPOINTS` | `outbound.trustedPrivateEndpoints` | Comma-separated exact hosts (`hsb.haier.net`) or `*.` suffix wildcards (`*.corp.example.com`, which matches any subdomain but not `corp.example.com` itself). A bare `*`, a wildcard over a single label (`*.com`) and metadata or loopback names are refused at startup. |
| `FLOW_TRUSTED_PRIVATE_NETWORKS` | `outbound.trustedPrivateNetworks` | Comma-separated CIDRs (`10.218.0.0/16`) the trusted hosts may resolve to. Required when hosts are listed; `/0` is refused. |
| `FLOW_TRUSTED_CA_FILE` | `outbound.trustedCA` (a mounted Secret or ConfigMap) | PEM CA bundle added to the system roots, for intranet certificates issued by an enterprise PKI. TLS verification is never turned off. |

A private destination is allowed only when the URL's host matches a trusted
host pattern **and** every address it resolves to is inside a trusted
network. Whatever is listed, these stay blocked: loopback, link-local
(`169.254.0.0/16`, `fe80::/10`), cloud metadata (`169.254.169.254`,
`fd00:ec2::/32`, `100.100.100.200`, `metadata.google.internal`, …),
unspecified, multicast and broadcast addresses. Plain HTTP stays refused (the
development-only loopback switches are unchanged).

The same policy runs when a URL is saved and on every connection. Flow
resolves the host, checks every answer and dials the address it checked, so a
DNS answer that changes after the check (DNS rebinding) is checked again
rather than trusted; TLS keeps the URL's host name for SNI and certificate
verification. Redirects are checked the same way, and a request that started
at a trusted private host may only be redirected to another trusted host.
Environment proxies (`HTTPS_PROXY`) are not used for these requests, since a
proxy would resolve the destination itself.

Refusals tell the member what to do without revealing internal addresses, for
example `hsb.haier.net resolves to a private address; an operator can allow it
with FLOW_TRUSTED_PRIVATE_ENDPOINTS and FLOW_TRUSTED_PRIVATE_NETWORKS`. The
resolved addresses are written only to the server log.

Which requests honour the trusted endpoints is decided per caller:

| Honour trusted private endpoints | Public HTTPS only |
| --- | --- |
| MCP connectors (install, OAuth discovery, tool calls, URL elicitations, the coding-session MCP gateway); webhooks, application webhooks and the audit log stream; integration deliveries and integration OAuth token/revoke endpoints; enterprise identity providers (OIDC discovery, SAML metadata); self-hosted GitLab and GitHub Enterprise, member code-host links and review provider APIs. | Link previews, attachments imported from a URL and file previews: any member can paste those URLs, so they never reach the intranet. |

Example: an intranet code-search MCP server at `https://hsb.haier.net/api/mcp`
(resolving to `10.218.161.13`) with a certificate from the enterprise CA:

```dotenv
FLOW_TRUSTED_PRIVATE_ENDPOINTS=hsb.haier.net
FLOW_TRUSTED_PRIVATE_NETWORKS=10.218.0.0/16
FLOW_TRUSTED_CA_FILE=/var/run/flow/trusted-ca/ca.crt   # only if the certificate is not publicly trusted
FLOW_MCP_CREDENTIALS={"mcp-sourcebot-production":{"url":"https://hsb.haier.net/api/mcp","token":"<server token>"}}
```

```yaml
# Helm
outbound:
  trustedPrivateEndpoints: [hsb.haier.net]
  trustedPrivateNetworks: [10.218.0.0/16]
  trustedCA:
    secretName: enterprise-ca   # key ca.crt; or configMapName
```

A workspace admin then installs it (`PUT /api/application-policies` with
`{"id":"mcp-sourcebot-production","name":"Sourcebot","kind":"mcp","url":"https://hsb.haier.net/api/mcp","shared":true,"status":"approved"}`)
and, to let coding sessions use it, enables it for coding sessions (see
[MCP servers in coding sessions](#mcp-servers-in-coding-sessions)). The Flow
Pods need a network route to the host; sandboxes do not.

## Object storage

`FLOW_STORAGE_DRIVER` accepts `local` or `s3`. Objects remain private: Flow
checks workspace authorization before streaming `/uploads/:key` from either
backend.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_STORAGE_DRIVER` | `local` | Object backend. |
| `FLOW_STORAGE_LOCAL_PATH` | `data/uploads` | Local object directory. |
| `FLOW_S3_BUCKET` | empty | Required S3 bucket. |
| `FLOW_S3_REGION` | empty | Required AWS/S3 region. |
| `FLOW_S3_ENDPOINT` | AWS default | Optional MinIO/R2/S3-compatible endpoint. |
| `FLOW_S3_ACCESS_KEY_ID` | AWS chain | Static access key when not using workload identity. |
| `FLOW_S3_SECRET_ACCESS_KEY` | AWS chain | Static secret key. |
| `FLOW_S3_SESSION_TOKEN` | empty | Optional temporary credential token. |
| `FLOW_S3_PATH_STYLE` | `false` | Enable for MinIO and providers requiring path-style requests. |
| `FLOW_S3_PREFIX` | `uploads` | Key prefix inside the bucket. |
| `FLOW_S3_CONTENT_PREFIX` | `content` | Key prefix for database content kept in object storage (see [Database growth](#database-growth)). |
| `FLOW_S3_VALIDATE_ON_START` | `true` | Fail startup when the bucket or credentials cannot be accessed. |

The standard `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
`AWS_REGION`, `AWS_S3_BUCKET_NAME`, and `AWS_S3_ENDPOINT_URL` names are also
accepted. Flow-specific names take precedence.

## API rate limits

Public API requests are rate limited per credential subject with leaky
buckets (see [api.md](api.md#rate-limits)). Buckets are kept in memory, or in
Redis when Redis coordination is enabled so limits hold across instances.
Browser sessions are exempt unless a session budget is set. Set
`FLOW_TRUST_PROXY_HEADERS=true` behind a trusted proxy so unauthenticated
budgets apply per client address rather than to the proxy.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_API_RATE_LIMIT` | on | `off` disables API rate limiting. |
| `FLOW_API_RATE_LIMIT_API_KEY_REQUESTS` / `_COMPLEXITY` | `2500` / `3000000` | Hourly budgets per user for personal API keys. |
| `FLOW_API_RATE_LIMIT_OAUTH_REQUESTS` / `_COMPLEXITY` | `5000` / `2000000` | Hourly budgets per user (or app user) and OAuth application. |
| `FLOW_API_RATE_LIMIT_UNAUTHENTICATED_REQUESTS` / `_COMPLEXITY` | `600` / `100000` | Hourly budgets per client address. |
| `FLOW_API_RATE_LIMIT_SESSION_REQUESTS` / `_COMPLEXITY` | `0` / `0` | Hourly budgets for browser sessions; `0` means unlimited. |
| `FLOW_API_RATE_LIMIT_AGENT_RUN_REQUESTS` | `1200` | Hourly `/mcp` request budget per agent run (worker run tokens); `0` means unlimited. |
| `FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_REQUESTS` | `36000` | Hourly `/mcp` request budget shared by all agent runs of a workspace; `0` means unlimited. |
| `FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_OVERRIDES` | empty | Per-workspace overrides of the agent workspace budget, e.g. `acme=100000,trial=2000`. |
| `FLOW_TOOL_CALL_CONCURRENCY` | 2 per CPU (at least 4) | Flow tool calls (MCP, agent runs, in-process agent) executing at once; more wait. Each holds a copy of the workspace metadata. `0` disables the bound. |
| `FLOW_API_MAX_COMPLEXITY` | `10000` | Largest complexity a single request may have. |
| `FLOW_FILE_URL_SIGNING_KEY` | generated | HMAC key for signed file URLs. When unset, Flow generates one and stores it in the database (shared by all instances). Changing it invalidates outstanding signed URLs. |

## Authentication

Email/password remains enabled by default. Login buttons are obtained from
`GET /api/auth/providers`; disabled providers are not rendered. New identities
can be provisioned into the default workspace, optionally restricted by email
domain.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_AUTH_EMAIL_ENABLED` | `true` | Enable registration/password login and reset. |
| `FLOW_AUTH_AUTO_PROVISION` | `false` | Create users and default workspace membership after trusted SSO. Set to `true` only when IdP users should join without an invitation. |
| `FLOW_AUTH_ALLOWED_DOMAINS` | empty | Comma-separated SSO email domains. |
| `FLOW_AUTH_GOOGLE_ENABLED` | `false` | Enable Google OIDC. |
| `FLOW_GOOGLE_CLIENT_ID` | empty | Required Google client ID. |
| `FLOW_GOOGLE_CLIENT_SECRET` | empty | Required Google secret. |
| `FLOW_GOOGLE_REDIRECT_URL` | derived | Google callback URL. |
| `FLOW_AUTH_OIDC_ENABLED` | `false` | Enable enterprise OIDC discovery. |
| `FLOW_OIDC_ISSUER_URL` | empty | Required issuer/discovery base URL. |
| `FLOW_OIDC_CLIENT_ID` | empty | Required OIDC client ID. |
| `FLOW_OIDC_CLIENT_SECRET` | empty | Required OIDC secret. |
| `FLOW_OIDC_SCOPES` | `openid profile email` | Space-separated scopes. |
| `FLOW_OIDC_DISPLAY_NAME` | `OpenID Connect` | Login button label. |
| `FLOW_OIDC_IDENTITY_CLAIM` | `sub` | Stable claim used to bind the IdP identity. Use an employee-number claim only when the IdP guarantees it is unique, immutable, and never reused. |
| `FLOW_OIDC_ROLE_CLAIM` | empty | Optional claim containing a workspace role, such as `groups`. |
| `FLOW_OIDC_ROLE_MAPPING` | empty | Optional comma-separated mappings such as `staff=admin,contractor=guest`. |
| `FLOW_OIDC_DEFAULT_ROLE` | empty | Fallback role when the claim is missing or unmapped. |
| `FLOW_AUTH_SAML_ENABLED` | `false` | Enable SAML 2.0 SP endpoints. |
| `FLOW_SAML_METADATA_URL` | empty | IdP metadata URL; alternatively use metadata XML. |
| `FLOW_SAML_METADATA_XML` | empty | Inline/file IdP metadata. |
| `FLOW_SAML_ENTITY_ID` | app URL | Service provider Entity ID. |
| `FLOW_SAML_ACS_URL` | derived | Assertion Consumer Service URL. |
| `FLOW_SAML_SP_PRIVATE_KEY` | empty | Required PEM RSA key. Prefer `_FILE`. |
| `FLOW_SAML_SP_CERTIFICATE` | empty | Required PEM SP certificate. Prefer `_FILE`. |
| `FLOW_SAML_DISPLAY_NAME` | `SAML` | Login button label. |

Google and generic OIDC use authorization code, state, nonce, PKCE, discovery,
and signed ID-token verification. OIDC identities are stored separately from
`auth_users.email`: `issuer + subject` (or the configured identity claim) is the
login key, while email is optional profile data. `LoginExternal` remains the
email-based compatibility API for email/password and existing integrations.
SAML assertions are validated against IdP metadata and mapped from standard
email/name attributes.

For an enterprise IdP that exposes an immutable employee number instead of an
email address, configure for example:

```dotenv
FLOW_OIDC_SCOPES=openid profile
FLOW_OIDC_IDENTITY_CLAIM=employeeNumber
```

The token must still contain a stable `sub` fallback or the configured claim;
Flow never derives an email address from the employee number.

Workspace member and bootstrap user records include an optional `userId` for the
enterprise identity subject (for example, the configured OIDC employee-number
claim). A SCIM identifier is only used in its own workspace.
People pickers display this identifier and optional email. Internal database IDs
are hidden from profiles and mention suggestions; search still accepts both
identifiers and email. Assignment payloads continue to use the internal
`id`, so adding this profile field does not change existing relationships.

SCIM role groups and team groups are configured in the workspace settings API.
Set `scimRoleGroups` for role group display names and
`scimTeamGroupMapping` as a map from Flow team ID to IdP group display name.
SCIM pushes then add and remove managed team memberships without removing
manually assigned members.

## Flow Agent

Flow Agent streams provider output through a unified SSE transport. It supports
OpenAI Responses, Anthropic Messages, and OpenAI-compatible Chat Completions.
The browser sends selected issue IDs to Flow; the API loads authorized workspace
context, executes enabled Flow tools server-side, and never exposes provider
credentials to the browser.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_AGENT_ENABLED` | `false` | Enable Agent chat requests. |
| `FLOW_AGENT_PROTOCOL` | `openai-responses` | `openai-responses`, `anthropic-messages`, or `openai-chat-completions`. |
| `FLOW_AGENT_BASE_URL` | `https://api.openai.com/v1` | Provider API base URL. |
| `FLOW_AGENT_API_KEY` | empty | Provider credential; `_FILE` is supported. |
| `FLOW_AGENT_MODEL` | `gpt-5-mini` | Provider model identifier. |
| `FLOW_AGENT_TIMEOUT` | `60s` | Provider request timeout: how long a request may wait for the provider's first byte, or between two chunks of a streamed answer. A streamed answer may run longer than this as long as the provider keeps sending. |
| `FLOW_AGENT_MAX_OUTPUT_TOKENS` | `4096` | Maximum output tokens per provider turn. |
| `FLOW_AGENT_ANTHROPIC_VERSION` | `2023-06-01` | Anthropic API version header. |
| `FLOW_AGENT_TOOLS_ENABLED` | `true` | Expose Flow MCP read tools to the Agent. |
| `FLOW_AGENT_WRITE_TOOLS` | `false` | Also expose write tools. Enable only when automatic mutations are acceptable. |
| `FLOW_AGENT_REASONING_EFFORT` | empty | Reasoning effort for OpenAI-compatible models: `none`, `minimal`, `low`, `medium`, `high` or `xhigh`. Empty leaves the provider default. |
| `FLOW_AGENT_NATIVE_WEB_SEARCH` | `true` | Without `FLOW_WEB_SEARCH_PROVIDER`, Flow Agent chat and comment threads search the web with the model provider's hosted search tool (OpenAI Responses `web_search`, Anthropic `web_search`). A provider that rejects the tool is detected once and search is turned off; `fetch_url` keeps working. Web access is on per workspace unless an admin turns it off in Settings (stored as the explicit `agentWebAccess` workspace setting; never set means on). On upgrade, a one-time data migration keeps web access off where the stored settings show an admin turned it off: `agentWebSearchDisabled` was saved, or web search had been turned on (the event log has a save enabling it) and the workspace's settings were saved after that. |
| `FLOW_AGENT_MEDIA_BASE_URL` | `FLOW_AGENT_BASE_URL` | OpenAI-compatible API base URL the `transcribe_media` chat tool uses for audio and video attachments. Chat gives the model images directly; every other attachment reaches it as a reference (name, type, size, id) and the model reads it with `read_file` (PDF, XLSX, DOCX, PPTX, CSV/TSV, text; parsed in-process) or `transcribe_media` (audio, video). |
| `FLOW_AGENT_MEDIA_API_KEY` | `FLOW_AGENT_API_KEY` when `FLOW_AGENT_MEDIA_BASE_URL` equals `FLOW_AGENT_BASE_URL` | Bearer credential for the media endpoint; `_FILE` is supported. The Agent key is never sent to another host, so set this when the media endpoint differs. |
| `FLOW_AGENT_MEDIA_TRANSCRIBE_MODEL` | empty | Audio model for `POST {base}/audio/transcriptions` (multipart `file`, `model`, `response_format=verbose_json`, e.g. `whisper-1`). The tool prints `[mm:ss] text` lines from the response segments. Empty: the tool answers "Media transcription is not enabled on this server." for audio. |
| `FLOW_AGENT_MEDIA_VIDEO_MODEL` | empty | Multimodal model for video, called through `POST {base}/chat/completions` with the video as a `video_url` content part (a base64 data URL; accepted by vLLM, Qwen-VL/DashScope and Gemini's OpenAI-compatible endpoint). The model describes what is visible and any on-screen text, with timestamps. Empty: the tool answers "Media transcription is not enabled on this server." for video. |
| `FLOW_AGENT_MEDIA_TIMEOUT` | `5m` | Per-request timeout for media transcription. |

```dotenv
FLOW_AGENT_ENABLED=true
FLOW_AGENT_PROTOCOL=openai-responses
FLOW_AGENT_BASE_URL=https://api.openai.com/v1
FLOW_AGENT_API_KEY=secret
FLOW_AGENT_MODEL=your-model
```

For Anthropic Messages:

```dotenv
FLOW_AGENT_PROTOCOL=anthropic-messages
FLOW_AGENT_BASE_URL=https://api.anthropic.com/v1
FLOW_AGENT_API_KEY=secret
FLOW_AGENT_MODEL=your-claude-model
```

For a legacy OpenAI-compatible endpoint, use
`FLOW_AGENT_PROTOCOL=openai-chat-completions`. Responses and Messages streams
are normalized into text, reasoning, tool-call, error, and completion events.

### Loop runs

Loop runs are stored in their own tables (`loop_run_records` for each run's
state, `loop_run_events` for its append-only log); recording progress never
writes the workspace metadata. On first start after the upgrade, runs kept in
the workspace metadata by earlier versions are copied into `loop_run_records`
once (`migrateLoopRunRecords`, idempotent; about 0.7 s per 1,000 runs on MySQL)
and removed from the metadata. A running run holds a lease that its server
renews every quarter of `FLOW_LOOP_LEASE_TTL`; runs whose lease expired, or that
a previous process on the same host still held at startup, are marked
`interrupted`. Every run works within these budgets; exceeding one fails the run
with `failureReason` `budget_exhausted` or `timeout`.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_LOOP_MAX_CONCURRENT_RUNS_PER_WORKSPACE` | `2` | Runs (and replies on runs) executing at once per workspace on one server. Runs over the limit are recorded as failed. |
| `FLOW_LOOP_MAX_CONCURRENT_RUNS` | `4` | Runs executing at once on one server. |
| `FLOW_LOOP_MAX_TOOL_OUTPUT_BYTES` | `49152` | Tool results sent to the model are cut to this size. |
| `FLOW_LOOP_MAX_CONTEXT_BYTES` | `786432` | Largest conversation a run may send to the model in one turn. |
| `FLOW_LOOP_MAX_TOOL_CALLS` | `80` | Tool calls per run. |
| `FLOW_LOOP_MAX_TURNS` | `24` | Model turns that may call tools; the run then has to summarize. |
| `FLOW_LOOP_RUN_TIMEOUT` | `10m` | Wall-clock limit of a run. |
| `FLOW_LOOP_LEASE_TTL` | `60s` | How long a run's lease holds without a heartbeat. |
| `FLOW_LOOP_RETRY_ATTEMPTS` | `2` | Retries of transient model-provider failures (timeouts, 429, 5xx) and of read-only tools. Changes are never retried. |
| `FLOW_LOOP_RETRY_BACKOFF` | `1s` | First retry delay; doubled for each further retry. |

## Pulse summaries and audio

Pulse summary notifications ("Daily Pulse" / "Weekly Pulse") are created by the
delivery scheduler at 06:00 in each member's time zone (the browser zone the
web app reports, else the member's first team's zone, else UTC). Opening a
summary in the Inbox generates a short summary of every update once and caches
it on the notification:

- When Flow Agent is enabled (`FLOW_AGENT_ENABLED=true`), the configured Agent
  model writes one or two sentences per update with a strict no-invention
  prompt.
- Without an Agent, or when the provider fails, Flow falls back to an
  extractive summary (the first sentences of each update) and marks the
  summary `ai: false`. A fallback caused by a provider failure is retried on a
  later open (after 10 minutes, doubling up to 6 hours, at most 5 attempts).
- Update text is sent as delimited, untrusted content; a summary is accepted
  only for the update it names, so one update cannot rewrite another's.

"Listen" reads the summary text (at most 4000 characters) aloud through an
OpenAI-compatible text-to-speech endpoint, `POST {FLOW_TTS_BASE_URL}/audio/speech`.
The MP3 is cached in the configured object store (`FLOW_STORAGE_DRIVER`), keyed
by a hash of the text, model and voice, so each summary is synthesized once.
`GET /api/pulse/capabilities` reports `{ "aiSummaries", "audio" }` so the web app
hides "Listen" when speech is disabled; the audio endpoint returns 404 then.
Workspace AI policy applies to both: in a HIPAA workspace, or when the
workspace turns AI off (Settings > Features, the `ai` feature), summaries are
extractive (`ai: false`), `capabilities` reports both as `false` and the audio
endpoint returns 404.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_TTS_ENABLED` | `false` | Enable "Listen" for Pulse summaries. |
| `FLOW_TTS_BASE_URL` | `FLOW_AGENT_BASE_URL` | OpenAI-compatible API base URL that serves `/audio/speech`. |
| `FLOW_TTS_API_KEY` | `FLOW_AGENT_API_KEY` when `FLOW_TTS_BASE_URL` equals `FLOW_AGENT_BASE_URL` | Bearer credential for the speech endpoint; `_FILE` is supported. Required when the speech endpoint is not the Agent provider: without it Flow logs a startup warning and turns "Listen" off, so the Agent key is never sent to another host. |
| `FLOW_TTS_MODEL` | `gpt-4o-mini-tts` | Speech model identifier. |
| `FLOW_TTS_VOICE` | `alloy` | Voice name passed to the speech endpoint. |
| `FLOW_TTS_TIMEOUT` | `60s` | Per-request timeout for speech synthesis. |

```dotenv
FLOW_TTS_ENABLED=true
FLOW_TTS_BASE_URL=https://api.openai.com/v1
FLOW_TTS_API_KEY=secret
FLOW_TTS_MODEL=gpt-4o-mini-tts
FLOW_TTS_VOICE=alloy
```

## Telemetry

Telemetry is opt-in. When enabled, Flow exports HTTP traces and request metrics through OTLP/HTTP.
The exporter honors standard `OTEL_EXPORTER_OTLP_*` endpoint, header, TLS,
compression, and timeout variables.

```dotenv
FLOW_TELEMETRY_ENABLED=true
FLOW_ENVIRONMENT=production
OTEL_SERVICE_NAME=flow-api
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example:4318
OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer%20secret
```

Set `OTEL_SDK_DISABLED=true` or `FLOW_TELEMETRY_ENABLED=false` for a no-op SDK.
No default Flow or vendor endpoint is configured.

## GitHub/GitLab review webhooks

Code review events can be delivered to the public endpoints below. Include the
workspace key as a query parameter (or `X-Workspace-Key` header), and configure
the same `webhookSecret` on the provider connection:

```text
POST /api/integrations/github/webhook?workspace=acme
POST /api/integrations/gitlab/webhook?workspace=acme
```

GitHub uses `X-Hub-Signature-256`; GitLab uses `X-Gitlab-Token`. Flow verifies
the secret before persisting the pull/merge request and creating Inbox
notifications. The delivery ID is used for idempotency, so provider retries do
not duplicate notifications.

### Importing pull and merge requests without webhooks

When webhooks cannot reach Flow (an intranet deployment, local development, a
broken delivery), Flow can list the open pull and merge requests of
**repositories a workspace admin approved** and create or refresh their
reviews. It is **off by default** and needs two switches:

1. The server allows it: `FLOW_REVIEW_IMPORT=on`. With `off` (the default)
   nothing runs, whatever a workspace configured: no background runs, no
   **Sync now**, not even the scheduler's query.
2. A workspace admin turns it on and approves repositories under
   **Settings › Integrations › GitHub / GitLab › Import pull requests without
   webhooks** (`PATCH /api/reviews/import` with `enabled` and
   `repositories: [{provider, repository}]`, at most 100). Repositories are
   entered as `owner/name` (a GitLab project may sit in nested groups) or by
   URL. An empty list imports nothing: Flow never enumerates what a token can
   see, and a connection without an `organization` does not mean "all".

Then the import runs every `FLOW_REVIEW_IMPORT_INTERVAL` and on **Sync now**
(`POST /api/reviews/import`, workspace admins). `GET /api/reviews/import`
(workspace members, read-only) reports whether it is allowed and enabled, the
approved repositories, the limits, whether a run is in progress, the last run
(start and end, scanned, imported, updated, unchanged, failed, backed off,
writes, errors), the next run and the repositories and requests that are
backing off with their last error and retry time.

Each run is bounded and resumable:

| Limit | Value |
| --- | --- |
| Repositories read per run | 10 (in rotation; the next run continues at a cursor) |
| Open requests listed per repository | 50 (most recently updated first) |
| Requests examined per run | 200 |
| Workspace writes per run | 20 (all changed requests are one write; each full sync of files and comments is one more) |
| Pause between writes | 2 seconds |

- **Unchanged requests are skipped.** A request whose head commit, update time
  and state are unchanged since the last import is neither written nor synced.
  Files and comments are synced only for a new review or a new head commit.
- **One write and one realtime event per run** for all new and changed
  reviews (`review.imported`), plus one per review whose files and comments were
  synced; an unchanged run writes nothing and publishes nothing.
- **Failures back off.** A repository or request whose code host call fails
  (for example a GitLab `merge request diffs` HTTP 500, or 429) is retried after
  15 minutes, doubling per failure up to 6 hours; the others continue. A token
  with fewer than 100 requests left in its rate limit window, or a rate limited
  answer, ends the run until the reset.
- **One run per workspace across replicas.** A run holds a lease row in
  `review_import_state` (database migration 13; SQLite, MySQL and Postgres)
  for at most 15 minutes, so two API replicas never import the same
  workspace at once.

Imported reviews link issues whose identifier appears in the title, description
or branch and run the teams' Git automations (opened, draft, merged) with the
same team and repository rules as webhooks; they do not create Inbox
notifications. Imports and webhooks share one review per request: a review is
matched by code host and external id (or repository and number) inside the
workspace write. Reviews that are open in Flow but no longer listed are read
once, so closed and merged requests are updated (a merge request deleted on
GitLab reads as closed). Code host requests use the same credentials and
outbound policy as the review sync (see
[Trusted private endpoints](#trusted-private-endpoints)).

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_REVIEW_IMPORT` | `off` | `on` lets workspace admins turn the import on for their approved repositories. `off` turns it off for every workspace, including **Sync now**. The earlier values `auto` and `always` mean `on` (still opt-in per workspace). |
| `FLOW_REVIEW_IMPORT_INTERVAL` | `10m` | Period of the background import per workspace (a Go duration, at least `1m`). `0` turns the import off. |

Before setting `FLOW_REVIEW_IMPORT=on` on a deployment, have a workspace admin
approve the repositories to import; workspaces that never turned the import on
are not touched.

## GitHub App

Coding sessions (see [Code hosts](#code-hosts)) use a GitHub App: only an App can mint short-lived,
repository-scoped tokens for sandboxes. The App's webhooks also feed the review
sync above, so App-created PRs show up under Reviews without an OAuth or PAT
connection. A workspace that already has a classic GitHub connection with a
webhook secret keeps syncing through that connection, and the App does not add
a second copy of the same events.

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_GITHUB_APP_ID` | empty | App id (the JWT `iss`) |
| `FLOW_GITHUB_APP_SLUG` | empty | App slug, used for the install link `https://<host>/apps/<slug>/installations/new` |
| `FLOW_GITHUB_APP_PRIVATE_KEY` (`_FILE`) | empty | PEM private key (PKCS#1 or PKCS#8). Required when `FLOW_GITHUB_APP_ID` is set |
| `FLOW_GITHUB_APP_WEBHOOK_SECRET` (`_FILE`) | empty | Webhook secret (`X-Hub-Signature-256`). Required when `FLOW_GITHUB_APP_ID` is set |
| `FLOW_GITHUB_APP_CLIENT_ID` / `FLOW_GITHUB_APP_CLIENT_SECRET` (`_FILE`) | empty | User-to-server OAuth, which lets members link their own GitHub account (see [Members' personal accounts](#members-personal-github-and-gitlab-accounts)) |
| `FLOW_GITHUB_APP_HOST` | `github.com` | GitHub Enterprise Server host |
| `FLOW_GITHUB_APP_API_URL` | `https://api.github.com`, or `https://<host>/api/v3` for GHE | REST API base |

Register the App with these settings:

- Webhook URL: `{FLOW_APP_URL}/api/integrations/github-app/webhook`.
- Setup URL, and Callback URL for user authorization: `{FLOW_APP_URL}/api/integrations/github-app/setup`.
- Permissions: Contents RW, Pull requests RW, Metadata R, Checks R, Actions R, Commit statuses R, Issues R.
- Events: installation, installation repositories, pull request, pull request review, pull request review comment, issue comment, check suite, check run, push.

Installation tokens are minted for one repository with minimal permissions.
They are cached in memory until 5 minutes before they expire and are never
persisted. Each sandbox gets its own token, which is revoked when the sandbox
stops. Members' linked GitHub accounts are described in the next section.

## Members' personal GitHub and GitLab accounts

Code hosting works at two levels. An admin connects the workspace once (the
GitHub App or a GitHub connection, a GitLab connection for gitlab.com or a
self-hosted instance); that covers repository access, sync and webhooks. Each
member can then link their own account in **Settings › Account › Connected
accounts** (the GitHub and GitLab integration pages show "Your personal
account" with a shortcut there). With a linked account Flow:

- attributes host activity to the member: PR/MR authors, requested reviewers,
  comment senders in Reviews, and commenters on coding session PRs/MRs;
- acts as the member: Reviews comments, replies, inline comments, thread
  resolution, approvals, change requests, merges and close/reopen are posted
  with the member's own token, and coding
  sessions push, commit and open their PR/MR as the member (see
  [coding-sessions-security.md](coding-sessions-security.md#member-credentials)).

Without a link Flow falls back to the workspace integration, and the action
shows who it was posted as ("Posted as @flow-bot via the workspace
integration"). The workspace credentials are tried in order: the GitHub App
installation, then the classic connection. A member token is only ever used
for that member's own actions. A member who lacks a permission on the host
(HTTP 403), or cannot see the pull request or comment (HTTP 404), is refused;
Flow does not retry with the workspace credential. A token the host rejects
as invalid (HTTP 401), or a refresh that fails, marks the account
**Reconnect** and the action falls back to the workspace.

Comments, replies, inline comments and thread resolution are saved in Flow
first and then written to the host. When the host refuses one (for example
"Your GitHub account can't make this change"), the change stays in Flow
marked "Not synced" with the error, and **Retry** sends it again as the same
member, for instance once they have been given access. Approvals, change
requests, merges and close/reopen are applied on the host first and fail
without changing Flow.

Tokens are sealed in the encrypted connector secret store
(`FLOW_CONNECTOR_SECRET_KEY` is required), never returned by the API, and
never logged. Every link flow uses a one-time server-side state with a PKCE
(S256) challenge, bound to the member who started it. Linking and unlinking
write `code_host_account.linked` / `code_host_account.unlinked` audit entries.
The routes (`/api/account/code-host-accounts…`) accept only the signed-in
member's session, not API keys.

**GitHub.** When the GitHub App has `FLOW_GITHUB_APP_CLIENT_ID` and
`FLOW_GITHUB_APP_CLIENT_SECRET`, members link through the App's user-to-server
OAuth (callback: the App's setup URL above). Turn on **Expire user
authorization tokens** in the App settings: access tokens then last 8 hours
and Flow refreshes them. Without App credentials, a classic GitHub OAuth app
from `FLOW_INTEGRATION_GITHUB_CLIENT_ID` / `_CLIENT_SECRET` is used (callback
`{FLOW_APP_URL}/api/account/code-host-accounts/oauth/callback`, scopes
`repo read:user user:email`); its tokens do not expire, so they are used only
server side (Flow's own API calls, and coding-session pushes through Flow's
git relay; never in a sandbox).

**GitLab, including self-hosted.** Members only see instances an admin
connected to the workspace (Settings › Integrations › GitLab); they never type
a host, so Flow cannot be pointed at an arbitrary server and no token is sent
anywhere else. A link is keyed by the instance URL and stops applying if the
admin points the connection at another host.

- *OAuth (preferred).* On the GitLab instance, register an application (Admin
  Area › Applications for an instance-wide one, or a group or user
  application):
  - Redirect URI: `{FLOW_APP_URL}/api/account/code-host-accounts/oauth/callback`
    (also shown on the GitLab integration page);
  - Confidential: yes; Scopes: `api` and `read_user`.

  Paste its **Application ID** and **Secret** into the GitLab integration page
  ("OAuth application for member sign-in"). The secret is stored encrypted and
  never shown again. GitLab access tokens last 2 hours; Flow refreshes them
  with this application, and revokes the token when a member unlinks.
- *Personal access token (fallback).* When no application is configured,
  members paste a personal access token with the `read_user` and `api`
  scopes. Flow validates it against `/api/v4/personal_access_tokens/self` and
  `/api/v4/user` and lists missing scopes. A personal access token stays on
  the server: Flow uses it for Reviews actions, to open MRs as the member,
  and for the member's coding-session pushes, which go through Flow's git
  relay where Flow adds the token itself. It is never handed to a coding
  sandbox (see [docs/coding-sessions-security.md](coding-sessions-security.md#member-credentials)).

Self-hosted instances on a private network must be named in
`FLOW_INTEGRATION_GITLAB_HOST` (as for the workspace connection); other hosts
must be public HTTPS addresses.

### Development only: loopback GitLab

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS` | `false` | Development only. Lets the workspace GitLab integration and members' personal GitLab links use a plain-HTTP loopback URL (`http://127.0.0.1:<port>`, `http://localhost:<port>`) while authentication stays enabled, for example to run a local GitLab or a fake one. |

Without it, a plain-HTTP loopback GitLab URL is accepted only when
`FLOW_AUTH_DISABLED=true`; otherwise GitLab must be a public HTTPS address (or
the host named in `FLOW_INTEGRATION_GITLAB_HOST`). The switch only covers
loopback addresses: private ranges such as `10.0.0.0/8`, `172.16.0.0/12`,
`192.168.0.0/16` and link-local addresses stay blocked. Flow refuses to start
with it on when `FLOW_ENV` or `FLOW_ENVIRONMENT` is `production` (or `prod`),
or when `FLOW_APP_URL` is not a localhost or loopback URL. Never set it in a
shared or production deployment.

### Personal Google Calendar and Notion connections

Settings › Account › Connected accounts has a Google Calendar and a Notion row
for each member. Both rows show "Not configured" until the operator sets the
variables below.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_GOOGLE_OAUTH_CLIENT_ID` / `FLOW_GOOGLE_OAUTH_CLIENT_SECRET` | unset | A Google OAuth client (type Web application). Authorized redirect URI: `{FLOW_APP_URL}/api/account/connections/google-calendar/callback`. Enable the Google Calendar API in the project. Needs `FLOW_CONNECTOR_SECRET_KEY`. |
| `FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL` | `15m` | How often each connected member's calendar is read (5 minutes to 24 hours). |
| `FLOW_NOTION_CONNECTION_URL` | unset | HTTPS address of the Notion connection page members are sent to (the Notion connection gallery page of your link-preview connection). |
| `FLOW_DEV_GOOGLE_BASE_URL` | unset | Development only, honored only with `FLOW_AUTH_DISABLED=true` or `FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS=true`: a loopback fake that replaces Google's authorize, token, revoke, userinfo and Calendar endpoints. |

**Google Calendar.** A member authorizes Flow (OAuth, PKCE, offline access) with
the scopes `openid`, `email` and
`https://www.googleapis.com/auth/calendar.events.readonly`. Flow reads only the
member's out-of-office events (`eventTypes=outOfOffice`, next 14 days, one
request per member per interval) and shows the member as out of office while
one is active (profile card, "Away" in assignee pickers). All-day events are
read as UTC. The sync is off unless the Google client and
`FLOW_CONNECTOR_SECRET_KEY` are set, reads at most 25 members a minute, backs
off exponentially (up to 6 hours) after errors, and writes to the database
only when a member's window changed. Replicas share the work through a
database claim on each connection (`personal_connections.sync_next_at`), so a
calendar is read once per interval however many replicas run. The tokens are
sealed in the connector secret store; Disconnect revokes the grant at Google
and deletes them, and a grant Google revoked turns the row into "Reconnect
needed". Link and unlink write `personal_connection.linked` /
`personal_connection.unlinked` audit entries. Google's endpoints are reached
through the outbound policy for public HTTPS addresses.

**Notion.** Notion's link previews are a connection on Notion's side: a member
pastes a Flow link into Notion and authorizes it there. The Connected accounts
row turns on previews for the member (Flow cannot see Notion's side of the
authorization) and opens `FLOW_NOTION_CONNECTION_URL`; Disconnect turns them
off. While on, `GET /api/notion/link-preview?url=` answers with the title,
status, assignee and team of an issue or project link of this deployment, as
the signed-in member (their session or the OAuth token Notion holds for them),
with the same access checks as the issue and project reads. Register the
connection with Notion as an OAuth application of Flow (Settings › API ›
OAuth applications) whose redirect URI is the one Notion gives you.

## Coding sessions

Coding sessions run an agent (Claude Code, Codex or OpenCode) in a sandbox Pod
or container and open a draft pull or merge request. The operator guide is
[docs/coding-sessions.md](coding-sessions.md); the security model is in
[docs/coding-sessions-security.md](coding-sessions-security.md) and the runbook
in [docs/coding-sessions-operations.md](coding-sessions-operations.md). The
design reference is `docs/coding-sessions-design.md` (all variables are listed
in §8). The Helm chart (`deploy/helm/flow`) maps every variable below to a
value.

Everything is off until `FLOW_CODING_ENABLED=true`; validation is strict only
then. Workspaces also need the `coding-sessions` feature flag. Secrets accept
`_FILE` variants. Durations are Go durations (`90s`, `15m`, `4h`).

### Runtime and listener

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_ENABLED` | `false` | Turn the coding-session runtime on. |
| `FLOW_CODING_PROVIDER` | `disabled` | `disabled`, `fake`, `docker` (development and CI), `kubernetes`, `opensandbox` (an [OpenSandbox](https://github.com/alibaba/OpenSandbox) deployment), or `local` (development only). See [Sandbox providers](#sandbox-providers). |
| `FLOW_CODING_RUNTIME_URL` | `FLOW_APP_URL` | URL sandboxes use to reach Flow's runtime API and LLM proxy, e.g. `http://flow.flow.svc.cluster.local:8080`. |
| `FLOW_CODING_RUNTIME_ADDR` | empty | When set (for example `:8090`), Flow serves `/internal/coding-runtime/*` and `/internal/llm-proxy/*` only on this second listener, and the main listener answers 404 for them, so a NetworkPolicy can expose just this port to sandboxes. Empty keeps them on the main listener. |
| `FLOW_CODING_INSTANCE_ID` | `flow` | `flow.dev/instance` label on every object Flow creates; orphan GC only touches this value. Give every Flow installation sharing a cluster its own id. |
| `FLOW_CODING_MAX_SESSIONS` / `FLOW_CODING_WORKSPACE_SESSIONS` | `20` / `5` | Sessions holding a sandbox (provisioning through publishing, and stopping), per installation and per workspace. Further sessions wait in `queued` until `FLOW_CODING_QUEUE_TIMEOUT`, then fail with `capacityUnavailable`. |
| `FLOW_CODING_RECONCILE_INTERVAL` | `5s` | How often the session and sandbox reconcilers lease due rows (minimum `1s`; writes wake them sooner). |
| `FLOW_CODING_GC_INTERVAL` | `5m` | Orphan GC interval. |
| `FLOW_CODING_ORPHAN_GRACE` | `10m` | Age before an object without a live row is deleted. |

### Phase timeouts and retries

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_QUEUE_TIMEOUT` | `30m` | Waiting for capacity, then `capacityUnavailable`. |
| `FLOW_CODING_APPROVAL_TIMEOUT` | `168h` | How long a session from an untrusted external source waits for a member's approval (and how long an agent question waits for an answer) before it ends with `approvalExpired`. |
| `FLOW_CODING_PROVISION_TIMEOUT` | `5m` | Resolving environment, access and snapshot. |
| `FLOW_CODING_START_TIMEOUT` | `10m` | Pod scheduling, image pull, restore and bootstrap. |
| `FLOW_CODING_SETUP_TIMEOUT` | `20m` | Clone, `mise install` and the prepare script without a snapshot. |
| `FLOW_CODING_TURN_TIMEOUT` | `1h` | One agent turn, then `turnTimeout`. |
| `FLOW_CODING_HEARTBEAT_TIMEOUT` | `2m` | Missing runner heartbeats, then `runnerLost`. |
| `FLOW_CODING_PUBLISH_TIMEOUT` | `5m` | Verifying the push and opening the draft PR/MR. |
| `FLOW_CODING_IDLE_TTL` | `15m` | A finished session keeps its sandbox warm this long. |
| `FLOW_CODING_STOP_GRACE` | `1m` | Time the harness gets after SIGINT on stop; the provider's SIGTERM → SIGKILL grace is this plus 30s so the runner can still commit and push work in progress. Stuck Pods are force-deleted after twice that. |
| `FLOW_CODING_MAX_SESSION_DURATION` | `4h` | Pod `activeDeadlineSeconds` and the cumulative running cap (`sessionTimeout`). |
| `FLOW_CODING_TOKEN_GRACE` | `5m` | Sandbox token lifetime beyond the sandbox deadline. |
| `FLOW_CODING_PROVISION_RETRIES` | `3` | Retries of transient provisioning failures. |
| `FLOW_CODING_START_RETRIES` | `2` | New sandboxes tried when a start fails; each retry uses a new sandbox id. |
| `FLOW_CODING_PUBLISH_RETRIES` | `5` | Retries of transient PR/MR failures. |

Each phase ends with the error code listed in the design (§4.1). Retries of
transient failures back off 2s·2ⁿ, at most 1 minute. Heartbeat timeouts count
from the later of the last heartbeat and the Flow process start, so a Flow
restart never marks runners lost. Snapshot builds have their own timeout and
retries (see [Environments and snapshots](#environments-and-snapshots)), and the
debug terminal its own lifetime.

### Metering and spend limits

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_RUNTIME_PRICES` | `small=0.10,medium=0.20,large=0.40` | USD per sandbox-hour by size, recorded in `coding_usage_records`. Runtime is billed in 5-minute increments while a sandbox runs and to the second when it ends; warm-pool sandboxes are not billed. |

LLM tokens are priced by `FLOW_CODING_MODEL_PRICES` (see
[LLM proxy and models](#llm-proxy-and-models)).

Settings › Usage & limits spend limits (workspace, per-user and per-loop, daily/weekly/monthly resets with the
configured reset hour, weekday and month day, computed in UTC, plus per-user and per-loop overrides) are enforced at
session start, at every follow-up turn, by the LLM proxy per request (30-second cached decision) and after every metered
usage record. A session over its limit ends with `quotaExceeded`; loop sessions count against the loop limit and the
workspace limit, other sessions against the creator's limit and the workspace limit. An override of 0 inherits the
default limit.

### Sandbox providers

Coding sessions run in sandboxes created by `FLOW_CODING_PROVIDER`:

- `kubernetes`: one bare Pod per sandbox in `FLOW_CODING_K8S_NAMESPACE`, plus a
  token Secret (`flow-cs-<id>-token`, mounted as a file, owned by the Pod), a
  workspace PVC (`flow-ws-<id>`) when PVC workspaces are used, and two
  namespace-wide NetworkPolicies (`flow-sandbox-default-deny`,
  `flow-sandbox-egress`) when `FLOW_CODING_K8S_NETWORK_POLICY=true`. Flow uses the
  kubeconfig at `FLOW_CODING_K8S_KUBECONFIG`, otherwise the in-cluster service
  account; it never reads the default kubeconfig. Pods satisfy the restricted Pod
  Security Standard (uid 1000, no privilege escalation, all capabilities dropped,
  seccomp `RuntimeDefault`, read-only root filesystem, no service-account token,
  no hostPath).
- `docker`: one container per sandbox through the Engine API at
  `FLOW_CODING_DOCKER_HOST` (`unix://`, `tcp://`, `http(s)://`), on the internal
  network `FLOW_CODING_DOCKER_NETWORK`, with `FLOW_CODING_DOCKER_RUNTIME` as
  `--runtime`. The token is written into a per-sandbox volume through the archive
  API, so remote daemons work. `volumesnapshot` is not supported.
- `opensandbox`: one [OpenSandbox](https://github.com/alibaba/OpenSandbox)
  sandbox per Flow sandbox, created through the lifecycle server at
  `FLOW_CODING_OPENSANDBOX_URL` (API specs of release `1.1.0`). Flow finds its
  sandboxes by metadata (the `flow.dev/*` labels), uploads the sandbox token
  through execd to `FLOW_CODING_OPENSANDBOX_TOKEN_PATH` and starts `flow-runner`
  as an execd background command, whose exit code is the sandbox's result. The
  container runtime (gVisor, Kata), node placement and quotas are OpenSandbox's
  server configuration; Flow needs no Kubernetes RBAC. `volumesnapshot` is not
  supported (archives are). See
  [OpenSandbox](coding-sessions-operations.md#opensandbox) for the in-cluster
  deployment.
- `local` (development only, never production): each sandbox is a process on
  Flow's host, started as `<FLOW_CODING_LOCAL_COMMAND> <mode>` (preceded by
  `<FLOW_CODING_LOCAL_COMMAND> restore` when a snapshot archive is restored). The
  launcher must isolate the runner itself (bubblewrap, Landlock, a VM); Flow adds no
  resource limits, network policy, exec or debug terminal. Its environment is the
  container runner environment (`FLOW_TOKEN_FILE=/var/run/flow/token`,
  `HOME=/home/flow`, …) plus the host paths it must map or pass on:
  `FLOW_LOCAL_TOKEN_FILE` (mode 0600), `FLOW_LOCAL_HOME_DIR` and
  `FLOW_LOCAL_WORKSPACE_DIR`; nothing of Flow's own environment is inherited. A
  launcher without a mount namespace sets `FLOW_TOKEN_FILE`, `HOME` and
  `FLOW_WORKSPACE_DIR` to those paths; the runner then renames snapshot archives to
  and from the container layout. Sandboxes do not survive a Flow restart.
- `fake` (tests) and `disabled`.

#### Image, sizes, regions and warm pool

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_IMAGE` | `ghcr.io/leozhengliu-pixel/flow-sandbox:latest` | Sandbox image. Pin a digest in production. |
| `FLOW_CODING_IMAGE_PULL_POLICY` | `IfNotPresent` | Kubernetes image pull policy. |
| `FLOW_CODING_IMAGE_PULL_SECRETS` | empty | CSV of Secret names in the sandbox namespace. |
| `FLOW_CODING_SIZE_SMALL` | `cpu=1,memory=8Gi,ephemeral=20Gi,pids=4096` | Limits of the small size. |
| `FLOW_CODING_SIZE_MEDIUM` | `cpu=2,memory=16Gi,ephemeral=40Gi,pids=8192` | Limits of the medium size. |
| `FLOW_CODING_SIZE_LARGE` | `cpu=4,memory=32Gi,ephemeral=80Gi,pids=16384` | Limits of the large size. |
| `FLOW_CODING_WORKSPACE_SIZE` | `20Gi` | Workspace PVC size or emptyDir `sizeLimit`. |
| `FLOW_CODING_CPU_REQUEST_RATIO` | `1` | Requests = limits × ratio, in (0, 1]. `1` gives Guaranteed QoS. |
| `FLOW_CODING_REGIONS` | empty | CSV of region names; empty disables region pinning. Pinning uses required node affinity. |
| `FLOW_CODING_REGION_LABEL` | `topology.kubernetes.io/region` | Node label matched by region pinning. |
| `FLOW_CODING_DEFAULT_REGION` | empty | Region used when the workspace pins none. |
| `FLOW_CODING_WARM_POOL` | empty | Unbound sandboxes kept per size (and per region), e.g. `small=2,medium=1`. |
| `FLOW_CODING_WARM_POOL_MAX_AGE` | `1h` | Warm sandboxes are recycled after this age. |

PID limits are not a Pod field: sandboxes receive `FLOW_PIDS_LIMIT` (from the
size) for the runner's `ulimit -u`; configure kubelet `podPidsLimit` on the
sandbox node pool as well. The docker provider sets `PidsLimit`.

#### Kubernetes provider

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_K8S_KUBECONFIG` | empty | Kubeconfig path; empty uses the in-cluster ServiceAccount. |
| `FLOW_CODING_K8S_NAMESPACE` | `flow-sandboxes` | Namespace for every sandbox object. |
| `FLOW_CODING_K8S_RUNTIME_CLASS` | empty | `runtimeClassName` (e.g. `gvisor`, `kata`); empty uses runc. |
| `FLOW_CODING_K8S_HOST_USERS` | `true` | `false` sets `hostUsers: false` (user namespaces). |
| `FLOW_CODING_K8S_STORAGE_CLASS` | empty | StorageClass of workspace PVCs. |
| `FLOW_CODING_K8S_WORKSPACE_VOLUME` | `auto` | `auto` (PVC for volumesnapshot, emptyDir for archive), `pvc` or `emptyDir`. |
| `FLOW_CODING_K8S_NODE_SELECTOR` | empty | `k=v,k2=v2`, e.g. `flow.dev/pool=sandbox`. |
| `FLOW_CODING_K8S_TOLERATIONS` | empty | CSV in taint syntax `key[=value]:Effect`. |
| `FLOW_CODING_K8S_SERVICE_ACCOUNT` | `flow-sandbox` | ServiceAccount of sandbox Pods; it needs no RBAC and its token is not mounted. |
| `FLOW_CODING_K8S_PRIORITY_CLASS` | empty | PriorityClass of sandbox Pods. |
| `FLOW_CODING_K8S_EXTRA_LABELS` | empty | `k=v` CSV added to sandbox objects. |
| `FLOW_CODING_K8S_EXTRA_ANNOTATIONS` | empty | `k=v` CSV added to sandbox objects. |
| `FLOW_CODING_K8S_NETWORK_POLICY` | `true` | Flow ensures the sandbox NetworkPolicies itself. The Helm chart sets `false` when it renders them. |
| `FLOW_CODING_K8S_FLOW_NAMESPACE` | `flow` | Namespace of the Flow Pods (NetworkPolicy peer). |
| `FLOW_CODING_K8S_FLOW_POD_SELECTOR` | `app.kubernetes.io/name=flow` | Labels of the Flow Pods (NetworkPolicy peer). |
| `FLOW_CODING_K8S_PROXY_NAMESPACE` | `flow-sandboxes` | Namespace of the egress proxy. |
| `FLOW_CODING_K8S_PROXY_POD_SELECTOR` | `app.kubernetes.io/name=flow-egress` | Labels of the egress proxy Pods. |
| `FLOW_CODING_K8S_QPS` / `FLOW_CODING_K8S_BURST` | `20` / `40` | client-go rate limits. |

Example:

```dotenv
FLOW_CODING_PROVIDER=kubernetes
FLOW_CODING_INSTANCE_ID=flow             # flow.dev/instance label; GC only touches this instance
FLOW_CODING_IMAGE=ghcr.io/leozhengliu-pixel/flow-sandbox@sha256:...
FLOW_CODING_IMAGE_PULL_SECRETS=registry   # CSV of Secret names
FLOW_CODING_SIZE_MEDIUM=cpu=2,memory=16Gi,ephemeral=40Gi,pids=8192
FLOW_CODING_K8S_NAMESPACE=flow-sandboxes
FLOW_CODING_K8S_RUNTIME_CLASS=gvisor      # empty = runc
FLOW_CODING_K8S_NODE_SELECTOR=flow.dev/pool=sandbox
FLOW_CODING_K8S_TOLERATIONS=flow.dev/sandbox=true:NoSchedule
FLOW_CODING_K8S_EXTRA_LABELS=team=platform
FLOW_CODING_REGIONS=eu,us                 # region pinning via required node affinity
```

The egress NetworkPolicy allows only DNS (kube-dns), the Flow Pods and the egress
proxy; there is no `ipBlock`, so `169.254.169.254` and every other address stay
unreachable. When `FLOW_CODING_RUNTIME_ADDR` is set, only that port of the Flow
Pods is allowed; otherwise any Flow Pod port is (NetworkPolicy ports are Pod
ports, which the Service URL does not reveal). The proxy port is the port of
`FLOW_CODING_EGRESS_PROXY_URL`, default 3128. Enforcement depends on the CNI.

Flow's Role in the sandbox namespace needs: pods (create, get, list, delete),
pods/log (get), pods/exec (create, get), secrets (create, get, list, delete, patch),
persistentvolumeclaims (create, get, list, delete), namespaces (get, the sandbox
namespace only), networkpolicies (get, create, patch) when Flow manages them, and volumesnapshots (create, get, list, delete)
for the volumesnapshot strategy. Startup preflight checks these with
`SelfSubjectAccessReview`, plus the RuntimeClass, StorageClass and VolumeSnapshot
CRD.

#### Docker provider

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. |
| `FLOW_CODING_DOCKER_NETWORK` | `flow-sandbox` | Internal network (no default route) shared by sandboxes, the egress proxy and Flow. |
| `FLOW_CODING_DOCKER_RUNTIME` | empty | Container runtime, e.g. `runsc`. |

#### OpenSandbox provider

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_OPENSANDBOX_URL` | `http://opensandbox-server.opensandbox-system.svc.cluster.local` | Lifecycle server base URL (`/v1` is appended unless present). The default is the `opensandbox-server` chart's Service in the same cluster. |
| `FLOW_CODING_OPENSANDBOX_API_KEY` | empty | Sent as `OPEN-SANDBOX-API-KEY` (secret; `_FILE` supported). Must match the server's `server.api_key`. |
| `FLOW_CODING_OPENSANDBOX_REQUEST_TIMEOUT` | `30s` | Timeout of every lifecycle and execd call except create. |
| `FLOW_CODING_OPENSANDBOX_CREATE_TIMEOUT` | `10m` | Timeout of `POST /sandboxes`, which the server answers once the Pod runs (image pull included). Creates run in the background. Keep it above the server's `sandbox_create_timeout_seconds`. |
| `FLOW_CODING_OPENSANDBOX_RETRIES` | `3` | Retries of idempotent calls on network errors, 409, 429 (honouring `Retry-After`) and 5xx (0-10). |
| `FLOW_CODING_OPENSANDBOX_CA_FILE` | empty | PEM CA bundle for an `https` URL. |
| `FLOW_CODING_OPENSANDBOX_INSECURE_SKIP_VERIFY` | `false` | Skip TLS verification (development only). |
| `FLOW_CODING_OPENSANDBOX_EXTRA_METADATA` | empty | `k=v` CSV added to every sandbox's metadata (Kubernetes label rules; `flow.dev/` and `opensandbox.io/` keys are reserved). |
| `FLOW_CODING_OPENSANDBOX_EXTENSIONS` | empty | `k=v` CSV passed through as the create request's `extensions`. |
| `FLOW_CODING_OPENSANDBOX_POOLS` | empty | OpenSandbox Pool per size, e.g. `small=flow-small` (`extensions.poolRef`; the Pool template must use the Flow sandbox image and provides image and resources). Cannot be combined with the egress policy. |
| `FLOW_CODING_OPENSANDBOX_SIZE_<SMALL\|MEDIUM\|LARGE>` | empty | `resourceLimits` of a size verbatim (e.g. `cpu=8,memory=64Gi,gpu=1`), instead of cpu and memory from `FLOW_CODING_SIZE_*`. |
| `FLOW_CODING_OPENSANDBOX_EPHEMERAL_STORAGE` | `false` | Also send the size's ephemeral storage as the `ephemeral-storage` limit. |
| `FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW` | `1h` | Sandboxes are created with this OpenSandbox timeout (at most Flow's deadline) and renewed at half of it while Flow observes them, so sandboxes of a Flow that is gone expire on their own (minimum `5m`). |
| `FLOW_CODING_OPENSANDBOX_EXECD_ACCESS` | `proxy` | `proxy` reaches execd through the server's `/sandboxes/{id}/proxy/44772` route; `endpoint` resolves `GET /sandboxes/{id}/endpoints/44772` and calls the Pod directly. |
| `FLOW_CODING_OPENSANDBOX_EXECD_TOKEN` | empty | `X-EXECD-ACCESS-TOKEN` for execd, when it requires one (secret). |
| `FLOW_CODING_OPENSANDBOX_RUN_AS_UID` | `1000` | uid and gid of the runner, exec and the debug terminal (`0` keeps execd's user). |
| `FLOW_CODING_OPENSANDBOX_TOKEN_PATH` | `/tmp/flow/token` | Where Flow writes the sandbox token (mode 0600) inside the sandbox; the runner's `FLOW_TOKEN_FILE`. Its directory also holds the runner's pid and exit code. |
| `FLOW_CODING_OPENSANDBOX_EGRESS_POLICY` | `off` | `proxy` sends a deny-by-default OpenSandbox `networkPolicy` allowing only the Flow runtime host, the egress proxy host and `FLOW_CODING_OPENSANDBOX_EGRESS_ALLOW`, so everything else must go through Flow's egress proxy. Needs OpenSandbox's egress sidecar (`[egress]`; `dns+nft` mode also blocks direct IPs). |
| `FLOW_CODING_OPENSANDBOX_EGRESS_ALLOW` | empty | Extra domains for the egress policy (domains only, no IPs). |
| `FLOW_CODING_OPENSANDBOX_IMAGE_USERNAME` / `FLOW_CODING_OPENSANDBOX_IMAGE_PASSWORD` | empty | Registry credentials for a private sandbox image (`image.auth`); the password is a secret. |

`FLOW_CODING_RUNTIME_URL` must be reachable from OpenSandbox's sandbox Pods; in
the same cluster use Flow's Service URL (the Helm chart's default). The
`FLOW_CODING_K8S_*` settings do not apply; RuntimeClass and user namespaces are
OpenSandbox server settings (`[secure_runtime]`, its pod template).

#### Local provider (development only)

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_LOCAL_COMMAND` | empty | Absolute path of the launcher that isolates and runs the sandbox runner. Required with `FLOW_CODING_PROVIDER=local`. |
| `FLOW_CODING_LOCAL_ROOT` | `$TMPDIR/flow-local-sandboxes` | Directory for per-sandbox directories (token, home, `runner.log`) and workspace volumes. |

#### Reconcile loops, warm pool and GC

The sandbox reconciler runs every `FLOW_CODING_RECONCILE_INTERVAL` (leased rows,
safe on any number of replicas), gives a sandbox `FLOW_CODING_START_TIMEOUT` and
`FLOW_CODING_START_RETRIES` to start, archives runner logs as
`coding-logs-<workspace hash>-<sandbox>.log` on stop, and meters runtime in
5-minute increments priced by `FLOW_CODING_RUNTIME_PRICES`. The warm pool keeps
`FLOW_CODING_WARM_POOL` unbound sandboxes and recycles them after
`FLOW_CODING_WARM_POOL_MAX_AGE`. Orphan GC runs every `FLOW_CODING_GC_INTERVAL`
and deletes this instance's objects that no live row owns once they are older
than `FLOW_CODING_ORPHAN_GRACE`, and sweeps expired sandbox tokens. The warm
pool and GC take the Redis leader lock when Redis is enabled.

### Sandbox runner environment

The sandbox image (`deploy/sandbox-image/`) runs `flow-runner` (`sandbox-runner/`). Flow's sandbox provider sets these
variables on the sandbox container; operators don't set them on the Flow server.

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_RUNTIME_URL` | required | Flow's runtime endpoint as the sandbox reaches it (`FLOW_CODING_RUNTIME_URL`) |
| `FLOW_TOKEN_FILE` | `/var/run/flow/token` | File holding the per-sandbox token (`fcs_…`, mounted 0440 for gid 1000), re-read on every call |
| `FLOW_SANDBOX_ID` | from bootstrap | Sandbox id, used in logs |
| `FLOW_RUNNER_MODE` | `session` | `session`, `snapshot`, `debug`, `warm` or `restore` (the container command argument wins) |
| `FLOW_SANDBOX`, `LINEAR_SANDBOX` | `1` | Always set for setup scripts and the agent, so repository scripts can detect the sandbox |
| `FLOW_SNAPSHOT`, `LINEAR_SNAPSHOT` | unset | `1` while a snapshot is being built (setup and prepare script only) |
| `FLOW_CODING_BRANCH_PREFIX` | `flow/` | Fallback push prefix when the bootstrap carries no `repo.branchPrefix` (the server setting is described under [Triggers](#triggers)) |
| `FLOW_PIDS_LIMIT` | unset | `ulimit -u` applied to the agent process tree (the sandbox size's PID limit) |
| `FLOW_SNAPSHOT_ID` | from bootstrap | Snapshot to unpack in `restore` mode |
| `FLOW_ARCHIVE_URL` | unset | Archive URL for `restore` mode (fetched with the sandbox token); without it the runner uses the snapshot archive route. `flow-runner restore` exits `86` when the archive can't be restored |
| `FLOW_SANDBOX_SIZE` | unset | Sandbox size (`small`, `medium`, `large`), logged at startup |
| `FLOW_RUNNER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` for the runner's JSON log (the Pod log) |
| `FLOW_WORKSPACE_DIR`, `FLOW_REPO_DIR`, `FLOW_BIN_DIR` | `/workspace`, `/workspace/repo`, `/opt/flow/bin` | Path overrides, for tests |

The runner never receives model provider keys. It points Claude Code (`ANTHROPIC_BASE_URL` plus `ANTHROPIC_AUTH_TOKEN`),
Codex (`~/.codex/config.toml` provider `flow`, key in `FLOW_LLM_TOKEN`) and OpenCode (`OPENCODE_CONFIG` with an
OpenAI-compatible provider) at Flow's LLM proxy, using the sandbox token. git, `gh` and `glab` get short-lived
credentials from Flow at runtime (`flow-git-credential`; `~/.flow/cli-env`, which sets `GH_TOKEN`/`GH_HOST` or
`GITLAB_TOKEN`/`GITLAB_HOST`/`GITLAB_API_PROTOCOL`/`GLAB_CONFIG_DIR`/`NO_PROMPT=1`), and signed commits go through
`flow-sign`, which calls Flow's `/sign`. The image pins the CLI versions as Dockerfile build arguments.

### Environments and snapshots

Coding-session environments (Settings › Coding sessions) are stored in the
`coding_environments` table; the legacy `codingAgentSettings.environments` list
is moved there once at startup. Environment secrets are encrypted with
`FLOW_CODING_SECRET_KEY` (see [Secrets and commit signing](#secrets-and-commit-signing)).
These variables control snapshot builds and the debug terminal:

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_SNAPSHOT_STRATEGY` | `archive` | `archive` (tar.zst in `FLOW_STORAGE_DRIVER`, works everywhere) or `volumesnapshot` (Kubernetes CSI VolumeSnapshots; kubernetes provider and PVC workspaces only). |
| `FLOW_CODING_VOLUME_SNAPSHOT_CLASS` | empty | VolumeSnapshotClass; empty uses the cluster default. |
| `FLOW_CODING_ARCHIVE_PREFIX` | `coding-snapshots` | Object key prefix of snapshot archives in `FLOW_STORAGE_DRIVER`. |
| `FLOW_CODING_ARCHIVE_MAX_BYTES` | `10737418240` | Largest accepted snapshot archive upload. |
| `FLOW_CODING_SNAPSHOT_TIMEOUT` | `30m` | A build that runs longer fails with `snapshotFailed`. |
| `FLOW_CODING_SNAPSHOT_RETRIES` | `1` | Extra attempts of a snapshot build or restore after a lost sandbox (a failing prepare script is not retried). |
| `FLOW_CODING_SNAPSHOT_TTL` | `168h` | Ready snapshots expire and their storage is deleted after this. |
| `FLOW_CODING_SNAPSHOT_RETAIN` | `2` | Ready snapshots kept per environment; older ones are deleted. |
| `FLOW_CODING_SNAPSHOT_REFRESH_INTERVAL` | `24h` | A snapshot older than this is rebuilt when the default branch has moved. |
| `FLOW_CODING_DEBUG_TERMINAL` | on | `off` disables the environment debug terminal. |
| `FLOW_CODING_DEBUG_TERMINAL_TTL` | `20m` | Lifetime of a debug-terminal sandbox (billed as sandbox runtime to the user). |

Saving an environment queues a snapshot build whenever the image, mise config,
prepare script, dependency mode, secret names or base branch change. Build logs
are kept in the database (8 MiB per snapshot) together with the last 20
snapshots of each environment.

Snapshot strategies:

- `archive` (default): tar.zst of `/workspace` and `/home/flow`, stored through
  `FLOW_STORAGE_DRIVER` under
  `<FLOW_CODING_ARCHIVE_PREFIX>-<workspace hash>-<snapshot>.tar.zst` (object keys are
  flat: the local driver and signed file URLs reject `/`), capped by
  `FLOW_CODING_ARCHIVE_MAX_BYTES`, and restored by the `restore` init container
  (inline in the docker container).
- `volumesnapshot`: CSI `VolumeSnapshot` (`snapshot.storage.k8s.io/v1`, through
  the dynamic client) of a build PVC, with `FLOW_CODING_VOLUME_SNAPSHOT_CLASS`;
  restores are PVCs with the snapshot as `dataSource`.

### Egress proxy

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_EGRESS_PROXY_URL` | empty | Proxy sandboxes must use, e.g. `http://flow-egress.flow-sandboxes.svc.cluster.local:3128`. Set as `HTTP(S)_PROXY` with the sandbox's credentials. Empty means sandboxes have no Internet access. |
| `FLOW_CODING_EGRESS_ALLOWLIST` | empty | Extra domains for every sandbox; `.example.com` also matches subdomains. |
| `FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST` | on | `off` drops the built-in code host, registry and toolchain list. |
| `FLOW_CODING_EGRESS_NO_PROXY` | empty | Added to `NO_PROXY`; the Flow runtime host is always included. |
| `FLOW_CODING_EGRESS_ACL_SECRET` | empty | Shared secret of the Squid ACL helper. When set, `POST /internal/coding-runtime/egress-check` answers the proxy and per-environment network modes apply; when empty the route returns 404 and the proxy uses its static allowlist. |

The proxy container (`deploy/egress-proxy`) reads `FLOW_EGRESS_FLOW_URL` (Flow's
runtime URL), `FLOW_CODING_EGRESS_ACL_SECRET` or `_FILE`,
`FLOW_CODING_EGRESS_ALLOWLIST`, `FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST`,
`FLOW_EGRESS_ALLOWLIST_FILE`, `FLOW_EGRESS_BLOCK_CIDRS`, `FLOW_EGRESS_PORT`,
`FLOW_EGRESS_HELPER_CHILDREN` and `FLOW_EGRESS_ACL_TIMEOUT` (default `5s`).

### LLM proxy and models

Sandboxes never receive model API keys. Claude Code, Codex and the open-source harness call Flow's LLM proxy (`/internal/llm-proxy/{anthropic,openai,opensource}/v1/...`) with their sandbox token, and Flow forwards each request with the real key (design §12 and §20).

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_LLM_ANTHROPIC_BASE_URL` / `FLOW_CODING_LLM_ANTHROPIC_API_KEY` | `https://api.anthropic.com` / empty | Anthropic Messages upstream for Claude Code. Without a key the route answers 503. |
| `FLOW_CODING_LLM_OPENAI_BASE_URL` / `FLOW_CODING_LLM_OPENAI_API_KEY` | `https://api.openai.com/v1` / empty | OpenAI Responses and Chat Completions upstream for Codex. The base includes `/v1`. |
| `FLOW_CODING_LLM_OPENAI_WIRE_API` | `responses` | What the OpenAI upstream speaks: `responses`, or `chat` for providers that only offer Chat Completions (DeepSeek, vLLM, …). With `chat`, the LLM proxy translates Codex's Responses requests to Chat Completions and the answers (streamed or not, tool calls, reasoning, usage) back. Hosted tools such as web search are not available then, and `previous_response_id` is refused. |
| `FLOW_CODING_LLM_OPENSOURCE_BASE_URL` / `FLOW_CODING_LLM_OPENSOURCE_API_KEY` | empty | OpenAI-compatible upstream for OpenCode (vLLM, Ollama, OpenRouter). The key is optional. |
| `FLOW_CODING_MODELS_CLAUDE_CODE` | `claude-sonnet-4-5,claude-opus-4-1` | Claude Code model catalog. |
| `FLOW_CODING_MODELS_CODEX` | `gpt-5-codex,gpt-5` | Codex model catalog. |
| `FLOW_CODING_MODELS_OPEN_SOURCE` | empty | Open-source model catalog. |
| `FLOW_CODING_MODEL_INFO` | empty | JSON display metadata for the model pickers: `{"gpt-5-codex":{"label":"GPT-5 Codex","group":"frontier","description":"Strong reasoning for multi-file changes","tier":"standard"}}`. `group` is `frontier` or `other`; `tier` (`small`, `standard` or `large`) is the tier Auto routes a task to (unknown models are `standard`); any field left out falls back to Flow's built-in labels and descriptions for well-known models, the model id, and a positional group (the first three catalog entries are Frontier, the rest Other models). Pickers list Auto, then Frontier, then Other models. |
| `FLOW_CODING_MODEL_PRICES` | empty | JSON, USD per million tokens: `{"claude-sonnet-4-5":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}}`. Unpriced models are recorded at zero cost, with a warning log and the `flow_coding_llm_unpriced_total` metric. |
| `FLOW_CODING_LLM_TIMEOUT` | `10m` | Upper bound for each proxied request, including streaming. |

In each catalog the first entry is what `auto` resolves to; "Restart with default model" switches a session back to `auto`. An Auto session falls back once to the next entry when the upstream reports the model missing (404 or model-not-found) or overloaded (503/529).

How the proxy behaves:

- **Models.** Requests that name another model are rewritten to the session's model. Claude Code's background Haiku calls are rewritten to the cheapest priced Claude model in the catalog.
- **Metering.** Usage comes from the response, including streamed usage events: Anthropic `message_start`/`message_delta`, Responses `response.completed`, and the chat completions usage chunk (the proxy sets `stream_options.include_usage`). Each request writes one `coding_usage_records` row.
- **Spend limits.** Over the spend limit, the proxy answers `402 {"error":{"type":"quota_exceeded"}}` with `X-Flow-Error-Code: quotaExceeded`. Spend decisions are cached for 30 seconds.
- **Fixed limits.** These are not configurable:
  - request bodies: 32 MiB;
  - rate: 240 requests per minute per session, with a burst of 60;
  - concurrency: 16 in-flight requests per session.
- **Logs.** Logs carry ids, the model, the status, token counts and the cost. They never contain prompts, completions, tokens or keys.

### Secrets and commit signing

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_SECRET_KEY` | `FLOW_CONNECTOR_SECRET_KEY` | Base64 32-byte AES-256-GCM key (`_FILE` works) for environment secrets, users' commit signing keys and token material. Required (directly or through the fallback) when coding sessions are enabled. |

Environment secrets cannot be read back or changed, only deleted and added
again. Rotating or losing the key makes stored environment secrets and signing
keys unreadable; they must be added or uploaded again.

**Commit signing.** A key uploaded under Settings › Account › Commit signing (SSH or unencrypted OpenPGP) is stored encrypted under `FLOW_CODING_SECRET_KEY`, and the private key never enters a sandbox. The runner's `flow-sign` shim calls `POST /internal/coding-runtime/sign`, and Flow answers with an SSHSIG (namespace `git`) or an armored OpenPGP detached signature. When the workspace requires signed commits and the user has no stored key, the session fails with `missingCommitSigningKey`. Keys uploaded before this release stored no private key: `GET /api/account/signing-key` returns `needsReupload: true` for them, and the user must upload the key again.

### MCP servers in coding sessions

Coding-session agents (Claude Code, Codex, OpenCode) can use the workspace's
approved MCP connectors, for example an intranet code-search server. The
sandbox never gets the connector's credential or a network route to it:
Flow's MCP gateway (`/internal/coding-runtime/mcp/{id}`) relays the calls,
the same way the LLM proxy relays model traffic and the git relay relays
pushes.

| Variable | Helm value | Meaning |
| --- | --- | --- |
| `FLOW_CODING_MCP_SERVERS` | `coding.mcpServers` | Comma-separated application policy ids exposed to coding sessions in every workspace where that policy exists, in addition to the ones a workspace admin enables. |

A connector reaches a session when it is an MCP connector that is
**approved**, **workspace-wide** (shared), allowed by the workspace's
connector settings (MCP connectors on, not blocked by sensitive data
protection), and either enabled for coding sessions by a workspace admin
(Settings › Security › *Enable for coding sessions*, the policy's
`codingSessions` field) or listed in `FLOW_CODING_MCP_SERVERS`.

The runner bootstrap lists the session's servers (`mcpServers`), and the
runner adds each one to the harness config as a streamable-HTTP MCP server at
the gateway, authenticated with the sandbox token. For every request the
gateway checks the sandbox token (refused once the session or its sandbox
ends) and the policy again (refused as soon as an admin revokes it), then
opens the upstream connection with the stored credential
(`FLOW_MCP_CREDENTIALS`, or the session creator's connector authorization)
through the [trusted-endpoint policy](#trusted-private-endpoints). Each
`tools/call` is written to the workspace audit log as
`coding_session.mcp_tool_call` (server, tool, outcome, session, sandbox and
user; not the arguments or results). The gateway is stateless and offers no
server-to-client stream; elicitation is not available in sandboxes.

For the intranet example above, enable `mcp-sourcebot-production` for coding
sessions in the workspace, or set `FLOW_CODING_MCP_SERVERS=mcp-sourcebot-production`
(`coding.mcpServers: [mcp-sourcebot-production]`). Sessions then see a
`sourcebot` MCP server.

### Code hosts

**GitHub.** Coding sessions on GitHub use the GitHub App; its settings
(`FLOW_GITHUB_APP_*`) and registration are documented under
[GitHub App](#github-app).

**GitLab.** Coding sessions use the workspace's existing GitLab connection (access token or
OAuth). When coding sessions are enabled, the same GitLab webhook (see
[GitHub/GitLab review webhooks](#githubgitlab-review-webhooks)) also turns
Merge Request, Note and Pipeline events on session merge requests into session
follow-ups; enable those three triggers on the GitLab webhook. Flow opens the
draft merge request itself, so the sandbox token never needs to create one.

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_GITLAB_CREDENTIAL_MODE` | `project-token` | How sandboxes authenticate to GitLab: `project-token`, `oauth` or `connection` |
| `FLOW_CODING_GITLAB_TOKEN_SCOPES` | `read_repository,write_repository,read_api` | Scopes of the project access tokens Flow mints. Add `api` to let the agent reply with `glab mr note` |

- `project-token` (recommended): for each session Flow creates a project access
  token named `flow-cs-<sessionId>` (Developer role, the scopes above, expiring
  after at least one full day) and revokes it when the session ends. The
  orphan sweep (every `FLOW_CODING_GC_INTERVAL`) revokes any `flow-cs-*` token
  whose session has ended and `flow-sb-*` tokens of snapshot builds after 2
  hours. The connection token needs the `api` scope and the **Maintainer** role
  (or higher) on each project. On GitLab.com, project access tokens need the
  **Premium or Ultimate** tier; self-managed GitLab offers them on every tier.
  Without these, sessions fail with `noCodeAccess` and explain what is missing.
- `oauth`: sandboxes get the workspace's GitLab OAuth access token (about 2
  hours; Flow refreshes it with `FLOW_INTEGRATION_GITLAB_TOKEN_URL`). It is not
  limited to one project.
- `connection`: sandboxes get the connection token itself. It is long-lived and
  broad; Flow logs a warning at startup when this mode is on.

Inside the sandbox, `glab` is authenticated at runtime only, through
`GITLAB_TOKEN`, `GITLAB_HOST`, `GITLAB_API_PROTOCOL`, `GLAB_CONFIG_DIR` and
`NO_PROMPT` from the short-lived credential. For a self-managed host the runner
also writes `glab config set --global host <host>` (never the token). The agent
is told to use `glab mr view`, `glab mr diff`, `glab ci status`, `glab ci get`
and `glab ci trace` (and `glab mr note` when the scopes allow it), and never to
create, merge or approve merge requests.

### Triggers

Coding sessions start from a delegated issue, Flow Agent chat, an issue
comment, a Loop's "Start a coding session" action, a pull/merge request, or
"Fix this PR/MR" on the review page. These variables shape the triggers:

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_PR_MENTION` | `@flow` | Mention that turns a PR/MR comment into a follow-up. `@flow rebase`, `@flow fix lint`, `@flow add tests` and `@flow fix` pick the matching quick action; anything else is passed on as review feedback. A "changes requested" review on a Flow PR needs no mention |
| `FLOW_CODING_BRANCH_PREFIX` | `flow/` | Branches Flow owns and the agent may push (besides the PR head for PR follow-ups). A rebase (a force push) from a comment is allowed only on these; the review page asks for confirmation on other branches |

Issues from integrations (email intake, Slack, Intercom and other Asks, API
applications) and PR/MR comments by people who are not workspace members are
external. Unless their source is listed under Settings › Coding sessions ›
Trusted external sources (`slack`, `email`, `integration:<name>`,
`appUser:<id>`, or `github:<owner>` / `gitlab:<group>` for PR comments), the
session waits in `awaitingApproval` for up to `FLOW_CODING_APPROVAL_TIMEOUT`
(see [Phase timeouts and retries](#phase-timeouts-and-retries)) and the team's
members get an Inbox notification. Loops may start sessions only on the
repositories allowed by "Repository access for Loops", and their spend counts
against the Loop limit (and per-loop overrides) in Usage & limits.

### Browser testing and artifacts

Coding-session agents can start the app inside their sandbox, test it in headless Chromium, and attach before/after
screenshots and recordings to the session and to the draft PR/MR (`docs/coding-sessions-design.md` §17).

| Variable | Default | Meaning |
|---|---|---|
| `FLOW_CODING_BROWSER` | on | `off` removes the browser tools from the sandbox, drops the browser guidance and refuses artifact uploads (403) |
| `FLOW_CODING_ARTIFACT_MAX_BYTES` | `10485760` | Maximum size of one screenshot or recording. Larger uploads get 413; the runner retries a too-large PNG as JPEG |

Fixed limits: PNG and JPEG screenshots and WebM or MP4 recordings only (the type is detected from the file contents,
not the declared type); at most 20 artifacts per turn and 200 per session.

Storage and links:

- Artifacts are stored through `FLOW_STORAGE_DRIVER` (local or S3) as `coding-artifact_<session>_<artifact>.<ext>`.
- The web app reads them through `GET /api/coding/sessions/{id}/artifacts/{artifactId}/content`, which follows the
  visibility of the session's agent thread. The artifact listing also returns signed URLs valid for one hour.
- PR/MR bodies get a "Screenshots" section between `<!-- flow:screenshots:start -->` and
  `<!-- flow:screenshots:end -->` with signed `/api/files/signed/` URLs valid for 30 days. The section is rewritten,
  with fresh URLs, after every upload while the PR/MR is open. Signed URLs use `FLOW_FILE_URL_SIGNING_KEY` (or the
  generated key) and the public base URL `FLOW_APP_URL`. Anyone holding a link can view the image until it expires.

Inside the sandbox (set as environment variables on the coding environment, never as secrets) the agent's
`browser_start_app` tool uses:

| Variable | Meaning |
|---|---|
| `FLOW_BROWSER_APP_COMMAND` | Command that starts the dev server (default: the `dev`, `start`, `preview` or `serve` script of `package.json`, run with the package manager found from the lockfile) |
| `FLOW_BROWSER_APP_URL` | URL the app serves (default: the URL the server prints, or `http://127.0.0.1:$PORT`) |
| `FLOW_BROWSER_APP_CWD` | Directory to run the command in, relative to the repository |

A repository can instead commit `.flow/browser.json` (`{"command", "url", "cwd", "port", "readyPattern", "env"}`).
The image installs a pinned Playwright and Chromium under `/opt/flow/playwright` and `/opt/ms-playwright`
(`deploy/sandbox-image/install-browser.sh`).

## Compose profiles

The default Compose configuration uses SQLite and local files. Optional service
profiles are available for local infrastructure testing:

```bash
# PostgreSQL
FLOW_DATABASE_DRIVER=postgres \
FLOW_DATABASE_URL='postgres://flow:flow@postgres:5432/flow?sslmode=disable' \
docker compose --profile postgres up -d

# MySQL
FLOW_DATABASE_DRIVER=mysql \
FLOW_DATABASE_URL='mysql://flow:flow@mysql:3306/flow' \
docker compose --profile mysql up -d

# MinIO-backed S3
FLOW_STORAGE_DRIVER=s3 FLOW_S3_BUCKET=flow-uploads FLOW_S3_REGION=us-east-1 \
FLOW_S3_ENDPOINT=http://minio:9000 FLOW_S3_ACCESS_KEY_ID=flow \
FLOW_S3_SECRET_ACCESS_KEY=flow-development-secret FLOW_S3_PATH_STYLE=true \
docker compose --profile s3 up -d

# Standalone Redis coordination with PostgreSQL
FLOW_DATABASE_DRIVER=postgres \
FLOW_DATABASE_URL='postgres://flow:flow@postgres:5432/flow?sslmode=disable' \
FLOW_REDIS_MODE=standalone FLOW_REDIS_ADDRS=redis:6379 \
docker compose --profile postgres --profile redis up -d

# Three-node development Redis Cluster with PostgreSQL
FLOW_DATABASE_DRIVER=postgres \
FLOW_DATABASE_URL='postgres://flow:flow@postgres:5432/flow?sslmode=disable' \
FLOW_REDIS_MODE=cluster \
FLOW_REDIS_ADDRS='redis-cluster-1:6379,redis-cluster-2:6379,redis-cluster-3:6379' \
docker compose --profile postgres --profile redis-cluster up -d
```

The Compose cluster is a development sharding fixture with three primary
nodes and no replicas. Production clusters should add replicas across failure
domains and use TLS plus ACL credentials.

The app service uses `restart: unless-stopped`, so it retries while a selected
database service completes its first-time initialization.

The `coding` profile adds the egress proxy for coding sessions with the docker
provider. The override file mounts the Docker socket into the app, which gives
it root-equivalent access to the host, so use it only on a development machine:

```bash
FLOW_CODING_SECRET_KEY=$(openssl rand -base64 32) \
FLOW_CODING_EGRESS_ACL_SECRET=$(openssl rand -hex 24) \
DOCKER_GID=$(stat -c %g /var/run/docker.sock) \
docker compose -f docker-compose.yml -f deploy/egress-proxy/compose.coding.yml --profile coding up --build
```

## Settings Execution

### Workspace policy execution

Workspace security preferences are enforced by the API, including session authentication methods, second-factor requirements, guest Agent access and file types. Passkeys can satisfy the second-factor challenge. A positive authenticator ceremony still needs verification on each deployment's origin/RP configuration.

Database migration 4 adds per-session authentication context for existing installations. New password sessions write this context atomically; external callbacks replace it with the verified provider context before issuing a cookie. Legacy sessions with an unknown authentication method must sign in again when method restrictions apply. MFA checks also apply before an interactive OAuth grant is issued for a workspace.

`reviewThirdPartyApplications` gates incoming OAuth applications. An authorization attempt creates an approval request without issuing a code. Administrators review it in Security settings. Approval binds the client ID and its scopes; requesting additional scopes requires another decision. Rejecting or removing approval blocks existing access tokens and refresh tokens while the policy is enabled. A delegated API key needs the `admin` scope to approve applications.

`GET/PUT /api/application-policies` and `DELETE /api/application-policies/{id}` manage application decisions and external Agent MCP connectors. A connector has a server URL, owner, personal/workspace scope and pending/approved/rejected status. Personal connectors are available only to their owner. When `allowedMcpConnectors` is `approved`, discovery and execution require approval; rejected connectors are always blocked. Disabling `mcpConnectorsEnabled` also blocks tools from already-open Agent conversations.

External MCP connectors support Streamable HTTP initialization, JSON/SSE responses, paginated `tools/list` and `tools/call`. They require both `FLOW_AGENT_TOOLS_ENABLED` and `FLOW_AGENT_WRITE_TOOLS`. In chat, comment threads and delegated issues, Flow Agent runs connector tools without asking, and records each call (connector, tool, arguments and a truncated result) in the conversation or the issue's agent activity. Tool annotations such as `destructiveHint` do not change this. It asks first only when the connector is set to always ask: `"toolApproval": "always"` on the connector (**Ask before running tools** in its settings row; workspace admins for workspace connectors, owners for personal ones). That is the only approval Flow Agent asks for: its own writes, deletes and status updates included, run at once. Loops run connector tools unattended. Discovery is bounded to 30 seconds, 10 pages per server and 128 tools total. Every call checks current membership and workspace policy again before it runs, so a connector removed, rejected or revoked since discovery is refused. Redirects and non-public production endpoints are rejected, unless the operator trusts the host (see [Trusted private endpoints](#trusted-private-endpoints)).

For servers requiring bearer authentication, set `FLOW_MCP_CREDENTIALS` to a JSON object keyed by connector ID. Each value contains `url` and `token`. The URL must match the registered URL (ignoring a trailing slash). Alternatively, use the connector's advanced authentication headers or its Authorize action. Credentials are never returned by the policy API.

Remote OAuth discovers protected-resource and authorization-server metadata, binds PKCE S256 and resource audience, and supports client metadata documents, dynamic registration and pre-registered clients. `FLOW_MCP_OAUTH_CLIENTS` is a JSON object keyed by issuer URL, with `clientId` and optional `clientSecret` values for pre-registration. Callbacks are bound to the initiating browser session and current workspace policy. Refresh credentials are rotated under a connector-scoped lock, without blocking unrelated workspace mutations.

Set `FLOW_CONNECTOR_SECRET_KEY` to 32 random bytes encoded as base64 before using stored provider credentials or connector OAuth. Keep this key outside Git, stable across restarts, and identical across replicas. Database migration 5 stores authenticated encrypted credentials separately from workspace data, bootstrap, events and exports. Rotating this encryption key requires reauthorizing stored connectors; losing it makes existing encrypted credentials unreadable.

The official Go MCP SDK handles protocol negotiation and bidirectional messages. Both form and URL elicitation render in full-page and toolbar Agent conversations. Form responses validate against the requested schema; declining or canceling sends no form content. Submitted field values are not copied into conversation history by Flow. URL confirmations open separately, and the user explicitly continues or declines. Legacy HTTP+SSE transport is not supported; use Streamable HTTP.

The support privacy control omits sender identity from **new** email intake records, their persisted events and email Ask requesters. It does not redact freeform message bodies or rewrite historical data. Sensitive data protection blocks Agent requests, external MCP tools and integration deliveries, and strips business content from outgoing notification emails. These technical controls are not a HIPAA certification or a substitute for deployment-specific compliance measures.

### Scheduled workflows

Team stale-issue and closed-item archive sweeps run once per minute with up to 100 stale and 100 archive candidates per team. Production release completion rules run on the status transition, from either the API or a CI event, and do not repeat for a replayed completion. A pipeline-specific rule takes precedence over an all-production-pipelines rule. Invalid cross-team statuses or pipeline scopes are rejected when saving rules.

Pulse summaries run at 09:00 in the member's first team's configured timezone (UTC when unavailable); weekly summaries run on Mondays. Personal `daily`, `weekly` or `never` overrides the workspace schedule; `default` inherits it. Summaries count updates visible to the recipient and use the normal Inbox/email/desktop delivery preferences. A persisted per-recipient cursor prevents duplicates across scheduler restarts. New summary notifications are appended without rewriting existing notifications or issue records.

Asks email addresses must reference enabled, DNS-verified team intake addresses. Production verification reads the `_flow-intake` TXT record; sending the displayed verification string alone is insufficient. Forward received messages through the existing token-authenticated email intake endpoint. Matching addresses create a linked Ask and issue in the address's team. Message IDs deduplicate delivery retries. Flow does not run an SMTP receiving server; configure your mail service to forward inbound messages.

The integration directory offers GitHub, GitLab and Slack through deployment-configured applications and OAuth/webhooks. A configuration record alone does not mean authorization succeeded. Provider-owned marketplace agents and connectors without a Flow installation/authorization flow are not offered. Custom MCP servers remain available through the standard MCP connection flow.

## Repository Actions

GitHub and GitLab merge/close/reopen actions require an OAuth access token on
the configured connection or `FLOW_INTEGRATION_GITHUB_ACCESS_TOKEN` /
`FLOW_INTEGRATION_GITLAB_ACCESS_TOKEN` in the deployment. They return an error
when credentials are missing instead of changing only the local review state.
For self-hosted providers, set `FLOW_INTEGRATION_GITHUB_API_URL` (for example,
the enterprise `/api/v3` endpoint), `FLOW_INTEGRATION_GITLAB_API_URL`, or
`FLOW_INTEGRATION_GITLAB_HOST`. Workspace configuration cannot redirect these
deployment credentials to another API host. GitLab merge behavior remains
subject to the repository's merge policy, including asynchronous rebases.

## Row-Backed Issue Collections

Favorites, favorite folders, and resource subscriptions use metadata-only
transactions. Issue targets are authorized through indexed visibility checks.
Single-notification changes update the notification row directly. Browser
preference refreshes use `/api/resource-preferences`, preserving the loaded issue
and discussion caches instead of requesting another full bootstrap. Personal
favorite/subscription events are sent only to their owner; notification events
are restricted to the recipient.

Existing clients using `/api/issues` now send writes through the bounded issue
record implementation without changing their URLs. SSE connections and presence
heartbeats also use indexed entity visibility checks regardless of the web build
flag; they no longer retain a complete issue collection for each connection.
Legacy scalar patches keep their existing field-merge behavior, while document
snapshot versions remain checked.

The legacy `/api/bootstrap` response still includes complete collections. Issue
bodies and discussion records stream from SQL; a lightweight issue outline is
retained for visibility and reference projection. Bootstrap and export downloads
share one concurrency slot per API handler, with a five-minute request/write
deadline. Cancelled queued requests do not load the workspace. Each download
uses one consistent SQL read snapshot and releases it on completion or
cancellation. This reduces transient memory but does not turn the legacy client
into a paged client. Other compatibility workflows still require complete
collections.

For an app container limited to 1 GiB, `GOMEMLIMIT=650MiB` can leave headroom for
runtime overhead and filesystem cache. Compose passes this optional environment
variable through. It is a soft Go memory target, not a hard cap or an assurance
that arbitrary workspaces fit in 1 GiB; measure the actual workload and retain
the container memory limit.

The API stores issues in `issue_records`, with separate indexed label,
subscriber, permission, and activity references. Comments, activity events,
notifications, and notification deliveries use entity records instead of the
workspace JSON. Existing workspace collections migrate when the API opens the
database. Back up the database and stop older API instances before upgrading;
mixed old/new binaries are not supported. Rollback requires restoring the backup.

The bounded API is `/api/issue-records`, including `/groups`, `/bootstrap`,
`/{id}/context`, and the issue mutation subroutes. Cursors are tied to their
query and access scope. Clients must not reuse a cursor after changing filters.
Group counts describe the entire matching collection, independently of pages.
Project issue tabs and milestone filters use the same paged list/board. The
`/api/issue-records/project-summary?projectId=...` endpoint returns server-side
totals for milestones, assignees, labels, and project progress; these totals
must not be computed from the browser's retained pages.

Optional issue properties use sparse indexed rows, including milestone,
estimate, due date, completion dates, template, and external source. Existing
records are backfilled in checkpointed batches of 250 on upgrade. Allow this
migration to finish before sending traffic to the upgraded instance.

Set `VITE_PAGED_ISSUES=true` when building the web client to opt into the new
issue list and board path. Docker builds accept
`--build-arg VITE_PAGED_ISSUES=true`. The default remains `false` during rollout.
The browser retains at most 40 pages and an estimated 16 MiB of issue records,
and reloads evicted pages using their cursors. Request `projection=list` to omit
description and document state; these results have `isSummary: true` and must
not replace full detail content. Pages also have a 4 MiB payload budget, except
that one oversized record can be returned to ensure forward progress. Follow
`nextCursor` rather than assuming every page contains the requested row count.
Cursor checkpoints do not serialize workspace snapshots into localStorage.

Issue context accepts independent `commentsCursor` and `activitiesCursor`
parameters. Omit a cursor to start from the latest page; use the corresponding
cursor in the response for older history, or `-` to skip an exhausted stream.
Each stream returns at most 100 entries and approximately 1 MiB, with the same
single-record exception. MCP issue/document/milestone comment lists use opaque
SQL cursors and return at most 100 entries even if a higher limit is requested.

Search indexes are backfilled in checkpointed batches during startup. SQLite
uses FTS5 trigram indexing, MySQL uses its `ngram` full-text parser, and
PostgreSQL uses `pg_trgm`. For managed PostgreSQL, have a database administrator
install `pg_trgm` before upgrading if the app role cannot create extensions.
MySQL must provide the `ngram` parser. Search results use a bounded candidate
set; candidate-derived facets and semantic ranking are not exhaustive global
counts. Short terms can still require scans.

JSON requests have a 16 MiB body limit. Realtime replay has a 256 KiB per-event
limit, an 8 MiB per-workspace budget, and a 64 MiB process-wide budget; oversized
events trigger resynchronization. Each collaboration socket has an 8 MiB
outgoing queue budget and disconnects slow consumers. The browser event backlog
is bounded to 256 events and approximately 4 MiB before requesting a resync.
Offline editor changes remain in the Yjs document; after 128 queued updates or
1 MiB, the provider derives a new delta after acknowledgements instead of
retaining another growing edit log. This does not impose a document-size cap
or provide durable offline storage; closing an unsaved editor can lose changes.

Imports reject more than 5,000 rows while parsing and run at most two background
jobs per API process. Agent output is bounded to 8 MiB and 1,024 parts; provider
history uses at most the latest 32 messages and approximately 8 MiB.

This is a staged migration, not a claim that every Flow module has completed
large-workspace support. Projects and several other modules still use complete
metadata collections; some advanced issue filters are not implemented in the
SQL compiler and return an explicit query error. Compatibility APIs can still
materialize complete collections. Keep the new UI mode limited to validated
workflows until those remaining paths migrate. Synchronous issue graph mutations
are bounded to 1,000 records; larger graph operations require batch processing.
