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
`before` or `orderBy`) is present.

Issues use the issue query API, which predates connections:
`GET /api/issue-records?limit=50&cursor=…&sort=updatedAt&direction=desc`
returns `items`, `hasMore` and `nextCursor`, and `filter` takes the view
filter tree used by saved views. `GET /api/issue-records/{id}` accepts an id
or an identifier such as `ENG-123`.

Comments on issues and documents are read from an index and are ordered by
`createdAt` only; they do not accept `filter`.

Prefer webhooks over polling. When you must poll, order by `updatedAt` and
stop at the first node you have already seen.

## Filtering

`filter` is a JSON object in the style of the reference app's filters. Conditions in one
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

## Rate limits

Flow uses the reference app's leaky-bucket model: each budget refills continuously at
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

Uploading follows the reference app's `fileUpload` flow: prepare, PUT the bytes, then use
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

Link attachments connect an issue to an external resource
`attachmentCreate`:

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

Customer requests (the reference app's customer needs) are created with
`POST /api/customer-requests`. Select the customer with `customerId` or
`customerExternalId`; `attachmentUrl` adds a link attachment to the request's
issue (`issueId`) and becomes the request's `sourceUrl`. Update with
`PATCH /api/customer-requests/{id}`. Application tokens need `customer:read` /
`customer:write`.

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

## Prefilled issue links

Open the issue composer with fields already set, like linear.new:

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
