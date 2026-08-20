# Linear Settings: Personal audit

Measured 2026-08-20 in the user's authenticated Chrome session against
`linear.app/leozhengliu/settings/account/*`. Flow was checked at
`http://127.0.0.1:5174/cleantrack/settings/account/*`.

## Coverage

| Page | Linear route | Flow route | Primary states inspected |
| --- | --- | --- | --- |
| Preferences | `/preferences` | `/preferences` | select closed/open/focused, toggle on/off, disabled action, keyboard select |
| Profile | `/profile` | `/profile` | clean/dirty form, focus, save state, leave confirmation |
| Notifications | `/notifications` | `/notifications` | enabled/disabled channels, channel dialog, toggles |
| Code & reviews | `/code-and-reviews` | `/code-and-reviews` | selects, toggles, unavailable signed-commit actions, code preview |
| Security & access | `/security` | `/security` | current/other sessions, empty states, revoke confirmation, disabled capabilities |
| Connected accounts | `/connections` | `/connections` | disconnected and connected rows, unavailable OAuth actions |
| Agent personalization | `/agents` | `/agents` | empty guidance, edited guidance, disabled save, empty skills, MCP disabled state |

Linear does not expose an archived state on these account pages. Archived-state
coverage is therefore not applicable to Personal Settings.

## Linear DOM measurements

Desktop viewport was `1470 x 693`, DPR `2`, Light theme.

| Element | Linear measured value |
| --- | --- |
| Settings navigation | `x=0`, `y=0`, `w=244`, full viewport height |
| Main panel | `x=244`, `y=8`, `w=1218`, `h=649` on the measured browser chrome; `0.5px` border, `12px` radius |
| Content column | `640px`, centered; left edge `x=533` |
| Section title | `15px/23px`, weight `500`, left edge `x=549` |
| Standard row | `640 x 65px`, `16px` padding, `12px` gap |
| Standard group radius | `10px`; first/last rows own the corresponding corners |
| Select | height `30px`, padding `1px 28px 1px 10px`, radius `8px` |
| Select shadow | `0 0 0 .5px lch(0 0 0/.088), 0 3px 6px -2px lch(0 0 0/.02), 0 1px 1px lch(0 0 0/.04)` |
| Select transition | background, border, color and shadow: `150ms` |
| Toggle | `30 x 20px`, radius `72px` |
| Toggle transition | background and opacity: `150ms ease-out` |
| Text input | `180 x 32px`, padding `6px 12px`, `0.5px` border, radius `8px` |
| Pill action | height `32px`, horizontal padding `12px`, radius `9999px`, `13px/500` |
| Portal menu | Flow measured `180 x 75px`, radius `12px`, `z-index:500`; `150ms ease` settings animation |
| Dialog portal | Flow uses `z-index:1200`, `12px` radius, theme surface tokens |

Representative Linear control widths: default-home select `172.92px`, display
name `97.83px`, weekday `88.60px`, submit key `71.09px`, Customize
`91.02 x 32px`. Profile text fields are exactly `180 x 32px`; Leave workspace
is `134.22 x 32px`.

## Component inventory

### Preferences

- General: default home, display names, first weekday, emoticon conversion,
  comment submit key. Flow adds Language because locale is an explicit account
  setting.
- Interface: sidebar customization, font size, pointer cursor, underlined links,
  interface theme.
- Desktop link handling and two issue-assignment workflow toggles.
- Linear's sidebar customization is a separate functional surface. Flow marks
  Customize disabled until its ordering model exists; it is not a fake action.

### Profile

- Avatar, immutable email, full name, title, username, dirty save state.
- Leave workspace opens an in-product confirmation. The destructive API call is
  not made until the final confirmation.
- Flow currently supports avatar URL through the profile API but has no media
  upload endpoint; the replica does not present a fake upload button.

### Notifications

- Linear overview cards: Desktop, Mobile, Email and Slack.
- Flow Desktop and Email open a settings dialog and persist through
  `/api/notification-preferences`.
- Mobile and Slack are visibly disabled because Flow has no mobile push token or
  per-user Slack OAuth capability.
- Changelog, newsletter, marketing, invite, privacy and DPA switches persist in
  user settings.

### Code & reviews

- Code reviews, draft conversion, merge strategy, code theme/font, review
  notification filters, git attachment format and two issue-status automations.
- Signed commits are represented as disabled until a signing-key API exists.
- The code preview is visual state only and has no misleading action.

### Security & access

- Current and other sessions load from `/api/account/sessions`; revoke-all uses
  `/api/account/sessions/others` and requires confirmation.
