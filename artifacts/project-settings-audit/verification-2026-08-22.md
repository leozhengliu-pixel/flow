# Linear Project Labels and Statuses verification

Date: 2026-08-22

## Reference

- Linear labels: `https://linear.app/leozhengliu/settings/project-labels`
- Linear statuses: `https://linear.app/leozhengliu/settings/project-statuses`
- Flow labels: `http://127.0.0.1:5175/cleantrack/settings/project-labels`
- Flow statuses: `http://127.0.0.1:5175/cleantrack/settings/project-statuses`

The Linear pages were traversed in the user's signed-in Chrome session. All
measurements below come from live DOM rectangles and computed styles. Screenshots
were used after the DOM audit for visual comparison only.

## Model and API audit

| Linear behavior | Flow representation | Result |
| --- | --- | --- |
| Project labels and groups | `IssueLabel.resourceType=project`, `LabelGroup.resourceType=project` | Existing model supports create, edit, group, archive, restore and delete |
| Label usage and dates | Project `labelIds`, `createdAt`, `lastAppliedAt` | Table renders project count, last applied and created/archived date |
| Workspace/Archived views | `archivedAt` on labels and groups | Scope Portal and disabled archived creation verified |
| Five project lifecycle groups | `ProjectStatus.type` | Backlog, Planned, Started, Completed and Canceled rendered separately |
| Single-status drag protection | Status count per lifecycle type | A row is sortable only when its lifecycle type has more than one status |
| Custom status ordering | `ProjectStatus.position` and reorder API | Drag/drop plus `Alt+ArrowUp/Down`; persisted ordering verified |
| Status editing/deletion | Project status CRUD | Every status is editable; deleting the last status of a type is rejected with the Linear Toast |

Backend hardening:

- Canonical and custom statuses use the same edit/delete menu behavior.
- A custom status cannot change lifecycle type after creation.
- Reordering is allowed only within a lifecycle type and persists through reload.
- Deleting the final status of a lifecycle type returns a validation error.

## Project Labels measurements

Desktop viewport: 1470 x 693.

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Page title x/y/height | 300.5 / 64.5 / 32 | 300.5 / 64.5 / 32 | exact |
| Search x/y/size | 300.5 / 117.5 / 300 x 32 | 300.5 / 117.5 / 300 x 32 | exact |
| Search padding/radius | 6 25 6 32 / 8 | 6 25 6 32 / 8 | exact |
| Toolbar x/y/height | 300.5 / 116.5 / 34 | 300.5 / 116.5 / 34 | exact |
| Table header x/y/size | 244.5 / 190.5 / 1217 x 32 | 244.5 / 190.5 / 1217 x 32 | exact |
| Data row x/y/size | 244.5 / 222.5 / 1217 x 44 | 244.5 / 222.5 / 1217 x 44 | exact |
| Name editor x/width/height | 334 / 150 / 25 | 334 / 150 / 25 | exact |
| Description x/width/height | 630.195 / 200 / 25 | 630.195 / 200 / 25 | exact |
| Row menu x/y/size | 1401.5 / 228.5 / 32 | 1401.5 / 228.5 / 32 | exact |
| Row menu width/rows/radius | 189 / 32 / 12 | 189 / 32 / 12 | exact |

The Workspace/Archived scope menu uses z-index 500. Row and nested group menus
use z-index 600. Archive/delete confirmation uses overlay 700 and dialog 701.

