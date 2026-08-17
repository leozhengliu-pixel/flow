# Views replication modules

Measured against the signed-in Flow workspace on 2026-08-15.

## Routes

| Surface | Workspace | Team |
| --- | --- | --- |
| Issue view directory | `/:workspace/views/issues` | `/:workspace/team/:teamKey/views/issues` |
| Project view directory | `/:workspace/views/projects` | `/:workspace/team/:teamKey/views/projects` |
| New issue view | `/:workspace/views/issues/new` | `/:workspace/team/:teamKey/views/issues/new` |
| New project view | `/:workspace/views/projects/new` | `/:workspace/team/:teamKey/views/projects/new` |

Saved view rows reuse the existing issue and project saved-view detail routes. The directory does not own a second filtering engine.

## Measured shell

- App sidebar: `244px`
- Main panel margin: `8px 8px 8px 0`
- Main panel radius: `12px`
- Header: `44px`
- Team favorite button starts `8px` after the `Views` title and is a persisted `role="switch"`
- Resource toolbar: `44px`
- Resource pills and icon buttons: `28px`
- Column header: `36px`
- Saved-view row: `60px`
- Workspace group header: `38px`

## Directory modules

1. Header
   - `Views` title
   - Team directory favorite toggle
   - New view button
2. Resource tabs
   - Issues
   - Projects
3. Display menu
   - Ordering: name, owner, created, updated
   - Ascending/descending direction
   - Optional owner, created, and updated columns
4. Scope grouping
   - Personal views
   - Workspace views
   - Team-scoped views
   - Scope-level create action
5. Saved view row
   - Resource icon
   - Name and generated/explicit description
   - Owner menu
   - Optional created/updated dates
   - Keyboard open and right-click menu
6. Row context menu
   - Edit
   - Duplicate
   - Change owner
   - Move to Personal, Workspace, or Team
   - Favorite/unfavorite
   - Subscribe/unsubscribe
   - Copy link
   - Delete
7. Empty state
   - Original Flow empty custom views illustration
   - Resource-aware Issues/Projects copy
   - Save-view icon and `Option V` keycaps
   - Functional create action and custom-views documentation link

## Measured empty state

- Remaining-content container: full area below the `44px` resource toolbar, centered on both axes
- Content column: `340px`
- Illustration wrapper: `340px × 80px`; SVG uses Flow's original `viewBox="15 14 92 112"`
- Illustration-to-copy gap: `24px`
- Empty-state title: `15px / 23px`, weight `600`
- Copy: `13px / 18.2px`, weight `450`
- Copy is rendered as Flow-style block text spans; paragraph elements produce a measurable multi-line rounding drift
- Title-to-copy gap: `8px`; paragraph gap: `16px`
- Copy-to-actions gap: `24px`
- Actions: `28px` height, `8px` gap, pill radius
- Primary button: `116.328px`; documentation button: `108.508px`
- Empty directories do not render the saved-view column header

## Measured display menu

- Popover border box: `301px × 136px`, at `x=1152.5`, `y=92.5` in a `1470px × 698px` viewport
- Direction button: `24px × 24px`, at `x=1347`, `y=113`
- Ordering select: `62px × 24px`, at `x=1375`, `y=113`
- Created property: `62.625px × 24px`, at `x=1169`, `y=192`
- Updated property: `65.969px × 24px`, at `x=1236.625`, `y=192`
- Owner property: `55.055px × 24px`, at `x=1307.594`, `y=192`
- Ordering listbox: `97px × 104.5px`, four `24px` options, exact selected check icon
- Direction, ordering, and property selections persist per scope and resource
- Property and direction changes keep the popover open; an outside click closes it

## New view editor

- New view is a routed editor, not a dialog.
- Workspace creation defaults to `Personal`.
- Team creation defaults to that team.
- Save destination supports Personal, Workspace, and every team.
- Issue views reuse `IssueExplorerPage` and its filter/display state.
- Project views reuse `ProjectsPage` and its filter/display state.
- Save creates a single `SavedView` record and routes to the existing saved-view detail.

## Persisted SavedView shape

```ts
type SavedView = {
  id: string
  name: string
  description: string
  resource: 'issues' | 'projects'
  scope: 'personal' | 'workspace' | 'team'
  teamId?: string
  ownerId?: string
  favorite?: boolean
  subscribed?: boolean
  view: 'active' | 'backlog' | 'all'
  filters: unknown[]
  display: Record<string, unknown>
  createdAt: string
  updatedAt: string
}
```

Directory display preferences are local user preferences. Saved-view content and row actions persist through the Go API and its domain-event store.
