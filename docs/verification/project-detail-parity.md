# Project Detail vs. Linear Comparison

Date: 2026-09-27. Scope: project detail page (Overview, Activity, Issues tabs,
right sidebar, header, and property menus) in dark and light themes.

## Method

Both apps ran in the same Chrome window at a 1405x706 CSS viewport (DPR 2),
Inter Variable in both. An identical fixture existed on each side:

- Name `Compare Test`, summary `Pixel-level comparison fixture`
- Description: one paragraph, an H2 `Goals`, a two-item bullet list
- Status In Progress, priority High, no lead, start date Sep 28th, no target
- One milestone `Alpha`, no issues, default team

A script walked every rendered element in the content area (x >= 240) and
recorded its text, rect, font size / line height / weight / letter spacing,
color, background, border, and radius. Colors were normalized to sRGB hex
through a canvas, because Linear reports `lch()`. Menus were opened one at a
time and measured the same way. All coordinates below are CSS pixels.
Hover, animation, and keyboard behavior were not measured.

Reference values come from Linear's rendered DOM. The left app sidebar is
out of scope.

## Root Cause for Most Dark-Theme Color Drift

`web/src/components/project-detail/project-detail-page.css` sets
`--pd-copy: var(--theme-text-secondary)` in the base (dark) block but
`--pd-copy: var(--text-body)` for `[data-theme="light"]`. The light theme
therefore matches Linear, while in dark the summary, description body, list
items, and overview property chips all render at secondary gray `#98999c`
instead of Linear's body `#e3e4e6`. `--pd-card-border` has the opposite
problem in light: `lch(97.3% 0 282)` renders as `#f7f7f7`, so the sidebar
cards are nearly borderless (Linear: 0.5px `#e8e8e8`).

## Findings

Severity: **P1** is a visible layout, content, or behavior difference; **P2**
is a measurable style difference; **P3** is minor.

### Header (y 0-52)

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| H1 | P1 | Breadcrumb | `Projects › [icon] Compare Test` | No `Projects ›` segment, so the favorite and actions buttons sit 77px further left (x 374 vs 451) |
| H2 | P1 | Breadcrumb project icon | Project color (indigo default) | New projects default to red `#eb5757` |
| H3 | P2 | Header icon color (dark) | `#e3e4e6` | `#7f7f85`. Light matches (`#2f2f31` / `#2e2e30`) |
| H4 | P2 | Header icon size | Actions/bell 14px | 16px |
| H5 | P3 | Breadcrumb text | 13px, line-height normal (16px box) | 13px/19.5px (20px box) |

### Tab bar (y 60)

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| T1 | P1 | Inactive tab pill (dark) | bg `#1c1c1d` | Transparent |
| T2 | P2 | Active tab pill (dark) | bg `#29292b`, text `#ffffff` | bg `#202022`, text `#e5e6e8` |
| T3 | P3 | Tab label line box | 12px / normal (15px) | 12px/18px |
| T4 | P2 | "Add new view" icon | 14px, `#e3e4e6` | 16px, `#7f7f85` (dark) |
| T5 | P1 | Right actions (Overview/Activity) | Close details only | Extra "Open project insights" button |
| T6 | P2 | Right actions (Issues) | filter / display / close, 34px pitch, icons `#e3e4e6` | filter / display / insights / close, 32px pitch, icons `#959597` |

Light-theme tab colors match (`#ececed` active, `#ffffff` inactive).

### Overview: title block

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| O1 | P1 | Icon tile | 36x36, bg project color at 18% alpha, 24px glyph | 28x32, bg `#202022` (dark) / `#f8f8f9` (light), 22px glyph |
| O2 | P2 | Title | y 208, 24px/32px/600, letter spacing -0.16px, `#ffffff` | Input at y 204, no letter spacing, `#e5e6e8` |
| O3 | P1 | Summary (dark) | `#e3e4e6`, 15px/23px, letter spacing -0.1px | `#98999c` (see root cause), 15px/24px |

### Overview: property rows

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| P1 | P1 | Rows shown | Properties, Resources | Properties, Initiatives, Labels, Resources, Customers (empty rows are always visible) |
| P2 | P2 | Row label width | Fits content (65px) | Fixed 82px |
| P3 | P2 | Chip y / icon size | y 287, 16px icons | y 283, 14px icons |
| P4 | P1 | Chip text (dark) | All `#e3e4e6` | Status/priority/lead `#98999c`, dates `#e5e6e8` |
| P5 | P2 | Date arrow | 16px SVG | `→` glyph, 11px `#66666b` |
| P6 | P2 | Team chip text (dark) | `#e3e4e6` | `#8f8f95` |
| P7 | P1 | Resources control | `+ Add document or link…` button (177px, text `#565759`) | Bare 28px `+` icon, no text |
| P8 | P2 | Row pitch | Properties → Resources 70px (with chip wrap) | 44px per row |

