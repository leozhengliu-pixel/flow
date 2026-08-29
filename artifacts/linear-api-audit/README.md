# Linear GraphQL API vs Flow capability audit

Audit date: 2026-08-29

Linear source: `linear/linear` commit
`fed840a976a755c8a9e5ecb6948f66307114e88c` (2026-08-28),
`packages/sdk/src/schema.graphql`.

## Scope and method

- Parsed the complete 51,449-line GraphQL schema with `graphql-js`.
- Enumerated every root Query, Mutation, and Subscription.
- Enumerated all 119 object types implementing `Node`.
- Extracted the original 283 registered Flow REST routes and all Flow domain structs.
- Generated heuristic operation-to-handler candidates, then manually reviewed
  the domain-level gaps below.
- One Flow PATCH endpoint can cover several specialized Linear mutations, so
  the heuristic counts are a triage signal rather than an endpoint parity KPI.

## Inventory

| Root | Linear operations |
| --- | ---: |
| Query | 169 |
| Mutation | 373 |
| Subscription | 82 |
| **Total** | **624** |

77 operations are marked internal or deprecated. The remaining 547 public
operations were heuristically classified as:

| Initial classification | Count |
| --- | ---: |
| likely implemented | 139 |
| partial candidate | 250 |
| likely missing | 158 |

## Post-audit implementation result

The audit was followed by a capability implementation pass. Flow now registers
401 REST/SSE routes. The generated `flow-routes.json` was refreshed after that
pass and is the current route inventory.

The pass added persisted models, authorization, backend behavior, tests, and UI
where the capability is user-facing. It did not add one REST endpoint for every
Linear convenience mutation; Flow update endpoints continue to cover multiple
GraphQL mutations when their business effects are identical.

| Capability group | Implemented behavior |
| --- | --- |
| Dashboards and insights | Dashboard/widget CRUD, visibility, sharing/revocation, subscriptions, CSV export and responsive UI |
| Search and filters | Weighted cross-entity search, bilingual synonyms, facets, filter suggestions and cursor pagination |
| Triage and workflow automation | Ordered/round-robin responsibilities, routing history, manual/scheduled/issue-created workflows, retries and run history |
| Email and push | Verified/rotatable intake addresses, Message-ID idempotence, issue creation, push-subscription lifecycle and delivery queue |
| Enterprise identity | OIDC/SAML providers, discovery domains, PKCE/nonce/state, external identities, session listing and revocation |
| Integrations and VCS | Provider scopes/channels, linkbacks, durable bounded retries, delivery stats, Git automation states/branches and webhook attachments |
| Imports and migrations | Preview, interactive mapping/invites, resumable jobs, cancellation/retry, Flow bundles, Linear scan/import and rollback manifests |
| Relations and history | First-class project/initiative relations, document drafts, release notes/history and customer request archive lifecycle |
| Customer taxonomy | First-class customer statuses and tiers with CRUD/archive behavior |
| Collaboration/content | Team resources, posts, feed items, meetings, document draft publishing and revision conflict detection |
| Agent and AI | Agent activity, conversations and prompt progress persistence |
| Usage and billing | Usage alerts, paid-subscription projection and threshold notifications |

Network calls are performed outside workspace mutation locks, public list APIs
are bounded or cursor-paginated, retry queues are capped, webhook/intake paths
are idempotent, and bootstrap responses filter private/team-scoped content.

## Domain review (pre-implementation baseline)

