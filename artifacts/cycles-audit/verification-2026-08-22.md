# Linear Cycles replication verification

Date: 2026-08-22

## Reference and scope

- Linear directory: `https://linear.app/leozhengliu/team/LEO/cycles`
- Linear detail: `https://linear.app/leozhengliu/team/LEO/cycle/2`
- Linear upcoming: `https://linear.app/leozhengliu/team/LEO/cycle/upcoming`
- Flow directory: `http://127.0.0.1:5175/cleantrack/team/CLE/cycles`
- Flow detail: `http://127.0.0.1:5175/cleantrack/team/CLE/cycle/50`
- Flow upcoming: `http://127.0.0.1:5175/cleantrack/team/CLE/cycle/upcoming`

The Linear pages were inspected in the signed-in Chrome session. Measurements
come from live `getBoundingClientRect()` and computed styles, not screenshots.
Screenshots were used only after DOM inspection for visual verification.

## Data model and API audit

| Linear behavior | Flow representation | Verification |
| --- | --- | --- |
| Named numbered cycle, description, dates, capacity and lifecycle | `Cycle` plus `PATCH /api/cycles/:id` | Existing mutation path retained and edit/date dialogs exercised |
| Directory favorite and cycle favorite | `CycleSettings.favoriteView`, `Cycle.favorite` | Toggle persisted after reload and was restored after the test |
| Resources | `Cycle.resources: CycleResource[]` | Added create/delete endpoints and backend tests |
| Calendar subscription | Private `Cycle.calendarToken` and public tokenized ICS endpoint | Bootstrap leaked zero tokens; copied feed had a non-empty token |
| Insight defaults | `Cycle.insight: Record<string,string>` | Estimate/Assignee/Project survived reload; defaults then restored |
| Issue membership | `Issue.cycleId` | List and Board both render and edit the same cycle-scoped issues |
| Current, upcoming and previous cycles | `Cycle.status` plus derived earliest-upcoming/planned presentation | Directory, canonical upcoming and numbered detail verified |

New endpoints:

- `POST /api/cycles/:id/resources`
- `DELETE /api/cycles/:id/resources/:resourceId`
- `POST /api/cycles/:id/calendar-token`
- `GET /api/calendar/cycles/:id.ics?token=...`

Only the current canonical routes remain. The old `/cycles/current` and
`/cycles/upcoming` compatibility routes were removed.

## DOM measurement comparison

Desktop content width was 1217 px at a 1470 px browser width. Flow's browser
height was 749 px and Linear's was 693 px, so vertical content height is not
compared across the two sessions.

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Directory title x/y/height | 262.5 / 22.5 / 15.5 | 262.5 / 22.5 / 15.5 | 0 / 0 / 0 px |
| Directory title font | 13 px, 500 | 13 px, 500 | exact |
| Directory row x/width/height | 244.5 / 1217 / 70 | 244.5 / 1217 / 70 | exact |
| Cycle name x/y | 402.5 / 79.75 | 402.5 / 79.75 | exact |
| Status text x/y | 1132.5 / 79.75 | 1131.5 / 79.75 | -1 / 0 px |
| Capacity percent x/y | 1237.875 / 79.75 | 1235.5 / 79.75 | -2.375 / 0 px |
| Scope track x/y | 1345.5 / 79.75 | 1341.5 / 79.75 | -4 / 0 px |
| Row menu x/y/size | 1417.5 / 71.5 / 32 | 1417.5 / 71.5 / 32 | exact |
| Detail header height | 52 | 52 | exact |
| Detail toolbar height | 44 | 44 | exact |
| Toolbar icon size | 28 x 28 | 28 x 28 | exact |
| Details/Insights rail x/width | 1021.5 / 440 | 1021.5 / 440 | exact |
| Details card x/width | 1025.5 / 428 | 1025.5 / 428 | exact |
| Details card radius | 10 | 10 | exact |
| Cycle switcher | 230 px wide, radius 12, z 600 | 230 px wide, radius 12, z 600 | exact |
| Row menu | radius 12, 32 px rows, 5 px padding, z 600 | radius 12, 32 px rows, 5 px padding, z 600 | exact |
| Modal Portal | z 701 | z 701, overlay 700 | exact |

Both products use the same Inter Variable family and 13 px / 19.5 px base menu
type. Flow menu motion is 150 ms with `cubic-bezier(.16,1,.3,1)`; the observed
Linear floating surface resolved to 190 ms using the same easing curve.

## Interaction inventory

- Directory: favorite, row navigation, hover/focus row surface, per-row menu.
- Row/detail menu: edit name/description, date submenu, favorite, canonical
  link, calendar submenu, start/complete confirmation.
- Detail breadcrumb: team overview, directory, searchable cycle switcher.
- Toolbar: full issue property hierarchy, nested values, List/Board, grouping,
  sub-grouping, ordering, completed window, subissues, empty groups and display
  properties.
- Details: duplicate favorite/menu access, resource menu, link dialog, document
  action, removable resources, collapsible planning metrics.
- Insights: graph, measure/slice/segment controls and persisted defaults.
- Empty state: measured 91.11 x 80 cycle illustration, education copy, create
  issue and documentation actions.
- Keyboard: filter command search, arrow navigation, nested Escape, cycle
  switcher arrows/Enter, modal Escape and `Cmd/Ctrl+I` details toggle.
- Disabled states: invalid cycle name/date save, empty link submission, busy
  document/link actions and pending confirmation actions.
- Completed state is the Linear previous-cycle state represented by Flow's
  completed rows; no separate fake archive action is exposed.

## Theme, locale and responsive verification

| Combination | Result |
| --- | --- |
| English / Light / desktop | Directory, detail, switcher, menu and empty state verified |
| Chinese / Light / desktop | Toolbar, full filter hierarchy, entities and resource dialog verified |
| Chinese / Dark / desktop | Display Portal colors, border, shadow and z 600 verified |
| Chinese / Dark / 390 x 844 | No document overflow; rows remained 70 px; details used a bounded overlay |

Business entity names such as `Cycle 49`, workflow states, users, labels and
project names remain unchanged across locales. Portal surfaces inherit tokenized
theme colors. Menus use z-index 600, mobile panels 500, and dialogs 700/701.

## Automated verification

- `go test ./...`
- `npm run build`
- `npm run lint` (one pre-existing `App.tsx` unused-variable warning)
- `git diff --check`
- Browser console checked after the final pass
