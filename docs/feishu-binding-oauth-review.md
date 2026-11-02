# Feishu binding and account linking review

## Findings and limits

The workspace authorization middleware intercepted team binding mutations before
the handlers' team-owner checks. The regression fixture now signs in over HTTP
and uses the authentication and workspace authorization middleware, rather than
injecting an authenticated user into a bare route handler.

The old OAuth error did not establish whether code exchange or user-info read
failed. No real tenant callback was performed during this change. Simulator
failures establish the new diagnostics, not the cause of the owner's incident.
For example, a simulated HTTP 400 / platform 20049 reports
`stage=code_exchange`; HTTP 200 / platform 20005 reports `stage=identity_read`.
Permission codes have their own API/UI reason instead of being mislabeled as
application availability failures.

Browser verification also exposed a callback timing bug: the page stored an
English error before the Chinese dictionary loaded, then consumed the callback
parameters. Errors now retain their source string and translate on render.

Official documentation checked on 2026-11-02:

- [Authorization code](https://open.feishu.cn/document/common-capabilities/sso/api/obtain-oauth-code):
  the v1 authorization endpoint documents pairing PKCE with v2 token exchange
  while support for the latest token endpoint is pending. Flow sends an S256
  challenge and the original verifier.
- [v2 token exchange](https://open.feishu.cn/document/authentication-management/access-token/get-user-access-token):
  POST `https://open.feishu.cn/open-apis/authen/v2/oauth/token`, JSON body,
  top-level `access_token`, numeric `code`, `error` and `error_description`.
- [v3 token exchange](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/authentication-management/access-token/get-user-access-token-v3):
  form encoding is valid for v3. The old implementation was not missing PKCE,
  and its encoding alone is not evidence of a bug. The change selects the
  documented compatible v1/PKCE/v2 pair; Lark keeps its v2 JSON exchange.
- [User info](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/authen-v1/user_info/get):
  GET `https://open.feishu.cn/open-apis/authen/v1/user_info`, using
  `Authorization: Bearer <access_token>`. Identity is in `data.open_id` and
  `data.tenant_key`. These fields require no extra scope. Flow does not request
  directory, email, phone or offline access, and does not use profile email to
  create or match a Flow user. Tokens are used only for this read.

## Owner's real-tenant checklist

Use the review build and your own signed-in browser. Do not share credentials,
tokens, authorization codes, state values, callback URLs or raw network exports.

1. In the Feishu app settings, verify the published app is available to the
   test member and its redirect URL exactly matches
   `<Flow origin>/api/integrations/feishu/chat/identity/oauth/callback`.
   Confirm the workspace integration reports an authorized tenant.
2. As an ordinary workspace member who owns the target team, open
   `/<workspace>/settings/integrations/feishu`. Add an internal group of that
   tenant with the bot present, select your team, approve sharing and save.
   Edit and save; revoke; add again; remove. Each operation should succeed.
3. As a plain team member and then another team's owner, try those operations
   against the target team's binding. Each must be refused. Moving a binding
   to a team you own must not grant control of the original team's binding.
   External groups, another tenant's groups, missing bot membership, absent
   sharing consent and incompatible private-team project/template scope must
   still be refused. No roles should change.
4. As that ordinary member, open
   `/<workspace>/settings/account/connections`, Feishu, Connect. Approve in
   Feishu. Confirm the same Flow user is still signed in, the row is connected
   after refresh and signing in again, and workspace member count is unchanged.
5. Unlink. Start linking and deny authorization: no link. Start again and
   finish in another Flow login session: no link. Try a different tenant's
   account: no link. Revisit a completed callback locally: no second link or
   code exchange. Start again after each refused attempt.
6. Confirm only a workspace administrator can replace app credentials or
   disconnect the whole integration. Personal Connect must remain available
   without workspace or tenant administration. Check English and zh-CN.
7. If linking fails, collect only the timestamp and the
   `chat_identity_oauth` diagnostic's provider, stage, HTTP status, numeric
   platform code and invalid-response flag. Report the visible stable reason
   code. Do not include full callback parameters or response bodies.

Only a real tenant can establish live authorization/PKCE compatibility, actual
app availability and granted permissions, published bot/group membership,
real user-info contents and successful persistence after the real callback.
The incident's actual failing stage remains unproven until step 4 or 7.

## UI review without provider sign-in

With Feishu configured in a review workspace, open
`/<workspace>/settings/account/connections?chat_provider=feishu&chat_auth=error&chat_auth_error=chat_code_exchange_failed`.
Repeat with `chat_identity_read_failed` and `chat_oauth_permission_required`.
Each shows its distinct message, clears the callback parameters, and offers
Retry. Retry clears the alert and keeps Connect available. Check both languages
and themes. The browser tests also hold a simulated start response to verify
Connect is disabled while pending, then enabled after failure. No provider
credentials or provider sign-in are involved in these UI checks.

The 36 reviewed crops in `/tmp/codex-feishu-binding-oauth/shots/` show all three
intentional error banners for English/zh-CN, light/dark and 1440/375/390 widths.
Text wraps without crossing Retry or the viewport edge. Resting, hover,
keyboard focus-visible, active and pending-disabled states are covered; these
buttons have no selected or expanded state. No avatar or chip control changed.

## Verification commands

All commands below exited 0. Heavy commands used `~/.flow-local/heavy.sh`.
Go commands ran from `api/`; web commands ran from `web/` with the requested
Node path. No real model calls or Docker containers were used.

```sh
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server -run 'Test(Chat(IdentityProtocol|IdentityDiagnostics|AuthorizationGate|PrivateTeamBinding|PublicTeamBinding|Tenant|BindingProject|BindingRejectsPrivate|ApplicationSetupIdentityOAuth|IdentityBindingRequires|MembersCanBind|Initiative)|PermissionPathClassification|WorkspacePermissionMatrix|SecuritySurfaceIntegrationCallbacksAndWebhooks)' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server -run '^TestChatPrivateTeamBindingNeedsTeamOwnerAuthorization$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server -run '^TestChatTenantAuthorizationIsAutomaticAndSeparateFromWorkspaceAdministration$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server -run '^TestChatProviderRefreshesRejectedTokenAndClassifiesFailures$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./cmd/server -run '^TestChatIdentityProtocolLinksExistingMemberAndPersists$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet ./...
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet ./cmd/server
~/.flow-local/heavy.sh npm run typecheck
~/.flow-local/heavy.sh npm run lint
~/.flow-local/heavy.sh npx vitest run --maxWorkers=2 src/lib/chat-integrations-api.test.ts src/components/settings/chat-connection-rows.test.tsx src/components/settings/chat-integration-settings.test.tsx src/components/settings/chat-platform-copy.test.tsx src/components/settings/chat-platform-entry-points.test.tsx
~/.flow-local/heavy.sh npx playwright test --config=/tmp/codex-feishu-binding-oauth/playwright.config.mjs
```

The final component run passed 68 tests in 5 files. The final browser run passed
12 cases using bundled Chromium, a scratch API database and a Vite server.
The local browser config selects only `chat-identity-errors.spec.ts` and points
to those scratch servers; the spec uses `workspace-fixture` and can also run
under the repository's normal CI configuration. Error bodies and route payload
shapes are unchanged; no new routes or migrations were introduced.
