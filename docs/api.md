# Flow REST API

This page is the reference for integrating with Flow over HTTP: authentication,
pagination, filtering, errors, rate limits, files, attachments, customers, the
audit log, prefilled issue links, versioning and the MCP server. Application
registration, OAuth installation and agents are covered in
[developers.md](developers.md).

The machine-readable contract is [`openapi.json`](openapi.json) (OpenAPI 3.1).
Operations tagged with a resource (identity, teams, issues, files, …) carry
request and response schemas reflected from the server's Go types, so clients
can be generated with any OpenAPI generator. Remaining routes are listed as an
inventory and may change without notice. Regenerate the file after changing
routes or contract types:

```bash
node scripts/generate-openapi.mjs          # writes docs/openapi.json
node scripts/generate-openapi.mjs --check  # CI: fails when stale
```

## Authentication

| Credential | Header | Rate-limit bucket |
| --- | --- | --- |
| Personal API key (`flow_api_…`, Settings › API) | `Authorization: Bearer <key>` | per user (all of a user's keys share it) |
| OAuth access token (`flow_oauth_…`) | `Authorization: Bearer <token>` | per user (or app user) and application |
| Browser session | cookie + `X-Workspace-Key: <workspace>` | exempt by default |

API keys and OAuth tokens belong to one workspace and select it automatically.
A bearer token that is invalid is rejected; Flow never falls back to a browser
cookie on the same request.

Scopes: `read`, `write`, `admin`, `create_issues` (`issues:create`),
`create_comments` (`comments:create`) and the application scopes
`customer:read|write`, `initiative:read|write`, `release:read|create`,
`releaseStage:update`, `app:assignable`, `app:mentionable`. Team-restricted
keys and application team access narrow every list below.

```bash
curl --fail-with-body "$FLOW_BASE_URL/api/viewer" -H "Authorization: Bearer $FLOW_TOKEN"
```

`GET /api/viewer` returns the user, workspace, role, `actor` (`user` or `app`),
`authentication` (`session`, `apiKey`, `oauth`), scopes, team restriction and,
for OAuth tokens, the application. `GET /api/organization` returns the
workspace with user and team counts.

## Lists and pagination

Lists are Relay-style connections:

```json
{ "nodes": [ … ], "pageInfo": { "hasNextPage": true, "hasPreviousPage": false, "startCursor": "…", "endCursor": "…" } }
```

| Parameter | Meaning |
| --- | --- |
| `first` (alias `limit`) | Page size forward, 1-250, default 50 |
| `after` (alias `cursor`) | Continue after `pageInfo.endCursor` |
| `last` / `before` | Page backward from `pageInfo.startCursor` |
| `orderBy` | `createdAt` (default) or `updatedAt`; newest first |
| `includeArchived` | Include archived resources (default `false`) |
| `filter` | Filter object, see below |

Cursors are opaque, encode the sort key and the id, and stay valid when new
resources are created: continuing from a cursor never repeats or skips a node
that existed when the cursor was issued. A cursor only works with the
`orderBy` it was issued for.

| Endpoint | Nodes | Extra parameters |
| --- | --- | --- |
| `GET /api/teams` | Team | |
| `GET /api/users` | User | `includeDisabled` |
| `GET /api/workflow-states` | WorkflowState | `teamId` |
| `GET /api/issue-labels` | IssueLabel | `teamId`, `resourceType` (`issue`, `project`, `all`) |
| `GET /api/cycles` | Cycle | `teamId` |
| `GET /api/projects` ¹ | Project | `teamId` |
| `GET /api/project-milestones` | ProjectMilestone | `projectId` |
| `GET /api/initiatives` | Initiative | |
| `GET /api/documents` ¹ | Document | `teamId` |
| `GET /api/comments` | Comment | exactly one of `issueId`, `documentId`, `projectId`, `initiativeId` |
| `GET /api/attachments` | Attachment | `issueId` and/or `url` |
| `GET /api/customers` | Customer | |
| `GET /api/customer-requests` | CustomerRequest (need) | `customerId`, `issueId`, `projectId` |
| `GET /api/releases` ¹ | Release | `pipelineId` |
| `GET /api/webhooks` ¹ | Webhook (admins) | |
| `GET /api/workspace/audit-log` ¹ | AuditLogEntry (admins) | see [Audit log](#audit-log) |

¹ Older list routes keep their original response (an array, or the project
directory page) unless a pagination argument (`first`, `last`, `after`,
`before` or `orderBy`) or `connection=1` is present. The OpenAPI document
describes both shapes (`oneOf`, and `x-flow-response-shapes` for generators).

Issues are queried with `GET /api/issue-records`, an indexed keyset query of at
most 100 issues per page. With `first`/`after`, `last`/`before`, `orderBy`
(`createdAt` default, `updatedAt`, `priority`, `title`, `sortOrder`),
`orderDirection` or `connection=1` it answers with a connection that pages in
both directions (`totalCount` with `includeTotal=true`); `last` without
`before` returns the final page. Without them it returns the original page,
`{items, hasMore, nextCursor, total}`, paged forward with `limit` and `cursor`
(`sort`, `direction`). `archived` is `false` (default), `true` (only archived)
or `all`; `includeArchived=true` means `all`. `filter` takes the view filter
tree used by saved views (`IssueFilter` in the OpenAPI document). A cursor is
bound to the filter and ordering it was issued for. `GET /api/issue-records/{id}`
accepts an id or an identifier such as `ENG-123`.

### Single records

`GET /api/teams/{id}` (id or key), `/api/users/{id}` (id or `me`),
`/api/workflow-states/{id}`, `/api/issue-labels/{id}`, `/api/cycles/{id}`,
`/api/projects/{id}` (id or `slugId`), `/api/initiatives/{id}` (id or
`slugId`) and `/api/customers/{id}` return one record with the visibility of
the matching list (a record you cannot see is a `NOT_FOUND`). Archived records
are returned.

Comments on issues and documents are read from an index and are ordered by
`createdAt` only; they do not accept `filter`.

Prefer webhooks over polling. When you must poll, order by `updatedAt` and
stop at the first node you have already seen.

### Record ids

Treat every id as an opaque string: compare it for equality, store it whole
(up to 191 bytes) and pass it back unchanged. Do not parse it, match it
against a pattern such as `prefix_\d+$` or derive a time from it.

Ids Flow generates today look like `<prefix><19 digits>_<10 characters>`, for
example `evt_1793112233445566778_k3j9x0abcd` or
`notification_webhook_disabled_<webhook id>_1793112233445566778_k3j9x0abcd`:
the digits are a zero-padded creation time in nanoseconds and the characters
are random lowercase base32, so ids created at the same instant on different
servers stay distinct. The generated part uses only digits, lowercase
letters and `_`, so it is safe in URLs. Ids of one kind created by Flow sort by creation time as plain
strings, after ids in the older `<prefix><digits>` shape; records keep the
id they were created with, so both shapes appear in one workspace. Ids that
are a sequence (`issue_42`) or derived from another record or a schedule
(one notification per project update due time) keep their shape. To order
records, sort by `createdAt` rather than by id. Issue identifiers such as
`ENG-123` are not ids and keep their `KEY-number` shape.

## Filtering

`filter` is a JSON object. Conditions in one
object must all match; `and`/`or` take arrays of filters.

```bash
curl -G "$FLOW_BASE_URL/api/projects" -H "Authorization: Bearer $FLOW_TOKEN" \
  --data-urlencode 'first=20' \
  --data-urlencode 'filter={"status":{"type":{"eq":"started"}},"updatedAt":{"gt":"-P2W"},"or":[{"lead":{"email":{"eq":"ada@example.com"}}},{"teams":{"key":{"eq":"ENG"}}}]}'
```

| Comparators | Applies to |
| --- | --- |
| `eq`, `neq`, `in`, `nin`, `null` | every field |
| `lt`, `lte`, `gt`, `gte` | numbers, dates |
| `eqIgnoreCase`, `neqIgnoreCase`, `startsWith`, `startsWithIgnoreCase`, `notStartsWith`, `endsWith`, `notEndsWith`, `contains`, `notContains`, `containsIgnoreCase`, `notContainsIgnoreCase` | strings |

Relations (`team`, `lead`, `owner`, `customer`, `project`, …) are objects:
filter on their fields (`{"team":{"key":{"eq":"ENG"}}}`); a comparator applied
to the relation itself compares its id. List fields (`teams`, `labels`,
`domains`, `externalIds`, …) match when any element matches; `every` and
`some` make that explicit. Dates accept RFC 3339 timestamps, `YYYY-MM-DD` and
ISO 8601 durations relative to now (`-P2W` two weeks ago, `P1D` tomorrow).
Each endpoint's filterable fields are listed in the OpenAPI `filter`
parameter; an unknown field or comparator is an `INVALID_INPUT` error.

## Errors

Every error under `/api/` and `/uploads/` uses one envelope; all values are
strings:

```json
{ "error": "issue not found", "code": "NOT_FOUND", "type": "NOT_FOUND", "requestId": "5f0c…" }
```

| `type` | HTTP | Meaning |
| --- | --- | --- |
| `INVALID_INPUT` | 400, 405, 413, 415, 422 | Malformed or invalid request |
| `AUTHENTICATION_ERROR` | 401 | Missing, invalid or expired credentials |
| `FORBIDDEN` | 403 | Not allowed for this user, key or scope |
| `FEATURE_NOT_ACCESSIBLE` | 403 | The workspace has the feature turned off |
| `NOT_FOUND` | 404, 410 | Missing or not visible to the caller |
| `CONFLICT` | 409, 412 | Concurrent change or uniqueness conflict |
| `RATELIMITED` | 429 | A rate-limit budget is exhausted |
| `INTERNAL_ERROR` | 500 | Server error; retry with backoff |
| `UNAVAILABLE` | 502, 503, 504 | Dependency unavailable; retry with backoff |

Switch on `type`. `code` equals `type` unless the endpoint reports something
more specific (for example `VERSION_CONFLICT` with the current resource in
`current`, or `mfa_required`). `requestId` matches the `X-Request-ID` header;
include it when reporting a problem. OAuth endpoints (`/oauth/*`) keep RFC 6749
errors (`error`, `error_description`) and MCP keeps JSON-RPC errors.

## Updates that change nothing

A `PATCH` (or `PUT`) whose values all equal the resource's current values is
accepted and writes nothing: the response is `200` with the resource as it is,
its `version` and `updatedAt` stay the same, and no history entry, domain
event, realtime update, webhook delivery or notification is produced. Retrying
a write or syncing the same state again is therefore safe. This holds for every
stored resource, among them issues, documents, comments, projects and their
milestones, resources, relations and updates, initiatives, labels, teams and
team settings, workflow and project statuses, cycles (settings and capacity),
views, customers (including `PUT /api/customers` on a match), customer
requests, releases, release pipelines and release notes, templates, webhooks,
OAuth applications (and their webhook and manifest), API keys, SLA rules,
favorites, subscriptions, notifications and notification preferences, account
and workspace settings, members and team memberships, and link attachments.
Setting a state that is already set (favoriting a favorite, muting a muted
thread, archiving an archived release or post, publishing a published release
note) keeps the original date.

A few writes are actions rather than edits and always run: file uploads,
connecting an integration or a chat application (each runs the provider's
credential check), reinstalling an application (which issues a new webhook
secret), saving a connector's encrypted headers, and editing an agent chat
message (which runs the reply again). SCIM provisioning writes no events and
audits only attributes that changed.

Optimistic locking is checked first: a stale `expectedVersion` is refused with
`409 VERSION_CONFLICT` (and the current resource) even when the values match.
This applies to `PATCH /api/issues/{id}` as well: without `expectedVersion`
the given fields are applied to the current issue, with a stale one the write
is refused.

## People who left the workspace

People removed from or suspended in the workspace keep their place on the
records that name them (assignee, subscribers, project lead and members,
history), so those stay readable. A write cannot add them again: naming one
as a new assignee, delegate, subscriber, project lead or member, initiative
owner, triage owner or template assignee is refused with `400` and
`This person is no longer a member of the workspace`. A write that keeps such
a reference as it is (for example a `PATCH` that sends the current
`subscriberIds`) goes through. They get no new notifications, triage rotations
pass over them, and a template whose default assignee left creates issues
unassigned. Membership is read from the workspace's memberships at write
time, so a resumed member can be assigned again at once.

Mentions follow the same rule. A mention of a former member stays in the text
it was written in (comments, documents, descriptions and updates render it as
before, and an edit that keeps it saves), but rich text (`bodyData`,
`contentData` or `descriptionData`) that adds a mention node of one is refused
with `400` and `This person is no longer a member of the workspace and can't
be mentioned`. A plain-text `@name` is kept as text; Markdown written through
MCP does not turn a former member's name into a mention.

Suspending, removing or resuming a member sends a realtime
`workspace_member.suspended`, `workspace_member.removed` or
`workspace_member.resumed` event (`aggregateId` and `payload.userId` are the
member, `payload.status` is `suspended`, `removed` or `active`), including when
SCIM provisioning makes the change, so open sessions update their people
pickers without a reload.

## Rate limits

Flow uses a leaky-bucket model: each budget refills continuously at
limit ÷ hour.

| Credential | Requests / hour | Complexity points / hour | Bucket |
| --- | --- | --- | --- |
| API key | 2,500 | 3,000,000 | user |
| OAuth token | 5,000 | 2,000,000 | user (or app user) per application |
| Unauthenticated (`/oauth/*`, `/mcp` without a valid token, upload and signed-file links, failed credentials) | 600 | 100,000 | client IP |
| Browser session | unlimited | unlimited | user |

Complexity is the REST equivalent of GraphQL query cost: 1 point per request
plus 1 per node a list may return (its `first`/`last`/`limit`, or 50).
Workspace snapshots (`/api/bootstrap`, export downloads) cost 5,000 points; a
single request may cost at most 10,000. Some endpoints also have their own
budget: `fileUpload` (POST /api/files, 1,000/h), `attachmentFromUrl` (250/h),
`exportCreate` (30/h).

Every limited response carries:

| Header | Value |
| --- | --- |
| `X-RateLimit-Requests-Limit` / `-Remaining` / `-Reset` | request budget; reset is when the bucket is full again, in UTC epoch milliseconds |
| `X-Complexity` | points this request cost |
| `X-RateLimit-Complexity-Limit` / `-Remaining` / `-Reset` | complexity budget |
| `X-RateLimit-Endpoint-Name` / `-Requests-Limit` / `-Remaining` / `-Reset` | endpoint budget, where one applies |

An exhausted budget returns HTTP 429 with `Retry-After` (seconds) and
`{"type":"RATELIMITED"}`. Back off until then, reduce page sizes, and prefer
webhooks to polling.

Buckets live in process memory on a single instance and in Redis when
`FLOW_REDIS_MODE` is enabled, so limits hold across API instances. Operators
can change the budgets (see [configuration](configuration.md#api-rate-limits)).

## Files

Uploading is a three-step flow: prepare, PUT the bytes, then use
the asset URL.

```bash
# 1. Prepare (write scope). size must be exact; at most 20 MB.
curl -s "$FLOW_BASE_URL/api/files" -H "Authorization: Bearer $FLOW_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"filename":"screenshot.png","contentType":"image/png","size":48213}'
# -> {"uploadUrl":".../api/files/uploads/upload_…","assetUrl":".../uploads/asset_….png",
#     "headers":[{"key":"Content-Type","value":"image/png"}],"expiresAt":"…"}

# 2. Upload within 15 minutes, sending the listed headers. No Authorization:
#    the token in uploadUrl is the credential. One successful PUT per token.
curl -X PUT --data-binary @screenshot.png -H 'Content-Type: image/png' "$UPLOAD_URL"

# 3. Reference assetUrl in Markdown (issue descriptions, comments, documents)
#    or attach it to an issue (see Attachments).
```

Pending uploads are stored in the database, so the prepare call, the PUT and
later downloads may reach different API instances. Workspace upload policies
(restricted file types) are enforced on the PUT; a size mismatch returns 400
and the upload can be retried with the same URL.

### Downloads and signed URLs

Files are private. `GET /uploads/{name}` requires the same credentials as any
API call (session or `Authorization` header). Uploaded assets are readable by
the uploader and the workspace's members (not guests); attachment files follow
the visibility of the issue, document or update they belong to.

For clients that cannot send headers (an `<img>` tag, a chat unfurl), request
short-lived signed URLs, valid for 1 second to 7 days:

- `GET /api/files/sign?url=<file URL>&url=…&expiresIn=3600` returns
  `{"files":[{"url","signedUrl","expiresAt"}]}` for files you can read.
- Or send `X-Flow-File-Urls-Expire-In: <seconds>` (alias
  `Public-File-Urls-Expire-In`) on any API request: every `/uploads/…` URL in
  the JSON response, including Markdown, is replaced by a signed URL.

Signed URLs (`/api/files/signed/{name}?expires=…&signature=…`) work without
credentials until they expire; treat them as secrets. They are HMAC-signed
with `FLOW_FILE_URL_SIGNING_KEY`, or with a random key that Flow stores in the
database on first use.

## Attachments

Link attachments connect an issue to an external resource:

```bash
curl "$FLOW_BASE_URL/api/issues/ENG-123/link-attachments" -H "Authorization: Bearer $FLOW_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"url":"https://support.example.com/tickets/42","title":"Ticket 42","subtitle":"Open · Priority high","iconUrl":"https://support.example.com/icon.png","metadata":{"status":"open","priority":"high"}}'
```

The URL is the attachment's identity on its issue: sending the same URL again
updates the title, subtitle, icon and metadata (HTTP 200) instead of creating a
duplicate (HTTP 201), so integrations can re-sync safely. Omitted fields are
left unchanged. Metadata is a JSON object of at most 16 KB. Attachments
created with an OAuth application token default to the application's logo as
icon.

| Operation | Route |
| --- | --- |
| Create or update by URL | `POST /api/issues/{id}/link-attachments` |
| Update by id | `PATCH /api/issues/{id}/attachments/{attachmentId}` |
| Delete | `DELETE /api/issues/{id}/attachments/{attachmentId}` |
| Get | `GET /api/attachments/{id}` |
| List an issue's attachments | `GET /api/attachments?issueId=ENG-123` |
| Find attachments by URL (`attachmentsForURL`) | `GET /api/attachments?url=https://…` |
| Upload a file attachment | `POST /api/issues/{id}/attachments` (multipart `file`) |

The MCP tool `save_attachment` performs the same create-or-update.

## Customers

Customers carry `domains` and `externalIds` (identifiers in your CRM or support
desk; each is unique in the workspace). `PUT /api/customers` upserts: it
matches an existing customer by `id`, then by `externalId`/`externalIds`, then
by domain, merges the domains and external ids you send and updates the other
fields; when nothing matches it creates the customer (`name` required, 201).

```bash
curl -X PUT "$FLOW_BASE_URL/api/customers" -H "Authorization: Bearer $FLOW_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"externalId":"crm-1042","name":"Acme","domains":["acme.com"],"tier":"Enterprise","size":250}'
```

Customer requests are created with
`POST /api/customer-requests`. Select the customer with `customerId` or
`customerExternalId`; `attachmentUrl` adds a link attachment to the request's
issue (`issueId`) and becomes the request's `sourceUrl`. Update with
`PATCH /api/customer-requests/{id}`. Application tokens need `customer:read` /
`customer:write`.

## Team members

Every team membership has an `origin`: `manual` (added by a person in Flow;
memberships from before origins existed read as manual), `invitation`, `api`
(an API key or OAuth application that named no origin), `sync` (a directory or
HR sync that said so) or `scim` (a SCIM group mapped to the team).
`GET /api/teams/{id}/members` lists them. `PUT /api/teams/{id}/members/{userId}`
adds a member or changes their role; send `"origin":"sync"` from a sync.

`DELETE /api/teams/{id}/members/{userId}` removes a member. With
`origin=sync` it removes only a membership with that origin (`409
TEAM_MEMBER_ORIGIN_MISMATCH` with the actual `origin` otherwise) and never an
owner (`409 TEAM_OWNER_PROTECTED`). `dryRun=true` writes nothing and returns
`{dryRun, removed, kept}`, where `kept` says why a member would stay. Nobody
can remove themselves from their last active team (`409 LAST_ACTIVE_TEAM`,
"You should be a member of at least one active team"), by this endpoint or by
leaving the team in Flow; an admin may still remove someone else.

`POST /api/teams/{id}/members/sync` makes a team match a directory list
(`members`: user ids or emails). It adds missing users with `origin` (default
`sync`) and removes unlisted members only when their origin is in
`removeOrigins` (default: the sync's origin). Owners and SCIM-managed members
are always kept, and manual members unless `removeOrigins` lists `manual`.
Run it with `"dryRun":true` first:

```bash
curl -X POST "$FLOW_BASE_URL/api/teams/$TEAM_ID/members/sync" -H "Authorization: Bearer $FLOW_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"members":["ada@example.com","grace@example.com"],"dryRun":true}'
```

Every add, removal, role change and origin change (from team settings, the
API, MCP and the agent, invitations, workspace member removal and SCIM) is an
audit log entry with resource type `team_member` and the affected user as
`resourceId`; its metadata holds the team, the user, `roleBefore`/`roleAfter`,
`origin`, `source` (`manual`, `api`, `agent`, `sync`, `invitation`, `scim`) and
the API key or SCIM connection that made it.
`GET /api/workspace/audit-log?resourceType=team_member&resourceId=<userId>`
answers "who removed this person, when, and how".

SCIM provisioning writes its own entries with `source` `scim`: resource type
`workspace_member` (`provisioned`, `updated` with `changedFields`,
`suspended`, `restored`, `deprovisioned`, `removed`, `role_changed` with
`roleBefore`/`roleAfter`) and `scim_group` (`created`, `linked`, `updated`,
`deleted`, `members_changed` with the added and removed user ids).

Workspace members provisioned by an active SCIM connection have
`managedBy: "scim"`; changing their role, name or email
(`PATCH /api/workspaces/{workspaceKey}/members/{userId}`), suspending,
resuming or removing them answers 403 with code `SCIM_MANAGED` until SCIM is
turned off or every SCIM token is revoked (see the SCIM section of the
configuration guide).

## Audit log

`GET /api/workspace/audit-log` (workspace admins or the `admin` scope) accepts
`type` (`resourceType.action`, e.g. `customer.created`, or just the action),
`actorId`, `actorEmail`, `resourceType`, `resourceId`, `ip`, `since` and
`until` (RFC 3339, `YYYY-MM-DD` or durations such as `-P7D`), plus a `filter`
object over `id`, `type`, `action`, `resourceType`, `resourceId`, `actor`, `ip`
and `createdAt`. With `first`/`after` it returns a connection.
`GET /api/workspace/audit-log/types` lists the entry types present with their
counts. The log keeps the latest 1,000 entries; stream it to your SIEM with the
audit log stream (Settings › Security) for longer retention.

## OAuth applications

Workspace admins, or API keys with the `admin` scope, manage the workspace's
OAuth applications (tag `oauth-applications` in `docs/openapi.json`; the
manifest format and consent flow are in [oauth.md](oauth.md)):

| Route | |
| --- | --- |
| `GET /api/oauth-applications`, `GET /api/oauth-applications/{id}` | The applications, without client secrets. |
| `POST /api/oauth-applications`, `PATCH`/`DELETE /api/oauth-applications/{id}` | Create (returns the client secret once), update, delete. |
| `POST /api/oauth-applications/manifest`, `PUT /api/oauth-applications/{id}/manifest`, `GET /api/oauth-applications/{id}/manifest`, `POST /api/oauth-applications/manifest/validate` | Manage an application as a manifest. |
| `GET /api/oauth-applications/{id}/installs` | Members' authorizations and the app installation. |
| `GET /api/oauth-applications/{id}/failures` | Webhook delivery failures. |
| `GET`/`PUT /api/oauth-applications/{id}/webhook`, `POST …/webhook/rotate-secret`, `GET …/webhook/secret` | The application webhook and its signing secret. |
| `GET …/client-secret`, `POST …/rotate-secret`, `GET`/`POST …/developer-token` | Credentials (below). |

```bash
curl --fail-with-body "$FLOW_BASE_URL/api/oauth-applications/$APP_ID/client-secret" \
  -H "Authorization: Bearer $FLOW_ADMIN_KEY"
```

**Credentials with API keys.** Reading or rotating the client secret, reading
or creating developer tokens and reading or rotating the webhook signing
secret work with a personal API key only when the key has the `admin` scope
(or full access), is not restricted to selected teams and belongs to a
workspace admin. OAuth tokens, read-only keys, keys of members and
team-restricted keys get `403`. Secrets are returned only in the response
body with `Cache-Control: no-store`. Every such call made with an API key
writes an audit entry (resource type `oauth_application`, actions
`client_secret_read`, `client_secret_rotated`, `client_secret_issued`,
`developer_token_read`, `developer_token_created`, `webhook_secret_read`,
`webhook_secret_rotated`, `webhook_secret_issued`) naming the key's owner, the
key (`apiKeyId`, `apiKeyName`) and the application, never the secret. Treat an
admin key like the secrets it can read: keep it in a secret manager, give it a
short expiry and revoke it when it is no longer needed.

## Prefilled issue links

Open the issue composer with fields already set:

```
$FLOW_BASE_URL/<workspace>/new?title=Crash+on+launch&description=Steps…&priority=high&labels=bug,ios&assignee=me
$FLOW_BASE_URL/<workspace>/team/ENG/new?status=Todo&cycle=36&project=Mobile+app&template=<template id>
```

| Parameter | Values |
| --- | --- |
| `title`, `description` | text (`+` is a space) |
| `team` | team key, id or name (the `/team/<KEY>/` path wins) |
| `status` | workflow state id or name in the team |
| `priority` | `urgent`, `high`, `medium`, `low`, `none` or 0-4 |
| `assignee` | `me`, user id, username, name or email |
| `estimate` | a point value of the team's scale |
| `cycle` | id, number or name |
| `label` / `labels` | comma-separated names or ids |
| `project`, `projectMilestone` | id or name |
| `template` | issue template id or name |
| `parent`, `dueDate` | issue id or identifier; `YYYY-MM-DD` |

Unknown values are ignored. In an issue, the command menu action **Copy
prefilled create issue URL** builds such a link from the issue's properties.

## Versioning and deprecations

The REST API is unversioned. Changes are additive: new endpoints, new optional
fields and new enum values can appear at any time, so ignore unknown fields.
Removals and incompatible changes are announced in `CHANGELOG.md`; affected
operations are marked `deprecated: true` in `openapi.json` and keep working for
at least six months. `info.version` in the OpenAPI document is the Flow release
that produced it.

## MCP server

Flow serves the Model Context Protocol (Streamable HTTP) at
`$FLOW_BASE_URL/mcp`, and a read-only variant at `$FLOW_BASE_URL/mcp/readonly`.
Clients that support OAuth discover Flow's authorization server
(`/.well-known/oauth-protected-resource`) and register themselves; others can
send an API key or OAuth access token as a bearer token. Tokens with only the
`read` scope see read tools.

| Client | Setup |
| --- | --- |
| Claude Code | `claude mcp add --transport http flow $FLOW_BASE_URL/mcp` (add `--header "Authorization: Bearer $FLOW_API_KEY"` to skip OAuth) |
| Claude (desktop / web) | Settings › Connectors › Add custom connector › `$FLOW_BASE_URL/mcp` |
| Cursor | `~/.cursor/mcp.json`: `{"mcpServers":{"flow":{"url":"$FLOW_BASE_URL/mcp"}}}` |
| VS Code | `.vscode/mcp.json`: `{"servers":{"flow":{"type":"http","url":"$FLOW_BASE_URL/mcp"}}}` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json`: `{"mcpServers":{"flow":{"serverUrl":"$FLOW_BASE_URL/mcp"}}}` |
| Codex CLI | `~/.codex/config.toml`: `[mcp_servers.flow]` with `url = "$FLOW_BASE_URL/mcp"` |
| Zed | `settings.json`: `"context_servers": {"flow": {"url": "$FLOW_BASE_URL/mcp"}}` |
| Clients without HTTP transport | `npx mcp-remote $FLOW_BASE_URL/mcp` as a stdio command |

Troubleshooting: an OAuth sign-in that keeps failing is usually a stale client
registration; remove the server from the client and add it again. One
connection works in one workspace (the one you approve during sign-in, or the
API key's workspace); add the server again under another name for a second
workspace. MCP calls count against the same rate limits as REST calls. Files
are uploaded with `prepare_attachment_upload` (a durable upload URL, see
[Files](#files)) and `create_attachment_from_upload`; link attachments with
`save_attachment`.

### Attaching files to issues and documents over MCP

The attachment tools (`prepare_attachment_upload`,
`create_attachment_from_upload`, `create_attachment`, `save_attachment`) take
exactly one target: `issue` or `document`. A document must be one you can edit
(the same rule as uploading in the editor, including the access a document
inherits from a private team).

1. `prepare_attachment_upload` returns `uploadUrl` (send the bytes there with
   one `PUT` and the returned `headers`, within 15 minutes; no `Authorization`)
   and `assetUrl`, the file's stable URL. `url` is the same file as a path
   (`/uploads/asset_….png`), the form the editor stores.
2. `create_attachment_from_upload` with the same target and `assetUrl` (the
   `uploadUrl` is accepted too) attaches the file and returns its `id`, `url`,
   `assetUrl`, `markdown` and `inserted`.
3. `insert` decides the body: `"append"` adds an image, video or file card at
   the end, `"none"` only records the attachment. Issues default to
   `"append"` (as before); documents default to `"none"`, so you place the
   returned `markdown` yourself with `save_document`: `![title](url)` for
   images, `[title](url)` for other files. In a document, a line holding only
   an image shows it inline.

The asset URL serves nothing until the attachment is created, and afterwards
only to people who can see the issue or document. Images, PDF, audio and video
open in place; every other type downloads. `delete_attachment` removes an
issue's or a document's attachment, and the file with it.