## Project Statuses measurements

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Title x/height/font | 549 / 32 / 24px 500 | 549 / 32 / 24px 500 | exact |
| Status card x/y/width/radius | 533 / 162.5 / 640 / 10 | 533 / 162.5 / 640 / 10 | exact |
| Group x/width/base height | 533 / 640 / 96 | 533 / 640 / 96 | exact |
| Group header x/y/size | 545 / 178.5 / 616 x 36 | 545 / 178.5 / 616 x 36 | exact |
| Group header padding/radius | 4 6 4 16 / 6 | 4 6 4 16 / 6 | exact |
| Status row width/height/radius | 640 / 60 / 3 | 640 / 60 / 3 | exact |
| Add button x/y/size | 1131 / 184.5 / 24 | 1131 / 184.5 / 24 | exact |
| Create row x/y/size | 549 / 274.5 / 608 x 60 | 549 / 274.5 / 608 x 60 | exact |
| Color control x/y/size | 549 / 288.5 / 33 x 32 | 549 / 288.5 / 33 x 32 | exact |
| Color SVG size/center delta | 16 x 16 / 0,0 | 16 x 16 / 0,0 | exact |
| Editor name x/width/height | 594 / 140.48 / 34 | 594 / 140.48 / 34 | exact |
| Editor description x/width/height | 746.48 / 255.98 / 34 | 746.48 / 255.98 / 34 | exact |
| Editor input padding/radius | 6 12 / 8 | 6 12 / 8 | exact |
| Cancel x/y/size | 1014.46 / 288.5 / 68.15 x 32 | 1014.45 / 288.5 / 68.14 x 32 | < 0.02 px |
| Create x/y/size | 1090.61 / 288.5 / 66.39 x 32 | 1090.59 / 288.5 / 66.39 x 32 | < 0.02 px |
| Color picker size/radius/z-index | 414 x 172 / 6 / 500 | 414 x 172 / 6 / 500 | exact |
| Saturation area / hue rail | 350 x 100 / 14 x 100 | 350 x 100 / 14 x 100 | exact |
| Drag handle hit area / SVG | 20 x 60 / 10 x 10 | 20 x 60 / 10 x 10 | exact |
| Drag handle x / idle / hover opacity | row x - 2 / 0 / 1 | row x - 2 / 0 / 1 | exact |
| Status menu width/height/radius | 175 / 77 / 12 | 175 / 77 / 12 | exact |
| Status menu items/order | Edit, Delete | Edit, Delete | exact |
| Menu row height/padding | 32 / 0 18 0 14 | 32 / 0 18 0 14 | exact |
| Menu open/close animation | scale .98, 150 / 120 ms | scale .98, 150 / 120 ms | exact |

Menus animate at 150 ms with `cubic-bezier(.16,1,.3,1)` and use z-index 600.
Color selection is a theme-aware Portal at z-index 600. Confirmation dialogs
use 700/701.

## Interaction verification

Project Labels:

- Search, name/description/usage/date sorting.
- Workspace and Archived views.
- New label and group row, disabled description before entering a name.
- Enter saves and Escape cancels inline editing.
- Preset/custom color, group collapse, selection and bulk action bar.
- Edit, move-to-group submenu, project-filter navigation, archive confirmation,
  restore and delete confirmation.
- Archived empty state and disabled New group/New label controls.

Project Statuses:

- Five lifecycle groups and count-based sortable rows.
- Create form in each group; all add controls disabled while editing.
- Group-specific Linear defaults: orange dashed Backlog, grey Planned, 70%
  yellow In Progress, checked purple Completed, and crossed grey Canceled.
- Name, description and full HEX/saturation/hue color edit states.
- Linear keeps Create visually active for an empty name; native required
  validation blocks submission and returns focus to Name.
- Every row menu contains only the measured Edit and Delete actions with Linear SVGs.
- The hidden filter keeps Linear-compatible keyboard focus without adding a visible menu item.
- Six-dot drag handles remain at opacity 0 until row hover and never shift content.
- When a lifecycle type has multiple statuses, every row in that type, including
  its canonical row, renders the handle. Each row exposes only its own handle
  while hovered; sibling handles remain at opacity 0.
- The create/edit row has the same hover-only handle at x = row x - 18.
- Deleting the last status of a type shows the matching title/description Toast.
- HTML drag/drop and `Alt+ArrowUp/Down` ordering; persisted order verified.
- Project-count navigation and delete confirmation.

## Locale, theme and responsive matrix

| Combination | Result |
| --- | --- |
| English / Light / desktop | Labels, Statuses, menus, form and confirmations verified |
| Chinese / Light / desktop | All controls translated; entity names preserved |
| English / Dark / desktop | Cards and row-menu Portal colors/shadows verified |
| Chinese / Dark / desktop | Status card, color Portal and menu z-index verified |
| 390 x 844 mobile | No horizontal overflow; status form reflows and color Portal clamps to 374 px |

On mobile, Labels uses a two-row toolbar and a compact name/menu table. Statuses
keeps 36 px headers and 60 px rows while hiding project counts. Business names
such as `Project delivery`, `产品`, and custom status names carry
`data-i18n-ignore` and are never translated.

## Automated checks

- `go test ./...`
- `npm run build`
- `npm run lint`
- `git diff --check`
- Fresh browser console error check after final reload
