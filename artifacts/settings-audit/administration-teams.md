# Linear Settings DOM audit: Administration and team settings

Measured against `https://linear.app/leozhengliu/settings/*` in the user's signed-in Chrome session on 2026-08-20. Measurements were taken from live computed DOM, not screenshots.

## Pages and visible states

### Administration

| Page | Components and states verified |
| --- | --- |
| Workspace | Logo uploader, editable name, read-only URL suffix, fiscal-month select, immutable region, Enterprise-disabled onboarding rows, danger row, deletion verification dialog |
| Teams | Search, active/retired filter, create action, sortable column headers, active count, row metadata, row menu |
| Members | Search, status filter, export, invite, sortable columns, active/invited/application sections, row menus |
| Security | Invite link toggle, domain empty state/add action, authentication toggles, Enterprise-disabled SAML/SCIM, management permission selects, integration/AI/MCP/compliance toggles |
| API | OAuth application empty state, webhook empty state, member API-key policy, empty API-key state |
| Applications | Authorized application list, application row and overflow menu |
| Billing | Plan selector, current/trial plans, disabled/available plan actions, feature list, usage row, invoice empty state |
| Usage & limits | Credit balance, auto-reload, spend limits, period selector, previous/disabled-next navigation, feature series, empty analytics state |
| Import & export | Five migration assistants, CLI importer, export dialog trigger, private-team selection |

### Team settings

The team root is an overview, not a default `General` tab. The overview links to: General, Access and permissions, Members, Slack notifications, Issue labels, Templates, Recurring issues, Issue statuses, Workflows and automations, Triage, Cycles, Team agents, Agent skills, Project updates prompt, and Resolved thread summaries. Parent team, sidebar initiatives, leave/retire/delete actions are also present.

Measured detail pages:

- `general`: icon/name, identifier, description, timezone, estimates, issue-by-email, detailed history.
- `security`: public/private access, membership restriction, six team permission selectors.
- `notifications`: Slack connection and six disabled notification toggles before connection.
- `recurring-issues`: explanatory empty state and create action.
- `workflow`: five PR status automations, branch rules, release automations, parent/subissue auto-close, stale close, archive delay, status-progress ordering.
- `triage`: enabled/disabled dependent controls, responsibility selector, rule empty state, Loops link, Triage Intelligence state.
- `agents`: integration-dependent empty state.
- `agent-skills`: empty state and create action.
- `ai/updates`: rich prompt editor.
- `ai/summaries`: resolved-thread summary toggle.
- Existing member, label, template, status, and cycle pages were also verified from their live routes.

## Common geometry and style

Desktop viewport measured at `1470 x 693`:

| Element | Linear measurement |
| --- | --- |
| Settings navigation | `244px` wide, `0,0`, full viewport height |
| Main surface | `x=244`, `y=8`, `1218 x 649`, `12px` radius, `0.5px` border |
| Main background | `lch(97.94 0.5 282)` |
| Main shadow | `0 3px 6px -2px lch(0 0 0/.02), 0 1px 1px lch(0 0 0/.04)` |
| Content column | `640px` wide, `x=533`, top `72.5px` |
| Settings list | `640px` wide |
| Standard row | `64-66px` high, `16px` padding, `12px` gap |
| Group corner radius | first `10px 10px 0 0`, middle `0`, last `0 0 10px 10px` |
| Input | `256 x 32`, `6px 12px`, `8px` radius, `0.5px` border, `13px` text |
| Compact select | `30px` high, `8px` radius, `13px` text |
| Destructive text button | `32px` high, pill radius, `0 12px`, `13px/500` |
| UI font | `Inter Variable`, fallback `SF Pro Display` and system fonts |
| Control transition | `150ms ease` |

### Portal verification

The fiscal-month listbox is portaled outside the settings content. Live measurements:

- `z-index: 500`
- light background `lch(100 0 282)`
- `0.5px solid lch(86.5 0 282)`
- `12px` radius
- shadow: `0 6px 18px`, `0 3px 9px`, `0 1px 1px` layers
- options are `32px` high with `0 35px 0 12px` padding
- Arrow navigation is owned by the active listbox; `Escape` closes it and restores the trigger context.

