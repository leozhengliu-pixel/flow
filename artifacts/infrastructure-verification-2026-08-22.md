# Configurable infrastructure verification

## Database

The same Flow API binary was started against disposable official images:

| Driver | Image | Migration | API write | Restart persistence |
| --- | --- | --- | --- | --- |
| SQLite | embedded `modernc.org/sqlite` | Pass | Pass | Existing automated suite |
| PostgreSQL | `postgres:17-alpine` | Pass | Created `CLE-34` | Pass, 6 issues after restart |
| MySQL | `mysql:8.4` | Pass | Created `CLE-34` | Pass, 6 issues after restart |

Both server databases contained one `workspace_states` row and two seeded
`auth_users` rows. The temporary containers were removed after verification.

## Object storage

Flow was started with `FLOW_STORAGE_DRIVER=s3` against a private disposable
`minio/minio:latest` bucket using path-style requests. `README.md` was uploaded
through `POST /api/issues/issue_33/attachments`, streamed back through the
authorized `/uploads/:key` route, and deleted through the attachment API.

Source and downloaded SHA-256 were identical:

```text
344b82a2df77cce04bf73936b3fac06c750d69faaf2fe4056c6a34d6c21f6119
```

The bucket contained no object after the API delete. The temporary MinIO
container was removed after verification.

## Authentication

- Default provider response: email enabled, no external providers.
- Google-only configuration: email disabled and Google returned from
  `/api/auth/providers`.
- `/api/auth/google/start` returned Google discovery endpoints with state,
  nonce, and S256 PKCE challenge.
- Password login returned `404` while email authentication was disabled.
- SAML middleware is initialized in tests from generated SP credentials and
  IdP metadata; metadata and ACS paths are asserted.

## Telemetry and fail-fast startup

The production image was executed with intentionally incomplete selections.
Each exited before binding the HTTP listener with a specific error:

- PostgreSQL without `FLOW_DATABASE_URL`.
- S3 without bucket/region.
- All authentication providers disabled.
- Telemetry enabled without an OTLP endpoint.

## Container

The final static image contains the CA trust bundle required by PostgreSQL TLS,
MySQL TLS, S3, OIDC discovery, SAML metadata, and OTLP HTTPS endpoints. The image
started with default SQLite/local configuration; `/api/health` and `/` both
returned HTTP 200. Default and optional Compose profiles passed `docker compose
config --quiet`.
