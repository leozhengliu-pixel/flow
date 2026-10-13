# OAuth 2.0 for Flow Applications

This page is the reference for Flow's OAuth 2.0 server: the authorization code
flow (with and without PKCE), client credentials, `actor=app`, scopes, token
refresh, revocation, introspection, "via App" attribution and OAuth application
manifests. [Developing Flow Applications and Agents](developers.md) explains
when to use each identity and how to build an Agent on top of it.

The protocol follows RFC 6749, RFC 7636 (PKCE), RFC 7009 (revocation), RFC 7662
(introspection) and RFC 8414 (metadata at
`/.well-known/oauth-authorization-server`). Parameter names and behaviour
match the reference app's OAuth API, so an integration written for the reference app's OAuth flow
needs only its base URL changed. Flow has no GraphQL API; after OAuth, call the
REST API described in [openapi.json](openapi.json).

## Endpoints

| Purpose | Endpoint |
| --- | --- |
| Authorization (browser) | `GET /oauth/authorize` |
| Token exchange and refresh | `POST /oauth/token` (`application/x-www-form-urlencoded`) |
| Revocation (RFC 7009) | `POST /oauth/revoke` |
| Introspection (RFC 7662) | `POST /oauth/introspect` |
| Dynamic registration of public clients (RFC 7591) | `POST /oauth/register` |
| Server metadata (RFC 8414) | `GET /.well-known/oauth-authorization-server` |
| Who am I | `GET /api/viewer` |
| Manifest JSON Schema | `GET /.well-known/oauth-app-manifest.schema.json` |

## Client types

