# Linear Initiatives replication audit

Audit date: 2026-08-20 (Asia/Shanghai)

Targets:

- Linear list: `https://linear.app/leozhengliu/initiatives/active`
- Linear detail: `https://linear.app/leozhengliu/initiative/test-081735980f52/overview`
- Flow list: `http://127.0.0.1:5173/cleantrack/initiatives/active`
- Flow detail: `http://127.0.0.1:5173/cleantrack/initiative/enterprise-process-experience/overview`
- Desktop viewport: `1470 x 693/749`, DPR `2`
- Mobile viewport: Chrome device metrics `390 x 844`, DPR `2`

All Linear geometry, computed styles, menu states and keyboard behavior were read from the live DOM in the user's signed-in Chrome session. Linear data was not modified.

## Component and state inventory

Validated Linear surfaces:

- Active, Planned and All initiatives list tabs; row property controls; details rail tabs
- Filter search, AI/advanced entries and Status, Priority, Owner, Creator, Lead team, Contributing teams, Labels, Health and Dates submenus
- Display grouping, ordering, team-initiative visibility and the complete property list
- Inline initiative creation, disabled/enabled create, icon, status, priority, owner, lead team, target date and labels menus
- Planned empty state, documentation action and no-details rail state
- Detail header, favorite, action/copy/subscribe/reminder submenus, notifications and Add project menus
- Overview editable identity, properties, resources, first update, description and associated projects
- Activity comment/update modes, health, attachment, disabled post and populated feed states
- Projects timeline ruler, Today/Zoom, project bars, milestones and property controls
- Details rail Properties, Progress (Health/Status/Leads), missing-update count and Activity
- Update schedule, description history, add-link, create-project and delete dialogs
- Hover, focus, open, checked, disabled, empty, destructive confirmation and Escape/arrow/Enter behavior

Flow implements the same functional surfaces. AI filtering and Slack remain disabled because the workspace has no configured integration; they no longer present a success toast or pretend to complete an action.

## Data and API parity

| Linear behavior | Flow representation | Result |
| --- | --- | --- |
| Initiative creator | `Initiative.creator` | Added; migration supplies viewer for older workspaces |
| Lead and contributing teams | `leadTeamId`, `contributingTeamIds` | Added with team ID validation |
| Notification event choices | `Initiative.notificationRules` | Persisted by the initiative mutation API; live toggle verified and restored |
| Update expectation | `Initiative.updateSchedule` cadence/weekday/time range | Added with validation and real dialog |
| Reminder | Initiative reminder API creates persisted `Notification` | Added and lifecycle-tested |
| Description history | `descriptionHistory` revision snapshots with editor/time | Added; empty/history/restore dialog uses real data |
| Delete and restore for 30 days | `TrashEntry` stores initiative plus updates | Added; delete/restore lifecycle-tested |
| Project association | Bidirectional initiative/project IDs | Existing; Add existing and Create project both mutate real data |
| Grouping and property display | Computed from initiative/team/label data | Added; no placeholder groups or hard-coded team names |

## Desktop measurements

Values are CSS pixels. Entity text widths are data-dependent.

| Element | Linear | Flow | Delta/result |
| --- | ---: | ---: | ---: |
| Main shell `x / y / w` | `244 / 8 / 1218` | `244 / 8 / 1218` | Exact |
| Shell border / radius | `.5 / 12` | `.5 / 12` | Exact |
| Combined header/tool area | `87.5` | `88` | `+0.5` |
| List columns `x / y / w / h` | `244.5 / 96 / 995 / 32` | `244.5 / 96.5 / 995 / 32` | `+0.5y` |
| Initiative row `x / y / w / h` | `244.5 / 128 / 995 / 52` | `244.5 / 128.5 / 995 / 52` | `+0.5y` |
| Row radius | `8` | `8` | Exact |
| List details rail `x / w` | `1061.5 / 400` | `1061.5 / 400` | Exact |
| Rail tabs height / gap | `28 / 4` | `28 / 4` | Exact |
| Overview content `x / width` | `328.5 / 649` | `328.5 / 649` | Exact |
| Overview icon `x / y / w / h` | `328.5 / 160 / 32 / 32` | `328.5 / 159.5 / 32 / 32` | `-0.5y` |
| Overview name `x / y / w / h` | `328.5 / 204 / 649 / 32` | `328.5 / 204 / 649 / 32` | Exact after correction |
| Overview summary `x / y / w / h` | `328.5 / 240 / 649 / 23` | `328.5 / 240 / 649 / 23` | Exact after correction |
| Overview details rail `x / w` | `1061.5 / 400` | `1061.5 / 400` | Exact |
| Actions Portal `w / h / radius` | `249.47 / 412.12 / 12` | `250 / 413 / 12` | `+0.53 / +0.88 / 0` |
| Actions row height | `31.93` | `32` | `+0.07` |
| Display Portal `w / h / radius` | `302 / 326.5 / 12` | `302 / 327 / 12` | `0 / +0.5 / 0` |
| Notification Portal width | `342` | `342` | Exact |

