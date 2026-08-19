# Linear view detail replication audit

Audit date: 2026-08-19 (Asia/Shanghai)

Targets:

- Linear: `https://linear.app/leozhengliu/views/issues` and the saved view opened from that directory
- Flow: `http://127.0.0.1:5173/cleantrack/view/view_business`
- Desktop viewport: `1470 x 693`, DPR `2`
- Mobile viewport: Chrome device metrics `390 x 844`, DPR `2`

All geometry and computed styles below were read from the live DOM in the user's Chrome session. Linear data was not modified.

## Component and state inventory

Validated surfaces:

- Views directory, issue/project resource tabs, ordering menu and submenu, empty directory state
- New view and edit view states, icon/color picker, destination/scope menu, cancel, save and keyboard cancellation
- Saved view header, favorite switch, action menu, owner/move/subscription submenus, duplicate, copy, CSV export entry and delete confirmation entry
- Filter menu and nested value menus, applied values, operators, clear/remove controls and empty results
- List/board display menus, grouping, sub-grouping, ordering, completed window, sub-issue settings and visible properties
- List group headers, issue rows, property controls, collapse/create actions, hover and keyboard focus
- Details panel identity, visibility, owner, Assignees/Labels/Projects tabs and summary filtering
- Insights introduction, dismissed state, Measure/Slice/Segment menus, archived/no-priority display options, segmented chart, result table, persisted default, action menu and application fullscreen
- Light/dark Portal theme, collision handling, z-index, Escape close and Radix keyboard navigation

## Data and API parity

| Linear behavior | Flow representation | Result |
| --- | --- | --- |
| Persist view icon, color, filters and display | `SavedView` plus create/update API | Existing |
| Persist insights defaults | `SavedView.insights` JSON and mutation input | Added; lifecycle API test covers create/update persistence |
| Analyze archived issues without showing them in the list | Insight source can include `Issue.archivedAt`; list source remains unarchived | Added |
| Slice and segment the same report | `SavedViewInsightsConfig.slice` and `.segment` | Added; chart and table now aggregate the cross-product |
| Owner, scope, favorite and subscription events | Existing saved view and subscription APIs | Verified through live menu interactions |
| Upgrade seeded views to current display schema | Safe migration recognizes the legacy `direction` field | Added without overwriting later user display choices |

## Desktop measurements

Values are CSS pixels. The compared view names differ, so heading text width is intentionally data-dependent.

| Element | Linear | Flow | Delta |
| --- | ---: | ---: | ---: |
| Main panel `x / y / width` | `244 / 8 / 1218` | `244 / 8 / 1218` | `0 / 0 / 0` |
| Main border / radius | `.5 / 12` | `.5 / 12` | `0 / 0` |
| Header heading `x / y / height` | `286.5 / 21 / 18.5` | `286.5 / 20.75 / 18.5` | `0 / -0.25 / 0` |
| Group header height | `36` | `36` | `0` |
| First issue row `x / width / height` with side panel | `244.5 / 817 / 44` | `244.5 / 817 / 44` | `0 / 0 / 0` |
| First issue row `y` | `134` | `133.5` | `-0.5` |
| Side panel `x / width` | `1061.5 / 400` | `1061.5 / 400` | `0 / 0` |
| Side panel `y` | `96` | `96.5` | `+0.5` |
| Measure control `x / y / w / h` | `1082 / 174 / 110.33 / 30` | `1082 / 174 / 110.33 / 30` | `0 / 0 / 0 / 0` |
| Slice control `x / y / w / h` | `1208.33 / 174 / 110.34 / 30` | `1208.33 / 174 / 110.34 / 30` | `0 / 0 / 0 / 0` |
| Segment control `x / y / w / h` | `1334.66 / 174 / 110.34 / 30` | `1334.66 / 174 / 110.34 / 30` | `0 / 0 / 0 / 0` |

