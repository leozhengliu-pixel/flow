# Developing Flow Applications and Agents

[简体中文](developers.zh-CN.md)

Flow applications run in your own service and connect to a Flow deployment over
HTTP. Installing an application creates an application member in a workspace;
it does not upload or run your program inside the Flow server.

Use the REST API for issues, projects, documents, comments and other resources.
Use OAuth for authorization. An Agent additionally consumes tasks and emits
activities so people can see progress and reply in Flow.

The [OpenAPI inventory](openapi.json) lists HTTP routes. The
[domain model](domain-model.md) describes resources. This guide describes the
application authorization and execution contract; it does not require a
particular model provider or Agent framework.

## Choose an Identity

| Use case | Identity | Credentials |
| --- | --- | --- |
| A personal script | The person running the script | Personal API key |
| A tool acting on behalf of each signed-in person | `actor=user` | OAuth authorization code and refresh token |
| An external application or Agent installed in a workspace | `actor=app` | OAuth authorization code and refresh token |
| CI, scheduled jobs or an internal service | Application member | Opt-in `client_credentials` grant |
| Trying an application from its settings page | App or User, explicitly selected | Developer access token |

An app user is a non-login workspace identity with the `app` role. Each
installation has a stable workspace-specific user ID. The installing person is
recorded separately from the identity that performs API actions. An application
does not inherit the installer's administrator privileges.

The built-in Flow Agent is separate: its execution is constrained by the
requesting person's access as well as its application access. Build external
automation using an independent application, rather than obtaining credentials
for the built-in system identity.

## Register an Application

A workspace owner or administrator opens **Settings > API > New OAuth
application** and supplies:

- A recognizable name and, optionally, a description and logo.
- Exact OAuth redirect URIs. HTTPS is required except for local loopback
  development callbacks. Wildcard redirects are not supported.
- The maximum scopes the application may request, including `read`.
- **Client credentials**, only if the application needs server-to-server access.
- **Allow installation in other workspaces**, only when distributing the application.

Administrator-created applications are private to their registration workspace
by default. Public availability allows another workspace to approve installation;
it does not grant access to that workspace automatically.

Creation returns a client ID and a client secret. Keep the secret in your
service's secret manager or environment. Retrieve a replacement through
**Rotate secret** in the application's detail page if you no longer have it.
Normal application listings never return the client secret.

Public PKCE clients can instead register using `POST /oauth/register`:

```http
POST /oauth/register
Content-Type: application/json

{
  "client_name": "Incident helper",
  "redirect_uris": ["http://127.0.0.1:3000/oauth/callback"],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

Dynamic registration creates a client, not a workspace member. Such public
clients use PKCE; they cannot use `client_credentials`.

## Install Through OAuth

Generate a random `state` and PKCE verifier on the server handling installation.
Retain them for the callback. The challenge is the base64url encoding of
`SHA256(verifier)`, without padding. Flow requires `S256`.

Send the administrator to this URL on the target Flow deployment, replacing
the example values with properly URL-encoded values:

```text
/oauth/authorize?response_type=code
  &client_id=YOUR_CLIENT_ID
  &redirect_uri=YOUR_REGISTERED_CALLBACK
  &state=YOUR_RANDOM_STATE
  &code_challenge=YOUR_S256_CHALLENGE
  &code_challenge_method=S256
  &actor=app
  &scope=read%20app%3Amentionable%20app%3Aassignable
```

The administrator chooses a workspace, reviews the permissions, and selects:

1. All public teams, including public teams created later.
2. All public teams plus explicitly selected private teams.
3. Only selected teams.

Third-party application review, when enabled, must approve the requested scopes
before installation completes. Applications registered by administrators in
their own workspace are governed by their configured scopes. Registration in
one workspace does not waive another workspace's approval requirements.

Verify the callback's `state`, then exchange its code:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode 'grant_type=authorization_code' \
  --data-urlencode "client_id=$FLOW_CLIENT_ID" \
  --data-urlencode "code=$FLOW_AUTHORIZATION_CODE" \
  --data-urlencode "redirect_uri=$FLOW_REDIRECT_URI" \
  --data-urlencode "code_verifier=$FLOW_PKCE_VERIFIER"
```

