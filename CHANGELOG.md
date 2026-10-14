# Changelog

All notable changes to Flow will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-10-14

### Added

- Flow Agent on every surface: side panel, floating chat
  in documents (quote chips, "Use document as context", chat history), agent
  conversations in project and document comment threads, delegation, streamed
  questions and option pills; writes apply immediately and stay inside the
  issues a chat has seen.
- Agent runtime: run queue, run tokens and an internal runtime API, with a
  worker on the OpenAI Agents SDK (`agent-worker/`).
- Third-party application agents and OAuth apps: install and re-consent
  flow, team access ("All public teams and select other
  teams…"), realtime webhooks, and a public admin API for apps and their
  credentials.
- Webhooks and API platform pages; `@flow/sdk`, the TypeScript SDK for the
  Flow API.
- Selection toolbar on every rich text surface (create issue from selection,
  ask agent, comment) and an "Issue created" toast with quick actions.
- Mermaid diagrams: a code block shown as the diagram, with source, error
  console and fullscreen views.
- Reworked Releases, Documents, Customer requests, Triage Intelligence,
  Insights, project statuses and templates, label settings and initiative
  hierarchy.
- Members page with Invited, Application and Inactive / Left workspace
  sections; redesigned transactional and invitation emails.
- New workspaces start with Getting started tutorial issues; product-demo
  feature tour clips in `docs/media/`.
- Team membership changes are audited with where each membership came from.
- Typed public contract for webhook management and deliveries, documents,
  project milestones, project and initiative updates, deletes and single-record
  reads (`GET /api/teams/{id}`, users, workflow states, labels, cycles,
  projects, initiatives, customers); JSON Schemas of every webhook payload in
  the OpenAPI `webhooks` section; `createAsUser` / `displayIconUrl` on issue,
  comment, document and link attachment inputs.
- `GET /api/issue-records` pages backward (`last` / `before`) and answers with a
  connection (`totalCount`, `pageInfo`) when asked with first / last / after /
  before / orderBy / `connection=1`; list routes with two response shapes
  document both and accept `connection=1`.
- `@flow/sdk` 0.3.0: `fetchPrevious()` for issues, generated webhook event
  types, writes for documents, milestones and updates, single-record getters.

### Changed

- Member and admin roles: admin pages open as "Not found" for
  members, read-only settings explain themselves on hover, invite entries and
  ⌘K commands follow the workspace's who-can settings, and nobody can leave
  their last active team (409 "You should be a member of at least one active
  team").
- Private teams: admins see and manage them in settings, but their content
  needs membership.
- Projects and initiatives get stable unique URLs.
- Deletes: permanent customers and loops, Copy/Restore-only
  trash rows, application uninstall.

### Fixed

- A stale cached issue could be served after an update.
- The saved language no longer resets to the browser language after logout.
- Markdown tables written through the API/MCP become editor tables;
  `save_project` no longer reports success for a state or date it didn't
  apply.
- Plain-text parts of emails were empty.
- Team routes refused a team key (`/api/teams/ENG/...`) in permission checks.

### Security

- A browser-session request without a workspace could read the most recently
  written workspace; it is now scoped to the viewer's own workspace.
- Attach-from-URL can no longer reach internal addresses.

## [0.2.0] - 2026-10-04

### Added

- Recurring issues: the recurring issue is the first
  instance, the next one is created the day after its due date passes (team
  time zone), sub-issues and icon are recreated, and Team settings has a full
  New recurring issue page with an indexed, paged-safe list.
- Loops (scheduled and issue-triggered agent workflows) with versions, run
  replies with skills and attachments, and an audit log API.
- Flow Agent: streaming conversations with OpenAI Responses, Anthropic
  Messages and Chat Completions providers, Flow tool execution, replies that
  survive page reloads, and a stop control.
- AI Triage Intelligence and a dedicated triage issue page.
- Paged issue storage for large workspaces, Redis hot cache, and server-side
  grouping and counts.
- Project detail: resources with menus and brand icons, rich project
  updates, milestones, progress graph in paged mode, document reminders.
- Members, teams, projects and views list pages, settings and sub-teams,
  SCIM, OAuth applications, MCP OAuth workflows, integrations (GitHub, Slack,
  Jira, Figma, Sentry) and webhooks.
- Simplified Chinese coverage for settings.

### Changed

- Workspace writes cost proportional to the change instead of the workspace
  metadata size; reads never wait on a write's database time.
- MySQL connections default to `interpolateParams=true` (add
  `interpolateParams=false` to the DSN to opt out).
- Pulse summaries are scheduled in one write per tick.
- Slow workspace writes are logged with stage timings
  (`FLOW_SLOW_MUTATION_MS`, default 500).

### Fixed

- Member removal, team deletion, project changes and release edits that took
  tens of seconds (or ran out of memory) on large MySQL workspaces.
- MCP OAuth tokens accumulating as API keys; `save_team` demoting owners;
  theme reverting during slow settings saves.

## [0.1.0] - 2026-08-30

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

## 2026-08-17

### Added

- Initial Flow application with issue, project, initiative, cycle, document,
  customer, notification, search, authentication, and workspace management modules.
- React and TypeScript web client with English and Simplified Chinese support.
- Go API with SQLite persistence, local attachments, domain events, and real-time updates.
