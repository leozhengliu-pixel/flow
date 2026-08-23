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
| `FLOW_AUTH_SAML_ENABLED` | `false` | Enable SAML 2.0 SP endpoints. |
| `FLOW_SAML_METADATA_URL` | empty | IdP metadata URL; alternatively use metadata XML. |
| `FLOW_SAML_METADATA_XML` | empty | Inline/file IdP metadata. |
| `FLOW_SAML_ENTITY_ID` | app URL | Service provider Entity ID. |
| `FLOW_SAML_ACS_URL` | derived | Assertion Consumer Service URL. |
| `FLOW_SAML_SP_PRIVATE_KEY` | empty | Required PEM RSA key. Prefer `_FILE`. |
| `FLOW_SAML_SP_CERTIFICATE` | empty | Required PEM SP certificate. Prefer `_FILE`. |
| `FLOW_SAML_DISPLAY_NAME` | `SAML` | Login button label. |

Google and generic OIDC use authorization code, state, nonce, PKCE, discovery,
and signed ID-token verification. SAML assertions are validated against IdP
metadata and mapped from standard email/name attributes.

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
```

The app service uses `restart: unless-stopped`, so it retries while a selected
database service completes its first-time initialization.