### Overview: update card, description, milestones

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| D1 | P1 | "Write first project update" | Inside a 688x65 card, 0.5px `#2c2d2f`, radius 10 | No card; the button floats |
| D2 | P1 | Description body x | 293 (aligned with labels) | 307 (14px indent); list items 331 vs 317 |
| D3 | P1 | Description color (dark) | `#e3e4e6` | `#98999c` (see root cause) |
| D4 | P2 | Description label → text gap | 47px | 62px |
| D5 | P1 | Document outline rail | Minimap at x 249 (Description / Goals / Milestones / Alpha) | Missing |
| D6 | P2 | Heading hover actions | `Heading actions` button in gutter | Missing |
| D7 | P1 | Milestone row | Static title 15px/23px/600, expand chevron, `Set target date` (`#565759`), `0 issues · 0 %` | Always-editable input 13px/23px/600, collapse button, `Choose date` (`#e5e6e8`) |
| D8 | P1 | Milestone description | Collapsed by default | 98px textarea expanded by default |
| D9 | P2 | Milestone empty text | None | Extra `No issues in scope` |

### Right sidebar

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| S1 | P1 | Card frame | x 1001, width 388 (8px from the panel edge), 0.5px border `#29292c` (dark) / `#e8e8e8` (light) | Width 396, flush with the panel edge, 1px `#202022` (dark) / `#f7f7f7` (light, nearly invisible) |
| S2 | P2 | Section header | 28px row; chevron 16px `#98999c`; `+` 28x28 with 14px icon `#e4e5e7` | 41px row; chevron 11px `#565759`; `+` 23x23 with 13px icon `#7d7d82` |
| S3 | P1 | Value typography | 12px, line-height normal, weight 450 | 13px/19px |
| S4 | P1 | Empty-value styling | Placeholder text dimmed `#98999c`: `Add lead`, `Add members`, `Target`, `Slack channel`, `Add label` | All values `#e5e6e8`; empty lead shows `Lead` instead of `Add lead`; `Add member` (singular) |
| S5 | P1 | Extra row | None | `Initiatives: No initiative` |
| S6 | P2 | Dates arrow | 16px SVG | `→` glyph |
| S7 | P2 | Milestone row | `Alpha` `#e4e5e7`, `0% of 0` | `Alpha` `#98999c`; extra `No date` button; `No milestone` shows a `0` count |
| S8 | P1 | Progress section | Hidden while the project has no issues | Always shown (Scope/Started/Completed, Assignees/Labels) |
| S9 | P1 | Activity entries | Project glyph + `Skyler Anderson created the project · Sep 27`, 12px/16.8px | 18px avatar + 10px/15px text; also logs `changed priority from No priority to High` for a value set at creation |
| S10 | P2 | `See all` | 12px | 10px |
| S11 | P2 | Section visibility per tab | Activity tab: Properties + Milestones only | Always Properties, Milestones, Progress, Activity |

### Activity tab

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| A1 | P1 | Composer default mode | `Comment` | `Update` (with health pill and property diff) |
| A2 | P1 | Feed | Shows `created the project` below the composer | No entries under the composer |
| A3 | P2 | Mode toggle | 130x25, track border `#343538`, items 12px/18px/500, active `#313133` with shadow | 125x29, no track border, items 11px/16.5px/450, active `#3c3d40` |
| A4 | P2 | Mode toggle (light) | Track white with border `#e8e8e8` and shadow; active `#f2f2f2` / text `#303032` | Track `#f8f8f9`, no border; active `#d8d8d8` / text `#5c5c5e` (low contrast) |
| A5 | P2 | Composer frame | 0.5px `#29292c` / `#e8e8e8`, radius 10 | 0.5px `#2f2f32` / `#d8d8d8`, radius 8 |
| A6 | P2 | Submit button | `Comment`, 12px/500 | `Post update`, 11px/450 |
| A7 | P2 | Update diff rule | n/a | 1px left rule `#e5e6e8` (dark) / `#1b1b1b` (light), which is high contrast |

### Issues tab (empty project)

