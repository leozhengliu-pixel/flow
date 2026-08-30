# Projects Page Replication Modules

Status: implementation in progress. Measurements below come from the signed-in Flow workspace at `/acme/projects/all`, not from inferred designs.

Reference viewport: `1470 x 754`, dark theme, workspace sidebar expanded, Health sidebar expanded.

## Module Map

| # | Module | Atomic surfaces | Required interactions | Status |
|---|---|---|---|---|
| 1 | Page surface and header | main panel, title row, New project icon button | hover, focus-visible, create callback, responsive shell | Implemented in isolated component |
| 2 | View switcher | All projects pill, saved-view pill/icon, add-view button | route/view selection, active state, truncation, keyboard focus | Implemented in isolated component |
| 3 | Filter builder | filter icon, count state, search input, filter rows, nested pickers | auto-focus, typeahead, arrows, Enter, Escape, outside click, submenu traversal | First-level measured and implemented; filter-specific nested pickers pending |
| 4 | Display options | icon/count, List/Board/Timeline tabs, grouping selects, list options, property toggles, defaults | open/close, tab selection, keyboard focus, persistence callback | Panel and view-state controller implemented; exact custom select menus pending |
| 5 | Projects table header | column grid, sorting controls, show/hide columns | hover, direction toggle, keyboard sorting | Implemented; dynamic column visibility remains owned by Display settings integration |
| 6 | Status group | disclosure, status icon/name/count, create button | collapse/expand, quick create, drag target | Implemented except drag target |
| 7 | Project row | selection, icon/name/summary, health, priority, lead, target date, issues, progress/status | row open, inline property menus, selection, hover/focus | Implemented, including Display property visibility |
| 8 | Project board card | card identity, summary/milestone, metadata | open, drag/drop, context menu, selection | Implemented except drag/drop |
| 9 | Health/Leads sidebar | segmented control, facet rows/counts, close button | tab switch, facet filter, width/responsive behavior | Implemented with data filtering |
| 10 | Context menu | project actions, view actions, group actions | pointer context, keyboard navigation, nested menus | Project menu plus Status/Priority/Lead/Target nested surfaces implemented |
| 11 | Create project flow | create button, modal/dialog, name/icon/team/status/lead/date | validation, submit, cancel, errors, focus trap | Implemented as isolated dialog; parent API integration pending |
| 12 | States | skeleton, empty group/view, loading, recoverable error | retry, preserved toolbar, no layout shift | Implemented for project data body |
| 13 | Responsive | narrow surface, horizontal table, sidebar overlay/collapse | `768 x 754`, keyboard and pointer parity | List and Board horizontal behavior implemented |

## Module 1-4 Evidence

### Surface and header

At `1470 x 754`:

```text
main:        x=244,   y=8,     w=1218, h=710
header:      h=44
toolbar:     h=44, starts y=52
main bg:     lch(5.52 0.4 272)
main border: 0.5px solid lch(13.08 1.48 272)
radius:      12px
shadow:      lch(0 0 0 / .3) 0 .5px 1px 1px
```

The title is a visual 14px/500 label. `New project` is an icon-only `28 x 28` circular button at `(1425.5,16.25)`.

At `768 x 754`, the real Flow main panel becomes `(0.45,0,767.55 x 718.02)` and loses the surrounding workspace-side gap. The right Health sidebar remains visible at `350px` wide, starting at `x=418`; it is not automatically removed at this width.

### View switcher

```text
All projects:        x=252.5, y=60.25, w=85.27,  h=28
Saved view:          x=341.77,y=60.25, w=172.67, h=28
Add view:            x=518.44,y=60.25, w=28,     h=28
active radius:       9999px
active font:         Inter Variable 12px/500
active background:   lch(16.706 0.979 272)
```

The saved view includes a people/view icon. The add-view icon uses Flow's stacked-layer production SVG path.

### Toolbar buttons

```text
Add filter:       x=1357.5,y=60.25,w=28,h=28
Display options:  x=1391.5,y=60.25,w=28,h=28
Close sidebar:    x=1425.5,y=60.25,w=28,h=28
gap:              6px
```