The response contains `access_token`, `refresh_token`, `token_type`,
`expires_in` and a space-separated `scope`. Access tokens last one hour.
Refresh grants last 30 days. Save replacements atomically because consuming a
refresh token rotates it; concurrent reuse is rejected.

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode 'grant_type=refresh_token' \
  --data-urlencode "client_id=$FLOW_CLIENT_ID" \
  --data-urlencode "refresh_token=$FLOW_REFRESH_TOKEN"
```

Use `actor=user`, or omit `actor`, when each person should authorize their own
identity. Agent scopes apply to app installation and are not granted to a
personal authorization.

## Internal Services and CI

Enable **Client credentials** for an administrator-created OAuth application.
Fetch a token at the beginning of a run, rather than manually treating an access
token as a permanent API key:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --user "$FLOW_CLIENT_ID:$FLOW_CLIENT_SECRET" \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'scope=read app:mentionable app:assignable'
```

Client ID and secret can alternatively be form fields `client_id` and
`client_secret`. Use one authentication method per request. The requested scopes
must be a subset of the application's configured scopes. Omitting `scope` uses
the configured scopes.

This grant creates an app actor in the application's home workspace on first
use, with public-team access. It returns an opaque Bearer token valid for 30
days, without a refresh token, and includes `actor=app` and `app_user_id`.
Administrators can subsequently narrow team access through **Members >
Applications**. A machine token cannot install itself in an arbitrary workspace
or reactivate a suspended installation.

Up to 1,000 active app tokens can coexist. Requesting different scopes through
`client_credentials` revokes the application's existing app tokens with different
scopes. Fetch a replacement after expiration or client secret rotation.
Rotating the client secret invalidates all existing client-credentials tokens
immediately. Authorization-code and developer tokens retain their independent
expiration and revocation rules.

`POST /api/oauth/token` remains a compatibility alias and additionally accepts
the legacy JSON client-credentials request. New integrations should use the
standard form-encoded `/oauth/token` endpoint.

## Developer Tokens

In an application's detail page, **Create & copy token** opens an actor and
scope selection dialog. Choose **Application** for application attribution or
**User** for your personal attribution. The default selected permission is
`read`; add only the permissions being tested.

Developer tokens last one hour. The dialog shows the secret after creation and
allows copying it when automatic clipboard access fails. Closing the dialog
removes it from the UI. Flow stores a hash, not a retrievable access-token secret.
Issuance and client-secret rotation require an interactive administrator session;
an API token cannot use those endpoints to mint another credential.

## Permissions and Team Boundaries

| Scope | Capability |
| --- | --- |
| `read` | Read normal team-accessible resources |
| `write` | Mutate normal team-accessible resources |
| `create_issues` / `issues:create` | Create or update issues, without general mutation access |
| `create_comments` / `comments:create` | Create issue comments |
| `app:mentionable` | Allow structured mentions to start Agent tasks |
| `app:assignable` | Allow issue delegation to the Agent |
| `customer:read`, `customer:write` | Read, or read and modify, customer resources |
| `initiative:read`, `initiative:write` | Read, or read and modify, initiative resources |
| `release:read` | Read releases |
| `release:create` | Create releases |
| `releaseStage:update` | Change a release's stage |

Applications cannot request `admin`, `owner`, `openid` or `email`. Customer,
initiative and release access requires the corresponding explicit scopes, even
when `write` is granted. Mentions and delegation do not automatically grant
general write access. An Agent with the relevant Agent scope may report its own
task activities without `write`.

Effective access is the intersection of token scopes, current application
authorization, team grants, resource permissions and workspace policy.
Explicit resource permissions, such as a restricted document, remain enforced.
Team access and installation status are checked on requests rather than copied
permanently into the token. A separately narrowed token cannot widen to the
application's full team grant.

## Call the REST API

Send `Authorization: Bearer ACCESS_TOKEN` on API requests. Flow's credential
lookup resolves the token's workspace; `X-Workspace-Key` can select that same
workspace explicitly, but cannot switch the token to another workspace.

