# Deployment configuration

Flow uses environment variables for infrastructure and authentication choices.
Configuration is validated before migrations or the HTTP listener start. A
selected backend with missing required values causes a clear startup error.
Secrets may be supplied as `VARIABLE_FILE=/run/secrets/name`; the file value
takes precedence over `VARIABLE`.

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
| `FLOW_DATABASE_MAX_IDLE_CONNS` | driver default | `0` for SQLite, `5` otherwise. |
| `FLOW_DATABASE_CONN_MAX_LIFETIME` | `30m` | Go duration for pooled connections. |
| `FLOW_SEED_PROFILE` | `none` | Fresh deployments use `none` and ask the first user to create a workspace. Set `zentao-demo` only for an intentional demo dataset. |

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

## Flow Agent

Flow Agent uses an OpenAI-compatible Chat Completions endpoint. The browser
sends selected issue IDs to Flow; the API loads the authorized workspace issues
and builds the model context server-side. Provider credentials are never sent to
the browser.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_AGENT_ENABLED` | `false` | Enable Agent chat requests. |
| `FLOW_AGENT_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible API base URL. |
| `FLOW_AGENT_API_KEY` | empty | Provider bearer token; `_FILE` is supported. |
| `FLOW_AGENT_MODEL` | `gpt-5-mini` | Provider model identifier. |
| `FLOW_AGENT_TIMEOUT` | `60s` | Per-request Go timeout. |

```dotenv
FLOW_AGENT_ENABLED=true
FLOW_AGENT_BASE_URL=https://api.example.com/v1
FLOW_AGENT_API_KEY=secret
FLOW_AGENT_MODEL=your-model
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
