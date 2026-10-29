# Feishu and Lark

Flow supports notifications and issue actions through an enterprise custom
application bot. Feishu and Lark are separate integrations with separate app
credentials and tenants. Marketplace applications and group webhook bots are
not supported by this connection flow.

## Connect an application

1. Configure `FLOW_APP_URL` with the public HTTPS origin of Flow. Configure
   `FLOW_CONNECTOR_SECRET_KEY` with a stable base64-encoded 32-byte encryption
   key, shared by all Flow replicas. App credentials are stored in the encrypted
   connector-secret store, not in bootstrap responses or workspace exports.
2. Create a custom application in the [Feishu developer console](https://open.feishu.cn/app)
   or [Lark developer console](https://open.larksuite.com/app). Enable its bot
   capability, set its availability range, and create a version. The
   tenant administrator approves and publishes that version in the admin
   console; this is the tenant authorization (see [Permissions](#permissions)).
3. Grant these app permissions:
   - `im:message:send_as_bot` (send messages as the bot);
   - `im:message.group_at_msg:readonly` (read group messages that mention
     the bot);
   - `im:chat:read` (read the bot's groups);
   - `tenant:tenant:readonly` (获取企业信息, obtain the tenant's
     information), so Flow reads the tenant's key and name with the app's
     tenant token on every connection check. It is an ordinary app
     permission, granted when the version is approved; nobody signs in
     for it.

   For [creating issues from messages](#create-an-issue-from-a-message),
   also grant `im:message:readonly` (读取单聊、群组消息) and, so the bot can
   confirm a group message before replying under it, `im:message.group_msg`
   (获取群组中所有消息). Without them the shortcut still works, but its result
   goes to the member's bot conversation instead of the group.

   Flow needs no contact scopes, the contact directory, phone numbers, or
   email addresses.
4. Register the redirect URL
   `https://flow.example.com/api/integrations/feishu/chat/identity/oauth/callback`
   (replace `feishu` with `lark` for Lark, and use your actual Flow origin).
5. In Flow, open **Settings > Integrations > Feishu** or **Lark**. A workspace
   administrator enters the App ID, App Secret, Encrypt Key, and Verification
   Token. Saving obtains a tenant token and reads the bot's info; when both
   succeed, the tenant is authorized automatically. If the version is not
   approved yet, saving reports that and nothing is stored. Blank secret
   fields on subsequent saves preserve the existing encrypted values.
6. Check **Permissions** on the same page: **Tenant** shows the tenant the
   app is connected to (its name and tenant key), or that the app is not
   approved yet.
7. Copy Flow's callback URL into the application's HTTP event and callback
   configuration. Enable encryption using the same Encrypt Key and
   Verification Token. Subscribe to `im.message.receive_v1` and the current
   `card.action.trigger` callback; do not also subscribe to the legacy card
   callback. For [link previews](#link-previews), also subscribe to the
   `url.preview.get` callback. Publish configuration changes when required
   by the platform.
8. Add the bot to an internal group and allow it to speak. Back in Flow, select
   the group, a team, an optional compatible project/template, and the
   notification and issue-action options. Saving a group binding explicitly
   authorizes disclosing the selected resource summaries to that group; Flow
   records who authorized it and when. Team owners do this from the team's
   notifications page as well.

Use **Test connection** to recheck credentials and bot publication (and with
them the tenant authorization). Event subscription, app availability,
permissions, and group speech settings must also be configured correctly in
the platform console.

## Permissions

A chat integration has three permission domains. Each is granted on its own,
and the settings page shows which one is missing and who can grant it; API
refusals carry a `permissionDomain` (`tenant`, `workspace` or `team`)
next to the stable error `code`.

- **Tenant** (`tenant`). Granted on the chat platform: the tenant
  administrator approves and publishes the custom app's version in the admin
  console (and sets its availability range there). Until then the platform
  does not issue the app a tenant token, so Flow authorizes the tenant
  automatically once the configured App ID and App Secret obtain a
  `tenant_access_token` and the bot's info is readable. Nobody signs in for
  it in Flow, and a Flow workspace role never stands in for it. Flow records
  when it was authorized, the tenant key and the workspace administrator who
  configured or tested the connection. It re-checks on **Test connection**,
  every few hours (every few minutes while the app is not approved) and after
  the platform refuses the app's token. When the token or the bot's info is
  refused, the page shows "The app isn't approved or published in the tenant
  yet, or its credentials are wrong" (`chat_tenant_not_approved`, or
  `chat_bot_unpublished` when the bot capability is off), and notifications
  and group actions stop until the next successful check (updates made in
  between are not sent later). An unreachable
  platform (`chat_platform_unreachable`) never withdraws the authorization.

  The tenant token and bot info do not name the tenant, so every check then
  reads the tenant's information with the same token
  (`GET /open-apis/tenant/v2/tenant/query`, permission
  `tenant:tenant:readonly`) and records its `tenant_key` and name at once;
  the page shows both. Every group, account and callback must match that
  key.

  - If the app lacks the permission (the platform answers `99991672`, or
    the endpoint's own `1184001`), the tenant stays authorized and the page
    asks to "Grant the app the 「获取企业信息」 permission
    (tenant:tenant:readonly) and publish a new version so Flow can show the
    tenant" (`chat_tenant_info_permission_missing`). Only then is the key
    taken from the first platform answer under the app's credentials that
    names it: the bot's internal groups (only groups of the app's tenant can
    be internal for a custom app's bot) or a member's linked account (only
    accounts of that tenant can authorize a custom app). The page marks
    such a key as first seen. Checks run every few minutes until the
    permission is granted, and the next one records the key and name from
    the tenant information.
  - Another refusal of the tenant information
    (`chat_tenant_info_unavailable`) keeps the tenant authorized without
    that fallback; Flow reads it again on the next check, or when a member
    links an account or the group list is opened, and linking waits for it.
  - A recorded key is never replaced. If the tenant information names
    another tenant than the recorded one (for example a key first seen
    earlier), the check withdraws the authorization and shows the mismatch
    with both keys (`chat_tenant_mismatch`, permission domain `tenant`);
    notifications, group actions and linking stop. Disconnect the
    application and connect it again to start over with the platform's
    tenant.

  Connections set up while tenant authorization was a sign-in step need
  nothing: authorized ones stay authorized and are rewritten as
  automatically authorized on their next successful check, and ones still
  waiting for the sign-in are authorized by the first successful check.
  Connections stored without a tenant key, or with a first-seen one, get
  the key and name from the tenant information on their next check (which
  is due at once for connections never checked).
- **Workspace integration** (`workspace`). Workspace administrators manage the
  application settings and every group binding.
- **Team bindings** (`team`). A team's owners (inherited from parent teams)
  bind its groups; workspace administrators administer every team and so may
  too. Other members cannot create, change, revoke or remove a team's
  bindings, and see none of them.

### Private teams

A private team's owner may bind it to a specific internal group. The binding
records the authorizer, the time, the group, the shared notification types and
project, whether the group may create and update issues, and its revocation.
It never adds team memberships or widens who can see the team in Flow: its
notifications go to that group only, and group actions run as each member's
own linked Flow account with that account's permissions. A private team
binding stops as soon as its authorizer can no longer administer the team.
Projects shared with another private team, issues with their own access
restrictions, and initiatives with a private contributing team are never sent.

Public teams keep working as before; their bindings now also record who
authorized them. Binding a public team needs the same team binding permission,
so the rule is the same for every team.

**Revoke** stops a binding and keeps it listed (with who revoked it and when)
until the group is bound again or the binding is removed. Authorizing,
changing, revoking and removing bindings, the tenant being authorized or
reported as not approved, the tenant key being recorded (with its source:
`platform` or first seen, `observed`) and a tenant mismatch are in the audit
log. Entries of the former tenant sign-in and authorizer designation stay
readable there.

Flow still refuses external groups, groups of another tenant, groups the bot
is not in, and accounts or callbacks of another tenant. A member outside the
app's availability range who tries to link their account is told so
(`chat_app_unavailable_to_user`), separately from the tenant state.

## Notifications and issue actions

Notifications can include issue creation, state and assignee changes, new
comments, project updates, and initiative updates (sent to the groups of the
initiative's lead team). They include an approved summary and Flow link;
comment bodies and attachments are not copied to the group.

Group users link their own Flow account from **Personal > Connected accounts**.
Linking does not grant workspace or team membership, and names or email
addresses are never used to match accounts automatically.

![Feishu and Lark in Connected accounts](media/integrations/connected-accounts.png)

### Personal notifications

A member who linked their account can also receive their own Flow
notifications as a direct message from the app's bot: **Personal >
Notifications > Lark** (or **Feishu**) turns them on and chooses the same
notification types as email and desktop. Each message is a card with the
notification's title, a line saying what happened and **Open in Flow**; it
is sent through the chat outbox with its rate limit and retries.

A message is sent only while the tenant is authorized, the account is still
linked and the member is still active in the workspace, and only when they
can still open the issue it links to; workspaces with sensitive data
protection send none. Unlinking or leaving the workspace stops delivery,
including messages already queued. When the platform refuses a message
because the app isn't available to the member's account (its availability
range), the notifications row says so; the next delivered message clears it.

Mention the bot with `@Flow create issue <title>` or `@Flow 创建事项 <title>`.
The bot sends a personal confirmation form with the bound team/project,
title, state, priority, and assignee. After creation it replies to the source
message with an issue card. Shared issue cards open personal forms for state,
assignee, and comment changes, so other group members cannot see a person's
drafts. Ordinary group messages are not synchronized as comments.

### Card actions

Issue cards in a group and personal notification cards about an issue carry
**Assign to me**, a status select, a priority select and **Open in Flow**
(group cards also keep the private State, Assignee and Comment forms). The
platform sends each click to the `card.action.trigger` callback. Flow
answers at once with a short toast and runs the change in its chat outbox:

- as the clicking member's linked Flow account and with its permissions (a
  viewer of the issue is refused; an unlinked member is asked to link their
  account);
- on a group card, only while the group's binding still allows updates and
  the issue is still in its scope;
- on a personal card, only for its recipient (clicks by anyone else are
  rejected).

Each click is its own change, on the issue's current state; a repeated
callback is applied once. The card is then redrawn in place with the new
status, priority and assignee (group cards in every bound group that shows
the issue). A refusal reaches the member privately in their Flow language
(Chinese or English; without a choice, Chinese on Feishu and English on
Lark) with a stable code:

| Code | Meaning |
| --- | --- |
| `chat_binding_updates_disabled` | The group's binding does not allow updating issues. |
| `chat_binding_create_disabled` | The group's binding does not allow creating issues. |
| `chat_binding_unavailable`, `chat_binding_scope_changed` | The binding was revoked, removed or changed after the card was sent. |
| `chat_issue_unavailable`, `chat_issue_outside_binding` | The member can no longer open the issue, or it left the group's scope. |
| `chat_action_forbidden` | The member's Flow account may not make this change. |
| `chat_action_invalid`, `chat_form_invalid` | The value or form is no longer valid (for example a status of another team). |
| `chat_action_conflict` | The issue changed at the same moment; try again. |
| `chat_reconnect_required` | The linked chat account expired; link it again. |
| `chat_team_unavailable` | No team the member can create issues in is available here. |
| `chat_message_unavailable` | Flow could not read the message the shortcut was used on. |
| `chat_action_expired`, `chat_action_failed` | The card is too old, or Flow could not apply the change. |

### Create an issue from a message

Members create an issue from any message with the message shortcut
**创建事项** / **Create issue** (in the message's **More** menu). The
platform opens a message shortcut in a web app, so the app needs the web
app capability:

1. In the developer console, add the **Web app** (网页应用) capability. Set
   its desktop and mobile homepage to
   `https://flow.example.com/api/integrations/feishu/<connection id>/chat/message-action`
   (`lark` for Lark): Flow's callback URL from the settings page with its
   final `webhook` replaced by `message-action`.
2. In the web app's configuration, add the **Message shortcut**
   (消息快捷操作) scenario, name it 创建事项 (Create issue) and enter the
   web app's AppLink in sidebar mode, for example
   `https://applink.feishu.cn/client/web_app/open?appId=<App ID>&mode=sidebar`
   (`applink.larksuite.com` for Lark).
3. Add Flow's origin to the web app's trusted domains (H5 可信域名), grant
   the message permissions listed in
   [Connect an application](#connect-an-application) and publish a version.

The page reads the selected message with the client API
`getBlockActionSourceDetail` (signed with the app's JSAPI ticket) and signs
the member in with a one-time code (`requestAccess`); it needs no Flow
session. Flow resolves the code to the member's platform account, which must
belong to the app's tenant and be linked to a Flow account; an unlinked
member gets a card to link their account. The bot then sends a private form:
team, title and description prefilled from the message (with a link back to
its chat), assignee and priority. Submitting creates the issue as that
member, with their permissions, and replies under the original message with
the issue card.

- In a bound group, the binding's **create issues** option applies and the
  team is the group's bound team (with its project and template); the reply
  is the binding's issue card with its controls.
- In another group or chat, the form offers the teams the member can create
  issues in. The reply under the message appears only for an issue of a
  public team; otherwise the issue card goes to the member's bot
  conversation.
- Flow reads the message again with the bot. When the bot cannot read it
  (it is not in the chat, or lacks the message permissions), the form still
  opens and a bound group's option still applies, but the result goes to
  the member's bot conversation only.
- With sensitive data protection on, the result is only a Flow link in the
  member's bot conversation.

The platform's AppLink opens a chat, not a single message, so the link in the
description opens the source message's chat.

### Link previews

Flow links to issues, projects, documents and initiatives posted in a chat
expand into a preview card: status, assignee, priority and team for issues;
status, lead, priority and teams for projects; status, owner and teams for
initiatives; title and the beginning of the text for documents. To turn them
on:

1. In the developer console, add the **Link preview** (链接预览) capability
   and register Flow's URL rules, for example `flow.example.com/*/issue/**`,
   `flow.example.com/*/project/**`, `flow.example.com/*/document/**` and
   `flow.example.com/*/initiative/**` (no scheme; at most 10 rules).
2. Subscribe to the `url.preview.get` callback at Flow's callback URL (same
   encryption and verification token). The callback needs no extra
   permission; Flow does not ask for `user_id`.
3. Publish a version. Previews work only with a published version and for
   members in the app's availability range.

The platform asks for a preview once per viewer and shows it to that viewer
only, so each preview is filtered by the viewer's linked Flow account:

- content the viewer cannot open in Flow is not expanded, and viewers who
  have not linked an account only see a hint to link it;
- in a group (and whenever Flow cannot confirm that a chat is a direct
  conversation), content of private or restricted teams, issues with their
  own access lists and documents shared with specific people are expanded
  only when the group is bound to that team with its owner's authorization;
- with sensitive data protection on, nothing is expanded.

Flow answers within the platform's three-second limit and does not push
preview updates; a preview shows the state when the viewer's client fetched
it.

Creation uses the target team's effective workflow and default state,
including inherited workflows. A template must be compatible with that
team. Updates carry an expected issue version; stale cards cannot overwrite
newer changes. All actions recheck the actual Flow account's current
membership, resource permissions, and authentication policy. Deactivated or
unlinked accounts cannot continue using old cards.

Only internal groups belonging to the connected tenant can be bound. Private
or restricted teams are included only with their owners' authorization (see
[Private teams](#private-teams)). Workspace policies that disable external
processing also disable this path. Group membership itself never grants Flow
read or write permissions.

## Lark or Feishu as the deployment's chat platform

Set `FLOW_CHAT_PLATFORM=lark` or `feishu` (Helm `integrations.chat.platform`)
to make it the deployment's first-class chat platform. The team home shows
**Connect Lark group** (飞书 for Feishu in Chinese), the team's notifications
page manages its groups, project menus and the project details sidebar open
the project's groups, project and initiative update settings, onboarding and
the integration catalog point to it. The default (`slack`) keeps the Slack
entry points; the other chat integrations stay available in Settings >
Integrations either way.

## Delivery and recovery

Incoming events are authenticated and persisted before they are acknowledged.
Repeated message deliveries use the platform message ID for deduplication.
Issue and comment creation use stable IDs so a worker restart after a commit
cannot create the same item twice.

Notification consumption uses Flow's persisted, commit-ordered domain-event
log. The scheduler retries transient failures and rate limits with bounded
backoff. Database job claims and a workspace execution lease coordinate
multiple replicas, including deployments without Redis. Disconnecting removes
the connection's queued work, group bindings, account bindings, and actions.

The platform only guarantees send-request UUID deduplication for one hour.
Delivery retries use the same UUID, but an unknown result retried outside that
window cannot provide an absolute exactly-once external-delivery guarantee.

Previously sent cards also have platform expiry limits: shared cards can be
updated for 14 days, and interactions expire after 30 days. Use the Flow link
or a current card when an old message is no longer interactive. Individual
confirmation forms expire earlier to prevent stale operations.

Personal card inputs accept up to 1,000 characters, subject to Flow's title
byte limit. State dropdowns contain at most 100 options; assignee dropdowns
contain at most 99 active Flow users plus the unassigned option, retaining
the current or template assignee. Use Flow for a selection outside those
limits. Forms that exceed the card size limit return a Flow link instead.

Keep the domain-event retention window longer than an expected service outage.
If the chat consumer falls behind retained history, Flow reports a cursor
gap instead of silently treating old events as delivered. Already accepted
chat commands can still be processed independently.

## Verification

The repository tests cover mocked platform authorization, encrypted callback
verification, tenant isolation, duplicate events, permissions, workflow
defaults, stale updates, durable creation, and retry/lease behavior. Before
production use, complete a live acceptance run with a published test app and
internal group on each platform: connect, link a member, receive a real
notification, create an issue, change its state/assignee, and add a comment.
Also create an issue with the message shortcut in a bound group and in a
direct conversation, use the card controls on a group card and on a personal
notification card, and post an issue link in a bound and an unbound group as
a member of a private team and as one outside it.

### Verify with a test tenant

The "not approved" detection relies on platform behavior the repository tests
can only fake. Confirm it once per platform on a test tenant:

1. Create a custom app with the bot capability and a version, but do not
   approve it yet. In Flow, save its credentials: does
   `/open-apis/auth/v3/tenant_access_token/internal` refuse the token (Flow
   then reports `chat_tenant_not_approved`), or does it issue one? If it
   issues one, does `/open-apis/bot/v3/info/` refuse or return no `open_id`?
   Note the HTTP status and `code` of each.
2. If a token is issued before approval, call `GET /open-apis/im/v1/chats`
   with it and note how the group read fails (status and `code`); Flow lists
   groups only after the tenant check succeeds.
3. Approve and publish the version in the admin console, then select **Test
   connection**: the tenant should show as connected, with the tenant's
   name and key from `GET /open-apis/tenant/v2/tenant/query`. Compare the
   key with the `tenant_key` of an internal group the bot is in, or of a
   linked account.
4. Create a version without the 「获取企业信息」 permission
   (`tenant:tenant:readonly`), publish it and select **Test connection**:
   note the HTTP status and `code` of the tenant information request (Flow
   expects `99991672`, or `1184001`, and then asks for the permission). The
   tenant should stay connected, and the tenant key should appear once the
   bot is in an internal group or a member links an account, marked as
   first seen. Grant the permission in a new version: the next check should
   show the tenant's name and keep the same key.
5. Remove a test member from the availability range and have them link their
   account: note how the platform refuses (the authorize page itself, the
   token exchange or `/open-apis/authen/v1/user_info`). Flow reports
   `chat_app_unavailable_to_user` when `user_info` refuses the account. For a
   member who linked before being removed from the range, turn on personal
   notifications and confirm that the direct message is refused with code
   `230013`, which Flow shows as "the app isn't available to your account".
6. Withdraw or disable the app in the admin console: the next periodic
   check, or the next delivery after the token is refused, should show the app
   as not approved.

Platform references: [message events](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive),
[message shortcut](https://open.feishu.cn/document/client-docs/extensions/message-shortcut),
[getBlockActionSourceDetail](https://open.feishu.cn/document/getBlockActionSourceDetail),
[reading a message](https://open.feishu.cn/document/server-docs/im-v1/message/get),
[link preview](https://open.feishu.cn/document/uAjLw4CM/ukzMukzMukzM/development-link-preview/link-preview-development-guide),
[url.preview.get callback](https://open.feishu.cn/document/uAjLw4CM/ukzMukzMukzM/development-link-preview/pull-link-preview-data-callback-structure),
[chat AppLink](https://open.feishu.cn/document/uAjLw4CM/uYjL24iN/applink-protocol/supported-protocol/open-a-chat-page),
[sending messages](https://open.feishu.cn/document/server-docs/im-v1/message/create),
[card callbacks](https://open.feishu.cn/document/feishu-cards/card-callback-communication),
[updating cards](https://open.feishu.cn/document/server-docs/im-v1/message-card/patch),
[tenant information](https://open.feishu.cn/document/server-docs/tenant-v2/query)
(Lark: [tenant information](https://open.larksuite.com/document/server-docs/tenant-v2/query)),
[generic error codes](https://open.feishu.cn/document/server-docs/api-call-guide/generic-error-code).