The Linear main panel is 28px shorter in this account because Linear renders a global Business trial bar below it. Flow has no equivalent workspace billing bar; the view detail's top, width and internal geometry are compared independently.

## Computed visual tokens

| Token | Linear | Flow |
| --- | --- | --- |
| Light main background | `lch(97.94 0.5 282)` | `lch(97.94 0.5 282)` |
| Light main border | `lch(89.84 0 282)` | `lch(89.84 0 282)` |
| Light main shadow | `0 3px 6px -2px lch(0 0 0/.02), 0 1px 1px lch(0 0 0/.04)` | Same |
| Dark main background | `lch(5.52 0.4 272)` | `lch(5.52 0.4 272)` |
| Dark main border | `lch(13.08 1.48 272)` | `lch(13.08 1.48 272)` |
| Row radius | `8px` | `8px` |
| Tool button size/radius | `28 x 28 / 9999px` | `28 x 28 / 9999px` |
| Menu open animation | `120-150ms`, ease/cubic easing by menu | `120ms cubic-bezier(.2,.8,.2,1)` |

## Portal validation

| Portal | Theme | Layer | Keyboard/result |
| --- | --- | ---: | --- |
| Saved view actions | Light/dark | `600` | Arrow navigation, Enter, Escape; owner/move/subscription submenus at `601` |
| Insight selectors | Light/dark | `620` | Radio items, selected indicator, Enter/Space, Escape |
| Insight display options | Light/dark | `620` | Checkbox items; archived option was toggled and restored by keyboard |
| Insight actions | Light/dark | `620` | Copy link, examples, refresh; menu remains within mobile viewport |
| Details/insights panel | Light/dark | `90` | Sits below all menus and above the list |

Dark Portal measurement: background `lch(12.72 0.85 272)`, border `lch(25.68 1.93 272)`, text `lch(90.451 1.2 272)`.

## Interaction acceptance

- Hover changes the issue row and toolbar button background from transparent/light surface to `lch(94.4 0.5 282)`.
- View edit opens with the persisted name and description; Escape cancels and removes the editor.
- Duplicate opens `/views/issues/new?duplicate=view_business` with copied values; Cancel returns to the directory.
- Subscription event checkbox persisted through the API and was restored to its original state after verification.
- Changing Slice enabled the default-save action; save disabled again after the API response; reload restored the saved value. The original Status slice was saved back after the test.
- Insights fullscreen expands from `400 x 588` at `1061.5 / 96.5` to `1454 x 677` at `8 / 8`; Escape restores the side panel.
- Dismissing the insights introduction persists per saved view.
- Summary tabs and rows are real controls; selecting a summary row applies a view filter.
- Entity names, view names, issue titles, status names, project names, label names and user names are excluded from automatic translation.

## Locale, theme and responsive matrix

| Locale | Theme | Desktop | Mobile |
| --- | --- | --- | --- |
| Simplified Chinese | Light | Passed | Passed at `390 x 844` |
| Simplified Chinese | Dark | Passed | Responsive rules shared; Portal dark tokens passed |
| English | Light | Passed | Responsive rules shared |
| English | Dark | Passed | Responsive rules shared; Portal dark tokens passed |

Mobile measurements:

- Main: `0 / 0 / 390 / 808`
- Saved view panel: `x 24`, `width 366`, `top 88`, `height 720`
- Issue row: `x 0`, `width 390`, `height 44`
- Assignees/Labels/Projects tabs: three `108.33px` controls, all text fits, no horizontal overflow
- Insight actions Portal: `x 159.5`, `width 188`, right edge `347.5`; entirely inside the `390px` viewport
- Device metrics override was cleared after validation; Chrome returned to `1470 x 693`.

## Automated verification

- `go test ./...`
- `npm run build`
- `npm run lint` (warnings are pre-existing unused declarations plus the Fast Refresh mixed-export warning)
- `git diff --check`
- Docker image rebuilt and local stack restarted at `http://127.0.0.1:5173`