| Client | Created by | Authenticates with | PKCE |
| --- | --- | --- | --- |
| **Confidential**: a registered OAuth application | **Settings > API > New OAuth application**, `POST /api/oauth-applications` or a [manifest](#application-manifests) | `client_secret` (`client_secret_basic` or `client_secret_post`) | Optional |
| **Public**: an MCP client, CLI or desktop app | `POST /oauth/register` | `client_id` only (`token_endpoint_auth_method=none`) | Required (`S256`) |

## Authorization code flow

### 1. Redirect to Flow

```http
GET /oauth/authorize?response_type=code
  &client_id=CLIENT_ID
  &redirect_uri=https%3A%2F%2Fapp.example.com%2Foauth%2Fcallback
  &scope=read,write
  &state=RANDOM_STATE
  &actor=user
```

| Parameter | |
| --- | --- |
| `client_id` | Required. |
| `redirect_uri` | Required. Must exactly match a registered URI (HTTPS, or HTTP on a loopback host; loopback ports may vary). |
| `response_type=code` | Required. |
| `scope` | Comma- or space-separated [scopes](#scopes). `read` is always included. |
| `state` | Recommended. Returned unchanged on the callback (also on denial); reject callbacks whose `state` differs from what you sent. |
| `actor` | `user` (default): changes are made as the person who authorizes. `app`: an administrator installs the application, and changes are made by its app user (see [actor=app](#actorapp)). |
| `prompt=consent` | Always show the consent screen, even when the person already granted these scopes. Lets people connect another workspace. |
| `code_challenge`, `code_challenge_method=S256` | PKCE. Required for public clients, optional for confidential clients. `plain` is not supported. |

Without `prompt=consent`, a person who already authorized a confidential
application for the same (or broader) scopes, with `actor=user`, is sent
straight back to the callback with a new code. Public clients and `actor=app`
installs always show the consent screen.

The consent screen: a workspace switcher in the corner,
"<App> is requesting access · Authenticating with your <workspace> workspace",
the requested permissions (`write` hides the narrower `issues:create`,
`comments:create` and `timeSchedule:write`), team access for `actor=app`
installs ("All public teams" or "Only select teams…"), and Cancel / Authorize.
An invalid request shows Flow's mark and the error description instead, with
The reference app's wording:

| Problem | `error` | Description |
| --- | --- | --- |
| Unknown `client_id` | `invalid_client` | Could not find OAuth client with clientId `<id>`. Please check that you are logged into the correct workspace. |
| No `redirect_uri` | `invalid_request` | Invalid parameters. Make sure to include \`redirect_uri\` |
| `response_type` missing or not `code` | `invalid_request` | Invalid or missing \`response_type\`. Expected \`response_type=code\` |
| `redirect_uri` not registered | `invalid_request` | Invalid redirect_uri parameter for the application. |
| Unknown scope | `invalid_scope` | Invalid scope: `<scope>` |
| `app:*` scope without `actor=app` (registered applications) | `invalid_scope` | The scopes requested are not valid for this actor mode. |

### 2. Handle the callback

```http
GET https://app.example.com/oauth/callback?code=flow_code_...&state=RANDOM_STATE
```

If the person cancels, the callback receives `error=access_denied` and the
`state` (no `error_description`). Codes are single-use and expire after 10 minutes.

### 3. Exchange the code

Confidential client, `client_secret_post`:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode grant_type=authorization_code \
  --data-urlencode "code=$CODE" \
  --data-urlencode "redirect_uri=$REDIRECT_URI" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "client_secret=$CLIENT_SECRET"
```

Confidential client, `client_secret_basic` (`Authorization: Basic
base64(client_id:client_secret)`):

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode grant_type=authorization_code \
  --data-urlencode "code=$CODE" \
  --data-urlencode "redirect_uri=$REDIRECT_URI"
```

With PKCE, send `code_verifier`; `client_secret` is then optional:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" \
  --data-urlencode grant_type=authorization_code \
  --data-urlencode "code=$CODE" \
  --data-urlencode "redirect_uri=$REDIRECT_URI" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "code_verifier=$VERIFIER"
```

Use one client authentication method per request: sending Basic credentials
and a `client_secret` field together is `invalid_request`.

Response:

```json
{
  "access_token": "flow_oauth_...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "read write",
  "refresh_token": "flow_refresh_...",
  "actor": "user"
}
```

Access tokens last one hour (the reference app: 24 hours); refresh tokens last 30 days.
`scope` is space-separated and uses Flow's canonical names (`create_issues`
for `issues:create`, `create_comments` for `comments:create`).

## Refresh tokens

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode grant_type=refresh_token \
  --data-urlencode "refresh_token=$REFRESH_TOKEN"
```

- Authenticate with Basic or with `client_id` + `client_secret` fields. Tokens
  obtained with PKCE refresh with `client_id` alone; tokens obtained without
  PKCE need the client secret.
- Every refresh **rotates** the refresh token. Store the new one atomically.
- **Grace period:** for 30 minutes after a refresh token is used, replaying the
  same request returns the *same* new refresh token (and a fresh access token),
  so a client that lost the response to a network error can recover. Concurrent
  refreshes of one token therefore all succeed with the same successor. The
  grace period ends early once the new refresh token is itself used or revoked,
  or when the old one is revoked.
- Refreshing re-checks the authorization, the person's membership, the
  installation and workspace application policy.

## Revoking tokens (RFC 7009)

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/revoke" --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode "token=$TOKEN" \
  --data-urlencode token_type_hint=refresh_token
```

- `token` (required) and optional `token_type_hint` (`access_token` or
  `refresh_token`; any other value is `unsupported_token_type`).
- Client authentication is optional. When supplied it must be valid
  (`401 invalid_client`), and the token must have been issued to that client.
- Unknown, expired or already revoked tokens return `200`, as RFC 7009
  requires. (the reference app returns `400` for an already revoked token.)
- For compatibility, the legacy `Authorization: Bearer <access token>` header
  and the `access_token` / `refresh_token` form fields are accepted; they must
  not be combined with `token`.
- Revoking a refresh token revokes its whole chain: the refresh tokens rotated
  from the same code exchange (including an already issued successor) and every
  access token issued from them, and it ends the grace period. Other sessions of
  the same authorization keep working. To revoke everything an application holds
  for a person, revoke the authorization in **Settings > Applications**
  (`DELETE /api/oauth/authorizations/{id}`).

## Introspection (RFC 7662)

Confidential clients can check their own tokens:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/introspect" --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode "token=$ACCESS_TOKEN"
```

```json
{"active": true, "scope": "read write", "client_id": "flow_client_...", "token_type": "Bearer",
 "exp": 1760000000, "iat": 1759996400, "sub": "user_...", "actor": "user", "workspace": "acme"}
```

Tokens issued to another client, and revoked or expired tokens, are
`{"active": false}`. App actor tokens add `"actor": "app"` and `app_user_id`.

## Who am I

`GET /api/viewer` works with any credential and returns the user (for app
tokens, the application user), the workspace, the person's role,
`authentication` (`session`, `apiKey` or `oauth`), `actor` (`user` or `app`),
the OAuth `application`, the token's `scopes`, team restriction and expiry.

## Client credentials

Server-to-server applications with **Client credentials** enabled can request
an `actor=app` token without a browser:

```bash
curl --fail-with-body "$FLOW_BASE_URL/oauth/token" --user "$CLIENT_ID:$CLIENT_SECRET" \
  --data-urlencode grant_type=client_credentials \
  --data-urlencode 'scope=read write'
```

The token is valid for 30 days, has no refresh token and acts as the app user
in the application's home workspace with access to all public teams. See
[Internal Services and CI](developers.md#internal-services-and-ci) for limits
(1,000 parallel tokens with the same scopes), scope changes and secret
rotation (which invalidates client-credentials tokens only).

## actor=app

With `actor=app`, a workspace administrator installs the application and
chooses its teams; the application gets its own app user, and everything it
creates is attributed to that app user rather than to the installer. Use it for
agents and service accounts. `admin` cannot be requested with `actor=app`
(`invalid_scope`), and neither can the personal `openid` / `email` scopes.
Agent scopes (`app:assignable`, `app:mentionable`) require `actor=app`: a
registered application that requests them without it gets `invalid_scope`
(public MCP clients, which request every advertised scope, have them dropped).

## Scopes

| Scope | actor=user | actor=app | Grants |
| --- | --- | --- | --- |
| `read` | yes | yes | Always included. Read accessible resources. |
| `write` | yes | yes | Create and modify accessible resources. |
| `issues:create` (`create_issues`) | yes | yes | Create and update issues and their attachments. |
| `comments:create` (`create_comments`) | yes | yes | Create issue comments. |
| `timeSchedule:write` | yes | no | Accepted for the reference app compatibility; Flow has no time schedule API, so it grants nothing. |
| `admin` | yes, if configured | **no** | Admin-level endpoints, only when the person is an admin. The application must list `admin` in its configured scopes (applications created on the settings page can request every scope); public clients cannot request it. |
| `app:assignable`, `app:mentionable` | no | yes | Agent delegation and mentions. |
| `customer:read`, `customer:write`, `initiative:read`, `initiative:write`, `release:read`, `release:create`, `releaseStage:update` | yes | yes | Resource-specific access. |
| `openid`, `email` | yes (public/MCP clients) | no | Identity of the signed-in person. |

A registered application's configured scopes are the maximum it may request
(`invalid_scope` otherwise). Effective access is always the intersection of
the token scopes and what the person or app user can access.

## "via App" attribution

Records created with OAuth tokens remember the application:

- **actor=user**: issues, comments, attachments, documents and their activity
  belong to the person and carry `botActor` naming the application. Flow shows
  "Ada via Acme Sync".
- **actor=app**: records belong to the app user. To show the person in your
  system on whose behalf the application acted, pass `createAsUser` (at most 100
  characters) and optionally `displayIconUrl` (absolute HTTP(S) URL) in the
  JSON body when creating an issue, comment, attachment or document:

  ```bash
  curl --fail-with-body "$FLOW_BASE_URL/api/issues/$ISSUE_ID/comments" \
    -H "Authorization: Bearer $APP_TOKEN" -H 'Content-Type: application/json' \
    --data '{"body":"Customer replied","createAsUser":"Mark","displayIconUrl":"https://cdn.example.com/mark.png"}'
  ```

  Flow shows "Mark via Acme Sync" with Mark's avatar. `createAsUser` and
  `displayIconUrl` are rejected (`400`) for user-actor tokens, and
  `displayIconUrl` requires `createAsUser`.

The attribution is returned as:

```json
"botActor": {"id": "flow_client_...", "type": "oauthApplication", "name": "Acme Sync",
             "avatarUrl": "https://acme.example/icon.png", "userDisplayName": "Mark",
             "userAvatarUrl": "https://cdn.example.com/mark.png"}
```

MCP clients authorized with OAuth are attributed the same way ("Ada via
Claude").

## Errors

Token, revocation and introspection endpoints return RFC 6749 errors:
`{"error": "...", "error_description": "..."}`.

| Error | Status | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | Missing or malformed parameter, two client authentication methods, unsupported `prompt`, missing PKCE for a public client |
| `invalid_client` | 401 | Unknown client, wrong secret, Basic credentials for a public client, or a token issued to another client (`WWW-Authenticate: Basic` when Basic was used) |
| `invalid_grant` | 400 | Code or refresh token invalid, expired, already used outside the grace period, revoked, wrong `redirect_uri` or `code_verifier`, or a secret-only grant redeemed without the secret |
| `invalid_scope` | 400 | Unsupported scope, scope not configured on the application, `admin` with `actor=app`, `app:*` scopes without `actor=app` |
| `unauthorized_client` | 400 | Client credentials not enabled for the application |
| `unsupported_grant_type` | 400 | Grant other than `authorization_code`, `refresh_token`, `client_credentials` |
| `unsupported_token_type` | 400 | Unknown `token_type_hint` on revocation |

## Application manifests

A manifest describes an OAuth application as JSON, in the same format as
The reference app's OAuth app manifests (plus two Flow extensions). Use it to create apps
reproducibly, review them in code, or link people to a pre-filled setup page.

```json
{
  "$schema": "https://flow.example.com/.well-known/oauth-app-manifest.schema.json",
  "schemaVersion": "1.0.0",
  "distribution": "public",
  "display": {
    "description": "Adds issue creation to Acme Agent.",
    "iconUrl": "https://acme.dev/flow-agent-icon.png"
  },
  "developer": { "name": "Acme" },
  "oauth": {
    "client_name": "Acme Agent",
    "client_uri": "https://acme.dev/flow",
    "redirect_uris": ["https://acme.dev/api/flow/callback"],
    "grant_types": ["authorization_code", "client_credentials"],
    "scopes": ["read", "write", "issues:create"]
  },
  "webhook": {
    "enabled": true,
    "url": "https://acme.dev/api/flow/webhook",
    "resourceTypes": ["Issue", "Comment", "OAuthAuthorization"]
  },
  "agent": { "assignable": true, "mentionable": true }
}
```

| Field | Rules |
| --- | --- |
| `schemaVersion` | Required, `1.0.0`. |
| `distribution` | `private` (default: only this workspace) or `public` (other workspaces can install it). |
| `display.description` | Up to 1,000 characters. |
| `display.iconUrl` | Absolute HTTPS URL; at least 256×256 px recommended. |
| `developer.name` | Required, 2–80 characters. |
| `oauth.client_name` | Required, 2–80 characters, must not contain "Flow" or a URL. |
| `oauth.client_uri` | Absolute HTTP(S) homepage or documentation URL. Required for public applications (`distribution: "public"`). |
| `oauth.redirect_uris` | Required, 1–32 unique URIs (HTTPS, or HTTP on loopback). |
| `oauth.grant_types` | `authorization_code` (required, default) and optionally `client_credentials`. |
| `oauth.scopes` | *Flow extension.* Scopes the application may request (default `read`). |
| `webhook.url` | Required with `webhook`. HTTPS, not a loopback, private-network or internal host (the same check as every Flow webhook). |
| `webhook.resourceTypes` | Required with `webhook`, at least one, unique. Payload type names (`Issue`, `Comment`, `Attachment`, `IssueLabel`, `Reaction`, `Project`, `ProjectUpdate`, `ProjectMilestone`, `Document`, `Initiative`, `InitiativeUpdate`, `Cycle`, `Customer`, `CustomerNeed`, `User`, `IssueSLA`, `Release`, `ReleaseNote`, `IssueRelation`, and the application events `OAuthApp` (the reference app's `OAuthAuthorization` is accepted), `PermissionChange`, `AppUserNotification`, `AgentSessionEvent`) or Flow's webhook ids (`issues`, `comments`, …). Stored and exported under the payload type names. The reference app's `ProjectLabel` is not supported yet. |
| `webhook.enabled` | Default `true`. |
| `agent.assignable`, `agent.mentionable` | *Flow extension.* Add `app:assignable` / `app:mentionable`. |

Unknown fields are rejected. The JSON Schema at
`/.well-known/oauth-app-manifest.schema.json` lets editors and CI validate a
manifest locally.

### Manifest API

All endpoints require a workspace administrator.

| Request | Effect |
| --- | --- |
| `POST /api/oauth-applications/manifest/validate` | `{"valid": bool, "issues": [{"path", "message"}], "application": preview}`; never saves. |
| `POST /api/oauth-applications/manifest` | Creates the application. `201` with the application, including `clientSecret` (shown once) and, when the webhook is enabled, `webhookSecret` (the signing secret, shown once). |
| `PUT /api/oauth-applications/{id}/manifest` | Replaces the configuration; client ID and secret are kept. Omitted optional blocks (`display`, `webhook`, `agent`) are cleared. Returns `webhookSecret` the first time a webhook is enabled. |
| `GET /api/oauth-applications/{id}/manifest` | Exports the application as a manifest. |
| `GET /api/oauth-applications/{id}/installs` | This workspace's active user authorizations and app installations of the application. |

Invalid manifests return `400` with `{"error": "...", "issues": [...]}`.

### Pre-filled setup links

Link people to `/<workspace>/settings/api/applications/new` with either a
`manifest` query parameter containing the URL-encoded JSON, or the reference app's
dot-notation parameters: `distribution`, `display.description`,
`display.iconUrl`, `developer.name`, `oauth.client_name`, `oauth.client_uri`,
`oauth.redirect_uris` (repeat), `oauth.grant_types` (repeat), `webhook.enabled`,
`webhook.url`, `webhook.resourceTypes` (repeat). `manifest` wins when both are
present, and unsupported parameters are ignored. An application's page
(`/<workspace>/settings/api/applications/<id>`) downloads its manifest and
imports one from its menu, and its edit page saves through `PUT .../manifest`.
Turning Webhooks off on the form leaves the `webhook` block out of the
manifest, which clears the application's webhook.

```text
/acme/settings/api/applications/new?developer.name=Acme&oauth.client_name=Acme+Agent
  &oauth.client_uri=https%3A%2F%2Facme.dev&oauth.redirect_uris=https%3A%2F%2Facme.dev%2Fcallback
```

The webhook block configures the application-level webhook (the same settings
as `PUT /api/oauth-applications/{id}/webhook`). Every save reconciles the
managed webhook in each workspace that installed or authorized the
application: it is created, updated or removed to match the manifest.

## Settings pages

Flow's settings follow the reference app's:

- **Settings › API › OAuth applications** lists the workspace's applications
  ("N OAuth applications", each with its icon and "Updated <date>") and
  creates them at `/<workspace>/settings/api/applications/new`. An
  application's page (`…/api/applications/<id>`) shows the developer,
  description, redirect URIs and availability, then **OAuth credentials**:
  the client ID, the client secret (masked; copy reads it from
  `GET /api/oauth-applications/{id}/client-secret`, rotate asks first) and
  "Create & copy token". Its menu edits, downloads the manifest, deletes (type
  the name to confirm) or opens the application in the workspace. The icon
  picker uploads to `POST /api/oauth-applications/icon`; the returned
  `/uploads/oauth_app_icon_…` URL is accepted as `display.iconUrl`.
- **Settings › Applications** lists every application members authorized
  ("Authorized by N members") or installed as an app user. Admins load it from
  `GET /api/oauth/authorized-applications`, which includes every member's
  active authorizations. An application's page
  (`/<workspace>/settings/applications/<client_id>`) shows its permissions,
  the authorizing members and, for `actor=app` installs, the app user with its
  team access. **Revoke access** revokes every member's tokens (owners and
  admins may revoke other members' authorizations with
  `DELETE /api/oauth/authorizations/{id}`); the app user's own menu removes the
  installation.

