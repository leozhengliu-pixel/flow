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

## Redis coordination and cluster mode

Redis is optional for a single Flow process and disabled by default. Enable it
when running multiple API replicas. Flow uses Redis for shared authentication
rate limits, cross-instance realtime events, shared presence, and distributed
workspace write locks. A replica reloads the latest workspace JSON from SQL
inside the distributed lock before applying a mutation, preventing one process
from overwriting another process's update.

Redis does not replace the primary database. Multi-instance mode requires
PostgreSQL or MySQL; startup rejects Redis with SQLite because a local SQLite
file is not a horizontally scalable source of truth.

Flow serializes mutations per workspace and stores one bounded workspace
aggregate. This keeps single-instance deployment simple and guarantees atomic
domain updates, but it is intended for small and medium workspaces rather than
unbounded event ingestion. Monitor database row size and mutation latency before
raising `FLOW_WORKSPACE_STATE_MAX_BYTES`.

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
| `FLOW_S3_VALIDATE_ON_START` | `true` | Fail startup when the bucket or credentials cannot be accessed. |

The standard `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
`AWS_REGION`, `AWS_S3_BUCKET_NAME`, and `AWS_S3_ENDPOINT_URL` names are also
accepted. Flow-specific names take precedence.

## Authentication

Email/password remains enabled by default. Login buttons are obtained from
`GET /api/auth/providers`; disabled providers are not rendered. New identities
can be provisioned into the default workspace, optionally restricted by email
domain.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_AUTH_EMAIL_ENABLED` | `true` | Enable registration/password login and reset. |
| `FLOW_AUTH_AUTO_PROVISION` | `true` | Create users and default membership after trusted SSO. |
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
| `FLOW_AGENT_TIMEOUT` | `60s` | Per-request Go timeout. |
| `FLOW_AGENT_MAX_OUTPUT_TOKENS` | `4096` | Maximum output tokens per provider turn. |
| `FLOW_AGENT_ANTHROPIC_VERSION` | `2023-06-01` | Anthropic API version header. |
| `FLOW_AGENT_TOOLS_ENABLED` | `true` | Expose Flow MCP read tools to the Agent. |
| `FLOW_AGENT_WRITE_TOOLS` | `false` | Also expose write tools. Enable only when automatic mutations are acceptable. |

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