```bash
# Verify the authenticated identity.
curl --fail-with-body "$FLOW_BASE_URL/api/bootstrap" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN"

# Read a bounded issue page, rather than importing the whole workspace.
curl --fail-with-body "$FLOW_BASE_URL/api/issue-records?limit=50&projection=list" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN"

# Requires write or create_issues, and access to the specified team.
curl --fail-with-body "$FLOW_BASE_URL/api/issues" \
  -H "Authorization: Bearer $FLOW_ACCESS_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"title":"Investigate an incident","teamId":"YOUR_TEAM_ID"}'
```

Issue pages return `items`, `hasMore` and, when another page exists, `nextCursor`.
Pass the cursor back with the same filters and sort. Do not assume a bootstrap
response contains every issue in a large workspace.

Application-created issues, comments and activity are attributed to the
application user. Credential metadata separates `subjectUserId` from
`issuedByUserId`; `creatorId` remains a compatibility representation of the
credential subject. Audit events contain identities and grant types, not secrets.

## Build an Agent

Configure Agent scopes and install the app. People can then mention it in a
comment using the editor's structured mention, or delegate an issue to it. The
human remains the issue assignee; the app is its delegate.

An external Agent runs its own worker. Start with the dependency-free
[application Agent](examples/application-agent.mjs):

```bash
export FLOW_BASE_URL='http://127.0.0.1:8080'
export FLOW_CLIENT_ID='YOUR_CLIENT_ID'
export FLOW_CLIENT_SECRET='YOUR_CLIENT_SECRET'
node docs/examples/application-agent.mjs
```

Configure the application's scopes as `read`, `app:mentionable` and
`app:assignable`, and enable Client credentials before running the example.
Without a model key it reads the issue and returns a deterministic summary.
To enable a real streaming conversation, set the model provider variables before
starting the same worker:

```bash
export FLOW_LLM_API_KEY='YOUR_PROVIDER_KEY'
export FLOW_LLM_BASE_URL='https://api.deepseek.com'
export FLOW_LLM_MODEL='deepseek-flash'
node docs/examples/application-agent.mjs
```

`deepseek-flash` selects DeepSeek-V4.1-Flash. The worker reads credentials from
its environment; it never stores or prints them. It streams reasoning and
Markdown, and its `request_input` tool can ask a question with selectable options.
Choose an option in the comment thread and press Continue, or type a reply. The
worker uses the conversation history for the next response, including follow-up
messages after completion. Stop cancels the model request; version conflicts
prevent an old generation from overwriting a newer prompt. It batches deltas at
500 ms intervals, rather than writing every token to the database. This example
only reads issue context and reports activities; it does not modify issues or
execute arbitrary code.

### Receive Work

Polling uses `GET /api/agent-tasks`. App tokens only receive nonterminal tasks
owned by that app. An administrator can also configure an HTTPS webhook in
**Members > Applications**. Save returns a signing secret once; update the
receiver when saving rotates that secret.

An `AgentSessionEvent` delivery includes `action=created` for a new task and
`action=prompted` for follow-up input, an `agentSession`, and its issue, project
or document context. `eventId` and `X-Flow-Delivery` identify the delivery.

Verify `X-Flow-Signature` against a hex HMAC-SHA256 of the raw request body:

```js
import { createHmac, timingSafeEqual } from 'node:crypto'

const expected = createHmac('sha256', webhookSecret).update(rawBody).digest()
const signature = request.headers.get('x-flow-signature') ?? ''
const valid = /^[a-f0-9]{64}$/i.test(signature) &&
  timingSafeEqual(expected, Buffer.from(signature, 'hex'))
```

Check the signature before parsing or enqueueing work. Persist delivery IDs and
task versions in durable storage so retries or worker restarts do not execute a
business action twice. A successful webhook response acknowledges delivery;
activities report execution progress. Failed deliveries are retried. For the
precise task and delivery contract, see [Application members and Agent
tasks](application-agents.md).

### Report Progress

```http
POST /api/agent-tasks/TASK_ID/activities
Authorization: Bearer APPLICATION_TOKEN
Content-Type: application/json

{"expectedVersion":1,"type":"thought","body":"Reading the issue"}
```

