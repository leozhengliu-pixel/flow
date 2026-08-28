<div align="center">

# Flow

**A self-hosted workspace for planning, issue tracking, and team collaboration.**

[![CI](https://github.com/leozhengliu-pixel/flow/actions/workflows/ci.yml/badge.svg)](https://github.com/leozhengliu-pixel/flow/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![React](https://img.shields.io/badge/React-19-149eca.svg)](web/package.json)
[![Go](https://img.shields.io/badge/Go-1.26-00add8.svg)](api/go.mod)

[English](README.md) | [简体中文](README.zh-CN.md)

</div>

Flow brings issues, projects, cycles, initiatives, documents, customer requests,
and workspace administration into one focused application. It ships as a React
web client backed by a Go API. SQLite and local files require no external
services for development; PostgreSQL, MySQL, S3, and Redis coordination are configurable for production.

> [!IMPORTANT]
> Flow is under active development. Review the security settings and replace all
> development credentials before exposing an instance to the internet.

## Highlights

- **Issue workflow**: create, edit, archive, relate, subscribe, comment, attach files, and update in bulk.
- **Planning**: projects, project updates, milestones, initiatives, cycles, saved views, and team workflows.
- **Collaboration**: activity timelines, notifications, presence, full-text search, drafts, and rich-text documents.
- **Workspace administration**: account lifecycle, invitations, roles, teams, labels, templates, imports, and exports.
- **Customer context**: customers, requests, releases, asks, subscriptions, and SLA rules.
- **Internationalization**: English and Simplified Chinese interfaces with a persistent language preference.
- **Horizontal scaling**: Redis standalone/cluster coordination for shared limits, realtime events, presence, and serialized workspace writes.

## Architecture

```text
Browser
  |
  | HTTP + Server-Sent Events
  v
React 19 / TypeScript / Vite
  |
  | /api proxy in development
  v
Go HTTP API
  |-- SQLite, PostgreSQL, or MySQL
  |-- Redis standalone or cluster coordination (optional)
  |-- Local or S3-compatible object storage
  |-- Email, Google OAuth, OIDC, and SAML authentication
  |-- OpenTelemetry OTLP export (optional)
  `-- SMTP delivery (optional)
```

The API keeps workspace and team authorization at the request boundary. State
changes are persisted with domain events, while the web client uses optimistic
updates and real-time invalidation to keep active sessions synchronized.

## Repository Layout

```text
.
|-- api/                 Go API, domain model, SQL/object stores, and tests
|-- docs/                Product, routing, and module documentation
|-- web/                 React application and design system
|-- .github/             CI, dependency updates, and contribution templates
|-- CONTRIBUTING.md      Development and pull request workflow
|-- SECURITY.md          Vulnerability reporting policy
`-- LICENSE              Apache License 2.0
```

## Quick Start

### Prerequisites

- Go `1.26.3` or the version declared in [`api/go.mod`](api/go.mod)
- Node.js `24.19+` LTS (the production baseline declared in [`.nvmrc`](.nvmrc))
- npm `10.9.2`

CI also runs the complete web validation suite on Node.js 26 as a forward-
compatibility check. Node.js 24 remains the supported production baseline until
the newer release line reaches LTS and is promoted deliberately.

### 1. Clone the repository

```bash
git clone https://github.com/leozhengliu-pixel/flow.git
cd flow
```

### 2. Start the API

```bash
cd api
go run ./cmd/server
```

The API listens on `http://127.0.0.1:8080`. Its health endpoint is
`http://127.0.0.1:8080/api/health`.

### 3. Start the web client

In a second terminal:

```bash
cd web
npm ci
npm run dev
```

Open `http://127.0.0.1:5173`.

### Docker

For a local Docker run, start the single-container Compose service. The
multi-stage Dockerfile builds the frontend and API for the selected target
architecture:

```bash
docker compose up -d --build
```

Open `http://127.0.0.1:5173`. The API health endpoint is also exposed at
`http://127.0.0.1:8080/api/health`.

Every push to `main` also publishes a multi-architecture image to the public
GitHub Container Registry package:

```bash
docker pull ghcr.io/leozhengliu-pixel/flow:latest
```

The workflow builds `linux/amd64`, `linux/arm64`, `linux/arm/v7`,
`linux/ppc64le`, and `linux/s390x`, then verifies that the `latest` package can
be inspected without registry credentials.

### Development seed (optional)

Fresh deployments start without a workspace and open the workspace creation
flow. To load the local demo dataset instead, set `FLOW_SEED_PROFILE=zentao-demo`
before the first API start. The demo account is intended only for development:

```text
Email:    leo.zheng.liu@example.com
Password: flow-demo
```

Set `FLOW_SEED_PASSWORD` before the first API start to use a different seed
password. Never deploy the demo profile with the default credential.

## Configuration

Flow reads configuration from environment variables passed to the API process.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_DATABASE_DRIVER` | `sqlite` | `sqlite`, `postgres`, or `mysql`. |
| `FLOW_DATABASE_PATH` | `data/flow.db` | SQLite database path. |
| `FLOW_DATABASE_URL` | unset | PostgreSQL/MySQL connection URL. |
| `FLOW_SEED_PROFILE` | `none` | `none` for first-run workspace onboarding, `zentao-demo` for optional demo data, or `base` for the small legacy seed. |
| `FLOW_REDIS_MODE` | `disabled` | `disabled`, `standalone`, or `cluster`; PostgreSQL/MySQL is required when enabled. |
| `FLOW_REDIS_URL` | unset | Redis connection URL; alternatively use `FLOW_REDIS_ADDRS`. |
| `FLOW_STORAGE_DRIVER` | `local` | `local` or `s3`. |
| `FLOW_STORAGE_LOCAL_PATH` | `data/uploads` | Local attachment storage directory. |
| `FLOW_SEED_PASSWORD` | `flow-demo` | Password for the optional seeded demo account. |
| `FLOW_APP_URL` | unset | Public web origin used in account emails. |
| `FLOW_SMTP_HOST` | unset | SMTP server hostname. |
| `FLOW_SMTP_PORT` | `587` | SMTP server port. |
| `FLOW_SMTP_USERNAME` | unset | SMTP authentication username. |
| `FLOW_SMTP_PASSWORD` | unset | SMTP authentication password. |
| `FLOW_SMTP_FROM` | unset | Sender address for account emails. |
| `FLOW_COOKIE_SECURE` | `false` | Force secure authentication cookies. Set to `true` in production. |
| `FLOW_DEV_AUTH_TOKENS` | `true` | Include account action tokens in development responses. Disable in production. |
| `FLOW_TRUST_PROXY_HEADERS` | `false` | Trust forwarded client information from a controlled reverse proxy. |

Example production-oriented environment:

```bash
export FLOW_APP_URL=https://flow.example
export FLOW_DATABASE_DRIVER=postgres
export FLOW_DATABASE_URL='postgres://flow:secret@db:5432/flow?sslmode=require'
export FLOW_STORAGE_DRIVER=s3
export FLOW_S3_BUCKET=flow-uploads
export FLOW_S3_REGION=us-east-1
export FLOW_SMTP_HOST=smtp.example.com
export FLOW_SMTP_PORT=587
export FLOW_SMTP_USERNAME=apikey
export FLOW_SMTP_PASSWORD=change-me
export FLOW_SMTP_FROM=notifications@flow.example
export FLOW_COOKIE_SECURE=true
export FLOW_DEV_AUTH_TOKENS=false
```

Enable `FLOW_TRUST_PROXY_HEADERS` only when a trusted proxy overwrites incoming
forwarded headers.

See [Deployment configuration](docs/configuration.md) and [`.env.example`](.env.example)
for PostgreSQL/MySQL, S3/MinIO, Google OAuth, enterprise OIDC, SAML, secret-file,
connection-pool, and OpenTelemetry settings.

## Quality Checks

Run the same checks used by CI before opening a pull request:

```bash
cd web
npm ci
npm run lint
npm run build

cd ../api
go test ./...
```

The default Web job runs on Node.js 24 LTS. A separate required compatibility
job repeats install, lint, and build on Node.js 26.

## Documentation

- [Product modules](docs/product-modules.md)
- [Delivery roadmap](docs/delivery-roadmap.md)
- [Domain model](docs/domain-model.md)
- [Routing system](docs/routing-system.md)
- [Issue modules](docs/issue-page-modules.md)
- [Project modules](docs/projects-page-modules.md)
- [Workspace modules](docs/workspace-modules.md)

## Community

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before proposing a change.
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md) in all project spaces.
- Use [GitHub Issues](https://github.com/leozhengliu-pixel/flow/issues) for reproducible bugs and focused feature proposals.
- Report vulnerabilities through the process in [SECURITY.md](SECURITY.md), not through a public issue.
- See [SUPPORT.md](SUPPORT.md) for usage questions and troubleshooting.

Project decisions and maintainer responsibilities are documented in
[GOVERNANCE.md](GOVERNANCE.md). User-visible changes are recorded in
[CHANGELOG.md](CHANGELOG.md).

## License

Licensed under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for
attribution information.

Flow takes product interaction inspiration from Linear and is an independent, unaffiliated project.
