# Linear project detail replication audit

Audit date: 2026-08-20 (Asia/Shanghai)

Targets:

- Linear: `https://linear.app/leozhengliu/project/flow-对比演示-汽车之家车商城项目-2026-3dda7a5b6539/overview`
- Flow: `http://127.0.0.1:5173/cleantrack/project/autohome-car-mall-2026/overview`
- Desktop viewport: `1470 x 693`, DPR `2`
- Mobile viewport: Chrome device metrics `390 x 844`, DPR `2`

All Linear geometry, computed styles, SVG markup and interaction states were read from the live DOM in the user's Chrome session. Linear data was not modified.

## Component and state inventory

Validated surfaces:

- Project header, breadcrumb, favorite, actions, copy URL, notification popover and tabs
- Overview icon, editable name/summary/description, all property menus, dates, teams and additional properties
- Initiatives, labels, resources, resource actions, pin-to-team submenu, create/edit/delete confirmation
- Latest update, empty update state, activity composer/list and keyboard route switching
- Milestone expanded/collapsed/edit/create states, date picker, issue scope link, copy menu and delete confirmation
- Issues list/board, filter/value menus, display menu, applied filter bar, milestone scope, selection and empty results
- Details sidebar properties, dependencies, milestones, progress, grouping and recent activity
- Insights selectors, chart/table, display options, CSV, fullscreen and reset/default actions
- Delete confirmation and the Recently deleted projects empty state
- Hover, focus, open, checked, disabled, empty and destructive confirmation states

Disabled controls are limited to capabilities that require an unavailable external integration or match Linear's read-only state, such as Slack and the overview team display. Unsupported project issue filters were removed instead of being presented as non-functional options.

## Data and API parity

| Linear behavior | Flow representation | Result |
| --- | --- | --- |
| Favorite and subscribe to project events | `Favorite`, `Subscription.events`, mutation APIs | Existing; live toggle verified and restored |
| Project update expectation | `Project.updateCadence` with `none/weekly/biweekly/monthly` | Added and API-tested |
| Project reminders | Project reminder API producing persisted notifications | Added and API-tested |
| Description history | `Project.descriptionRevisions` with author/time snapshots | Added and empty/history dialogs verified |
| Milestone editable description and date | `ProjectMilestone.description/targetDate` | Added and API-tested |
| Exact milestone issue membership | `Issue.projectMilestoneId` | Added; create/update validation rejects cross-project assignment |
| Create an issue from milestone scope | Initial project and milestone override draft context | Added; Chrome verified both entity names in the dialog |
| Remove a milestone | Deletes the milestone and clears issue references | Added and API-tested |
| Resource pin-to-team submenu | `ProjectResource.pinnedTeamIds` | Added with team validation; live API toggle verified and restored |
| Resource copy/edit/delete | Resource mutation APIs plus delete confirmation | Verified; no destructive submission during browser audit |
| Insight aggregation | Real project issues, labels and assignees | Verified; no index-based or mock milestone bucketing remains |
| Deleted project recovery surface | `TrashEntry` and Recently deleted projects view | Existing; empty state verified without deleting the demo project |

## Desktop measurements

Values are CSS pixels. Text-dependent widths differ because the compared project content differs.

| Element | Linear | Flow | Delta/result |
| --- | ---: | ---: | ---: |
| Project shell `x / y / w / h` | `244 / 8 / 1218 / 649` | `244 / 8 / 1218 / 649` | Exact |
| Shell border / radius | `.5 / 12` | `.5 / 12` | Exact |
| Header height | `44` | `44` | Exact |
| View toolbar height | `43` | `43` | Exact |
| Overview content `x / width` with Insights | `292.5 / 721` | `292.5 / 721` | Exact |
| Insights `x / y / w / h` | `1065.5 / 96 / 396 / 552.5` | `1065.5 / 96 / 396 / 552.5` | Exact |
| Project icon `x / y / w / h` | `292.5 / 147 / 28 / 32` | `292.5 / 147 / 28 / 32` | Exact after DOM correction |
| Project icon SVG | `22 x 22` | `22 x 22` | Exact |
| Project name `x / y / w / h` | `292.5 / 191 / 721 / 32` | `292.5 / 191 / 721 / 32` | Exact |
| Active tab height / padding | `28 / 0 10` | `28 / 0 10` | Exact |
| Tab gap | `4` | `4` | Exact |
| Property control height / radius | `28 / 9999` | `28 / 9999` | Exact |
| Resource outer / inner link height | `28 / 24` | `28 / 24` | Exact after DOM correction |
| Resource inner padding / gap / radius | `3px 6px / 4 / 9999` | `3px 6px / 4 / 9999` | Exact |
| Expanded milestone block | `721 x 153` | `721 x 153` | Exact |
| Milestone block padding / radius | `4px 0 2px / 8` | `4px 0 2px / 8` | Exact |
| Milestone header height | `31` | `31` | Exact |
| Milestone date / issue link / menu height | `28 / 28 / 28` | `28 / 28 / 28` | Exact |
| Milestone description `w / h / padding` | `745 / 98 / 10px 14px 16px 26px` | Same | Exact |
| Project actions menu `w / h` | `250 / 349` | `250 / 349` | Exact |
| Project action row `w / h / padding` | `249 / 32 / 0 18px 0 14px` | Same | Exact |
| Notification popover `w / h / radius` | `412.5 / 265.5 / 8` | Same | Exact |