The response is the updated session. Use its `version` for the next operation.
`409` means another writer advanced the task; reload it before retrying.

| Activity | Meaning / session state |
| --- | --- |
| `thought`, `action`, `output` | Working / `active` |
| `elicitation` | Asking a person for input / `awaitingInput` |
| `response` | Finished / `complete` |
| `error` | Failed / `error` |

Read a task with `GET /api/agent-tasks/TASK_ID?after=ACTIVITY_CURSOR`.
Activities are returned in pages of up to 100; use the final activity ID as the
next cursor. Each external activity body is limited to 64 KiB.

Only the owning app can emit Agent output. Human `prompt`, `retry` and `canceled`
activities have separate permission checks. An app cannot impersonate those
actions or emit output to another app's task. Markdown is supported by the UI.

### Ask for Input and Continue

An app can post a clarification activity using either the flat activity fields
above or a nested `content` object:

```json
{
  "expectedVersion": 2,
  "content": { "type": "elicitation", "body": "Which language do you prefer?" },
  "signal": "select",
  "signalMetadata": {
    "options": [{ "label": "English", "value": "English" }, { "label": "Chinese", "value": "Chinese" }]
  }
}
```

Options require unique, nonempty values; there can be 1 to 20 options. A human
reply posts `type=prompt`, `body=Chinese` and the current `expectedVersion`.
The same task becomes pending again. A final response marks it complete, but
the person may continue the same conversation later. Issue source-comment tasks render
inside the original comment thread. Delegated tasks without a source comment
render separately in the Activity section.

For account linking, use `signal=auth` and `signalMetadata` containing `url`
(HTTPS), optional `providerName`, and optional `userId`. Only the targeted person
gets the connection link; other members see who is being waited on. Your service
must handle the provider callback and resume the task once linking completes.
This signal does not grant access or complete an OAuth handshake automatically.

A human stops work with `type=canceled`, or `type=prompt, signal=stop`.
Tool approval is a separate, explicit approval/rejection operation restricted to
the requester or an administrator; replying to a clarification never approves a
pending business write. Each task activity produces an `agent_task.updated` SSE
event with its task/resource/version; the UI refreshes that thread incrementally.

Human reactions are persisted as `type=feedback` activities with `activityId`,
an emoji `body`, explicit `enabled=true|false`, and the current `expectedVersion`.
They do not resume a completed or waiting session or approve a tool. They are
available while the Agent is waiting or finished; output generation keeps its
version reserved for the worker. Applications cannot submit human feedback.

## Revoke and Operate

Revoke an individual opaque access or refresh token using
`POST /oauth/revoke` with a form field `token`. Repeated revocation is safe.
Suspend the installation to revoke its current credentials and authorizations;
re-enabling it requires newly issued credentials. Removing or changing team
access affects existing tokens immediately. Deleting an administrator-created
OAuth registration makes that registration's issued tokens unusable.

| Response | Action |
| --- | --- |
| `400 invalid_scope` | Request supported, configured scopes, including `read` |
| `400 unauthorized_client` | Enable Client credentials, or use the authorization-code flow |
| `401` | Check token expiration, secret rotation, credential revocation and app approval |
| `403` | Check team access, resource permissions, workspace policy and the actor's role |
| `404` | The resource may be absent or inaccessible; do not fall back to an administrator token |
| `409` | Reload the task version; do not blindly replay a business write |
| `429` | Reduce concurrent credential issuance; reuse the token within its run |

For production, use HTTPS and keep callback state and PKCE verifiers
server-side. Store tokens and secrets outside source control. Log request IDs,
task IDs and HTTP statuses rather than Authorization headers or token responses.
Always recheck authorization when a task resumes. A token or webhook event
authorizes neither arbitrary code execution nor unrestricted external access.

## Existing Integration Migration

Older `/api/oauth/token` client-credentials implementations attributed actions
to the registration's creator. Those unbound legacy machine tokens are rejected.
Enable Client credentials, request a fresh app actor token, and verify the
returned app user before restarting workers. Personal API keys and existing
PKCE user authorizations keep their own identity semantics.
