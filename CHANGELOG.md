# Changelog

All notable changes to Flow will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The MCP attachment tools (`prepare_attachment_upload`,
  `create_attachment_from_upload`, `create_attachment`, `save_attachment`)
  attach files and links to a document as well as an issue: pass exactly one
  of `issue` or `document` (a document you can edit). Document files are
  recorded on the document and served to everyone who can see it; the body
  is left alone unless `insert: "append"` adds the file at the end. Every
  result includes a ready `markdown` snippet (`![title](url)` for images,
  `[title](url)` otherwise) to place with `save_document`, and
  `delete_attachment` also removes a document's attachment. On issues,
  `insert: "none"` keeps a file out of the description (the default still
  embeds it).
- `save_document` content turns a line that holds only an image
  (`![alt](url)`), a video (`::video[title](url)`) or a file card
  (`::file[title](url)`) into that block in the editor.
- `FLOW_CHAT_PLATFORM=lark|feishu` (Helm `integrations.chat.platform`) makes
  Lark or Feishu the deployment's first-class chat platform. The team home
  shows **Connect Lark group** (飞书 in Chinese for Feishu), the team's
  notifications page lists and binds its groups, project menus and the
  project details sidebar open the project's groups, project and initiative
  update settings, the sidebar and directory team menus, onboarding and the
  integration catalog point to it. The default (`slack`) is unchanged.
- Private teams can be shared with a Lark or Feishu group: the team's owner
  authorizes a binding to one internal group, choosing the notification
  types, project and whether the group may create or update issues. The
  binding adds no team membership and no Flow access for anyone; updates go
  to that group only, group actions use each member's own Flow permissions,
  and it stops once its authorizer can no longer administer the team. Team
  owners bind their own teams (public ones too) from the team's
  notifications page; only they and workspace administrators can change,
  revoke or remove a team's bindings. Every binding records who authorized
  it and when, **Revoke** keeps it listed as revoked, and binding changes are
  audited.
- Lark and Feishu groups can receive initiative updates of the initiatives
  their team leads.
- Personal Lark and Feishu notifications: a member who linked their account
  turns them on in **Personal > Notifications**, chooses the notification
  types as for email and desktop, and receives them as a direct message from
  the app's bot. Messages are sent only while the integration is healthy and
  for content the member can still open; unlinking or leaving the workspace
  stops them, and an account outside the app's availability range is shown
  as such on the notifications row.
- With `FLOW_AGENT_RUNTIME=worker`, Flow Agent chat replies run on the agent
  worker (`agent-worker`) instead of inside the API. The reply is the same
  chat message as before: saved while it is written (a reload or another tab
  shows it streaming), with its tool calls, results and change cards, and
  saved in its final form when the run ends. Tools set to always ask wait on
  the reply for approval and the run continues once every call is answered;
  questions with options end the reply as before; Stop ends the run, a
  connector question waiting in it included. The reply keeps the tool budget
  and the check that reported outcomes come from real tool results. A reply
  whose worker stopped responding is restarted when nothing was written, and
  otherwise shown as interrupted with Retry. The default
  (`FLOW_AGENT_RUNTIME=inprocess`) is unchanged.
- Chat replies on the agent worker no longer wait forever: a reply no worker
  takes within `FLOW_AGENT_WORKER_CLAIM_TIMEOUT` (default 2 minutes) ends with
  "No agent worker is available; try again or contact your admin" and its run
  is cancelled. Calls waiting for approval are rejected after 30 minutes and
  the reply goes on, as in-process, and the step limit
  (`FLOW_AGENT_MAX_TOOL_TURNS`) counts the whole reply, also after approvals.
- Chats with sent files run on the agent worker too
  (`FLOW_AGENT_RUNTIME=worker`): images reach the model as image inputs
  (read from storage when the run starts, with the same size and type checks),
  other files as the note the model opens them by, and `read_file` /
  `transcribe_media` work on the conversation's own files as in-process. The
  worker also starts with the tool groups of the chat's surface (an issue,
  project or document chat), as in-process.