## Computed visual tokens

| Token | Linear | Flow |
| --- | --- | --- |
| Light shell background | `lch(97.94 0.5 282)` | Same |
| Light shell border | `lch(89.84 0 282)` | Same |
| Light shell shadow | `0 3px 6px -2px lch(0 0 0/.02), 0 1px 1px lch(0 0 0/.04)` | Same |
| Active tab light background | `lch(93.483 0.5 282)` | Same |
| Inactive tab light background | `lch(99.997 0.5 282)` | Same |
| Property/resource light hover | `lch(92.44 0.5 282)` | Same |
| Dark Portal background | `lch(9.232 0.85 272)` | Same project Portal token |
| Dark Portal border | `.5px solid lch(25.68 1.93 272)` | Same |
| Menu shadow, light | `0 6px 18px /2%, 0 3px 9px /4%, 0 1px 1px /4%` | Same |
| Milestone control transition | `150ms` | `150ms` |
| Main font stack | `Inter Variable`, `SF Pro Display`, system fallbacks | Same; CJK fonts follow as fallbacks only |

Linear's project icon and demo content colors are entity data and are intentionally not copied. Business entity names remain byte-for-byte unchanged across locales.

## Portal validation

| Portal | Light/dark + I18n | Layer | Interaction/result |
| --- | --- | ---: | --- |
| Project actions and submenus | Passed | `600` | Hidden search, ArrowDown, ArrowRight, Enter and Escape |
| Project notifications | Passed | `500` | Event checkboxes, schedule submenu, disabled Slack integration |
| Project property/value menus | Passed | Above side panels | Search, selected check, arrows, Enter and Escape |
| Resource actions/pin submenu | Passed | `600` | Copy, real team checkboxes, edit and delete confirmation |
| Milestone menu | Passed | `600` | Copy link/name, destructive confirmation |
| Project date picker | Passed | `160` | Locale-native month/weekdays/ARIA, modes, navigation and remove |
| Issue filter/value menus | Passed | `700` | Only implemented fields shown; multiselect and empty result |
| History/resource/delete dialogs | Passed | `699/700/701` | Overlay isolation, initial focus, Escape/cancel |

The notification and project action menus use controlled React state; no translated DOM selector is used to open one Portal from another. Portal entity names carry explicit I18n boundaries.

## Interaction acceptance

- `ArrowDown` moves from the hidden project action filter to the first menu item; `ArrowRight` opens Copy; Escape closes each layer.
- `Cmd/Ctrl+I` toggles project details; `Cmd/Ctrl+U` opens Activity; `Alt+F` toggles favorite and was restored.
- Enter and Space toggle milestone collapse without changing layout dimensions; state was restored expanded.
- Milestone issue links retain `projectMilestoneId`; the visible scope chip is clearable.
- Create issue from milestone scope displays the exact project and milestone entity names despite stored drafts.
- A zero-count priority filter rendered the real `No matching issues` state and Create issue action; the filter was cleared afterward.
- Resource pin-to-team persisted through the API and was restored to its original state.
- Project and resource delete confirmations were opened and canceled; no production or demo entity was deleted.
- Details and Insights are mutually exclusive. On mobile, both cover only the workspace below the toolbar, so their close/switch controls remain reachable.

## Locale, theme and responsive matrix

| Locale | Theme | Desktop | Mobile `390 x 844` |
| --- | --- | --- | --- |
| Simplified Chinese | Light | Passed | Passed |
| Simplified Chinese | Dark | Passed, including Portals | Passed, including menu collision |
| English | Light | Passed | Passed via Preferences UI and live `390 x 844` device emulation |
| English | Dark | Passed | Passed, including details/Insights close |

Mobile measurements:

- Page: `0 / 0 / 390 / 808`; document scroll width `390`
- Header: `0 / 0 / 390 / 44`; workspace: `0 / 87 / 390 / 721`
- Details overlay: `0 / 87 / 390 / 721`, `z-index 80`
- Insights overlay: `4 / 87.5 / 390 / 712.5`, `z-index 80`
- Project action Portal: `x 140`, `width 249.77`, right edge `389.77`; fully inside viewport
- Business project, team, user, label, milestone and resource names remained untranslated
- Device metrics override was cleared; Chrome returned to `1470 x 693`

## Automated verification

- `go test ./...`
- `npm run build`
- `npm run lint` (four pre-existing unused-declaration warnings)
- `git diff --check`
- Docker image rebuilt and local stack restarted at `http://127.0.0.1:5173`