- API key management navigates to Flow's functional API settings page.
- Passkeys, signing keys and authorized external applications lack account-level
  APIs; the page renders accurate empty/unavailable states, not actionable fakes.

### Connected accounts

- Linear showed disconnected Slack, Google Calendar and Notion, plus a connected
  GitHub account.
- Flow only has workspace integration-reference records, not user OAuth grants.
  Existing records navigate to Integrations; absent providers are disabled and
  labelled Unavailable rather than pretending to complete OAuth.

### Agent personalization

- Guidance editor supports empty, focus, dirty, disabled-save and saved states;
  content is persisted as `agentInstructions`.
- Personal skills and personal MCP connector grants do not exist in Flow's
  domain model. Skills use a truthful empty state; MCP Configure navigates to
  workspace Security.

## Data model and API gap resolution

Added persisted `UserSettings` fields in TypeScript and Go for code review,
merge/code display preferences, review notification filters, git workflow
automations, and Flow update subscriptions. The account settings handler now
preserves non-empty enum/string values during partial updates. A round-trip API
test covers the new fields.

Remaining product capabilities that cannot be truthfully simulated:

| Missing capability | Current Flow behavior | Needed backend work |
| --- | --- | --- |
| Sidebar ordering/badges | Customize disabled | sidebar item/order/badge schema and mutation |
| Mobile push | channel disabled | device-token registration and delivery provider |
| Personal Slack/Google Calendar/Notion OAuth | Connect disabled | user-scoped OAuth grant, callback and revoke APIs |
| Passkeys | explanatory empty state | WebAuthn challenge/register/list/revoke endpoints |
| Signing keys | controls disabled | encrypted public signing-key CRUD |
| Authorized apps | empty state | user OAuth-consent list/revoke endpoint |
| Agent skills | explanatory empty state | user skill entity, CRUD and agent runtime selection |
| Per-user MCP grants | workspace Security navigation | user connector grant model and permission API |

## Interaction verification

- Opened the Language select through Chrome, measured its portal, and selected
  English with `ArrowDown` then `Enter`.
- Menu closed and focus returned to the trigger (`BUTTON`, aria-label
  `Language`). Escape/outside dismissal is supplied by the shared Radix menu.
- Portal surface used the active Light theme and `z-index:500`; dialogs use the
  same theme variables at `z-index:1200`.
- All seven Flow routes loaded without console errors before unrelated concurrent
  Settings changes introduced a bad `Github`/`Slack` import.
- English DOM snapshots were completed for all seven pages. Chinese Preferences
  was checked; business names are explicitly ignored by the legacy translator.
- Desktop Flow measurement matched the Linear shell: main `x=244`, `y=8`,
  `w=1218`, radius `12px`; content `w=640`, `x=533`; controls matched `30x20`,
  `30px`, and `32px` height classes.
- At mobile `390 x 844`, the main panel measured `378 x 832px`, the content
  column `353px`, and the document width remained `390px` (no horizontal
  overflow). The off-canvas sidebar translated exactly `-244px` while closed.

## Linear vs Flow comparison

| Measurement | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Sidebar width | `244px` | `244px` | `0px` |
| Main x / outer inset | `244px / 8px` | `244px / 8px` | `0px` |
| Main width at 1470 | `1218px` | `1218px` | `0px` |
| Main radius | `12px` | `12px` | `0px` |
| Content width / x | `640px / 533px` | `640px / 533px` | `0px` |
| Standard row | `640 x 65px` | `640 x 65px` | `0px` |
| Toggle | `30 x 20px` | `30 x 20px` | `0px` |
| Text input | `180 x 32px` | `180 x 32px` | `0px` |
| Select height/radius | `30px / 8px` | `30px / 8px` | `0px` |
| Pill action height/radius | `32px / 9999px` | `32px / 9999px` | `0px` |

## Verification status

- The final integrated tree passes `npm run build` and
  `npx oxlint src/components/settings/*.tsx`.
- The final backend passes `go test ./...`; formatting and `git diff --check`
  also pass.
- `go test ./cmd/server -run TestPersonalSettingsPersistence -count=1` passes.
- The account-settings autosave hook now distinguishes user-dirty state from
  bootstrap hydration. A Chrome CDP idle-window check after the fix measured
  `PATCH /api/account/settings = 0` and `GET /api/bootstrap = 1` over 3.5
  seconds, eliminating the former request feedback loop.
- Light desktop and mobile were visually captured. In the final integrated
  smoke pass, all seven Personal routes were loaded under Dark theme and checked
  for correct headings, runtime errors, portal tokens, and horizontal overflow.
  Individual retained Dark screenshots were not kept because the DOM and
  computed-style evidence was sufficient for acceptance.
