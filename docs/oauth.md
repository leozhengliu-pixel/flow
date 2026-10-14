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
`comments:create` and `timeSchedule:write`), then "Receive realtime updates
about your workspace" when the application has a webhook (see
[Realtime updates](#realtime-updates)), team access for `actor=app` installs,
and Cancel / Authorize. Team access offers "All public teams", "All public
teams and select other teams…" (only when the workspace has a private team; its
picker lists the private teams, including ones the administrator is not a
member of) and "Only select teams…" (every team); a picker needs at least one
team before Authorize is enabled.

An `actor=app` request for an application that is already installed in the
chosen workspace shows the reference app's re-consent card instead, without team access
or the data-sharing notice:

- When it asks for something the installation does not have yet:
  "<App> is requesting access", **Existing permissions** (the installed
  scopes) and **Requested permissions** (the new scopes, and the realtime
  permission when the installation was authorized before the application had a
  webhook), with **Update access**.
- Otherwise (the same or fewer scopes): "<App> already installed · Installed
  <date> by <member>", **Existing permissions** and **Continue**.

Either way the requested scopes are added to the installed ones (scopes are
never removed by asking for fewer), the app user, its installer and its team
access stay, and the code carries every installed scope. Redeeming it revokes
the access and refresh tokens issued to the app user before, so an
installation has one token chain at a time (client-credentials tokens are not
affected). An `actor=user` request never shows this card: a person who already
granted the scopes is sent straight back, and one asked for more sees the
regular consent screen; their earlier tokens stay valid. The consent page
reads the installation from `GET /api/oauth/installation?workspace=&client_id=`
(administrators only).

An invalid request shows Flow's mark and the error description instead, with
The reference app's wording:

| Problem | `error` | Description |
| --- | --- | --- |
| Unknown `client_id` | `invalid_client` | Could not find OAuth client with clientId `<id>`. Please check that you are logged into the correct workspace. |
| No `redirect_uri` | `invalid_request` | Invalid parameters. Make sure to include \`redirect_uri\` |
| `response_type` missing or not `code` | `invalid_request` | Invalid or missing \`response_type\`. Expected \`response_type=code\` |
| `redirect_uri` not registered | `invalid_request` | Invalid redirect_uri parameter for the application. |
| Unknown scope | `invalid_scope` | Invalid scope: `<scope>` |
| `app:*`, `customer:*` or `initiative:*` scope without `actor=app` (registered applications) | `invalid_scope` | The scopes requested are not valid for this actor mode. |

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
  "scope": "comments:create issues:create read write",
  "refresh_token": "flow_refresh_...",
  "actor": "user"
}
```

Access tokens last one hour (the reference app: 24 hours); refresh tokens last 30 days.
As in the reference app, `scope` is space-separated, sorted and uses the reference app's scope names
(`issues:create`, `comments:create`) in every token response, refresh and
introspection result and in `scopes_supported`. Requests may still use Flow's
older names `create_issues` and `create_comments`.

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
Agent scopes (`app:assignable`, `app:mentionable`) and the customer and
initiative scopes (`customer:read`, `customer:write`, `initiative:read`,
`initiative:write`) require `actor=app`: a registered
application that requests them without it gets `invalid_scope` (public MCP
clients, which request every advertised scope, have them dropped). User tokens
reach customers and initiatives through `read` and `write`.

Team access is stored as `allPublicTeams` plus `teamIds`: "All public teams"
is `allPublicTeams` alone, "All public teams and select other teams…" adds the
chosen private teams to it, and "Only select teams…" is `teamIds` alone. The
app user is a member of the chosen teams, so it reads and writes their issues
(private ones included) and can be delegated work there; other private teams
stay hidden from it.

### Realtime updates