Production SVG paths were captured for New project, Add filter, Display options, Close sidebar, and Add new view. The implementation uses those paths directly.

### Filter dialog

```text
dialog:       x=1252,y=92,w=207,h=641.5
background:   lch(12.72 0.85 272)
border:       0.5px solid lch(25.68 1.93 272)
radius:       12px
search:       x=1266.5,y=92.5,w=148,h=36
row:          w=206,h=32,padding 0 18px 0 14px
row font:     Inter Variable 13px/19.5px
```

Observed ordered groups:

```text
AI filter
---
Advanced filter
---
Status, Priority, Labels, Lead, Members, Creator, Health, Dates,
No initiatives, Milestones, Relations
---
Customers
---
Template, Title & summary, Specific project
```

Open behavior auto-focuses the searchbox. ArrowDown retains input focus and sets `aria-activedescendant`; Escape closes and returns to the page. Nested rows expose a right-pointing affordance.

### Display options

```text
panel:       x=1089,y=92,w=331,h=503
tablist:     x=1105.5,y=106.5,w=298,h=32
tab width:   ~98-100px
```

Visible controls, in order: `List / Board / Timeline`, Grouping, Sub-grouping, Ordering, Show closed projects, List options, Show empty groups, Display properties, Reset, Set default for everyone.

Property buttons: Milestones, Summary, Priority, Status, Health, Teams, Lead, Members, Dependencies, Start date, Target date, Issues, Created, Updated, Completed, Customers, Customer revenue, Labels.

## Interaction Matrix

| Control | Pointer | Keyboard/focus | Closing behavior | Data contract |
|---|---|---|---|---|
| New project | click | Tab + Enter/Space | n/a | `onCreateProject()` |
| View pill | click | Tab + Enter | route transition | `onChangeView(view)` or `href` |
| Add view | click | Tab + Enter/Space | n/a | `onAddView()` |
| Add filter | click toggles | search auto-focus; Up/Down + Enter | Escape, outside click, toggle | `onAddFilter(filterName)` |
| Display options | click toggles | natural Tab order; Space/Enter controls | Escape, outside click, toggle | `onChangeDisplay(settings)` |
| Sidebar | click | Tab + Enter/Space | n/a | `onToggleSidebar()` and controlled `sidebarOpen` |

## Module 9: Health And Leads Sidebar

The right insights area is a sibling of the project data viewport rather than
an overlay. It reserves `400px` at the desktop reference viewport and `350px`
at `768 x 754`, matching Flow's narrow layout instead of auto-hiding.

Health and Leads are a two-tab surface. Each visible facet is a 32px row with
its current project count. Clicking a row applies that facet to the mounted
List or Board data; clicking the active row clears it. Health uses the shared
`on-track`, `at-risk`, `off-track`, and `no-update` view values. Lead filtering
uses real shared-domain user IDs, including the empty string for No lead.

The toolbar close control removes the entire insights sibling and returns the
data viewport to full width. Its state remains ephemeral until the root adds a
verified saved-view preference contract.

## Integration Contract

The independent entry point is:

```tsx
import { ProjectsPageSurface } from '@/components/projects-page/projects-page-surface'
```

The parent owns routes, persistence, create flow, active filters, and sidebar state. The component owns only ephemeral open/focus state and a local display-settings draft, emitting complete settings through `onChangeDisplay`. Project table/board/timeline content is provided as `children`, so later modules can be integrated without changing the shell.

The component imports its own `projects-page.css`; it does not depend on or modify `tokens.css`.

## Modules 5-8: Table And Board Evidence

### List grid

The real table body at the desktop reference viewport starts at `(244.5, 96)` and is `1020px` wide while the Health sidebar is open. Flow uses this exact named grid:

```text
[indent]      8px
[checkbox]   18px
[title]     425px
[health]    130px
[priority]   68px
[lead]       48px
[targetDate] 92px
[issues]     49px
[status]    120px
[end]         8px
column gap:    6px
```

Header height is `32px`. Sort buttons are `24px` high with `6px` horizontal padding and 12px/500 text. Status-group headers are `36px`, background `lch(9.232 0.85 272)`. Project rows are exactly `48px` high.

### Project row atoms

