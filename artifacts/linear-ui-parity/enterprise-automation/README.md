# Linear enterprise and automation UI audit

Measured on 2026-08-30 in the user's authenticated Chrome session. Every route
below has a DOM snapshot, a full-page screenshot, and computed measurements for
interactive nodes in this directory.

## Route and capability mapping

| Capability | Linear entry | Flow entry | Result |
| --- | --- | --- | --- |
| Workspace authentication and domain discovery | `/settings/security` | `/settings/security` | Authentication methods, approved domains, admin bypass and workspace permissions use the measured section order and copy. |
| OIDC/SAML provider configuration | `/settings/security` → `SAML & SCIM` | `/settings/security` → `Identity providers` | Flow CRUD, verification, enable/enforce and domain discovery are wired. Linear provider editor is blocked by the Enterprise plan on the test workspace. |
| Account sessions | `/settings/account/security` | `/settings/account/security` | Current/other sessions, revoke one and revoke all are wired. |
| External and connected identities | `/settings/account/connections` | `/settings/account/connections` | Provider connections and IdP sign-in identities are displayed and removable. |
| Push notification devices | `/settings/account/notifications` and `/desktop` | `/settings/account/notifications` | Channel summary, category preferences, registered browser devices and removal are wired. |
| GitHub/GitLab connection | `/settings/integrations/github` and `/gitlab` | `/settings/integrations/github` and `/gitlab` | Connected organizations, branch format, linkbacks, magic words, review guides and disconnect lifecycle are wired. |
| Integration delivery lifecycle | Provider integration detail | Provider integration detail → `Delivery history` | Latest 50 attempts are bounded, status/error/attempt count shown, failed deliveries retry through the durable queue. |
| Pull request automations | `/settings/teams/LEO/workflow` | `/settings/teams/{team}/workflow` | All five measured events map to workflow states and persist both team settings and first-class Git automation records. |
| Target branch rules | Team workflow → `Add branch` | Team workflow → `Add branch` | Measured inline branch pattern, regex switch, cancel/submit, list and delete are wired. |
| Release automations | Team workflow → `Add rule` | Team workflow → `Add rule` | Pipeline and completion status selectors persist real team automation rules. |
| Triage responsibility and routing | `/settings/teams/LEO/triage` | `/settings/teams/{team}/triage` | Enable/priority/action, responsibilities, ordered rules, Loops count and intelligence status are wired. |
| Email intake | `/settings/teams/LEO/general` → `Create issues by email` | Same team General section | Enable/configure, address display/copy and disable use the real intake address API. |
| Generic WorkflowDefinition/run history | No distinct Linear settings UI | `/settings/workflows` | No 1:1 Linear surface exists. Linear explicitly says agent automations were renamed to Loops. Flow keeps this durable-automation administration page rather than pretending it is a measured Linear page. |

## Observed states

- Team workflow: target branch create, persisted, expanded per-event selectors,
  edit, action menu, delete confirmation and cleanup; release automation pipeline
  menu, create, persisted, edit, menu, delete confirmation and cleanup.
- Triage: disabled default, enabled state, responsibility action menu,
  Assign member state, full When/Then rule builder, persisted rule, edit menu,
  delete confirmation and restored disabled/no-action state.
- Account security: current session, two other sessions, revoke controls, empty
  passkeys/API keys, commit signing and authorized application rows.
- Notifications: desktop disabled, mobile/email summaries, desktop categories
  disabled because Linear Desktop is not installed.
- GitHub: connected organization, connection menu trigger, GitHub Issues empty
  state, branch format, linkback toggles and pull-request feature toggles.

## Blocked Linear states

- The test workspace exposes `SAML & SCIM` only as an Enterprise upgrade link,
  so Linear's provider editor, metadata validation errors and enforcement states
  could not be inspected.
- Desktop push category controls are disabled until Linear Desktop is installed.
- No failed GitHub delivery was present, and Linear does not expose a standalone
  delivery-log route in this workspace.

## Temporary cloud verification

After explicit user approval, temporary Target branch, Release automation and
Triage rule entities were created, edited, reloaded and deleted. Triage and
Email Intake were enabled, Email Intake copy/reset was exercised, then both
were restored to their original disabled state. `cloud-operation-log.json`
records the exact lifecycle. Final DOM snapshots confirm zero temporary rules,
Triage `No action` + disabled, and Email Intake disabled.

## Evidence format

For each route, `*.dom.txt` is the accessibility/DOM snapshot,
`*.measurements.json` contains element geometry and computed styles, and `*.png`
is the full-page reference image. The screenshots and snapshots contain account
and workspace data and must remain internal to this repository.
