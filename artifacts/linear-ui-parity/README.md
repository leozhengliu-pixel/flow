# Linear UI parity coverage

Audit and implementation date: 2026-08-30. Linear observations came from the
user's authenticated `leozhengliu` workspace in Chrome. Flow observations came
from the local test workspace after the corresponding API was connected.

An API does not necessarily have a standalone page. Callback, inbound, draft,
activity and feed-node APIs are mapped to the owning user-visible surface rather
than being exposed as invented administration pages.

| Backend capability group | Verified Linear UI owner | Flow UI result | Evidence |
| --- | --- | --- | --- |
| Dashboard, widgets, share, subscription, export | Views → Dashboards | Functional grouped dashboard index/detail; Enterprise Linear DOM remains blocked | `dashboards-analytics/` |
| Insights and semantic analysis | Issue view → Open view insights | Functional Insights controls, chart/table, display and action menus | `dashboards-analytics/` |
| Usage alerts and paid subscription | Settings → Usage & limits / Billing | Credits, limits, alerts, period analytics and plan usage | `dashboards-analytics/` |
| Identity providers and domain discovery | Settings → Security | OIDC/SAML CRUD/verify/enforce and domains | `enterprise-automation/` |
| Account sessions and external identities | Personal Security / Connected accounts | Session revoke and identity unlink UI | `enterprise-automation/` |
| Push subscriptions | Personal Notifications | Browser devices and channel preferences | `enterprise-automation/` |
| Integration deliveries | GitHub/GitLab integration detail | Bounded delivery history and real retry | `enterprise-automation/` |
| Git automations and target branches | Team Workflow | Five PR rules, target-branch pattern/regex and release rules | `enterprise-automation/` |
| Triage responsibility and routing | Team Triage | Responsibility/action/rule/Loop/intelligence sections | `enterprise-automation/` |
| Email intake receiver/address | Team General → Create issues by email | Address create/copy/disable lifecycle; receiver stays backend-only | `enterprise-automation/` |
| WorkflowDefinition/run history | No distinct Linear surface; Linear points to Loops | Retained as Flow durable-workflow administration, explicitly not a Linear clone | `enterprise-automation/` |
| Post and FeedItem | Pulse and New update | Pulse feed/composer; no fabricated FeedItem page | `content-relations/` |
| Meeting | No public UI; Linear schema marks it internal | API retained without invented UI | `content-relations/` |
| Project relation/history | Project Details / Activity | Dependency picker and activity history | `content-relations/` |
| Initiative relation/history | Initiative Overview / Activity | Activity mapped; Sub-initiatives are Enterprise-only and no control is exposed on Business | `relations-mutation/` |
| Document draft/history | Team Documents / document metadata history | Autosave editor, history and restore; drafts remain internal | `content-relations/` |
| Customer taxonomy/request lifecycle | Customer request settings/detail | Status/tier editors and measured request menu; Linear has Delete but no Archive/Restore action | `content-relations/` |
| Release notes/history | Pipeline Changelog / release notes editor | Release selector, editor and persisted history | `content-relations/` |
| Team resource sections/pins | Team Overview | Sections, documents/links, reorder/move/remove | `content-relations/` |
| Agent activity/conversation/progress | Agent page and toolbar chat | Conversation/history/composer; internal activity nodes render through the session | `agent-ai/` |
| Agent skills and guidance | Personal → Agent personalization | Guidance autosave, compact skill list/create and MCP section | `agent-ai/` |
| Migration APIs | Settings → Import & export | Existing preview/mapping/invite/execute/rollback wizard | `../import-roundtrip/` |
| Search and filter suggestions | Workspace Search and issue filter picker | Existing semantic results, facets and suggestion menus | `../linear-api-audit/` |

## Known blocking conditions

- Linear Dashboard and SAML/SCIM editors require Enterprise; this workspace is
  on a Business trial. Those surfaces are functional in Flow but are not claimed
  as pixel-measured Linear parity.
- Destructive Linear actions such as session revoke, integration disconnect and
  persisted triage rules were not executed.
- No failed Linear integration delivery was present. Populated document history,
  restore, Customer Request menus, deletion, target branches, release rules,
  Triage and Email Intake were subsequently measured with temporary data.
- Linear exposed no public Meeting UI or distinct WorkflowDefinition page.
  Sub-initiatives, Dashboard and SAML/SCIM editors remain Enterprise-only. Flow
  does not invent fake controls for these cases.

## Temporary Linear data cleanup

- Target branch, Release automation and Triage rules were deleted; Triage and
  Email Intake were restored to disabled and the original No action state.
- The temporary Customer Request and Customer were deleted; the linked `LEO-5`
  issue was preserved.
- Two temporary Projects and two temporary Initiatives were deleted.
- The version-history test document was restored and deleted. Linear retains
  deleted documents/projects/initiatives in Recently deleted for 30 days.

Each subdirectory contains route snapshots, computed measurements, screenshots,
and a detailed capability map. Missing Linear evidence is recorded as a blocker,
not silently treated as completion.