For the `Cruise` row:

```text
row:          x=244.5, y=166, w=1020, h=48
checkbox:     14x14 within an 18px named column
title cell:   x=282.5, w=425, h=28
project icon: 28x28 hit target, 16x16 glyph
health:       130px cell, 28px control
priority:      68px cell, 28px control
lead:          48px cell, 28px control, 16px avatar
target date:   92px cell/control
issues:        49px cell
status:       120px cell
```

The checkbox's native input is transparent; its 14px custom frame becomes visible on hover/focus/selection. Empty target dates still render a full-width transparent property control and expose their placeholder only on hover/focus.

Priority's real property dialog is `252 x 209.5`, with a `251 x 172` listbox and 32px option rows. The isolated implementation uses the same 252px menu width and keyboard Up/Down navigation.

### Board columns and cards

Switching Flow's Display panel to Board produces fixed-width horizontal lanes:

```text
lane width:        354px
card width:        328px
lane side padding: 12px
card gap:            8px
card radius:         8px
compact card:       66px high
full card:         141.5px high (summary + date + milestone)
```

At `768 x 754`, Flow keeps the `354px` lanes and `328px` cards; they do not compress. The board scrolls horizontally inside the clipped main panel. The local implementation follows that behavior.

### Project context menu

Right-clicking a project opens a `236px` dialog. The observed desktop example was positioned at `(504,190)` and measured `236 x 541`. Its groups contain Status, Priority, Project lead, Members, Start date, Target date, Labels, More properties; Copy/Move; Favorite/Subscribe/Remind me; New comment; Delete. The implemented first-level menu keeps the 236px width, grouped separators, focus-on-open, ArrowUp/ArrowDown cycling, Escape, outside-click close, action callbacks, and destructive Delete styling. Nested action dialogs remain a separate acceptance unit.

## Project Data Contract

```tsx
import {
  ProjectsDataView,
  type ProjectPageItem,
} from '@/components/projects-page/projects-data-view'
```

`ProjectsDataView` accepts grouped `ProjectPageItem[]` and `layout="list" | "board"`. It is controlled for selection and emits:

```text
onOpenProject(project)
onSelectionChange(ids)
onPropertyChange(project, property, value)
onSort(column, direction)
onCreateProject(status)
onProjectAction(project, action)
onRetry()
```

Loading, empty, and error are explicit props/states. This avoids coupling the view to the shared API or project domain types while the parent integration is still in flight.

## Module 11: Create Project Evidence

Flow's New project button opens a full-viewport modal layer rather than a side pane or route. At `1470 x 754`:

```text
overlay dialog:  x=0,   y=0,  w=1470, h=754
panel:           x=275, y=45, w=920,  h=663.77
panel radius:    22px
panel border:    0.5px solid lch(22.193 1.93 272)
body scroll:     x=275.5, y=45.5, w=919, h=663.52
content region:  x=275.5, y=97.5, w=919, h=550.02
content padding: 16px
name editor:     x=303.5, y=153.5, w=863, h=32
summary editor:  x=303.5, y=191.5, w=863, h=23
```

Default chips, in order: Backlog, No priority, Lead, Members, Start, Target, Labels, Dependencies. Each is `24px` high, pill radius, 12px/500 text. The Status child dialog is `252 x 209.5`, starts 4.5px below the trigger region, has a `251 x 172` listbox and the exact five values Backlog, Planned, In Progress, Completed, Canceled.

Other observed controls: disabled single-team `CLE` selector, Discard project, Choose icon, Project description, Milestones/Add, Cancel, Create project.

Submitting an empty form leaves the modal open and produces a bottom-right `383 x 62.5` alert:

```text
Project name required
The project name cannot be empty.
```

The implementation in `new-project-dialog.tsx` mirrors the large modal geometry, auto-focus, focus trap, Escape/cancel behavior, empty-name validation/toast, async create loading state, rejected-Promise error state, status/priority/lead/members/teams/dates/labels/dependencies, description, and milestones.

```tsx
<NewProjectDialog
  open={createOpen}
  defaultStatus={createStatus}
  leads={leads}
  members={members}
  teams={teams}
  labels={labels}
  dependencies={projects}
  onClose={() => setCreateOpen(false)}
  onCreate={draft => api.createProject(draft)}
/>
```

