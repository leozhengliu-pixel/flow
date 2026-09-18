# Application Members and Agent Tasks

Flow application members are non-login identities installed by a workspace owner
or administrator. They are distinct from the person who installed them. An
application's issue edits, comments and agent activities are attributed to that
identity.

## Enable the Built-in Agent

1. Configure the existing Flow Agent provider. The Responses, Messages and Chat
   Completions protocols remain supported. See the Agent settings in
   `docs/configuration.md`.
2. Open **Members > Applications** and choose **Flow Agent**.
3. Select the teams it can access and save.
4. In an issue, use **Delegate to agent**. The human assignee remains responsible
   for the issue. When no human assignee exists, the delegating user becomes the
   assignee.
5. Follow the task under **Agent sessions**. The built-in worker uses the same
   provider and tool inventory as the existing Agent. Write tools require
   `FLOW_AGENT_WRITE_TOOLS=true` and an explicit approval before execution.

The approval is bound to the stored tool call and task version. A text reply such
as "approve" cannot approve a tool. Only the requesting user or a workspace
administrator can approve it. Rejection does not execute the tool.

Workers use bounded concurrency and database leases. Task creation commits with
the issue mutation. Application suspension, removed team access and user
cancellation prevent further accepted activity. Interrupted built-in executions
are reported as errors rather than automatically repeating a possibly completed
write. A user can explicitly retry after inspecting the previous result.

## Install an External Agent

Register an OAuth client through the existing OAuth application settings or
`POST /oauth/register`. Authorize it using the PKCE code flow with:

Application clients should use the `actor=app` discovery variant when reading
the well-known OAuth metadata. Default discovery advertises only personal MCP
scopes; `/.well-known/oauth-authorization-server?actor=app` and the matching
protected-resource metadata also advertise `app:mentionable` and
`app:assignable`.

```text
/oauth/authorize
  ?client_id=YOUR_CLIENT_ID
  &redirect_uri=YOUR_REGISTERED_CALLBACK
  &response_type=code
  &code_challenge=YOUR_S256_CHALLENGE
  &code_challenge_method=S256
  &state=YOUR_STATE
  &actor=app
  &scope=read%20app:mentionable%20app:assignable
```

The authorization page requires an administrator to select teams. Exchange the
returned code at `/oauth/token`. The access token belongs to the application's
workspace-specific user, not to the installer. `actor=user` and omitted actor
retain the existing personal OAuth behavior.

| Scope | Capability |
| --- | --- |
| `read` | Read resources within the selected teams |
| `write` | Use permitted resource mutation APIs within those teams |
| `app:mentionable` | Start a task from a structured mention in an issue, project or document comment |
| `app:assignable` | Accept issue delegation |

An application with either agent capability can report its own task activities
without receiving general `write` access. It cannot request `admin`, `openid` or
`email` through app authorization. Token use checks the live installation and
team scope; suspending the application invalidates its usable access immediately.

An OAuth client registration alone does not create a member. Members only shows
installed identities, not the registry of available clients.

## Receive Work

An external agent can poll:

```http
GET /api/agent-tasks
Authorization: Bearer APPLICATION_ACCESS_TOKEN
```

The result contains up to 100 nonterminal tasks owned by that application.
Other agents' tasks and inaccessible resources are excluded. Issue viewers can
list that issue's recent tasks using `?issueId=ISSUE_ID`.
For document or project discussions use
`?resourceType=document&resourceId=DOCUMENT_ID` or
`?resourceType=project&resourceId=PROJECT_ID`. Their webhook envelopes contain
`document` or `project` instead of `issue`.

For push delivery, configure the installation's HTTPS webhook in
**Members > Applications**. Save returns a signing secret once. Saving the
installation rotates the secret; update the receiver accordingly. OAuth
reauthorization preserves the configured webhook and secret.

```json
{
  "type": "AgentSessionEvent",
  "action": "created",
  "eventId": "agent_task_example:1",
  "agentSession": {
    "id": "agent_task_example",
    "issueId": "issue_example",
    "appUserId": "app_example",
    "creatorId": "user_example",
    "status": "pending",
    "version": 1,
    "prompt": "Investigate the issue",
    "trigger": "delegation"
  },
  "issue": {}
}
```

Verify `X-Flow-Signature` as hex HMAC-SHA256 of the **raw request body** using the
installation signing secret. Use constant-time comparison. Deduplicate by
`X-Flow-Delivery` / `eventId`: delivery is at least once. A 2xx response acknowledges
delivery, not task completion. Non-2xx deliveries are retried after a minute.
Unacknowledged task execution can be redispatched after a day. Replies and retries
produce `action: prompted` with a new event ID.

## Report Progress and Results

```http
GET /api/agent-tasks/SESSION_ID?after=ACTIVITY_CURSOR
Authorization: Bearer APPLICATION_ACCESS_TOKEN
```

Returns `{ session, activities }`. Activities are ordered by their `id`, with at
most 100 per response. To read the next page, pass the final activity ID as `after`.

```http
POST /api/agent-tasks/SESSION_ID/activities
Authorization: Bearer APPLICATION_ACCESS_TOKEN
Content-Type: application/json

{
  "expectedVersion": 1,
  "type": "thought",
  "body": "Examining the failing tests"
}
```

Use the returned task version for the next activity. A stale version returns 409;
reload the session before retrying. This prevents two workers from independently
advancing the same task. Each activity is limited to 64 KiB of text and an optional
HTTPS result URL. The built-in worker can persist a final response up to 1 MiB;
streamed output is stored in smaller incremental activities.

| Activity | Resulting status |
| --- | --- |
| `thought`, `action`, `output` | `active` |
| `elicitation` | `awaitingInput` |
| `response` | `complete` |
| `error` | `error` |
| Human `prompt` | `pending` |
| Human `canceled` | `canceled` |
| Human `retry` on a terminal task | `pending` |

Only the task's application can emit agent output. Human replies and cancellation
require issue editing access. Application tokens cannot impersonate human replies.
Structured comment mentions use the editor's `mention` node with the app
user ID; plain matching text does not execute agents. Re-saving the same comment
does not duplicate the mention task, and an application cannot recursively invoke
another agent through its own comments.

## Operational Boundaries

- The built-in application reuses Flow's configured model endpoint. External agents
  must run their own service or polling worker. Installing a desktop coding tool
  does not install an application member or create a vendor-supported connection.
- Issue delegation and structured discussion mentions start these tasks. Merely
  editing a mention into a document's body does not automatically execute a task;
  use its comment composer to ask the application to work on the document.
- Agent tasks and append-only activities live in separate SQL tables. They do not
  rewrite the workspace JSON or the entire activity history for each output chunk.
- The issue UI retrieves new activity records incrementally while a task runs.
- Task reads and callbacks re-check current resource access. Removing access can
  hide a historical session from the application without deleting its audit trail.
- Team scope does not override a document's explicit permissions. Share a
  restricted document with the application before mentioning it, or grant access
  and retry a task that was canceled for lack of access.