Portal tokens must resolve from the root theme provider, not from a page-local light theme. Portal labels use UI translations; workspace/team/member names remain literal business entity values.

## Data/API capability audit

| Linear behavior | Flow before this pass | Required change |
| --- | --- | --- |
| Workspace fields and fiscal month | Supported | Match DOM and autosave behavior |
| Team directory and membership | Supported | Add filtering/sorting/grouped states |
| Workspace auth and permissions | Partially supported | Add missing authentication, AI, integration, and management policy fields |
| OAuth applications | Supported | Render on API page, not Authorized Applications page |
| Webhooks | Missing | Add structured webhook entity and CRUD API |
| Authorized third-party applications | Integration data exists | Render separately from developer OAuth apps |
| AI credit and spend limits | Missing | Add usage credits, reload, limits, and usage-series model/API |
| Team overview and missing routes | Missing | Add overview and every measured team route |
| Team access/permissions | Missing | Extend structured `TeamSettings` mutation |
| Slack notification settings | Missing | Add channel and notification preferences to `TeamSettings` |
| Recurring issues directory | Issue recurrence exists | Expose existing recurring issues and create/update actions |
| PR/close/archive workflows | Missing | Add structured workflow settings to `TeamSettings` |
| Triage responsibility/rules | Missing | Add triage fields and rule collection |
| Agent skills | Missing | Add structured team skill collection |
| AI update prompt/summaries | Missing | Add prompt and summary toggle to `TeamSettings` |

## Safety note

Opening Linear's workspace-deletion flow immediately sent a verification-code email. The dialog was closed; no code was entered, acknowledgement checked, or deletion submitted. Remaining destructive or externally transmitting flows are verified only through DOM/disabled state unless the user explicitly confirms the final action.

## Flow implementation and acceptance

Flow now exposes the measured Administration pages plus the team overview and all fifteen detail routes. Workspace security, team permissions, Slack dependency state, PR and close/archive workflows, triage rules, agent skills, AI prompts, resolved summaries, OAuth applications, authorized applications, webhooks, and AI credit limits persist through structured APIs. Recurring issues use the existing issue recurrence model. Unsupported Enterprise identity-provider provisioning is visibly disabled rather than simulated.

### Linear vs Flow desktop measurements

Measured in Chrome at `1470 x 693` after implementation:

| Element | Linear | Flow | Result |
| --- | ---: | ---: | --- |
| Settings navigation | `244px` | `244px` | exact |
| Main surface | `x244 y8`, `1218px` wide | `x244 y8`, `1218px` wide | exact |
| Main radius / border | `12px / 0.5px` | `12px / 0.5px` | exact |
| Content column | `640px`, `x533` | `640px`, `x533` | exact |
| Standard row | `64-66px`, `16px` padding | `64px`, `15-16px` padding | within `1px` |
| Settings input | `256 x 32px` | `256 x 30-32px` by control type | within `2px` |
| Portal option | `32px` | `32px` | exact |
| Portal radius / z-index | `12px / 500` | `12px / 500` | exact |
| Transition | `150ms ease` | `150ms ease` | exact |

### Acceptance matrix

| Dimension | Result |
| --- | --- |
| English / Chinese | Passed; fixed UI strings translate and `Cleantrack`, member names, providers, webhook and application names remain literal |
| Light / Dark | Passed for pages and portaled menus/dialogs |
| Desktop | Passed at `1470 x 693`; no document overflow |
| Mobile | Passed at `390 x 844`; `scrollWidth=390`, `353px` content, `244px` off-canvas navigation and scrim |
| Keyboard | Menu Arrow navigation and `Escape` close verified; focus returns to trigger |
| Portal | Root theme and locale inherited; settings menus measured at `z-index:500`, `12px` radius |
| Runtime | Idle settings page produces no repeated `PATCH /api/account/settings`; prior 250ms feedback loop is fixed |
| API | Webhook CRUD validation/persistence and team/workspace structured settings covered by Go tests |
