# TypeScript SDK

`@flow/sdk` is a typed client for the [Flow REST API](api.md). It offers
a `FlowClient` with
`client.viewer`, `client.issues(…)` and `client.createIssue(…)`, Relay-style
connections with `fetchNext()`, typed errors, a webhook receiver and OAuth
helpers. Flow has no GraphQL API, so the SDK talks to the same REST endpoints
you would call with `curl`.

- Zero runtime dependencies. It needs `fetch`: Node.js 18 or newer, browsers,
  Bun, Deno and edge runtimes. Only the webhook helpers use `node:crypto`.
- ESM and CommonJS builds with bundled type declarations.
- Models, operations and query types are generated from
  [`openapi.json`](openapi.json); the ergonomic layer on top is hand-written.

Contents: [Install](#install) · [Authentication](#authentication) ·
[Client options](#client-options) · [Querying data](#querying-data) ·
[Pagination](#pagination) · [Mutations](#mutations) · [Files](#files) ·
[Errors](#errors) · [Rate limits and retries](#rate-limits-and-retries) ·
[Advanced usage](#advanced-usage) · [Webhooks](#webhooks) · [OAuth](#oauth) ·
[Generated code](#generated-code) · [Design notes](#design-notes)

## Install

The package lives in the repository at `sdk/typescript`. Build it once and
install it from the folder (or from a tarball made with `npm pack`):

```bash
cd sdk/typescript && npm install && npm run build
npm pack                      # produces flow-sdk-0.1.0.tgz
npm install ./flow-sdk-0.1.0.tgz   # in your project
```

```ts
import { FlowClient } from '@flow/sdk'
import { FlowWebhookClient } from '@flow/sdk/webhooks'
```

`@flow/sdk` has two entry points: the client, errors, connections and OAuth
helpers, and `@flow/sdk/webhooks` (signature verification and handlers). A
complete tour is in [`examples/sdk-quickstart.ts`](examples/sdk-quickstart.ts).

## Authentication

Create an API key in **Settings › API**, or use an OAuth access token
([oauth.md](oauth.md)). Both belong to one workspace and select it
automatically. Point the client at your deployment with `apiUrl` (or the
`FLOW_BASE_URL` environment variable):

```ts
import { FlowClient } from '@flow/sdk'

const client = new FlowClient({ apiKey: process.env.FLOW_API_KEY!, apiUrl: 'https://flow.example.com' })

// An OAuth access token works the same way …
const asUser = new FlowClient({ accessToken: token, apiUrl: 'https://flow.example.com' })

// … and may be a function that is called for every request, which is where
// you refresh an expiring token.
const refreshing = new FlowClient({ accessToken: () => tokenStore.getFreshAccessToken(), apiUrl })

const me = await client.viewer // user, workspace, role, scopes, application (OAuth)
const org = await client.organization
```

Connection options may also be passed as the second argument:
`new FlowClient({ apiKey }, { apiUrl, retry: true })`. `apiUrl` is the origin
of your deployment; a trailing `/api` is ignored.

## Client options

| Option | Meaning |
| --- | --- |
| `apiKey` / `accessToken` | Credentials; pass exactly one. `accessToken` may be `() => string \| Promise<string>`. |
| `apiUrl` | Deployment origin. Defaults to `FLOW_BASE_URL`, then `FLOW_API_URL`. |
| `fetch` | Custom `fetch` (proxies, instrumentation, tests). Defaults to the global one. |
| `headers` | Headers for every request, for example `X-Request-ID` or `X-Workspace-Key`. |
| `timeout` | Per-attempt timeout in milliseconds. Default 30,000; `0` disables. |
| `retry` | `true`, a number of retries, or [`RetryOptions`](#rate-limits-and-retries). Default off. |
| `signedFileUrlsExpireIn` | Seconds; every `/uploads/…` URL in JSON responses is replaced by a [signed URL](api.md#downloads-and-signed-urls). |
| `onRateLimit` | Called with the [rate-limit state](#rate-limits-and-retries) of every response that carries it. |
| `userAgent` | `User-Agent` on Node.js (default `flow-sdk-typescript/<version>`); `false` omits it. |

Every method also takes a final `RequestOptions` argument:
`{ signal, headers, timeout, retries }`.

## Querying data

Lists are connections. Each endpoint takes the arguments documented in
[api.md](api.md#lists-and-pagination): `first`, `after`, `last`, `before`,
`orderBy` (`createdAt` or `updatedAt`, newest first), `includeArchived` and a
`filter`.

```ts
const teams = await client.teams()
const users = await client.users({ first: 100, includeDisabled: false })
const states = await client.workflowStates({ teamId: team.id })
const labels = await client.issueLabels({ teamId: team.id, resourceType: 'issue' })
const cycles = await client.cycles({ teamId: team.id })

const projects = await client.projects({
  first: 20,
  orderBy: 'updatedAt',
  filter: {
    status: { type: { eq: 'started' } },
    updatedAt: { gt: '-P2W' },
    or: [{ lead: { email: { eq: 'ada@example.com' } } }, { teams: { key: { eq: 'ENG' } } }],
  },
})

await client.projectMilestones({ projectId })
await client.initiatives()
await client.documents({ teamId })
await client.comments({ issueId: 'ENG-123' }) // exactly one of issueId, documentId, projectId, initiativeId
await client.attachments({ issueId: 'ENG-123' })
await client.attachmentsForURL('https://support.example.com/tickets/42')
await client.customers({ filter: { tier: { eq: 'Enterprise' } } })
await client.customerRequests({ customerId })
await client.releases()
```

Unknown filter fields and comparators are `INVALID_INPUT` errors; each
endpoint's filterable fields are listed in the OpenAPI `filter` parameter.
`FlowFilter`, `StringComparator`, `NumberComparator` and `DateComparator` are
exported for typing filters.

### Issues

`client.issues` uses the issue query API (`GET /api/issue-records`) in its
connection form: cursor paging in both directions, at most 100 per page, newest
first unless you choose another `orderBy` (`createdAt`, `updatedAt`,
`priority`, `title`, `sortOrder`) and `orderDirection`. `last` without `before`
starts at the final page, and `fetchNext()` / `fetchPrevious()` both work.

```ts
const issues = await client.issues({
  first: 50,
  teamId: 'ENG',
  stateId: 'started',
  query: 'checkout',
  includeArchived: false,
  // The view filter tree used by saved views (an object or its JSON text).
  filter: { and: [{ field: 'priority', operator: 'is', values: ['urgent'] }] },
  includeTotal: true,
})
issues.totalCount // matching issues in total (with includeTotal)

const latest = await client.issues({ last: 20 }) // the oldest 20 by the default order, i.e. the final page
await latest.fetchPrevious()

const issue = await client.issue('ENG-123') // an id or an identifier
```

## Pagination

A connection has `nodes`, `pageInfo` (`hasNextPage`, `hasPreviousPage`,
`startCursor`, `endCursor`) and the means to move:

```ts
const issues = await client.issues({ first: 50 })

// fetchNext() appends the next page to issues.nodes (fetchPrevious() prepends the one before).
while (issues.pageInfo.hasNextPage) await issues.fetchNext()

// Iterate lazily over this page and every following one. The connection is not modified.
for await (const issue of await client.issues({ first: 100 })) {
  console.log(issue.identifier)
}

// Page by page, or everything (with an optional cap).
for await (const page of connection.pages()) process(page)
const first500 = await connection.all({ limit: 500 })
```

Cursors are opaque and stay valid when new resources are created. Page
backward with `client.teams({ last: 25 })` and `fetchPrevious()`; the delivery
log of a webhook only pages forward. Routes that predate connections answer
with an array or a page unless a paging argument is given; the SDK always asks
for the connection, and `client.api.get<Name>Legacy` (for example
`getDocumentsLegacy`) returns the original response.

## Mutations

```ts
const issue = await client.createIssue({
  teamId: team.id,
  title: 'Customer cannot log in',
  description: 'Reported through the support desk.',
  priority: 2,
  labelIds: [bugLabel.id],
})

await client.updateIssue(issue.id, { stateId: started.id, assigneeId: me.user.id })
await client.archiveIssue(issue.id)

const comment = await client.createComment({ issueId: 'ENG-123', body: 'Fixed in 4.2.' })
await client.updateComment(comment.id, { issueId: 'ENG-123', body: 'Fixed in 4.2.1.', resolved: true })

await client.createProject({ name: 'Mobile app', teamIds: [team.id] })
await client.updateProject(project.id, { statusId })
await client.createInitiative({ name: 'Q4 reliability' })
```

Updates accept `expectedVersion` for optimistic concurrency. Issue updates
accept a stale version when change history proves the patched fields stayed
unchanged. Same-field changes, missing history, and stale versions on other
resources raise `ConflictFlowError` with `code === 'VERSION_CONFLICT'` and the
server's `current` resource.

### Link attachments

The URL identifies a link attachment on its issue, so syncing is idempotent:

```ts
const { attachment, created } = await client.upsertLinkAttachment('ENG-123', {
  url: 'https://support.example.com/tickets/42',
  title: 'Ticket 42',
  subtitle: 'Open · Priority high',
  iconUrl: 'https://support.example.com/icon.png',
  metadata: { status: 'open', priority: 'high' },
})
// created === true for HTTP 201, false when an existing attachment was updated (200)
```

### Customers

```ts
// Matches by id, then externalId/externalIds, then domain; creates when nothing matches (name required).
const { customer, created } = await client.upsertCustomer({
  externalId: 'crm-1042',
  name: 'Acme',
  domains: ['acme.com'],
  tier: 'Enterprise',
})
await client.createCustomerRequest({ customerExternalId: 'crm-1042', issueId: 'ENG-123', attachmentUrl })
```

### Documents, milestones, updates

```ts
const doc = await client.createDocument({ title: 'Runbook', content: '# Steps', projectIds: [project.id] })
await client.updateDocument(doc.id, { content: 'Edited', expectedVersion: doc.version }) // 409 on a concurrent change
await client.deleteDocument(doc.id)

const milestone = await client.createProjectMilestone(project.id, { name: 'Beta', targetDate: '2026-12-01' })
await client.reorderProjectMilestones(project.id, [milestone.id, ...others])
await client.createProjectUpdate(project.id, { body: 'On track', health: 'onTrack' })
const updates = await client.projectUpdates(project.id) // newest first, not paged
await client.createInitiativeUpdate(initiative.id, { body: 'Going well', health: 'onTrack' })
```

Single records: `client.team(idOrKey)`, `user(idOrMe)`, `workflowState`,
`issueLabel`, `cycle`, `project(idOrSlug)`, `initiative(idOrSlug)` and
`customer`.

### Acting for a person (`actor=app`)

Application tokens can show "Mark via Acme Sync" with `createAsUser` and
`displayIconUrl` on `createIssue`, `createComment`, `createDocument` and
`upsertLinkAttachment`; the fields are part of the generated input types
([oauth.md](oauth.md#via-app-attribution)).

### Webhooks, audit log

Administrators (or the `admin` scope) manage webhooks and read the audit log:

```ts
const { secret, ...webhook } = await client.createWebhook({ label: 'CI', url: 'https://ci.example.com/hooks/flow', resourceTypes: ['Issue', 'Comment'] })
await client.updateWebhook(webhook.id, { enabled: false })
await client.rotateWebhookSecret(webhook.id) // the new secret is returned once
await client.testWebhook(webhook.id)
for await (const delivery of await client.webhookDeliveries(webhook.id, { status: 'failed' })) console.log(delivery.id)
await client.revokeWebhookSecret(webhook.id) // 204: deliveries go out unsigned until you rotate

const log = await client.auditLog({ type: 'customer.created', since: '-P7D' })
const types = await client.auditLogTypes()
```

## Files

`uploadFile` runs Flow's upload flow: `POST /api/files` to prepare, `PUT` of the
bytes to the upload URL (without your credentials: the token in the URL is the
credential), then you use the `assetUrl`.

```ts
import { readFile } from 'node:fs/promises'

const upload = await client.uploadFile(await readFile('screenshot.png'), {
  filename: 'screenshot.png',
  contentType: 'image/png',
})
await client.createComment({ issueId: 'ENG-123', body: `![screenshot](${upload.assetUrl})` })
```

The body can be a `Blob`/`File` (name and type are read from it), an
`ArrayBuffer`, a typed array or `Buffer`, or a string (sent as UTF-8). The size
is measured for you; uploads are limited to 20 MB. `prepareFileUpload` does only
the first step.

Files are private. `downloadFile(url)` fetches one with your credentials and
`signFileUrls(urls, expiresIn)` returns short-lived URLs that work without
credentials (for an `<img>` tag or a chat unfurl). Credentials are never sent to
another origin.

## Errors

Every failure is a `FlowError` with `type`, `code`, `status`, `requestId`,
`method`, `url`, `body` and, when the server sent them, `rateLimit` details.
Switch on the class or on `type`:

| Class | `type` | HTTP |
| --- | --- | --- |
| `InvalidInputFlowError` | `INVALID_INPUT` | 400, 405, 413, 415, 422 |
| `AuthenticationFlowError` | `AUTHENTICATION_ERROR` | 401 |
| `ForbiddenFlowError` | `FORBIDDEN` | 403 |
| `FeatureNotAccessibleFlowError` | `FEATURE_NOT_ACCESSIBLE` | 403 |
| `NotFoundFlowError` | `NOT_FOUND` | 404, 410 |
| `ConflictFlowError` | `CONFLICT` | 409, 412 |
| `RatelimitedFlowError` | `RATELIMITED` | 429 |
| `InternalFlowError` | `INTERNAL_ERROR` | 500 |
| `UnavailableFlowError` | `UNAVAILABLE` | 502, 503, 504 |
| `NetworkFlowError`, `TimeoutFlowError` | `NETWORK_ERROR`, `TIMEOUT` | no response |
| `AbortFlowError` | `ABORTED` | your `AbortSignal` fired |
| `FlowOAuthError` | `OAUTH_ERROR` | RFC 6749 errors from `/oauth/*` |

```ts
import { FlowError, NotFoundFlowError, RatelimitedFlowError, isFlowError } from '@flow/sdk'

try {
  await client.updateIssue('ENG-123', { title: 'New title' })
} catch (error) {
  if (error instanceof NotFoundFlowError) …
  else if (error instanceof RatelimitedFlowError) wait(error.retryAfter) // seconds
  else if (isFlowError(error)) log(error.type, error.code, error.requestId)
  else throw error
}
```

`code` equals `type` unless the endpoint reports something more specific
(`VERSION_CONFLICT`, `mfa_required`). Include `requestId` when reporting a
problem. Responses that are not Flow's envelope (a gateway page) are mapped
from the HTTP status. `FlowConfigurationError` signals invalid client
construction (no credentials, no URL).

## Rate limits and retries

Flow limits requests and query complexity per credential
([api.md](api.md#rate-limits)). `client.rateLimit` holds the state of the last
response that carried it, errors carry it as `error.rateLimit`, and
`onRateLimit` lets you observe every response:

```ts
client.rateLimit?.requestsRemaining
client.rateLimit?.complexityRemaining
client.rateLimit?.requestsResetAt // Date, when the bucket is full again
```

Retries are off by default. Turn them on to retry HTTP 429, 5xx and network
errors with exponential backoff and jitter. `Retry-After` is honoured; if it
exceeds `maxDelayMs` the error is thrown instead of waiting.

```ts
new FlowClient({ apiKey, apiUrl }, {
  retry: { retries: 4, baseDelayMs: 500, maxDelayMs: 30_000, jitter: true },
})
```

A 429 is always safe to retry because the request was not processed. 5xx
responses and network errors are retried only for idempotent requests (`GET`,
`PUT`, `DELETE`), unless you set `retryNonIdempotent: true` for `POST` and
`PATCH`. Aborting never retries. Prefer [webhooks](#webhooks) to polling.

## Advanced usage

```ts
// Custom fetch (proxy, tracing, mocks), default headers, timeout.
const client = new FlowClient({
  apiKey,
  apiUrl,
  fetch: instrumentedFetch,
  headers: { 'X-Request-ID': crypto.randomUUID() },
  timeout: 10_000,
})

// Cancel one request.
const controller = new AbortController()
setTimeout(() => controller.abort(), 2_000)
await client.issues({ first: 100 }, { signal: controller.signal })

// Any route, typed or not. `path` starts with /api/…; errors are mapped as usual.
const delivery = await client.request<{ id: string }>('POST', '/api/webhooks/webhook_1/test')
const history = await client.request('GET', '/api/projects/proj_1/history', { query: { first: 20 } }) // objects in `query` are sent as JSON
await client.request('POST', '/api/issues/ENG-123/share')

// The generated API: one method per typed operation, exactly as in openapi.json.
await client.api.getIssueLabels({ query: { teamId, first: 100 } })
await client.api.patchIssuesId({ path: { id: 'ENG-123' }, body: { title: 'Renamed' } })
```

`expect: 'text' | 'none' | 'response'` on `client.request` returns the body as
text, nothing, or the `Response`. Absolute URLs must be on the API origin.

## Webhooks

[Webhooks](webhooks.md) are signed with a hex HMAC-SHA256 of the raw body in
`Flow-Signature`, and carry a signed `webhookTimestamp`. The webhook helpers
verify both and give you typed events. They are in `@flow/sdk/webhooks`
because they use `node:crypto` (Node.js, Bun, Deno, Cloudflare Workers with
`nodejs_compat`).

Event types (`WebhookEvent`, `IssueWebhookEvent`, `WebhookEventOf<'Comment'>`,
...) are generated from the `webhooks` section of `openapi.json`, which is
described by the same Go types the server's deliveries are checked against.
Entity `data` is the REST model with only `id` guaranteed (`IssuePartial`, ...).

### Verify a request yourself

```ts
import { constructWebhookEvent, verifyWebhookSignature, WebhookVerificationError } from '@flow/sdk/webhooks'

// Raw bytes in, boolean out. Use the exact body that was received.
verifyWebhookSignature(rawBody, signatureHeader, secret)

// Signature + 60 second replay window (on the signed body timestamp) + parsing.
try {
  const event = constructWebhookEvent(rawBody, signatureHeader, secret)
  if (event.type === 'Issue') console.log(event.action, event.data.identifier)
} catch (error) {
  if (error instanceof WebhookVerificationError) {
    // error.reason: missing_signature | invalid_signature | invalid_payload | missing_timestamp | stale_timestamp
    return reply(401)
  }
  throw error
}
```

Pass an array of secrets to accept the old and the new secret while you
rotate. Use `toleranceMs` to change the replay window or `null` to skip it.

### A receiver with typed handlers

```ts
import express from 'express'
import { FlowWebhookClient } from '@flow/sdk/webhooks'

const webhooks = new FlowWebhookClient(process.env.FLOW_WEBHOOK_SECRET!)

webhooks.on('Issue', async (event, { deliveryId }) => {
  // event is narrowed to the Issue event: action, data, updatedFrom, actor, …
  if (event.action === 'create') await notify(event.data.identifier)
})
webhooks.on('Comment', event => console.log(event.data.issueId))
webhooks.on('*', event => audit(event.type, event.eventId)) // every delivery, also types this SDK does not know

const app = express()
// The handler needs the raw body: use express.raw() (or no body parser).
app.post('/hooks/flow', express.raw({ type: 'application/json' }), webhooks.createNodeHandler())
app.listen(8080)
```

For fetch-style runtimes (Next.js route handlers, Bun, Deno, Workers, Hono):

```ts
export const POST = webhooks.createFetchHandler()
```

Both handlers answer 401 for a bad signature or a stale timestamp, 400 for a
body that is not an event, 413 for bodies over 5 MiB and 500 when a handler
throws (so Flow retries). Flow expects an answer within 5 seconds: pass
`background: true` to answer 200 right after verification and run the handlers
afterwards (errors go to `onError`). Deduplicate on `event.eventId` or the
`deliveryId` in the handler context.

Event types (`WebhookEvent`) cover every payload in [webhooks.md](webhooks.md):
`Issue`, `Attachment`, `Comment`, `IssueLabel`, `Reaction`, `IssueRelation`,
`Project`, `ProjectUpdate`, `ProjectMilestone`, `Document`, `Initiative`,
`InitiativeUpdate`, `Cycle`, `Customer`, `CustomerNeed`, `User`, `Release`,
`ReleaseNote`, `IssueSLA`, `WebhookTest` and the application events
`AgentSessionEvent`, `AppUserNotification`, `PermissionChange` and `OAuthApp`.
Entity `data` is the REST model, with every field except `id` optional.
`computeWebhookSignature(rawBody, secret)` signs a body, which helps when you
test your receiver.

## OAuth

`FlowOAuthClient` covers the [OAuth flows](oauth.md) with PKCE and no extra
dependencies:

```ts
import { FlowClient, FlowOAuthClient } from '@flow/sdk'

const oauth = new FlowOAuthClient({
  apiUrl: 'https://flow.example.com',
  clientId: process.env.FLOW_CLIENT_ID!,
  clientSecret: process.env.FLOW_CLIENT_SECRET, // omit for public (PKCE-only) clients
  redirectUri: 'https://app.example.com/oauth/callback',
})

// 1. Send the browser to Flow. Keep `state` and `codeVerifier` in the session.
const { url, state, codeVerifier } = await oauth.startAuthorization({
  scope: ['read', 'write', 'issues:create'],
  actor: 'user', // or 'app' to install the application for the workspace
})

// 2. On the callback: checks `state`, throws FlowOAuthError for access_denied.
const { code } = oauth.parseCallback(callbackUrl, state)
const tokens = await oauth.exchangeCode({ code, codeVerifier })

const client = new FlowClient({ accessToken: tokens.accessToken, apiUrl })

// 3. Access tokens last one hour. Refresh tokens rotate: store the new one.
const next = await oauth.refreshToken(tokens.refreshToken!)

// Revoke (a refresh token revokes its whole chain) and introspect.
await oauth.revokeToken(next.refreshToken!, 'refresh_token')
await oauth.introspectToken(next.accessToken)

// Server-to-server, no browser: an actor=app token (Client credentials must be enabled).
const app = await oauth.clientCredentials({ scope: ['read', 'write'] })
```

`OAuthTokens` has `accessToken`, `refreshToken`, `expiresAt`, `scope` (a list),
`actor` and the raw response. Confidential clients authenticate with HTTP Basic
by default; set `clientAuthentication: 'post'` for `client_secret` in the form
or `'none'` for public clients. Use `createPkcePair()` and `authorizationUrl()`
if you manage `state` yourself. Pair `new FlowClient({ accessToken: () => … })`
with `refreshToken` to refresh on demand.

## Generated code

`sdk/typescript/src/generated/` is produced from [`openapi.json`](openapi.json):
`models.ts` (one type per component schema, including the payload of every
webhook event), `operations.ts` (request and query types, the `operations`
table and the `FlowApi` class with one method per typed operation, and a
`...Legacy` method for list routes with two response shapes) and `webhooks.ts`
(the `WebhookEvent` union built from the `webhooks` section of the document). The generator needs only Node.js:

```bash
node sdk/typescript/scripts/generate.mjs           # regenerate
node sdk/typescript/scripts/generate.mjs --check   # CI: fails when the generated files are stale
```

After changing routes or contract types, run `node scripts/generate-openapi.mjs`
first, then regenerate the SDK. Routes marked `x-flow-internal` in
`openapi.json` serve the web app and are not part of the contract; they are
reachable through `client.request()` but may change.

Develop the package with `npm install`, `npm run build`, `npm test` and
`npx tsc --noEmit` inside `sdk/typescript`.

## Design notes

- REST, not GraphQL: no query documents and no field selection. Methods return
  whole resources, and connections are fetched with the REST pagination
  arguments.
- Results are plain JSON objects, not model classes: there are no lazy
  relation fetchers (`await issue.team`) or `issue.update()`. Use ids
  (`issue.team.id`) and the client methods.
- Mutations return the resource directly instead of a `{ success, … }` payload;
  failures throw.
- Nested resources take their parent in the input: `createComment({ issueId, … })`,
  `updateComment(commentId, { issueId, … })`.
- `fetchNext()` appends to the connection in place; `for await` over a
  connection walks every page.
- `client.rateLimit`, `error.rateLimit` and opt-in `retry` replace hand-written
  backoff around rate-limit errors.
- Versioning: the SDK follows Flow's release version. The REST API is
  additive-only ([versioning policy](api.md#versioning-and-deprecations)), so
  ignore unknown fields.