| # | Sev | Element | Linear | Flow |
|---|---|---|---|---|
| I1 | P1 | Empty state | Illustration 86x80, `Add issues to the project` 15px/600, two helper paragraphs 13px/18.2px, primary `Create new issue` button (`#5e6ad2` dark / `#6d78d5` light) with `C` key hint; left-aligned at x 451 | Centered `No matching issues` / `Change the filters or create a new issue.` (filter copy on an unfiltered project) plus secondary `Create issue` button |

### Menus

All Flow menus match Linear's container style (bg, 0.5px border, radius 12,
shadow). Differences:

| # | Sev | Menu | Linear | Flow |
|---|---|---|---|---|
| M1 | P2 | Project actions | 245px wide, items 31px, weight 400 | 264px wide, items 32px, weight 450; same items |
| M2 | P2 | Status / priority | 247x205, items 31px, filter input 35px | 252x213, items 32px, input 36px; same items and number hints |
| M3 | P1 | Lead | 172x166 | 240x207, item padding `0 8px` radius 6 and weight 450, unlike every other Flow menu |
| M4 | P2 | Members | 177x105, no group header | 252x177, extra `Users from the project team` header |
| M5 | P1 | Date picker | Week starts Sunday; weekend columns shaded; today ringed; input `MM/DD/YYYY` with a clear button | Week starts Monday (Flow preference default); no weekend shading; `YYYY/MM/DD`; no clear button |
| M6 | P2 | Labels | 256x80 | 252x94 |
| M7 | P1 | Milestone actions | Filter input; `Edit…`, `Set target date…`, `Copy ▶`, `Move milestone to ▶`, `Convert to project`, `Delete` | No filter input; `Edit target date…` plus a `No date` line; `Delete…`; weight 450 |
| M8 | - | Properties `+` | Could not be opened by automation | `Blocked by…` / `Blocking…` (not compared) |

### Theme-Independent Matches

The main panel frame, background, border, and radius match exactly in both
themes. Light-theme text colors for the title block, description, labels, and
sidebar match within ±3 per channel. Menu containers match.

## Suggested Fix Order

1. Dark `--pd-copy` → body text token, and light `--pd-card-border` → a
   visible border token (fixes O3, P4, D3, and S1 color).
2. Sidebar typography and placeholder dimming (S3, S4), card inset (S1).
3. Hide empty optional rows and sections (P1, S5, S8), and use the Linear
   empty state on the Issues tab (I1).
4. Activity tab: default to Comment and render the feed (A1, A2).
5. Header breadcrumb and tab pills (H1, T1, T2); description indent (D2);
   update card (D1).
6. Milestone row/description defaults (D7, D8), date picker details (M5),
   Lead menu item styling (M3).

## Interaction-State References (measured 2026-09-27, dark)

Viewport 1470x706 for these measurements.

### Document outline rail (D5)

- The rail is a column of 2px bars at x≈249 (8px active bar, bright; others
  dimmer) with a 12px pitch, vertically centered in the content viewport. It
  never overlaps the project icon or title.
- The hover card is centered vertically on the rail and opens to its right:
  x 252, 117x116, bg `#212122`, 0.5px border `#3c3d40`, radius 8, standard
  menu shadow, padding 10px 0.
- Rows have a 24px pitch and 13px/normal/450 text. Section rows (Description,
  Milestones) have a 16px icon at x 265 (align-left for Description, diamond
  for Milestones) and text at x 287. Child rows (description headings such as
  Goals, milestone names such as Alpha) have no icon; their text also sits at
  x 287.
- The row for the section in view is bright (`#e5e6e8`), other sections are
  mid (`#9c9d9f`), and children are dim (`#626365`).
- Every description heading (H1–H3 of the HTML description) is listed.

### Resources "Add document or link…" menu

- Popup 204x77 anchored under the button's left edge, bg `#212122`, 0.5px
  `#3c3d40`, radius 12, padding 6px 0.
- Its search input is visually hidden (placeholder "Add document or link…"),
  so there is no visible header row.
- Options are 32px tall, padding `0 18px 0 14px`, 16px icon, 13px/450 text:
  "Create new document…" (file icon) and "Add a link…" (link icon).
- The shortcut hint `Ctrl L` right-aligned at 11px/500 in Inter (not
  monospace), color `#9c9d9f`, with no keycap border.

### Activity composer focus

- The comment editor has no outline, border or ring when focused. It has
  padding 4px 16px, min-height 30px, spans the composer frame, and its
  placeholder starts at x≈277.
- With an empty editor, the "Comment" submit button renders in the normal
  neutral style (bg `#232325`, text `#e4e5e7`), not dimmed.

### Round-3 interaction references (dark, viewport 1470x706)