## Display State Controller

`useProjectsViewState(projects, initial?)` now bridges `ProjectsPageSurface` and `ProjectsDataView`. It owns the same display document for List/Board layout, property visibility, show-closed filtering, show-empty-groups behavior, group order, sorting, and controlled selection.

```tsx
const projectsView = useProjectsViewState(projects)

<ProjectsPageSurface
  displaySettings={projectsView.state.display}
  onChangeDisplay={projectsView.setDisplay}
  {...surfaceProps}
>
  <ProjectsDataView
    {...projectsView.dataViewProps}
    onPropertyChange={persistProjectProperty}
  />
</ProjectsPageSurface>
```

Changing Display properties now changes actual List columns and Board card atoms through `visibleProperties`; hidden columns collapse to zero-width grid tracks, preserving the measured named-grid positions for enabled columns.

## Drag/Drop Evidence

Drag/drop was not implemented because the current real Board card supplies no verifiable DnD contract. The inspected `Cruise` card is a focusable `<a tabindex="0">`; it and its eight closest ancestors expose none of `draggable`, `aria-grabbed`, `aria-roledescription="drag…"`, `data-rfd-drag-handle-draggable-id`, or other drag-handle attributes. The only `draggable="true"` elements in the page were sidebar links and the saved project view pill. Adding HTML5 card dragging here would invent behavior and event semantics not supported by the captured Flow page.

## Integration Composition

`projects-page.tsx` is the single entry point root should mount. It consumes the repository's current shared `Project`, `User`, `Team`, and `IssueLabel` types, then maps them into the measured Projects view model without requiring App to understand the UI's internal health/priority/status representations.

```tsx
<ProjectsPage
  projects={data.projects}
  users={data.users}
  teams={data.teams}
  labels={data.labels}
  error={projectsError}
  loading={projectsLoading}
  onRetry={load}
  onOpenProject={openProject}
  onCreateProject={createProject}
  onUpdateProject={updateProject}
  onDeleteProject={deleteProject}
/>
```

Callbacks:

```ts
type ProjectCreateInput = {
  name: string
  summary?: string
  description?: string
  icon?: string
  color?: string
  statusId?: string
  priority?: number
  health?: Project['health']
  leadId?: string
  memberIds?: string[]
  teamIds?: string[]
  startDate?: string
  targetDate?: string
}

onCreateProject(input): Promise<Project>
onUpdateProject(projectId, partialInput): Promise<Project>
onDeleteProject(projectId): Promise<void>
```

The composition injects real user IDs into Lead menus and real project status IDs into Status mutations. Visual values are mapped as follows:

```text
health:   onTrack <-> on-track, atRisk <-> at-risk,
          offTrack <-> off-track, noUpdate <-> no-update
priority: 0 none, 1 urgent, 2 high, 3 medium, 4 low
progress: domain 0..1 -> UI 0..100
```

Integration update (`2026-08-13`): `ProjectsPage` is mounted in the shared app shell. Go now exposes `POST /api/projects`, `PATCH /api/projects/{id}`, and `DELETE /api/projects/{id}` over the existing Project model, with SQLite persistence and `project.created`, `project.updated`, and `project.deleted` domain events. A browser create/delete probe and the Go lifecycle test both restored their temporary data. Draft fields `labelIds`, `dependencyIds`, and `milestones` still have no matching shared model fields and remain deliberately omitted rather than pretending they persist.

### Integrated QA Checklist

- `Surface/Header`: exact 44px rows, 28px controls, active view pill.
- `Filter`: auto-focus, typeahead, ArrowUp/Down, Enter, Escape, outside click.
- `Display`: List/Board selection updates the mounted data unit; property chips control real row/card atoms.
- `List`: sort, group collapse, row Enter open, Space select, checkbox, property menus, right-click context menu.
- `Board`: 354px lanes, 328px cards, horizontal overflow on narrow viewports, card Enter open.
- `New Project`: 920px desktop modal, full-screen narrow modal, name auto-focus, Tab trap, Escape, validation toast, async loading/error.
- `States`: loading skeleton, empty create action, recoverable error/retry.
- `Unsupported`: no card DnD; no persistence for project labels/dependencies/milestones until shared domain evidence exists.