"Receive realtime updates about your workspace" is not an OAuth scope (tokens
never carry it): the consent screen and the application's page in Settings ›
Applications list it while the application has a webhook. As in the reference app, an
installation authorized before the application had a webhook receives no
deliveries until an administrator authorizes it again (the consent screen then
lists it under **Requested permissions**); installations made with the webhook
in place, and installations created directly through
`POST /api/application-installations`, receive them.

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
| `customer:read`, `customer:write`, `initiative:read`, `initiative:write` | no | yes | Customer and initiative access for the app user ("Read data about customers in the workspace", "Read and write data about customers in the workspace", and the same for initiatives). |
| `release:read`, `release:create`, `releaseStage:update` | yes | yes | Release access ("Read releases in the workspace", "Create releases in the workspace", "Update release stages in the workspace"). |
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
| `invalid_scope` | 400 | Unsupported scope, scope not configured on the application, `admin` with `actor=app`, `app:*`, `customer:*` or `initiative:*` scopes without `actor=app` |
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
  "agent": { "assignable": true, "mentionable": true },
  "github": { "username": "acme-agent[bot]" }
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
| `webhook.secret` | *Flow extension, write-only.* The signing secret the settings form generated and showed (`flow_wh_…`). Used only when the application has no signing secret yet, otherwise ignored; never exported. Leave it out to have Flow generate one. |
| `agent.assignable`, `agent.mentionable` | *Flow extension.* Add `app:assignable` / `app:mentionable`. |
| `github.username` | *Flow extension* (the reference app's form field "GitHub username"). A GitHub user name, or a GitHub App's bot name with the `[bot]` suffix (`my-app[bot]`). Unique among the workspace's applications. Leaving `github` out clears it. See [GitHub username](#github-username). |

Unknown fields are rejected. The JSON Schema at
`/.well-known/oauth-app-manifest.schema.json` lets editors and CI validate a
manifest locally.

### Manifest API

All endpoints require a workspace administrator (signed in, or an API key with
the `admin` scope); they are part of the public contract (tag
`oauth-applications`, see [api.md](api.md#oauth-applications)).

| Request | Effect |
| --- | --- |
| `POST /api/oauth-applications/manifest/validate` | `{"valid": bool, "issues": [{"path", "message"}], "application": preview}`; never saves. |
| `POST /api/oauth-applications/manifest` | Creates the application. `201` with the application, including `clientSecret` and, when the webhook is enabled, `webhookSecret` (the signing secret: `webhook.secret` when given, otherwise a new one). |
| `PUT /api/oauth-applications/{id}/manifest` | Replaces the configuration; client ID and secret are kept. Omitted optional blocks (`display`, `webhook`, `agent`, `github`) are cleared. Returns `webhookSecret` the first time a webhook is enabled. |
| `GET /api/oauth-applications/{id}/webhook/secret` | `{"secret": "flow_wh_…"}`, the current webhook signing secret (`404` when there is none). Administrators signed in, or an admin API key (see [Credentials and API keys](#credentials-and-api-keys)). |
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
  creates them at `/<workspace>/settings/api/applications/new`. The create and
  edit form has the reference app's fields, including **GitHub username** (checked on
  submit against GitHub's public API: "Invalid GitHub username" when the
  account does not exist). With **Webhooks** on, the form shows the **Webhook
  signing secret**: a new application (or one whose webhook was never
  enabled) gets a secret generated when the form opens, shown in the clear and
  saved with the webhook; an existing secret is loaded masked, with Show and
  Copy ("The signing secret has been copied to your clipboard."). An
  application's page (`…/api/applications/<id>`) shows the developer,
  description, redirect URIs, availability and GitHub username, then **OAuth
  credentials**: the client ID, the client secret (masked; copy reads it from
  `GET /api/oauth-applications/{id}/client-secret`, rotate asks first) and
  [Create & copy token](#developer-tokens). With a webhook, **Webhooks** shows
  the delivery status (its menu disables or enables webhooks), the URL, the
  signing secret (masked; copy reads it from
  `GET /api/oauth-applications/{id}/webhook/secret`; rotate asks "Rotate your
  signing secret?" and copies the new secret) and the events, followed by
  **Webhook delivery failures**. Its menu edits, downloads the manifest,
  deletes (type the name to confirm) or opens the application in the
  workspace. The icon picker uploads to `POST /api/oauth-applications/icon`;
  the returned `/uploads/oauth_app_icon_…` URL is accepted as
  `display.iconUrl`.
- **Settings › Applications** lists every application members authorized
  ("Authorized by N members") or installed as an app user. Admins load it from
  `GET /api/oauth/authorized-applications`, which includes every member's
  active authorizations. An application's page
  (`/<workspace>/settings/applications/<client_id>`) shows its permissions
  (alphabetically, with "Receive realtime updates about your workspace" while
  the application has a webhook), the authorizing members and, for
  `actor=app` installs, the app user with its team access: the three options of
  the consent screen, each picker under its option, and **Update** asks "Update
  team access for <App>?" with the current and the new access before saving.
  **Revoke access** revokes every member's tokens (owners and
  admins may revoke other members' authorizations with
  `DELETE /api/oauth/authorizations/{id}`); the app user's own menu removes the
  installation.

## Developer tokens

**Create & copy token** on an application's page works: a
command menu ("Create developer access token…") offers **User** and **App**.

- **User** creates an `actor=user` token for you with `read` and `write` (as
  far as the application's configured scopes allow), copies it and says
  "Developer access token created and copied to your clipboard". Every click
  creates another token.
- **App** creates a token for the application's app user (installing the
  application in this workspace for all public teams if needed). The first
  time, **Select scopes** asks for its scopes (`read` and `write` on;
  `app:mentionable`, `app:assignable`, `customer:*` and `initiative:*` off).
  An app user has one developer token at a time: while it is active, **App**
  copies that same token again ("Existing app token copied to clipboard",
  listing its scopes). To change the scopes, revoke it first (**Revoke
  access** on the application's page in Settings › Applications, or
  `POST /oauth/revoke`).

| Request | Effect |
| --- | --- |
| `POST /api/oauth-applications/{id}/developer-token` `{"actor": "user"}` | A new user token; `scopes` may narrow it. |
| `POST /api/oauth-applications/{id}/developer-token` `{"actor": "app", "scopes": [...]}` | The app user's active token with `"existing": true`, or a new one with these scopes. |
| `GET /api/oauth-applications/{id}/developer-token` | The app user's active developer token (`"existing": true`), or `404`. |

The responses have the token endpoint's shape (`access_token`, `expires_in`,
`scope` with the reference app's names, `actor`, `app_user_id`). Developer tokens last
one hour, like other Flow access tokens, and have no refresh token. These
endpoints need a workspace administrator, signed in or with an admin API key.

### Credentials and API keys

The credential endpoints (`GET …/client-secret`, `POST …/rotate-secret`,
`GET`/`POST …/developer-token`, `GET …/webhook/secret`,
`POST …/webhook/rotate-secret`) accept a personal API key only when it has the
`admin` scope (or full access), is not restricted to selected teams and its
owner is a workspace admin. OAuth tokens, read-only keys, members' keys and
team-restricted keys get `403`. Secrets and tokens come back only in the
response body, with `Cache-Control: no-store`.

**Security note.** Such a key can read every application secret of the
workspace. Each read, rotation or token creation made with an API key is
written to the audit log (resource type `oauth_application`; actions
`client_secret_read`, `client_secret_rotated`, `client_secret_issued`,
`developer_token_read`, `developer_token_created`, `webhook_secret_read`,
`webhook_secret_rotated`, `webhook_secret_issued`) with the key's owner as
actor and the key's id and name, never the secret. Keep admin keys in a secret
manager with a short expiry, and revoke them when they are no longer needed.

## GitHub username

An application's **GitHub username** (the reference app: "For `actor=app` only, add a
GitHub username to associate commits and comments in GitHub with your
application") ties a GitHub account to the application's app user, the way a
member's connected GitHub account belongs to them. Use the account your
application acts as on GitHub, or a GitHub App's bot name such as
`my-app[bot]`. With the GitHub integration connected, pull requests opened by
that account and webhook events it sends are attributed to the app user in
every workspace that installed the application (`actor=app`): the review's
author, its events and the "linked a pull request" activity. A GitHub account
can belong to one application per workspace ("There is already a user in Flow
associated with this GitHub account."). Set it on the form, in the manifest
(`github.username`) or with `PATCH /api/oauth-applications/{id}`
(`githubUsername`); an empty value removes the association.

