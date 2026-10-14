# Webhooks

Flow sends an HTTP `POST` to a URL of your choice when data in a workspace is
created, updated or removed. Use webhooks to keep another system in sync, start
CI jobs or post messages, instead of polling the REST API.

The contract: an envelope with the fields `action`, `type`, `data`, `url`,
`updatedFrom`, `actor`, `createdAt`, `organizationId`, `webhookId` and
`webhookTimestamp`, payload type names such as `Issue`, `Comment` and
`IssueLabel`, an HMAC-SHA256 signature of the raw body and a fixed retry
schedule. Header names start with `Flow-`.

- [Create a webhook](#create-a-webhook)
- [Events](#events)
- [Request headers](#request-headers)
- [Payload](#payload)
- [Securing webhooks](#securing-webhooks)
- [Retries, delivery log and disabling](#retries-delivery-log-and-disabling)
- [Application webhooks](#application-webhooks)
- [Agent session events](#agent-session-events)
- [API reference](#api-reference)

## Create a webhook

Workspace administrators (or an API key / OAuth token with the `admin` scope)
manage webhooks in **Settings › API › Webhooks**. Click **New webhook**, enter a
label and the endpoint URL, choose the data change events and the teams, and
save. The signing secret is shown **once**; copy it into your receiver's
configuration.

Your endpoint must:

- use a publicly reachable `https://` URL (loopback, private-network and
  `.local`/`.internal` hosts are rejected; a development server started with
  authentication disabled may also use `http://localhost`),
- answer with a `2xx` status within **5 seconds**.

The same can be done over the API:

```bash
curl --fail-with-body "$FLOW_BASE_URL/api/webhooks" \
  -H "Authorization: Bearer $FLOW_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"label":"CI","url":"https://ci.example.com/hooks/flow","resourceTypes":["Issue","Comment"],"teamId":"TEAM_ID"}'
```

`resourceTypes` accepts payload type names (`Issue`, `IssueLabel`) or Flow ids
(`issues`, `labels`). `teamId` restricts the webhook to one team, `teamIds` to
several; omit both (or send `"allPublicTeams": true`) for every team you can
access. `label` is an alias of `name`. The response includes `secret`, the
signing secret, which is never returned again.

## Events

| Resource type (`resourceTypes`) | Payload `type` | Sent when |
| --- | --- | --- |
| `issues` | `Issue` | An issue is created, updated (including archive), or deleted |
| `attachments` | `Attachment` | A file or link attachment is added to or removed from an issue |
| `comments` | `Comment` | A comment on an issue, project, project update, initiative, initiative update or document is created, edited, resolved or deleted |
| `labels` | `IssueLabel` | A label is created, updated, merged or deleted |
| `reactions` | `Reaction` | An emoji reaction is added (`create`) or removed (`remove`) on an issue, a comment, a project update or an initiative update |
| `relations` | `IssueRelation` | An issue relation is added or removed |
| `projects` | `Project` | A project is created, updated or deleted |
| `project_updates` | `ProjectUpdate` | A project update is posted, edited or deleted |
| `milestones` | `ProjectMilestone` | A project milestone is created, updated, deleted or moved by a reordering |
| `documents` | `Document` | A document is created, updated or deleted |
| `initiatives` | `Initiative` | An initiative is created, updated or deleted |
| `initiative_updates` | `InitiativeUpdate` | An initiative update is posted, edited or deleted |
| `cycles` | `Cycle` | A cycle is created, started, completed, updated or deleted |
| `customers` | `Customer` | A customer is created, updated, merged or deleted |
| `customer_requests` | `CustomerNeed` | A customer request is created, updated or deleted |
| `users` | `User` | A workspace member changes |
| `releases` | `Release` | A release is created, updated, completed or deleted |
| `release_notes` | `ReleaseNote` | A release note is created, updated or deleted |
| `issue_sla` | `IssueSLA` | An SLA is set on an issue (`set`), is close to breaching (`highRisk`), or breached (`breached`) |

For `ProjectUpdate`, `ProjectMilestone` and `InitiativeUpdate` events, `data`
is the update or milestone itself (with `projectId` or `initiativeId`; for
`remove`, as it was before it was deleted) and `url` points to it. Milestones
also carry `sortOrder`, their position in the project (from 0). Reordering a
project's milestones sends one `update` event per milestone whose position
changed (`eventType` `project.milestone_reordered`, `updatedFrom` `{"sortOrder":
<old position>}`) to current-format webhooks; webhooks still on the version 1
format receive the single `project.milestones_reordered` event as before.

Application webhooks can additionally subscribe to `agent_session_events`
(`AgentSessionEvent`), `app_user_notifications` (`AppUserNotification`),
`permission_changes` (`PermissionChange`) and `oauth_app` (`OAuthApp`); see
[Application webhooks](#application-webhooks).

Internal events (collaborative editing sync, drafts, reminders, triage
suggestions, thread subscriptions) are never sent.

## Request headers

```http
POST /hooks/flow HTTP/1.1
Content-Type: application/json; charset=utf-8
Accept-Charset: utf-8
User-Agent: Flow-Webhook
Flow-Delivery: whd_5b8e0f0c1f6b4fd2a1d9b3c4e5f60718
Flow-Event: Issue
Flow-Timestamp: 1791822527821
Flow-Attempt: 1
Flow-Signature: 766e1d90a96e2f5ecec342a99c5552999dd95d49250171b902d703fd674f5086
```

| Header | Description |
| --- | --- |
| `Flow-Delivery` | Unique id of this delivery. A redelivery gets a new id. |
| `Flow-Event` | The payload `type` (`Issue`, `Comment`, ...). |
| `Flow-Timestamp` | Unix time in milliseconds when the attempt was sent (also `webhookTimestamp` in the body). |
| `Flow-Attempt` | 1 for the first attempt, up to 4. |
| `Flow-Signature` | Hex HMAC-SHA256 of the raw body, keyed with the signing secret. |
| `Flow-Signature-Status` | `unsigned` when the webhook has no signing secret (see [legacy webhooks](#webhooks-created-before-signing)); absent otherwise. |
| `X-Flow-Delivery`, `X-Flow-Event`, `X-Flow-Signature` | Older names of the same values, kept for existing receivers. |

## Payload

Every payload type is described as a JSON Schema in the `webhooks` section of
[`openapi.json`](openapi.json) (OpenAPI 3.1), generated from the Go types the
server's deliveries are tested against; entity `data` is a `*Partial` schema
because only `id` is guaranteed.

### Data change events

Every data change event has these fields:

| Field | Description |
| --- | --- |
| `action` | `create`, `update` or `remove` (`IssueSLA`, `OAuthApp`, `PermissionChange` and `AppUserNotification` use their own actions). |
| `type` | The entity type, as in the [events table](#events). |
| `actor` | Who made the change: `{id, type, name, email, url}` with `type` `user` or `application`; `null` for system changes. |
| `createdAt` | When the change happened (ISO 8601). |
| `data` | The entity after the change; for `remove`, the entity before it was removed. |
| `url` | The entity's page in Flow, when it has one. |
| `updatedFrom` | For `update`, the previous values of the changed properties. |
| `organizationId` | The workspace id. |
| `webhookId` | The webhook this delivery belongs to. |
| `webhookTimestamp` | Unix time in milliseconds when the delivery was sent. Signed. |
| `eventId` | Flow's id of the change. Every webhook receiving the same change gets the same `eventId`; use it to deduplicate. |
| `eventType` | Flow's detailed event (`issue.updated`, `comment.resolved`, `project.milestone_created`, ...). |

```json
{
  "action": "update",
  "type": "Issue",
  "actor": {
    "id": "usr_7f2c",
    "type": "user",
    "name": "Ada Lovelace",
    "email": "ada@example.com",
    "url": "https://flow.example.com/acme/profiles/ada"
  },
  "createdAt": "2026-10-12T16:28:47.818Z",
  "data": {
    "id": "issue_105131",
    "identifier": "ENG-42",
    "title": "Retry failed payments",
    "state": { "id": "state_started", "name": "In Progress", "type": "started" },
    "team": { "id": "team_eng", "key": "ENG", "name": "Engineering" },
    "priority": 2,
    "updatedAt": "2026-10-12T16:28:47.811Z"
  },
  "url": "https://flow.example.com/acme/issue/ENG-42/retry-failed-payments",
  "updatedFrom": { "title": "Retry payments", "updatedAt": "2026-10-11T09:02:13.004Z" },
  "organizationId": "workspace_acme",
  "webhookId": "webhook_1791822527794052000",
  "eventId": "evt_1791822527818889000",
  "eventType": "issue.updated",
  "webhookTimestamp": 1791822527821
}
```

Comments carry `issueId` (or their parent resource) and their `url` points at
the comment (`#comment-<id>`). A `Reaction` payload is
`{id, emoji, userId, issueId?, commentId?, projectUpdateId?, initiativeUpdateId?}`
with `action` `create` when the reaction was added and `remove` when it was
taken back.

### Other events

`IssueSLA`:

```json
{
  "action": "breached",
  "type": "IssueSLA",
  "createdAt": "2026-10-12T17:00:00.000Z",
  "issueData": { "id": "issue_105131", "identifier": "ENG-42", "title": "Retry failed payments" },
  "data": { "id": "issue_sla_1", "issueId": "issue_105131", "ruleId": "sla_rule_1", "dueAt": "2026-10-12T17:00:00Z", "status": "breached" },
  "url": "https://flow.example.com/acme/issue/ENG-42/retry-failed-payments",
  "organizationId": "workspace_acme",
  "webhookId": "webhook_1",
  "webhookTimestamp": 1791824400000
}
```

`set` is sent when an SLA starts, `highRisk` once when less than a quarter of
the SLA window (at most 24 hours) remains, `breached` when the deadline passes.

`OAuthApp` (application webhooks), sent when a workspace uninstalls the
application or revokes its authorization:

```json
{ "action": "revoked", "type": "OAuthApp", "oauthClientId": "flow_client_...", "organizationId": "workspace_acme", "webhookId": "webhook_app_...", "createdAt": "...", "webhookTimestamp": 1791824400000 }
```

`PermissionChange` (application webhooks), sent when the teams the
application can access change:

```json
{ "action": "teamAccessChanged", "type": "PermissionChange", "oauthClientId": "flow_client_...", "appUserId": "app_...", "addedTeamIds": ["team_ops"], "removedTeamIds": [], "organizationId": "workspace_acme", "webhookId": "webhook_app_...", "createdAt": "...", "webhookTimestamp": 1791824400000 }
```

`AppUserNotification` (application webhooks) forwards the inbox of the
application's user. `action` is one of `issueMention`, `issueCommentMention`,
`issueEmojiReaction`, `issueCommentReaction`, `issueAssignedToYou`,
`issueUnassignedFromYou`, `issueNewComment` and `issueStatusChanged`
(assignment covers both assignee and delegate):

```json
{
  "action": "issueCommentMention",
  "type": "AppUserNotification",
  "oauthClientId": "flow_client_...",
  "appUserId": "app_...",
  "notification": {
    "id": "notification_...",
    "type": "issueCommentMention",
    "issueId": "issue_105131",
    "commentId": "comment_...",
    "actorId": "usr_7f2c",
    "issue": { "id": "issue_105131", "identifier": "ENG-42", "title": "Retry failed payments", "teamId": "team_eng", "url": "..." },
    "comment": { "id": "comment_...", "body": "@Deploy bot can you check this?", "userId": "usr_7f2c" }
  },
  "organizationId": "workspace_acme",
  "webhookId": "webhook_app_...",
  "createdAt": "...",
  "webhookTimestamp": 1791824400000
}
```

`WebhookTest` is sent by **Send test event** (and `POST /api/webhooks/{id}/test`):
`{"action":"test","type":"WebhookTest","data":{"webhookId":"...","message":"This is a test event from Flow."}, ...}`.

### Webhooks created before signing

Webhooks created before this envelope existed keep the version 1 format until
an administrator switches them (**Use current format** on the webhook page, or
`PATCH /api/webhooks/{id}` with `{"payloadVersion": 2}`). Version 1 uses Flow
resource names as `type` (`issues`), the raw event verb as `action`
(`created`, `status_changed`), the mutation input as `data`, `previousValues`
for changed properties and `id` for the event id. Version 1 bodies now also
carry `actor`, `url`, `updatedFrom`, `organizationId`, `webhookId`,
`eventType` and the signed `webhookTimestamp`. New webhooks use version 2.

Those webhooks also kept only a hash of their secret, which cannot sign
anything. Their deliveries are sent with `Flow-Signature-Status: unsigned` and
the webhook page asks you to **Rotate** the secret; after rotating, every
delivery is signed with the new secret (shown once).

## Securing webhooks

Verify every request before acting on it:

1. Compute the hex HMAC-SHA256 of the **raw** request body with your signing
   secret and compare it, in constant time, with `Flow-Signature`. Do not
   re-serialize parsed JSON; the signature covers the exact bytes sent.
2. Reject the request when `webhookTimestamp` (from the verified body) is more
   than 60 seconds away from your clock, to prevent replays. Prefer the body
   field over the `Flow-Timestamp` header: only the body is signed.
3. Deduplicate on `Flow-Delivery` (one delivery retried) or `eventId` (one
   change delivered to several webhooks or redelivered).

Node.js (Express):

```js
import { createHmac, timingSafeEqual } from 'node:crypto'
import express from 'express'

const secret = process.env.FLOW_WEBHOOK_SECRET

function verify(rawBody, signature) {
  if (typeof signature !== 'string' || !/^[a-f0-9]{64}$/i.test(signature)) return false
  const expected = createHmac('sha256', secret).update(rawBody).digest()
  return timingSafeEqual(expected, Buffer.from(signature, 'hex'))
}

const app = express()
app.post('/hooks/flow', express.raw({ type: 'application/json' }), (req, res) => {
  if (!verify(req.body, req.get('flow-signature'))) return res.sendStatus(401)
  const event = JSON.parse(req.body.toString('utf8'))
  if (Math.abs(Date.now() - event.webhookTimestamp) > 60_000) return res.sendStatus(401)
  // Hand the event to a queue and answer within 5 seconds.
  enqueue(event)
  res.sendStatus(200)
})
app.listen(8080)
```

Go:

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

var secret = []byte(os.Getenv("FLOW_WEBHOOK_SECRET"))

func flowWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	given, err := hex.DecodeString(r.Header.Get("Flow-Signature"))
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if err != nil || !hmac.Equal(given, mac.Sum(nil)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	var event struct {
		Type             string          `json:"type"`
		Action           string          `json:"action"`
		Data             json.RawMessage `json:"data"`
		WebhookTimestamp int64           `json:"webhookTimestamp"`
	}
	if json.Unmarshal(body, &event) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if age := time.Since(time.UnixMilli(event.WebhookTimestamp)); age > time.Minute || age < -time.Minute {
		http.Error(w, "stale delivery", http.StatusUnauthorized)
		return
	}
	// Process asynchronously; answer within 5 seconds.
	w.WriteHeader(http.StatusOK)
}

func main() {
	http.HandleFunc("/hooks/flow", flowWebhook)
	http.ListenAndServe(":8080", nil)
}
```

Rotate the secret from the webhook page (or
`POST /api/webhooks/{id}/rotate-secret`) if it leaks. Rotation takes effect
immediately for new attempts, so deploy the new secret to your receiver right
away (accepting both secrets for a few minutes avoids rejecting in-flight
retries). **Revoke** removes the secret; deliveries then go out unsigned
until a new secret is rotated in.

Flow resolves the webhook host on every attempt and refuses private,
loopback and link-local addresses, and does not follow redirects to them.

## Retries, delivery log and disabling

A delivery fails when the endpoint cannot be reached, does not answer within
5 seconds, or answers with a non-`2xx` status. A failed delivery is retried
**after 1 minute, after 1 hour and after 6 hours** (4 attempts in total). Each
attempt has a fresh `Flow-Timestamp`, `webhookTimestamp` and signature, and the
same `Flow-Delivery`.

Deliveries are stored in the database before they are sent, so a restart or
deploy does not lose queued retries, and any Flow instance can send them (an
instance claims a delivery with a lease; a claim of a stopped instance expires
after a minute). The delivery log keeps 30 days of deliveries.

The webhook page in **Settings › API › Webhooks** shows **Recent deliveries**:
time, event, status (`Pending`, `Retrying`, `Delivered`, `Failed`,
`Canceled`), response code, duration and attempts. **View** opens the request
body, every attempt (status, duration, error) and the response body (first
2 KB). **Redeliver** sends the same payload again as a new delivery with a new
id, timestamp and signature; **Send test event** sends a `WebhookTest` event
and shows the result.

When deliveries keep failing for 24 hours with no successful delivery in
between, Flow disables the webhook when the next delivery exhausts its retries,
records it in the audit log and notifies the webhook's creator in their inbox
(and by email, according to their notification settings). Fix the endpoint,
then **Enable webhook** on the webhook page (or `PATCH /api/webhooks/{id}` with
`{"enabled": true}`); re-enabling resets the failure window. Disabling a webhook
cancels its queued retries.

## Application webhooks

An OAuth application's developer can configure the webhook once, for every
workspace that installs the application. Open the application in
**Settings › API › OAuth applications**, then **Application webhook**: turn it
on, set the HTTPS URL and choose the events, including the application-only
ones:

| Resource type | Payload `type` | Description |
| --- | --- | --- |
| `agent_session_events` | `AgentSessionEvent` | [Agent sessions](#agent-session-events) created for or prompted to the application |
| `app_user_notifications` | `AppUserNotification` | The application user's inbox (mentions, assignment, comments, status changes, reactions) |
| `permission_changes` | `PermissionChange` | The teams the application can access changed |
| `oauth_app` | `OAuthApp` | `revoked`: always sent when a workspace uninstalls or revokes the application |

Saving the first time creates the application's signing secret, shown once;
**Rotate** issues a new one. The same configuration can come from an
[application manifest](oauth.md) (`webhook.enabled`, `webhook.url`,
`webhook.resourceTypes`), or over the API with
`PUT /api/oauth-applications/{id}/webhook`.

When a workspace installs the application (`actor=app`) or a person authorizes
it (`actor=user`), Flow creates a managed webhook in that workspace. It delivers
to your URL, signed with the application's secret, and only for the teams the
application can access (all public teams for a user authorization). Workspace
administrators see it in their webhook list and delivery log and can pause it,
but cannot change its URL or events or delete it. When the application is
uninstalled or its last authorization is revoked, the managed webhook is
removed and an `OAuthApp` `revoked` event is sent. Changes of team access send
`PermissionChange`.

An installation's own webhook URL (set by the installing administrator in
**Members › Applications**) still takes precedence for agent session events.

## Agent session events

`AgentSessionEvent` deliveries tell an agent about work: `action=created` for a
new session (the agent was delegated an issue or mentioned) and
`action=prompted` when a person replies. Answer within 5 seconds and process
the work asynchronously.

```json
{
  "type": "AgentSessionEvent",
  "action": "created",
  "eventId": "mention_..._1:1",
  "createdAt": "2026-10-12T16:30:52.000Z",
  "organizationId": "workspace_acme",
  "oauthClientId": "flow_client_...",
  "appUserId": "app_...",
  "webhookId": "webhook_app_...",
  "agentSession": {
    "id": "mention_...",
    "status": "pending",
    "issueId": "issue_105131",
    "commentId": "comment_...",
    "issue": { "id": "issue_105131", "identifier": "ENG-42", "title": "Retry failed payments" },
    "comment": { "id": "comment_...", "body": "@Deploy bot can you take this?", "userId": "usr_7f2c" },
    "creator": { "id": "usr_7f2c", "name": "Ada Lovelace", "email": "ada@example.com" }
  },
  "issue": { "id": "issue_105131", "identifier": "ENG-42" },
  "previousComments": [
    { "id": "comment_root", "body": "Payments fail on retry", "userId": "usr_9a1b", "userName": "Grace Hopper", "createdAt": "..." }
  ],
  "guidance": [
    { "body": "Always open draft pull requests.", "origin": "workspace" },
    { "body": "Use the payments repository.", "origin": "team", "teamId": "team_eng", "teamKey": "ENG", "teamName": "Engineering" }
  ],
  "agentActivity": { "type": "prompt", "body": "Use the staging database" },
  "promptContext": "<issue identifier=\"ENG-42\">\n<title>Retry failed payments</title>\n...\n</issue>\n<primary-directive-thread comment-id=\"comment_root\">\n<comment author=\"Ada Lovelace\" created-at=\"...\">\n<user id=\"usr_7f2c\"/>\n@Deploy bot can you take this?\n</comment>\n</primary-directive-thread>\n<guidance>\n<guidance-rule origin=\"workspace\">Always open draft pull requests.</guidance-rule>\n<guidance-rule origin=\"team\" team-name=\"Engineering\">Use the payments repository.</guidance-rule>\n</guidance>\n",
  "webhookTimestamp": 1791822652000
}
```

- `promptContext` is a ready-to-use prompt: the issue (title, description,
  team, labels, parent, project), the thread that started the session
  (`primary-directive-thread`), other recent threads (`other-thread`) and the
  guidance rules. All text is XML-escaped.
- `previousComments` are the earlier comments of the triggering thread (empty
  for delegation).
- `guidance` lists the agent guidance that applies to the session, least
  specific first: the workspace's (**Settings › Agents › Additional
  guidance**, `origin: "workspace"`), then the guidance of the session team's
  parent teams and of the team itself (**Team settings › Team agents ›
  Additional guidance**, `origin: "team"` with `teamId`, `teamKey` and
  `teamName`). Later rules win when they conflict, so a team's own guidance
  wins over its parent team's and the workspace's. Empty levels are left out;
  each rule is at most 8,000 characters.
- `agentActivity` is the person's prompt, on `prompted` events only.

The delivery is signed like every other webhook (`Flow-Signature`,
`Flow-Timestamp`, `webhookTimestamp`); `Flow-Delivery` and `eventId` are
`<session id>:<version>`. Through the application webhook, the delivery also
appears in the managed webhook's delivery log.

### Session timing

Acknowledge a session within **10 seconds** of the event: emit an activity
(a `thought` is enough) or set an external URL. Otherwise the session is
reported with `unresponsive: true`. A session with no agent activity for
**30 minutes** reports `status: "stale"`; emitting another activity recovers
it. Both are derived when the session is read; the stored state is unchanged.

### Session API

| Request | Purpose |
| --- | --- |
| `POST /api/agent-tasks/{id}/activities` | Emit an activity. `"ephemeral": true` on a `thought` or `action` shows it until your next activity replaces it. |
| `PATCH /api/agent-tasks/{id}` | Update the session: `externalUrls` (replace all, `[{label, url}]`, HTTPS, at most 20), `addedExternalUrls`, `removedExternalUrls` (URLs), deprecated `externalLink`, and `plan` (`[{content, status}]` with `pending`, `inProgress`, `completed` or `canceled`; replaced as a whole). Does not change the session `version`. Setting a URL acknowledges the session. |
| `POST /api/agent-tasks` | Open a session yourself: `{"issueId": "..."}` or `{"commentId": "..."}`, optionally `externalUrls`. No `created` event is sent; a person's reply sends `prompted`. |

Only the session's application can update it or open sessions, and only on
issues in teams the application can access.

## API reference

Webhook management requires a workspace administrator (or the `admin` scope).

| Route | Description |
| --- | --- |
| `GET /api/webhooks` | List webhooks, with `payloadVersion` and `signingStatus` (`active`, `rotationRequired`, `revoked`) |
| `POST /api/webhooks` | Create; returns `secret` once |
| `GET /api/webhooks/{id}` | One webhook |
| `PATCH /api/webhooks/{id}` | Update `name`, `url`, `resourceTypes`, `teamIds`, `enabled`, `payloadVersion` |
| `DELETE /api/webhooks/{id}` | Delete (not for application-managed webhooks) |
| `POST /api/webhooks/{id}/rotate-secret` | New signing secret, returned once |
| `POST /api/webhooks/{id}/revoke-secret` | Remove the signing secret |
| `GET /api/webhooks/{id}/deliveries?status=&cursor=&limit=` | Delivery log, newest first: `{items, hasMore, nextCursor}` |
| `GET /api/webhooks/{id}/deliveries/{deliveryId}` | One delivery with its request `payload` |
| `POST /api/webhooks/{id}/deliveries/{deliveryId}/redeliver` | Send again; returns the new delivery after its first attempt |
| `POST /api/webhooks/{id}/test` | Send a `WebhookTest` event; returns the delivery |
| `GET /api/webhooks/{id}/failures` | Failed attempts (compatibility view of the delivery log) |
| `GET /api/oauth-applications/{id}/webhook` | Application webhook settings |
| `PUT /api/oauth-applications/{id}/webhook` | Set `enabled`, `url`, `resourceTypes`; returns `secret` when it is created |
| `POST /api/oauth-applications/{id}/webhook/rotate-secret` | New application signing secret, returned once |
