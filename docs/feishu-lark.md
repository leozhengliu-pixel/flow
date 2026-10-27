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
   `im:chat:read`. Flow does not need access to all group messages, the full
   contact directory, phone numbers, or email addresses.
4. Register the redirect URL
   `https://flow.example.com/api/integrations/feishu/chat/identity/oauth/callback`
   (replace `feishu` with `lark` for Lark, and use your actual Flow origin).
5. In Flow, open **Settings > Integrations > Feishu** or **Lark**. A workspace
   administrator enters the App ID, App Secret, Encrypt Key, and Verification
   Token. Saving verifies the application token and published bot. Blank secret
   fields on subsequent saves preserve the existing encrypted values.
6. Open **Personal > Connected accounts** and link the administrator's own
   Feishu or Lark account to verify the application's tenant.
   Follow the authorization in the same signed-in browser. The app remains
   pending until the tenant has been verified; saving credentials alone does
   not imply a complete connection.
7. Copy Flow's callback URL into the application's HTTP event and callback
   configuration. Enable encryption using the same Encrypt Key and
   Verification Token. Subscribe to `im.message.receive_v1` and the current
   `card.action.trigger` callback; do not also subscribe to the legacy card
   callback. Publish configuration changes when required by the platform.
8. Add the bot to an internal group and allow it to speak. Back in Flow, select
   the group, a public team, an optional compatible project/template, and the
   notification and issue-action options. Saving a group binding explicitly
   approves disclosing the selected resource summaries to that group.

Use **Test connection** to recheck credentials and bot publication. Event
subscription, app availability, permissions, and group speech settings must
also be configured correctly in the platform console.

## Notifications and issue actions

Notifications can include issue creation, state and assignee changes, new
comments, and project updates. They include an approved summary and Flow link;
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
or restricted teams/issues and projects involving private teams are excluded.
Workspace policies that disable external processing also disable this path.
Group membership itself never grants Flow read or write permissions.

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