## 2026-08-14 Full Interaction Audit

This section supersedes older completion notes above. The page was re-audited against the signed-in Flow workspace and exercised through the local React + Go application rather than judged from screenshots alone.

### Completed in this pass

| Module | Behavior now verified |
| --- | --- |
| Add project view (`+`) | Routes to workspace/team `projects/view/new`, creates the active `New view` pill, and renders the inline editor instead of doing nothing. |
| Inline view editor | Matches Flow's 134px editor band, supports icon/name/description/Cancel/Save, and includes the second Filter/Display control row. Both control rows use the same state and dismissible surfaces. |
| Saved project view lifecycle | Create, route, reload restoration, edit metadata, save current display/filter snapshot, and delete are backed by `/api/views`. Project views use `resource: projects` and no longer leak into Issue views. |
| Project filters | Status, Priority, Lead, Members, Health, Dates, Milestones, and Specific project have real second-level value menus, applied condition rows, `is`/`is not`, multi-value editing, clear/remove, immediate evaluation, and saved-view persistence. |
| Project status dictionary | Go exposes all five canonical Project statuses independently of current project occupancy. Empty Planned/Completed/Canceled options render and status changes persist instead of depending on another project already using the target status. |
| Project selection | Row checkboxes now open the measured bottom bulk toolbar. Status, Priority, Lead, Target date, copy names, delete, and clear operate on the selected projects. |
| Issue-count navigation | List and Board issue counts route to the selected project's `/issues` tab. |
| Team scope | Team Projects only projects projects whose `teamIds` contain the route team and creates team-scoped saved project views. |

Browser QA created and reloaded both an unfiltered and a filtered saved view. The filtered view restored `Priority is High` from Go persistence and projected exactly the two matching projects.

### Remaining Projects directory work

| Priority | Module | Missing behavior |
| --- | --- | --- |
| P0 | Board movement | Cards do not yet support Flow's pointer drag/reorder contract or status mutation when moved between columns. |
| P0 | Project row context actions | Status, Priority, Lead, Target date, Copy, and Delete work. Members, Labels, More properties, Move, Favorite, Subscribe, and New comment still lack domain-backed actions. |
| P0 | Board column menu | The column `...` control is still inert; create works but column-level actions do not. |
| P0 | Project detail additions | Add initiative, Add resource, Add customer, Add milestone, and detail-level Add new view are still inert. Their entities do not exist in the current Go model. |
| P1 | Timeline | Layout and controls render, but bar positions are fabricated from name/index rather than `startDate` and `targetDate`; resize/drag date editing is absent. |
| P1 | Full filter catalog | Labels, Creator, Initiatives, Relations, Customers, Template, and Title & summary cannot be truthfully evaluated because those fields are absent from `Project`. AI filter remains an external integration. |
| P1 | Board card secondary controls | Milestone buttons do not open a milestone. Board card selection and column bulk actions are absent. |
| P1 | Project icon in directory | Detail has an icon picker, but the directory row icon is still display-only rather than the same interactive picker used by Flow. |
| P1 | Target-date command | Directory date editing uses real persisted dates, but lacks Flow's full date-command search, natural-language parsing, and calendar surface. |
| P1 | Insights | Health and Lead facets work. Initiatives is an empty projection because Initiative is not in the domain model. |
| P2 | Saved-view filter breadth | Supported filters persist. Unsupported filter fields cannot be snapshotted until their project data exists. |
| P2 | Visual QA | Board cards, insights width, context submenu collision placement, and Timeline still need per-viewport pixel measurement after the missing behaviors are implemented. |

### Project detail audit

Project Overview, Activity, and Issues routes exist. Name, summary, description, icon/color, status, priority, lead, dates, update creation/deletion, issue opening, and issue creation are connected. Subscribe and Favorite are currently local UI state only. Initiatives, Resources, Customers, Milestones, documents, dependencies, labels, member editing, comments, and saved detail views require additional domain entities or endpoints and must not be marked complete until they survive reload.