## Computed visual tokens

| Token | Linear | Flow |
| --- | --- | --- |
| Light main background | `lch(97.94 0.5 282)` | Same |
| Light Portal background | `lch(100 0 282)` | Same |
| Light Portal border | `.5px solid lch(91.9 0 282)` | Same |
| Menu shadow | `0 6px 18px /2%, 0 3px 9px /4%, 0 1px 1px /4%` | Same |
| Main shell radius | `12px` | `12px` |
| Row radius | `8px` | `8px` |
| Menu action font | `13px / 19.5px` | Same |
| Main font stack | `Inter Variable`, `SF Pro Display`, system fallbacks | Same; CJK fonts are fallbacks only |

## Portal validation

| Portal | Theme/I18n | Layer | Result |
| --- | --- | ---: | --- |
| List filter/value menus | Light/dark, zh/en | `500` | Search, submenus, selection, Escape |
| List display/group/order | Light/dark, zh/en | `500` | Real grouping, ordering, visibility and properties |
| Detail actions/submenus | Light/dark, zh/en | `600` | Copy, subscription, reminder and keyboard traversal |
| Notification popover | Light/dark, zh/en | `600` | Persisted event checkboxes and update schedule |
| Team/project/label pickers | Light/dark, zh/en | `500` | Entity names remain untranslated |
| Schedule/history/reminder dialogs | Light/dark, zh/en | `700/701` | Focus isolation, Save/Cancel and Escape |
| Delete confirmation | Light/dark, zh/en | `700/701` | 30-day restore copy; destructive submit not used in Chrome |

## Interaction acceptance

- Notification checkbox persisted through `/api/initiatives/:id` and was restored to its original value.
- Action Copy submenu contains URL, title, linked title and Markdown overview actions.
- Reminder presets call the real reminder API; custom date/time validates future timestamps.
- Description edits create revisions; restore writes the selected revision through the mutation API.
- Grouping uses owner, teams, health, status, priority or labels and renders real counts.
- Filters use creator/team/date fields from the data model; hard-coded `Cleantrack` behavior was removed.
- Inline Create remains disabled until a name is present; Escape cancels and Cmd/Ctrl+Enter submits.
- `Cmd/Ctrl+I` toggles details; menu Arrow navigation, Enter and Escape use Radix behavior.
- Delete confirmation was opened and canceled; no Linear or Flow initiative was deleted during browser QA.

## Locale, theme and responsive matrix

| Locale | Theme | Desktop | Mobile `390 x 844` |
| --- | --- | --- | --- |
| Simplified Chinese | Light | Passed | Passed |
| Simplified Chinese | Dark | Passed, including Portals | Passed |
| English | Light | Passed | Passed |
| English | Dark | Passed, including Portals | Passed |

Business initiative, project, team, label and user names carry explicit I18n boundaries and remain byte-for-byte unchanged across locale changes.

Mobile measurements:

- Viewport/document: `390 x 844`; document scroll width `390`
- Main panel: `0 / 0 / 386 / 828`; no page-level horizontal overflow
- The dense table owns its `1020px` horizontal scroll; toolbar and page remain fixed
- Details rail defaults closed on a mobile mount and does not occlude list controls
- Display Portal: `74.5 / 84.5 / 302 / 327`; right edge `376.5`, entirely inside the viewport
- Dark Portal: `rgb(32 32 34)`, `.5px solid lch(25.68 1.93 272)`, `z-index 500`
- Chrome device metrics were cleared; the page returned to the desktop viewport

## Automated verification

- `go test ./...`
- `npm run build`
- `npm run lint` (four pre-existing unused-declaration warnings)
- `git diff --check`
- Docker image rebuilt and local stack restarted at `http://127.0.0.1:5173`