- **Insights toggle:** "Open project insights" (28x28) is shown on every tab
  (Overview, Activity, Issues) in the Business plan. It sits immediately left
  of "Close project details" with a 34px pitch. On Issues the order is
  Add filter (1323.5), Display options (1357.5), insights (1391.5), close
  (1425.5).
- **Tooltips:**

  | Control | Tooltip |
  | --- | --- |
  | Copy page URL | "Copy project URL" ⌘⇧C |
  | Notifications bell | "Project notifications" |
  | Favorite, Project actions, icon tile | none |
  | Tabs | "View Overview" 1, "View updates and activity" 2, "View Issues" 3 |
  | Add new view | "Create new view" |
  | Close details | "Close project details" ⌘I |
  | Status | "Change project status" P then S |
  | Priority | "Change project priority" P then P |
  | Lead | "Set project lead" P then A |
  | Members | "Change project members" P then M |
  | Start date | "Start date" Ctrl ⌥ S |
  | Target date | "Add target date" Ctrl ⌥ D |
  | Team | team name |
  | Slack | "Slack channel" |
  | Labels | "Add labels" P then L |
  | Resources add | "Add document or link" |
  | Milestones + | "Create new project milestone" |
  | Disabled Properties + | "All projects are related to this project" |
  | Display options | "Show display options" (with shortcut) |

- **Lead menu:** 171.5x165.6. Search is hidden (type to filter). Padding
  6px 0. Group headers are 29px tall at 12px and truncate with an ellipsis.
  Options are 31px. Icon at x14, label at x38, check at x122, shortcut `0`
  at x146 (Inter 11px, secondary color). "Invite and add…" uses a send
  (paper-plane) icon.
- **Members menu:** about 178x105. Search is hidden and there is no "Users
  from the project team" header. User rows show a checkbox (x15), avatar
  (x36) and name (x60). "New user" group header is 30px. The invite row has
  no checkbox.
- **Heading actions menu:** 165x72, radius 10, padding 4px 0. Right-aligned
  to the button and opens below it. Full-width 32px rows with padding 0 14px.
  16px icons in secondary color.
- **Date picker:** 304 wide. Height is clamped to the viewport (371 at
  y 319), and the content scrolls. When the value is empty, the input is
  prefilled with the suggested date (the start date or today) and selected.
  Focused input shows a 1px accent outline. The week starts on Sunday (en).
- **Update composer:** the health pill uses a 16px glyph: a 25%-alpha circle
  plus a trend polyline. Colors are on track lch(67.2 64.37 141.95), at risk
  lch(87.2 90 85), off track lch(65.2 73 29). The label is 12px/500 in the
  health color, pill padding 0 8px. The health menu is 175x109 with 32px
  rows at 13px/400 and no check mark. "Write with Agent" is 130x24, 12px/500,
  primary text, with a lines-plus-pointer icon. Controls use
  bg lch(13.861 1.139 272).
- **Filter menu:** 203x648, clamped to the viewport. Rows are 31px.
- **Display options:** 295x523. Contents: List/Board, Grouping, Sub-grouping,
  Ordering, Order completed by recency, Completed issues, Show sub-issues,
  List options (Nested sub-issues, Show empty groups), and Display
  properties chips.

## Correction: Popover Sizes (2026-09-28)

Earlier popover sizes in this document were taken with
`getBoundingClientRect()` while the Linear tab was in the background. Hidden
tabs pause animations, so Linear menus were frozen at their opening frame,
`scale(0.98)`, and the widths and heights came out about 2% small. Use
`offsetWidth`/`offsetHeight`, which ignore transforms. Corrected values:

| Menu | Size | Rows |
| --- | --- | --- |
| Project actions | 250 wide | 32px items |
| Status / priority | 252x210 | 32px items, 37px search row |
| Lead | 175x169 | 32px items, 30px group headers |
| Members | 181x107 | — |
| Labels | 261x82 | — |
| Milestone actions | 197x261 | Search is visually hidden. Starts with "Open milestone issues". Delete is not red. |
| More properties | 262x109 | — |
| Heading actions | 168x74 | — |
| Resources | 205x77 | — |
| Health | 175x109 | — |
| Date picker | 304x373, clamped | — |
| Comment options | 227x285, padding 6px 0 | 32px items |

Comment options groups, separated by 0.5px lines with 6px margins:

1. Edit, Unsubscribe from thread
2. Resolve thread
3. Copy link to comment, Copy content as Markdown
4. New issue from comment…
5. Delete (not red)

Row heights are 32px throughout, not 31px. `styles/picker-parity.css` sets a
206px minimum width for issue pickers; the project pickers override it at
matching specificity.