- Loop builder conversations run on the agent worker too
  (`FLOW_AGENT_RUNTIME=worker`): the draft and its files reach the model, the
  chip questions show on the reply and wait for the answer (Stop cancels
  them), the words before a question stay in the reply, and `save_loop` rows
  read "Updated workflow definition draft" or "Created automation" as before.
- The workspace Invite dialog (Settings → Administration → Members → Invite)
  has an **Add to team** field for every role. Pick one or more teams (a
  searchable multi-select; optional for Members and Admins, required for
  Guests) and the invitee joins them when they accept. The invitation API
  refuses teams the inviter could not add people to directly (a private team
  they are not on, or one whose member management is closed to them) with 403;
  workspace admins may use any team.
- Security response headers on every response (pages, static files, API,
  health, errors, redirects, downloads, streams): `X-Content-Type-Options`,
  `Referrer-Policy`, a `Permissions-Policy` that keeps only what the web app
  uses, `frame-ancestors` with `X-Frame-Options`, HSTS when HTTPS is
  confirmed (TLS, or `X-Forwarded-Proto` from a proxy trusted with
  `FLOW_TRUST_PROXY_HEADERS`), and a Content Security Policy that starts in
  report-only mode (`FLOW_SECURITY_CSP=enforce` to block). Each part has its
  own `FLOW_SECURITY_*` setting and `FLOW_SECURITY_HEADERS_SKIP` leaves
  headers to a gateway; Helm `securityHeaders` and `trustProxyHeaders`. See
  [Security response headers](docs/configuration.md#security-response-headers).
- Agent sessions can be archived as a whole: **Archive session…** in a
  session's "…" menu (and `POST /api/agent-tasks/{id}/archive`) hides the
  session and all of its messages, people's replies included, from the
  activity for everyone, live and after a reload. A running session is stopped
  first and late activity cannot bring it back; its log, audit entry, reports,
  attachments, pull requests and linked issues are kept. Only the person who
  started it or a workspace admin can archive it, and archiving twice changes
  nothing. MCP adds `list_agent_sessions`, `get_agent_session` and
  `archive_agent_session`. See [Application agents](docs/application-agents.md#archive-a-session).
- SCIM provisioning: ETags (`If-Match`/`If-None-Match`), `sortBy`/`sortOrder`,
  `POST /Bulk`, `POST /Users/.search` and `/Groups/.search`, the enterprise
  User extension and profile attributes, audit log entries for every
  provisioning change, a "When a user is deprovisioned" setting (suspend or
  remove from the workspace), team group mapping on the identity provider
  page, and "Managed by SCIM" members whose role, name, email and status
  change only in the identity provider.
- Settings › Account › Connected accounts: Google Calendar (members show as
  out of office from their calendar's out-of-office events) and Notion (link
  previews of issues and projects) connections, configured with
  `FLOW_GOOGLE_OAUTH_CLIENT_ID` / `_SECRET` and `FLOW_NOTION_CONNECTION_URL`.
- `POST /api/workspace/documents/{id}/revisions/import` (workspace admins):
  adds a preserved earlier body of a document to its version history without
  changing the document. See [Operations](docs/operations.md#re-adding-a-lost-document-version).
- Coding sessions: Pi as a fourth agent (harness `pi`, pinned 1.4.0 in the
  sandbox image), using the OpenAI-compatible upstream with its own model list
  `FLOW_CODING_MODELS_PI` (Helm `coding.llm.models.pi`, empty by default) and
  `contextWindow` from `FLOW_CODING_MODEL_INFO`.

### Changed

- `prepare_attachment_upload` returns the file's stable URL as `assetUrl`
  (`…/uploads/asset_…`) and its path as `url`, next to the one-time
  `uploadUrl` for the `PUT`. Before, `assetUrl` repeated the upload URL.
  `create_attachment_from_upload` accepts either value, so clients that pass
  the old `assetUrl` keep working; clients that sent the `PUT` to `assetUrl`
  must use `uploadUrl`.
- Record ids Flow generates have the shape
  `<prefix><19 digits>_<10 characters>` (a zero-padded creation time in
  nanoseconds and a random part) instead of `<prefix><digits>`, so ids made
  at the same instant on several servers no longer collide. Ids of one kind
  still sort by creation time; existing ids keep their shape. Integrations
  that match ids against a pattern such as `prefix_\d+$` must treat them as
  opaque strings; see [Record ids](docs/api.md#record-ids). The
  webhook-disabled notification, a document's baseline version and a reset
  custom email intake address no longer reuse a value built from the clock
  second or the document's save time.
- On a Lark or Feishu deployment (`FLOW_CHAT_PLATFORM`), the remaining
  chat platform copy names it instead of Slack (飞书 in Chinese for
  Feishu): the read-only notice of a team's notifications, the initiative
  updates description, settings search and the legacy integrations list.
  The custom view menu's Slack-only notifications item shows on Slack
  deployments only.
- Lark and Feishu tenant authorization is automatic. The tenant administrator
  approves it on the chat platform by approving and publishing the app
  version in the admin console; once the App ID and App Secret obtain a
  tenant token and the bot's info is readable, Flow marks the tenant
  authorized (recording when, the tenant key and the administrator who
  configured the connection). Nobody signs in for it in Flow and there is no
  tenant authorizer to designate, and the app no longer needs the contact
  scopes. Flow re-checks on **Test connection**,
  periodically and after a refused token; until the app is approved the
  integration page says so (`chat_tenant_not_approved`). Linking a personal
  account needs only a healthy connection and never authorizes the tenant;
  a member outside the app's availability range is told so
  (`chat_app_unavailable_to_user`). The integration page shows the tenant,
  the workspace integration permission and the team binding permission
  separately, and refusals name the missing one (`permissionDomain`:
  `tenant`, `workspace` or `team`). Existing connections become
  automatically authorized on their next successful check. Groups the bot is
  not in, external groups and groups of another tenant are refused with
  their own messages.
- Lark and Feishu connections record the tenant at once: every connection
  check (saving, **Test connection** and the periodic check) reads the
  tenant's information with the app's tenant token, and the integration
  page shows the tenant's name and key. This needs the app permission
  获取企业信息 (`tenant:tenant:readonly`), granted with the app version.
  Without it the connection stays authorized, the page asks to grant it
  and publish a new version (`chat_tenant_info_permission_missing`), and
  only then is the key taken from the bot's internal groups or the first
  linked account, marked as first seen. A recorded key is never replaced:
  when the platform names another tenant, the connection shows a tenant
  mismatch with both keys (`chat_tenant_mismatch`) and stops until it is
  reconnected. Existing connections get the key and name on their next
  check.

### Fixed

- With several API servers, metadata writes no longer silently undo each
  other. Comments, reactions and edits on one project or initiative update
  made on two servers are all kept (updates merge one by one, reactions key
  by key), as is an import job's progress written while another server edits
  the job. Nested settings merge key by key, so two servers turning on
  different notification switches of a team keep both. Team writers (rename,
  settings, deletion) and user settings apply only their change onto the
  stored team, label, cycle or settings record. When both servers changed the
  same value, every writer reruns on the stored record (with a short,
  jittered backoff) and answers 409 if it keeps conflicting, instead of
  keeping its own value; startup migrations and the progress backfill rerun
  until they succeed. Issue updates rerun the same way. A full workspace
  mutation computes project progress inside its transaction from the issues
  as committed, so an issue created on another server meanwhile is counted.
  Two servers filling the shared metadata cache at once no longer leave a
  partial copy, and Undo or Restore of an agent reply's changes runs once
  across servers.
- On MySQL, a long issue title (more than about 128 characters of CJK text,
  or a long English title) no longer fails the full-text search with Error
  191 "Too many words in a FTS phrase", which left triage suggestions,
  similar-issue lookups and search empty for that issue. A long query is
  searched as at most 16 required pieces of at most 64 characters each.
- 4xx responses, MCP tool errors and collaboration socket errors no longer
  carry raw database or object-storage text. Store errors keep their driver
  type through every wrap, object-storage (S3 and local file) errors are
  treated like database errors (a logged 500 with the request id), and a 4xx
  whose message still reads like driver or storage text answers "The request
  could not be completed (request id …)." with the original logged.
- Issue icons (191) and staged upload media types (255) and filenames (512)
  are limited in characters, as the tool schemas' `maxLength` and the
  database columns count them; a non-ASCII value at the limit used to be
  refused because the server counted bytes.
- People removed from or suspended in the workspace can no longer be made an
  assignee, delegate, subscriber, project lead or member, initiative owner,
  triage owner or template assignee: such a write is refused with 400 "This
  person is no longer a member of the workspace" (removed people used to get a
  403 that read like a permission problem, suspended people were accepted).
  References a record already has stay readable, and a write that keeps them
  goes through (keeping a removed project member used to fail with 403).
  Former members get no new notifications, triage rotations skip them, and
  pickers no longer offer suspended members. See
  [People who left the workspace](docs/api.md#people-who-left-the-workspace).
- With several API servers, an issue write no longer undoes a workspace
  change another server made at the same time. Creating, editing or deleting
  an issue (and commenting, attaching, or adding it to a release) writes
  the project, document, release, subscription and triage records it touches
  from the server's in-memory copy, which can be behind: a project renamed
  on another server got its old name back, the progress another server's new
  issue added to a project was lost, a release lost an issue added elsewhere,
  a removed subscription came back while a new one was deleted, and two
  round-robin triage assignments went to the same person. Each changed
  record is now locked and only the fields the write changed are applied to
  it as stored (lists of ids and entries are merged), project progress is
  computed from the stored history, and a write whose field another server
  changed too runs again on the fresh record. Writes on other servers also
  stop reading a stale copy of the workspace from the shared cache after an
  issue write changed it.
- A REST update that only repeats a resource's current values writes nothing:
  it returns 200 with the resource as it is, keeps its `version` and
  `updatedAt`, and records no history entry, event, realtime update, webhook
  or notification (documents, comments, projects, milestones, initiatives,
  labels, label groups, teams, workflow statuses, cycles, views, customers and
  document drafts; issues already did). A stale `expectedVersion` is still a
  409 when the values match. Archiving an archived document or project, or
  retiring a retired team, keeps its original date, and sending a workflow
  status's current type no longer fails when it is the only status of that
  type. See [Updates that change nothing](docs/api.md#updates-that-change-nothing).
- A server error (HTTP 500) no longer shows database or driver text: the body
  is `Internal server error` with the request id, and the error is logged with
  that id. MCP clients and agents get "The change could not be saved because
  of an internal server error (request id …)" instead of the database error.
  SCIM and OAuth errors carry the request id in their own formats.
- Over-long values are refused with a 400 that names the field and limit
  instead of failing with a 500 on MySQL and Postgres: issue titles over 2,048
  characters (REST, MCP `save_issue`, batch updates, templates, recurring
  issues, migrations; a CSV row is reported and skipped), SCIM `userName`,
  `externalId`, names and group `displayName`/`externalId` over 320 characters
  (SCIM `invalidValue`), MCP upload `title`/`subtitle` over 512 characters,
  account and member names over 320, emails over 254, avatar URLs over 2,048,
  team keys over 32, customer tier and owner over 191 and CSV source ids over
  160 bytes. Lengths count characters, not bytes. Long search queries are kept
  in search history cut to 300 characters. Webhook response bodies, agent run
  errors and chat job errors are cut on a character boundary and stored as
  valid UTF-8, so a receiver's binary or truncated response no longer fails
  the delivery on strict MySQL.
- On MySQL and PostgreSQL, generating or refreshing Triage Intelligence
  suggestions no longer fails with HTTP 500 (`Data too long for column
  'field'`) when a suggested label, assignee, project or team has an id of
  more than a few characters, and the suggestions are saved. Schema migration
  17 widens `issue_attribute_records.field` from `VARCHAR(32)` to
  `VARCHAR(191)` and keeps its rows; on MySQL it rebuilds that table and
  writes to it wait until the rebuild finishes, see
  [Wider issue attribute fields](docs/operations.md#wider-issue-attribute-fields-mysql-postgresql).
  Longer fields are stored as a prefix plus digest, so no id length can fail
  the write.
- On PostgreSQL, text containing a NUL character (U+0000) no longer fails
  with HTTP 500 (`invalid byte sequence for encoding "UTF8": 0x00`). NUL is
  removed from JSON request bodies (REST, MCP, SCIM), Flow tool arguments
  (MCP clients and the built-in agent), query strings and CSV/JSON import
  cells on every database, so issue titles, descriptions, comments, names,
  keys, documents and searches are saved without it. The store also drops it
  from any other text before PostgreSQL sees it, and keeps it out of the
  stored issue, comment and workspace JSON, where one `\u0000` used to make
  later queries that read descriptions or notifications fail.
- The MCP tool schemas advertise the server's text limits as `maxLength`
  (`save_issue` title 2,048, `save_template` name and title 2,048,
  `prepare_attachment_upload` title and subtitle 512 and `contentType` 255,
  `save_attachment` title and subtitle 255, `save_team` key 32), so MCP
  clients and the built-in agent see them, and an over-long value is refused
  by the schema check with an error naming the field before anything runs.
- On MySQL, editing a document larger than about 64 KiB no longer fails with
  HTTP 500 (`Data too long for column 'previous_values'`) and no longer
  discards the edit. This affected workspaces with an enabled webhook, an
  event-triggered loop or a connected Feishu/Lark app, through the web app,
  the API, `save_document` and application tokens. Schema migration 16
  widens `domain_events.previous_values` and `auth_identities.claims_json` to
  `LONGTEXT` and `code_host_accounts.scopes` to `MEDIUMTEXT`, and keeps their
  rows. On MySQL it rebuilds those tables, and writes to them wait until the
  rebuild finishes. Plan for this on a large `domain_events` table, see
  [Large event and identity values](docs/operations.md#large-event-and-identity-values-mysql).
  A document edit's stored event no longer repeats the body of every version
  of the document. Those bodies are stored once, with the document's other
  versions, and webhooks and `GET` event reads still return the full previous
  values. Each stored row now grows with the document instead of its history.
- `GET /api/health` answers anonymous probes with only `{"status":"ok"}`
  (`"degraded"` with `503` when Redis does not answer); the build version,
  commit and Redis mode moved to the startup log, and a coordinator outage is
  logged once when it starts and once when it ends.

- The floating agent chat, the entity agent panel and the loop chat follow a
  reply that runs on the server like the Agent page does: after a reload, in
  another tab or when the reply was asked from another surface they show its
  text so far, live approval and connector question cards and Stop, and an
  answer given in one surface shows in the others at once. An approval is
  answered only once, and Stop ends the reply on the server from every
  surface, including a reply waiting on a connector question (which is
  cancelled instead of waiting until it times out).
- Rolling updates and rollbacks no longer break pages whose scripts and
  styles were built by the other version. A refresh served by a new replica
  stayed on the loading screen when an old replica answered its files with
  404, and a page opened before the deploy could not open new routes. A
  replica now finds another release's hashed `/assets/` file in the earlier
  releases its image carries (`/app/web-previous`, Dockerfile
  `PREVIOUS_RELEASE_IMAGE`), on the other replicas (`FLOW_ASSET_PEERS`, Helm
  headless `<release>-peers` Service, on by default), in an optional shared
  directory every release publishes to (`FLOW_ASSET_SHARED_PATH`, Helm
  `assets.shared`), or at published origins (`FLOW_ASSET_FALLBACK_URL`, Helm
  `assets.fallbackUrls`). A file found nowhere is a 404 with
  `Cache-Control: no-store`, never the app's HTML. As a last resort the page
  reloads once when an entry file fails before the app starts. See
  [Rolling updates and web assets](docs/operations.md#rolling-updates-and-web-assets).
- Every route under an issue (`/api/issues/{id}/…` and
  `/api/issue-records/{id}/…`: comments, reactions, relations, attachments,
  links, permissions, subscriptions, history, sharing…) accepts the issue's
  identifier, and an identifier it had before it moved to another team, not
  only its internal id. Pull and merge requests (branch, title, description,
  magic words) and release references from commits link a moved issue by its
  old identifier too, and `ENG-1` no longer matches `ENG-12`. Subscribers who
  cannot see the team an issue moved to are not notified of the move.
- Collaboration: an issue permission change is in force when its request
  returns (a downgraded member's next edit is refused, an upgraded one's is
  applied) and on other servers as soon as the change reaches them.
  Re-parenting an issue (PATCH `parentId` or a sub-issue relation) re-checks
  the open descriptions it affects at once, since sub-issues inherit their
  parents' grants. Issue updates no longer leave other servers reading the
  previous record from the shared cache.
- Data loss with several API replicas: archiving an agent session (and other
  audit entries recorded after a change: connector and MCP tool calls, code
  host accounts, personal connections, coding environments, OAuth credentials,
  SCIM) rewrote every issue and comment record, deleting issues another
  replica created at the same moment. These writes now add only the audit
  entry. Writes that still replace whole collections keep issues, comments and
  activity that another replica committed meanwhile, and run again on fresh
  data when they changed the same record (`409` after three tries).
- Security: `X-Forwarded-Proto` and `X-Forwarded-Host` were believed from any
  client when Flow built links (OAuth and MCP metadata, SCIM locations, signed
  file URLs) and chose `Secure` for cookies, and rate limits trusted the
  client-supplied first `X-Forwarded-For` entry. Forwarding headers are now
  read only with `FLOW_TRUST_PROXY_HEADERS=true`, and only from the proxies in
  the new `FLOW_TRUSTED_PROXIES` (IPs or CIDR ranges; Helm `trustedProxies`)
  when it is set; the client address is the right-most entry that is not a
  trusted proxy. Without trust Flow uses TLS, the `Host` header and the peer
  address, and the scheme of `FLOW_APP_URL` for requests to its host, so links
  and cookies stay `https` behind a TLS ingress. See
  [HTTPS detection and trusted proxies](docs/configuration.md#https-detection-and-trusted-proxies).
- Security: documents attached to a private team's issue or project before
  inherited document scope existed stayed readable across the workspace until
  they were attached or detached again. A one-time startup migration records
  the inherited teams of every such document (including deleted ones), without
  changing grants, the document's own teams, its version or update time. See
  [Inherited document scope](docs/operations.md#inherited-document-scope).
- A panic in a request handler is now logged once (`http handler panic`, with
  the request id, method, path and stack; no headers, query strings or
  bodies) and answered with a JSON 500 in the standard error envelope that
  carries the request id and the security headers. A response that already
  started, such as an event stream, is aborted instead, and WebSocket
  connections are left to their handler.
- Flow Agent: asking it to create a team of 40 people no longer runs out of
  steps. `save_team` adds everyone it finds in one call and lists the rest
  (not in the workspace, or a name several members share, with candidates)
  instead of failing on the first. It does not send workspace invitations:
  for people who are not members its result carries `inviteHint`, which the
  reply passes on (Settings → Administration → Members → Invite, the emails
  to paste, the team to select, Send invites); `get_user` takes `queries` to
  look up many people at once;
  `list_users` honours `orderBy: createdAt`; and team requests get `save_team`
  without a `load_tools` step. A chat reply or agent thread run may now take
  `FLOW_AGENT_MAX_TOOL_TURNS` turns (default 40, was 8) and
  `FLOW_AGENT_MAX_TOOL_CALLS` tool calls (default 150); an identical call that
  already failed is not run again, and a reply that reaches its budget ends
  with what it did and what is left, with "Continue" to pick up, instead of
  "The agent used too many steps".
- Security: a member who may only view or comment on an issue (a viewer or
  commenter permission or share, a guest's read-only share, a workspace member
  outside a public team, or a read-only API key) could change its description
  through the realtime collaboration socket, which stored and relayed the
  edits although saving the description is refused to them. The socket now
  applies the same rule as saving the description (editor or owner access and
  a credential allowed to write issues): read-only members join read-only,
  still see others' edits and cursors live, and their edits are refused
  (`document.readonly`) without being stored or relayed. Raising or lowering a
  member's issue or document permission, role, team membership or a parent
  issue's permission reaches open editors on every server (`document.access`):
  the editor turns read-only, keeping unsaved changes downloadable, or
  editable again without a reload. Read-only API keys can no longer edit
  standalone documents on the socket either.
- Moving an issue to another team (`PATCH /api/issues/{id}` with `teamId`, the
  SDK, MCP `save_issue` with `team`, the team picker) returned success but left
  the issue in its team and published no event. The move now gives the issue
  the target team's key and a new number (`previousIdentifiers` keeps the old
  ones, which still open, read and update it), the target team's status of the
  same name and type, takes it out of its cycle, maps team labels by name, and
  records the move in the history and the `issue.updated` event, which also
  closes the description for members who cannot see the new team. Batch updates
  and accepted team suggestions move issues the same way. Moving needs access to
  the target team (guests cannot move issues), and update requests with fields
  the endpoint does not support are rejected with 400 instead of ignored.
- Several API servers starting at the same moment on one PostgreSQL or MySQL
  database no longer crash with errors such as `tuple concurrently updated`
  or a duplicate `schema_migrations` key. They take turns through a database
  lock (a PostgreSQL advisory lock, MySQL `GET_LOCK`, or a lock file next to a
  SQLite database), and a server that waited skips the work already done.
  `FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT` (default `10m`) bounds the wait. With
  several replicas, scheduled workflow runs and retries run once instead of on
  each replica, and storage maintenance runs on one replica.
- Agent chat: a reply still running when the page is reloaded, or when the
  chat is open in another tab, shows its progress (text so far, tool calls,
  and approval and connector question cards you can answer) instead of only
  "Thinking…" until it ends; answering in one tab updates the others. The
  in-progress reply is saved with the chat (message `status: "streaming"`), a
  reply cut off by a server restart is marked interrupted with Retry, and
  with Redis coordination an answer or Stop that reaches another API server is
  forwarded to the server running the reply.
- Issue descriptions open for collaborative editing follow access changes: a
  member removed from the issue's team, losing a private team or a shared
  issue, suspended or removed from the workspace, or whose session was signed
  out or revoked leaves the description at once on every API server (with
  Redis coordination), stops receiving its edits and can no longer send any.
  The editor turns read-only, says the member no longer has access, keeps
  unsaved changes downloadable and reloads the issue. Standalone documents
  now follow permission changes made on another server too.
- With several API servers, a request right after a change on another server
  no longer sees the old state: accepting an invitation to a workspace created
  a moment ago, using a new team, API key or member, or a just-granted role
  returned 403 or 404 until the server applied the peer's change (about 1.5
  seconds). Each change now bumps a per-workspace version in Redis, and a
  server that is behind (or does not have the workspace yet) reloads it from
  the database before authorizing the request.
- Coding session pushes (`coding_session.*` on `/api/realtime/events`) reach
  only the people who may open the session; before, every workspace member's
  stream carried each session's issue, phase and state, including another
  member's private chat session.
- The workspace event stream recovers from a connection that died silently
  (for example behind a proxy that kept it open after the API restarted): the
  server's keepalive is now a `keepalive` event the web app watches, and a
  stream quiet for 45 seconds reconnects from its cursor. After a resync, open
  coding session views and agent task threads reload too, instead of waiting
  for their next push.
- Resuming the workspace event stream replays the whole 256-event window; a
  client that missed more than 64 events used to get a resync (a full reload)
  instead.
- A reconnected event stream that went quiet before its first keepalive (the
  API went away again behind a proxy that kept the connection) is reconnected
  too; it used to stay dead until the page was reloaded.
- With several API replicas (Redis coordination): subscribers receive changes
  in `seq` order, so a change made on another replica no longer arrives after
  a newer one and leaves the older value on screen; a stream resumed on another
  replica resyncs instead of replaying a different order (skipping or repeating
  events); events published while a replica's Redis subscription was
  reconnecting no longer vanish (subscribers resync); and a burst of changes
  from another replica shares one workspace reload, so it reaches subscribers
  in tens of milliseconds instead of building a backlog of half a second.
- The sandbox runner reads agent output lines on LF only, so a JSON record
  holding U+2028 or U+2029 is no longer split and dropped.
- Coding sessions with the docker provider start on Docker Desktop and other
  daemons that use the containerd image store: the sandbox token is written
  through a short-lived helper container, because the archive API refuses
  copies into the read-only sandbox container ("container rootfs is marked
  read-only").
- Starting a coding session from an issue answers with the reason (for
  example `agentUnavailable` when the built-in Flow agent is off, or an
  invalid model) instead of an internal error.
- A new coding-session branch no longer tracks the base branch, and a bare
  `git push` by the agent publishes it under its own name; a summary line that
  ends with a colon no longer leaves the colon in the commit subject and pull
  request title.
- The first edit of a document that had a body but no version history (created
  with content through the API, MCP or a template, or written by an earlier
  release) no longer loses the body it replaced: the pre-edit state is kept as
  the oldest version, and the version history lists it.

### Removed

- Coding sandboxes no longer set the older duplicate sandbox and snapshot
  variables. Setup and prepare scripts detect the sandbox with
  `FLOW_SANDBOX=1` and a snapshot build with `FLOW_SNAPSHOT=1`.

## [0.1.0] - 2026-10-17

The release series has been reset to v0.1.0 using the current main branch.
Earlier release tags and GitHub Releases have been removed. The development
history below is retained for reference.

### Added

- Agent image, video and file attachments, on-demand document reading and
  media transcription, rich clipboard paste, chat renaming and delete with Undo.
- Lean bootstrap responses, resumable realtime change cursors, parallel route
  loading, smoother issue lists, and static asset caching.
- Pulse initiative updates, unread indicators and comment attachments.
- Updated sidebar menus and settings pages, including Billing and Usage & limits.

### Fixed

- Missing static assets return 404 and stale chunks trigger a guarded reload.
- Plain-text Markdown snippets preserve bare angle brackets.
- API inventory and SDK generated contracts match the server routes and types.
- PostgreSQL issue-list migration compatibility and asynchronous test races.

### Security

- Restricted authentication redirects, sanitized pasted HTML, removed browser
  persistence of API key secrets, and hardened public outbound requests.
- Correct JSON escaping, Unicode conversion bounds and SDK placeholder parsing.

## Archived 0.3.0 - 2026-10-14

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

## Archived 0.2.0 - 2026-10-04

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

## Archived 0.1.0 - 2026-08-30

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
