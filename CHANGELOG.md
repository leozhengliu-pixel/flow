# Changelog

All notable changes to Flow will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project intends to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
after the first tagged release.

## [Unreleased]

### Fixed

- Read authentication values from the submitted form so Chrome-autofilled credentials are handled reliably.
- Disable development authentication tokens by default.
- Remove workspace-specific data transformations and product defaults.
- Upgrade the Go runtime and XML signature dependency to patched releases.

### Added

- Releases, Asks, team archive, and audit log workspace surfaces.
- Professional open source project documentation and community health files.
- Apache License 2.0 licensing and attribution notice.
- Continuous integration for the React and Go applications.
- Automated dependency update configuration.
- Frontend unit coverage, browser end-to-end tests, Go race/coverage gates, CodeQL, dependency auditing, and secret scanning.
- Versioned database migrations and PostgreSQL, MySQL, Redis, and S3 integration tests.
- Non-root, read-only container runtime with application health checks.
- Streaming Agent conversations with OpenAI Responses, Anthropic Messages, Chat Completions compatibility, Flow tool execution, reasoning/tool activity UI, and cancellation.

## 2026-08-17

### Added

- Initial Flow application with issue, project, initiative, cycle, document,
  customer, notification, search, authentication, and workspace management modules.
- React and TypeScript web client with English and Simplified Chinese support.
- Go API with SQLite persistence, local attachments, domain events, and real-time updates.