| Domain | Flow status | Important gaps | Priority |
| --- | --- | --- | --- |
| Issues | Partial/strong | Source-specific import assistants and validation; filter/title/repository suggestions; issue drafts/history subscriptions; external sync lifecycle | P1 |
| Issue labels | Partial/strong | Explicit retire/restore lifecycle and real-time label events; add/remove convenience mutations are covered by PATCH semantics | P2 |
| Issue relations | Strong | Flow supports issue relations; external provider relation/history events are less complete | P2 |
| Projects | Partial/strong | First-class typed `ProjectRelation`; external sync disable/lifecycle; filter suggestions; full history entity | P1 |
| Project milestones | Strong | Internal milestone move operations are excluded | P3 |
| Project updates | Strong | Exact diff/history semantics and update subscriptions are partial | P2 |
| Initiatives | Partial/strong | First-class `InitiativeRelation`; relation ordering; label retire/restore; filter suggestions | P1 |
| Initiative to project | Partial | Association exists, but there is no dedicated relation entity/query or ordering lifecycle | P2 |
| Cycles | Strong | `cycleShiftAll` bulk date shift and equivalent automation are missing | P2 |
| Documents | Partial/strong | Dedicated content-history query, content drafts, revision/content subscriptions, and draft lifecycle UI | P1 |
| Saved views | Partial/strong | First-class `ViewPreferences`, subscriber-count query, richer per-user display preference persistence | P1 |
| Dashboards/Insights | Missing entity | Flow has an analytics page but no saved Dashboard entity, widgets, sharing, subscriptions, or dashboard CRUD | P0 |
| Customers | Partial | Customer Status and Tier are settings rather than first-class entities; merge/upsert/unsync lifecycle | P1 |
| Customer needs | Partial | Dedicated need queries, archive/unarchive, source attachment conversion, notification entities | P1 |
| Notifications | Partial/strong | Mark-read/archive/snooze-all operations; category-channel subscription mutation; entity-specific GraphQL subscriptions | P1 |
| Notification subscriptions | Partial | Generic Flow subscriptions exist, but target-specific subscription entities and event-type lifecycle are incomplete | P1 |
| Favorites | Strong enough | Linear exposes a richer first-class Favorite entity and subscriptions; Flow covers common behavior | P3 |
| Attachments | Partial | Provider-specific linkback lifecycle for GitHub/GitLab/Jira/Slack/Front/Intercom/Zendesk/Salesforce/Discord; URL lookup and Slack sync | P0/P1 |
| Releases | Partial/strong | Release history entity; stage archive/unarchive; exact release-note entity/history; provider sync diagnostics | P1 |
| Release pipelines | Strong | Flow covers pipeline CRUD/access/reorder; some provider diagnostics remain different | P2 |
| Search | Partial | Semantic search, facets, filter suggestions, VCS branch/repository suggestions | P1 |
| Triage responsibility | Missing | Responsibility entities, rotations/ownership, CRUD and routing integration | P0 |
| Workflow automation | Partial | Loops cover scheduled automation, but generic WorkflowDefinition, WorkflowCronJobDefinition, notifications, and target-branch automation are absent | P0 |
| Agent | Partial | AgentActivity entity/history, comment/issue-triggered session variants, external URL lifecycle, real-time activity subscriptions | P1 |
| AI | Partial | AiConversation, AiPromptProgress, prompt rules lifecycle, progress subscriptions | P1 |
| Git/VCS automation | Partial | GitAutomationState, GitAutomationTargetBranch, commit integration events, branch suggestions, complete external-sync controls | P0 |
| Code reviews | Partial/strong | Reviews/PR notifications exist; PullRequest and Diff are not first-class entities with Linear-equivalent lifecycle | P1 |
| Generic integrations | Large gap | Slack, Microsoft Teams, Discord, Google Sheets, Jira, Front, Intercom, Zendesk, Salesforce, Sentry, Gong, Figma, GitHub Enterprise | P0 |
| Integration settings/templates | Partial | First-class IntegrationTemplate and IntegrationsSettings entities, scope checks, per-channel notification configuration | P1 |
| Webhooks | Strong | Secret rotation is missing; delivery inspection/retry remains less complete | P1 |
| OAuth/applications | Strong | Main authorization/token/revoke lifecycle exists; approval notification parity is partial | P2 |
| Identity providers | Partial | Runtime OIDC/SAML config exists, but IdentityProvider CRUD, unlink lifecycle, verified domains, and provider policy objects are absent | P0/P1 |
| Organization domains | Missing entity | Domain ownership verification, SSO discovery and verified-domain lifecycle | P0 |
| Sessions/security | Partial | User session listing, individual/all-session revoke and external identity unlink are incomplete | P1 |
| Email intake | Missing | EmailIntakeAddress CRUD, SES-domain verification and address rotation | P0 |
| Push subscriptions | Missing | PushSubscription entity, registration, deletion and test delivery | P1 |
| Posts/Feed | Missing entity | Post, FeedItem, PostNotification, feed comments and associated subscriptions | P1 |
| Meetings | Missing entity | Meeting model and issue/project association | P2 |
| Team resources | Missing/partial | TeamPinnedResource and TeamResourceSection first-class ordering/customization | P2 |
| Usage/billing alerts | Missing | UsageAlert, PaidSubscription and usage-alert notifications | P1 |
| Product announcements | Missing | ProductAnnouncement and announcement notifications | P3 |
| Audit | Partial | Audit log exists; audit entry types metadata and equivalent export/filter APIs are incomplete | P2 |
| Rate limits | Missing public introspection | No Flow endpoint equivalent to `rateLimitStatus` | P2 |

## Missing first-class entities

The most consequential Linear `Node` types with no direct Flow domain entity are:

- `Dashboard`
- `TriageResponsibility`
- `WorkflowDefinition`, `WorkflowCronJobDefinition`
- `GitAutomationState`, `GitAutomationTargetBranch`
- `EmailIntakeAddress`, `OrganizationDomain`, `IdentityProvider`
- `PushSubscription`, `UsageAlert`, `PaidSubscription`
- `Post`, `FeedItem`, `Meeting`
- `ProjectRelation`, `InitiativeRelation`, `InitiativeToProject`
- `CustomerStatus`, `CustomerTier`, `CustomerNeedNotification`
- `DocumentContentDraft`
- `PullRequest`, `Diff`
- `TeamPinnedResource`, `TeamResourceSection`
- `IntegrationTemplate`, `IntegrationsSettings`
- `AgentActivity`, `AiConversation`, `AiPromptProgress`
- `ReleaseHistory`, dedicated release-note history

## What should not be cloned blindly

- Operations labeled `[Internal]` or deprecated were excluded from the product
  gap list unless Flow needs equivalent behavior through a public boundary.
- Dozens of Linear convenience mutations (`issueAddLabel`,
  `projectRemoveLabel`, etc.) are already covered by Flow's broader update
  endpoints and do not require one REST route per GraphQL mutation.
- GraphQL subscriptions do not require GraphQL parity if Flow emits the same
  business event through its SSE/WebSocket layer. The current gap is event
  coverage, not transport naming.

## Recommended implementation order

1. Dashboard entity, widgets, sharing and export.
2. Triage responsibility/rotation and routing engine.
3. Generic workflow definitions and durable scheduled execution.
4. Identity-provider/domain administration and session lifecycle.
5. Integration framework: provider scopes, OAuth health, channel routing,
   linkbacks and delivery retries.
6. Semantic search, filter suggestions and facets.
7. First-class project/initiative relations and history.
8. Email intake and push subscription delivery.

## Generated evidence

- `linear-graphql-operations.json`: every root operation, arguments, return
  type, description, deprecation/internal flags, heuristic status, and top Flow
  handler candidates.
- `flow-routes.json`: every registered Flow HTTP route.
- `node-entities.json`: all 119 Linear `Node` entity types and their fields.
- `likely-missing.tsv`: all public root operations initially classified as
  missing.
- `domain-gaps-heuristic.txt`: partial and missing operations by major domain.
- `flow-domain-types.txt`: current Flow domain structs.
