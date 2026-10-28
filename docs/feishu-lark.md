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
   capability, set its availability, and publish the application.
3. Grant `im:message:send_as_bot`, `im:message.group_at_msg:readonly`, and
   `im:chat:read`, plus read access to tenant information and to a user's
   basic contact profile (Flow reads only the tenant key and whether the
   authorizing account is a tenant administrator). Flow does not need access
   to all group messages, the full contact directory, phone numbers, or email
   addresses.
4. Register the redirect URL
   `https://flow.example.com/api/integrations/feishu/chat/identity/oauth/callback`
   (replace `feishu` with `lark` for Lark, and use your actual Flow origin).
5. In Flow, open **Settings > Integrations > Feishu** or **Lark**. A workspace
   administrator enters the App ID, App Secret, Encrypt Key, and Verification
   Token. Saving verifies the application token and published bot. Blank secret
   fields on subsequent saves preserve the existing encrypted values.
6. Complete the **enterprise tenant authorization** under **Permissions** on
   the same page (see [Permissions](#permissions)). A workspace administrator,
   or the member they designate as **Tenant authorizer**, selects **Authorize
   tenant** and signs in with a chat account that is a tenant administrator of
   the tenant the application is installed in. The app stays pending until
   then; saving credentials alone does not complete the connection, and a
   personal account link never authorizes the tenant.
7. Copy Flow's callback URL into the application's HTTP event and callback
   configuration. Enable encryption using the same Encrypt Key and
   Verification Token. Subscribe to `im.message.receive_v1` and the current
   `card.action.trigger` callback; do not also subscribe to the legacy card
   callback. Publish configuration changes when required by the platform.
8. Add the bot to an internal group and allow it to speak. Back in Flow, select
   the group, a team, an optional compatible project/template, and the
   notification and issue-action options. Saving a group binding explicitly
   authorizes disclosing the selected resource summaries to that group; Flow
   records who authorized it and when. Team owners do this from the team's
   notifications page as well.

Use **Test connection** to recheck credentials and bot publication. Event
subscription, app availability, permissions, and group speech settings must
also be configured correctly in the platform console.

## Permissions

A chat integration has three permission domains. Each is granted on its own,
and the settings page shows which one is missing and who can grant it; API
refusals carry a `permissionDomain` (`tenant`, `workspace` or `team`)
next to the stable error `code`.

- **Enterprise tenant authorization** (`tenant`). Done once per application,
  on the chat platform, by an account that is a tenant administrator of the
  application's own tenant. Flow checks this with the platform (the tenant the
  application token belongs to, and the account's tenant administrator flag);
  a Flow workspace role never stands in for it. Workspace administrators may
  start it, or designate the member who holds that platform role. Flow records
  who completed it, when, and the tenant key. Afterwards nobody is asked to
  confirm the tenant again: listing groups and binding them use the
  application's own token. Applications connected before this step existed are
  shown as authorized (completed before tenant authorization was separate).
- **Workspace integration** (`workspace`). Workspace administrators manage the
  application settings, the tenant authorizer, and every group binding.
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
changing, revoking and removing bindings, designating the tenant authorizer
and completing the tenant authorization are recorded in the audit log.

Flow still refuses external groups, groups of another tenant, groups the bot
is not in, and accounts or callbacks of another tenant.

## Notifications and issue actions

Notifications can include issue creation, state and assignee changes, new
comments, project updates, and initiative updates (sent to the groups of the
initiative's lead team). They include an approved summary and Flow link;
comment bodies and attachments are not copied to the group.

Group users link their own Flow account from **Personal > Connected accounts**.
Linking does not grant workspace or team membership, and names or email
addresses are never used to match accounts automatically.

![Feishu and Lark in Connected accounts](media/integrations/connected-accounts.png)

Mention the bot with `@Flow create issue <title>` or `@Flow 创建事项 <title>`.
The bot sends a personal confirmation form with the bound team/project,
title, state, priority, and assignee. After creation it replies to the source
message with an issue card. Shared issue cards open personal forms for state,
assignee, and comment changes, so other group members cannot see a person's
drafts. Ordinary group messages are not synchronized as comments.

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

Platform references: [message events](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive),
[sending messages](https://open.feishu.cn/document/server-docs/im-v1/message/create),
[card callbacks](https://open.feishu.cn/document/feishu-cards/card-callback-communication),
[updating cards](https://open.feishu.cn/document/server-docs/im-v1/message-card/patch).
