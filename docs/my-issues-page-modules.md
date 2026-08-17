# My Issues page module map

Reference: signed-in Flow `cleantrack/my-issues/assigned`, captured at 1470 x 754 and 768 x 754 on 2026-08-13. Measurements come from live DOM rectangles and computed styles. No browser storage or authentication material was read.

## Module sequence

1. Page surface and title header
2. View tabs and toolbar controls
3. Filter builder and nested condition pickers
4. Display options and view persistence
5. Group header - implemented
6. Issue row and property cells - implemented, including direct property editing
7. Row hover actions and context menu - implemented, including nested property menus
8. Selection and bulk action controls
9. Empty, skeleton, loading, and error states
10. Responsive and detail-pane coexistence

Each module must be measured, implemented as an independent component, compared at the same viewport, and verified for pointer, keyboard, focus, disabled, open, error, and persistence states where applicable.

## Module 1: surface, header, and toolbar

Status: implemented as an isolated integration component in `web/src/components/my-issues/`.

### Desktop measurements (1470 x 754)

| Element | x | y | width | height |
| --- | ---: | ---: | ---: | ---: |
| Main surface | 244 | 8 | 1218 | 710 |
| Title header | 244.5 | 8.5 | 1217 | 43.5 |
| `My issues` heading | 262.5 | 22.5 | 62.64 | 15.5 |
| Assigned tab | 252.5 | 60.25 | 74.53 | 28 |
| Created tab | 335.03 | 60.25 | 66.63 | 28 |
| Subscribed tab | 409.66 | 60.25 | 86.43 | 28 |
| Activity tab | 504.09 | 60.25 | 64.59 | 28 |
| Add filter | 1357.5 | 60.25 | 28 | 28 |
| Display options | 1391.5 | 60.25 | 28 | 28 |
| Open details | 1425.5 | 60.25 | 28 | 28 |

Surface computed values: `lch(5.52 0.4 272)` background, `.5px lch(13.08 1.48 272)` border, `12px` radius, `0 .5px 1px 1px lch(0 0 0 / .3)` shadow. Heading is 13px/500. Tabs and toolbar controls are 28px high with pill radius. Active tab background is `lch(16.706 0.979 272)`; inactive text is `lch(61.803 1.2 272)`.

### Narrow measurements (768 x 754)

| Element | x | y | width | height |
| --- | ---: | ---: | ---: | ---: |
| Main surface | 0 | 0 | 768 | 718 |
| `My issues` heading | 50 | 14 | 62.64 | 15.5 |
| Assigned tab | 8 | 51.75 | 74.53 | 28 |
| Add filter | 664 | 51.75 | 28 | 28 |
| Display options | 698 | 51.75 | 28 | 28 |
| Open details | 732 | 51.75 | 28 | 28 |

At 800px and below Flow removes surface border/radius/shadow, moves the sidebar off canvas, retains all four view tabs in a horizontally constrained row, and retains all three toolbar controls.

### Filter menu

- Opens from the `Add filter` 28px button and autofocuses a searchbox.
- Live menu rectangle: 207 x 622 at desktop, 12px radius, 0.5px border.
- Search field is 36px high inside a 44px header.
- First-level content, in exact observed grouping: AI filter; Advanced filter; Status, Assignee, Agent, Agent Session, Creator, Priority, Labels, Relations, Suggested label, Dates; Project, Project properties, Initiative, Customers; Subscribers, Auto-closed, Content, Links, Template.
- `ArrowUp`/`ArrowDown` moves command selection, typing filters results, `Enter` selects, `Escape` closes and restores trigger focus.

### Display menu

- Live rectangle: 301 x 531 at desktop; right edge aligns with the Display button.
- Contains List/disabled Board tabs, Grouping, Sub-grouping, Ordering, completed-recency toggle, Completed issues, Show sub-issues, Nested sub-issues, and 14 property toggles.
- Exact observed property labels: ID, Status, Assignee, Priority, Project, Due date, Milestone, Labels, Links, Customers, Customer revenue, Time in status, Created, Updated.
- Current standalone contract emits complete immutable `MyIssuesDisplayOptions`; persistence remains an integration responsibility.

### Interaction matrix

| Control | Pointer | Keyboard/focus | State output |
| --- | --- | --- | --- |
| View tab | click changes selected view | native Tab/Enter/Space, visible focus ring | `onViewChange(view)` |
| Add filter | click toggles menu | autofocus search, arrows/Enter/Escape | `onFilterSelect(key)` |
| Display options | click toggles menu | focus trap and Escape restoration from Radix | `onDisplayOptionsChange(options)` |
| Open details | click toggles pressed state | native Tab/Enter/Space, `aria-pressed` | `onDetailsOpenChange(open)` |

### Integration contract

Replace the current `PageShell` composition for `page === 'my-issues'` with `MyIssuesSurface`. The parent owns URL/navigation, applied filters, display-option persistence, and detail pane rendering; page rows are passed as children. This module intentionally does not import shared API/types and does not modify `App.tsx` or global tokens.

## Remaining modules

## Module 2: group header and issue row

Status: implemented as `MyIssuesList` and independent row/state components.

### Measured structure

At 1470 x 754 the list spans the full 1217px content width. Each group header is 36px high with 8px horizontal page margin and 8px radius. Its measured rectangle for `Other active` is x=252.5, y=96, width=1201, height=36. The create control is 24 x 24 at x=1421.5, y=102. Group title is 13px/500; count is 12px.

Each issue row is 44px high and 1217px wide. Flow's observed grid at this viewport is:

```text
[indent] 8px, gap 8px
[checkbox] 18px, gap 8px
[priority] 16px, gap 8px
[identifier] 50px, gap 8px
[status] 16px, gap 8px
[title + property badges] minmax(0, 1fr), gap 8px
[createdAt] 60px, gap 8px
[end padding] 18px
```

For CLE-33 the measured cells were: checkbox visual 14 x 14 at x=263.5/y=149; priority 16 x 16 at x=286.5/y=148; identifier x=310.5/width=50; status 14 x 14 at x=369.5/y=149; title/property area x=392.5/width=980; assignee 18 x 18; created date width=60.

Title uses 13px/500 bright text. Identifier and created date use muted 12-13px text. Label/project badges are 24px high, pill radius, 0.5px border, 9px color dot and 12px/450 text. Assignee is 18 x 18 with 9px initials.

### Selection and hover

- Checkbox hit cell is 18 x 22; visible control is 14 x 14 with 3px radius.
- Unselected checkbox is visually hidden until row hover/focus. A selected checkbox remains visible.
- Selected Flow color measured as `lch(47.918 59.303 288.421)` with `lch(52.418 61.103 288.421)` border.
- Shift-click is emitted independently as the `range` argument for parent-managed range selection.
- Clicking checkbox or status stops row open. Row itself supports click and focused Enter/Space open.
- Live Flow did not expose a separate hover ellipsis in this view; checkbox reveal is its observed hover action. Context actions are available by right click.

### Row property interaction matrix

Verified against signed-in Flow on 2026-08-13. Every interactive cell stops row navigation, opens a search-first command surface, supports Arrow navigation/Enter/Escape, and writes through the shared Issue update contract.

| Property | Visible condition | Direct pointer action | Popup behavior | Persistence field |
| --- | --- | --- | --- | --- |
| Selection | Always; checkbox fades in on hover/focus unless selected | Toggle selection; Shift-click selects range | None | Client selection set |
| Priority | Display property enabled | Click 16px priority icon | Single-select, `Change priority to...`, values 0-4 | `priority` |
| ID | Display property enabled | Row navigation only | None | Read-only `identifier` |
| Status | Display property enabled | Click 14px status icon | Dedicated 207px six-state picker, `Change status...`, current check, `S` hint and direct `1`-`6` shortcuts | `stateId` |
| Title | Always | Opens shared Issue detail | None | Edited in Issue detail |
| Labels | Enabled and non-empty | Click any rendered label badge | Multi-select, selected labels first, menu remains open for repeated toggles | Complete `labelIds` set |
| Project | Enabled and non-empty | Click project badge | Single-select including `No project` | `projectId`, empty clears |
| Due date | Enabled and non-empty | Click date badge | Single-select including `No due date`, Today, Tomorrow, and In one week | `dueDate`, empty clears |
| Assignee | Enabled and non-empty | Click 18px avatar | Single-select including `No assignee` | `assigneeId`, empty clears |
| Created | Display property enabled | Tooltip/accessibility label exposes full timestamp | None | Read-only `createdAt` |
| Updated | Display property enabled | Tooltip/accessibility label exposes full timestamp | None | Read-only `updatedAt` |

Empty optional properties do not reserve blank cells in Flow. They remain editable from the row context menu, which exposes the same Status, Priority, Assignee, Due date, Labels, and Project option sets as the direct row triggers.

The direct Status picker is intentionally separate from the generic row property picker. Its persisted order is Backlog, Todo, In Progress, Done, Canceled, Duplicate. It autofocuses search; pointer hover and ArrowUp/ArrowDown share one active row; Enter selects; Escape closes and restores trigger focus; `1` through `6` select the corresponding unfiltered status directly. Canceled and Duplicate share Flow's canceled workflow type but render distinct X and double-slash glyphs.

All property dropdowns share `usePropertyCommand` for query filtering, active-option state, ArrowUp/ArrowDown/Home/End navigation, Enter, Escape, IME protection, numeric shortcuts, selected IDs, and single-versus-multiple close behavior. The Issue row, create-Issue composer, Issue detail properties, label/project pickers, and new-project draft pickers only own their trigger/content rendering and measured surface preset. Date pickers, context menus, and the global command menu remain separate interaction models.

Milestone, Links, Customers, Customer revenue, and Time in status are visible in Flow's Display properties catalog but do not exist in the current Go/React issue domain. Their controls are explicitly disabled until those server fields exist; no placeholder data is fabricated.

### Context menu

The live row menu is backed by a command surface rather than the previous four-item generic menu; its search input is focusable but visually positioned off canvas in this interaction. Measured container is 192px wide, up to 617px high, 12px radius, with 32px options and 12px separators. The standalone implementation uses Radix ContextMenu for the visible first level and matches the captured order:

```text
Status, Priority, Assignee, Due date, Labels, Project, More properties
Create related, Mark as
Copy, Convert to, Move, Open in
Run loop on {identifier}
Favorite, Remind me
Delete
```

Radix ContextMenu supplies pointer opening, Arrow navigation, Enter selection, Escape close, collision positioning, and trigger focus restoration. Status, Priority, Assignee, Due date, Labels, and Project now open real nested menus. Labels uses checkbox items and remains open while toggling multiple values; all other properties close after a single selection.

### Mutation guarantees

- The row updates optimistically before the network round trip.
- Mutations for the same Issue are serialized so rapid label/property edits reach SQLite in user order.
- A monotonically increasing sequence prevents an older response from replacing a newer visible value.
- Failed writes restore the complete prior row, show a compact Retry action, and retain the failed update payload.
- Successful responses are reprojected from the canonical `Issue` returned by Go, keeping My Issues, the detail pane, and full-screen Issue detail on one shared data source.

### Narrow behavior

At 768 x 754 row width is 767.89px and remains 44px high. The measured first cells are x=16.11 checkbox, x=42.11 priority, x=66.11 identifier, x=124.11 status, and x=148.11 title/properties. Created date is removed. Title remains flexible; badges and the 18px assignee stay pinned to the right. At 520px and below the standalone component removes badges and slightly tightens gaps so identifier/title remain readable.

### State surfaces

- `MyIssuesListSkeleton`: stable 36px group placeholder and 44px rows, `aria-busy`.
- `MyIssuesListEmpty`: centered status with the exact short title `No issues`.
- `MyIssuesListError`: alert state with optional retry callback.

### Integration contract

Map shared issues to `MyIssuesRowData` and groups to `MyIssuesGroupData`. The parent owns selected/collapsed sets and all mutations. `onContextAction(issue, action)` is intentionally typed so status/priority/assignee/date/labels/project pickers can be connected without importing shared state into this module.

## Remaining modules

## Module 3: details pane, bulk actions, and state coordination

Status: implemented as controlled `MyIssuesDetailsPane` and `MyIssuesBulkActionBar` components.

### Details behavior confirmed from live Flow

The toolbar Details control does not select an issue. Its default state opens a view-summary sidebar with three tabs: Labels, Priority, Projects. Clicking an issue row navigates to the shared full issue detail route rather than replacing this summary with a second independent issue editor. The standalone component therefore models both states without duplicating the issue engine:

- no `selectedIssue`: measured summary sidebar;
- `selectedIssue`: compact property preview plus optional `previewContent` slot for the shared Issue detail component.

At 1470 x 754 the live aside is x=1111.5, y=96, width=350, height=621.5. The 7px resize hit target is x=1108.5. The tablist is x=1128, y=106.5, width=313, height=32; its tabs are 28px high. At 768 x 754 the aside remains 350px wide at x=418 and covers the right portion of the list. This is an overlaying split pane, not a full-screen modal. The implementation matches that behavior and adds a visible close control on narrow screens.

Resize is pointer-captured, clamped to 280-620px, and emits `onWidthChange`; persistence remains parent-owned. Loading, retryable error, empty summary, summary-item selection, close, and selected preview all have explicit states.

### Bulk action bar measurements

With one selected issue at desktop, the live bar rectangle is x=726.11, y=657.5, width=253.77, height=44. It has 8px padding/gap and pill radius. The count region is 73.4px wide; Actions is 84.38 x 28; Ask Flow and Clear are each 28 x 28. The bar is centered within the main content area rather than the full viewport.

The Actions control opens Flow's large command surface, not a small dropdown. Measured desktop command surface is x=375, y=98, width=720, height=450 with 12px radius. Search input is 708 x 40 and visible result rows are 46px high. Implemented command entries match the captured order:

```text
Assign to..., Un-assign from me, Change status..., Change priority...
Add to project..., Change or add labels..., Set due date...
Copy issue ID, Copy issue URL, Copy issue title, Copy title as link
Copy issue description as Markdown, Copy issue content as Markdown
Copy git branch name, Copy as prompt
Change subscribers..., Remove all subscribers, Mark issue as...
```

Cmdk provides input filtering, ArrowUp/ArrowDown, loop navigation, Enter selection, and a no-results state. Radix Dialog provides Escape close and focus restoration. All actions emit the complete selected issue array to a typed parent callback.

### Cross-module state contract

The list, details preview, and bulk bar do not maintain separate issue selections. The integration parent owns one `selectedIds` set and derives:

```text
selectedIssues = visibleIssues.filter(issue => selectedIds.has(issue.id))
previewIssue = previewIssueId ? visibleIssues.find(issue => issue.id === previewIssueId) : undefined
```

When loading replaces rows, the parent should retain IDs only if optimistic selection is intentional; otherwise clear them. When filtering removes selected rows, derive the bar from visible issues so stale hidden selections do not remain actionable. Bulk loading disables all three controls and changes the count label to `Updating...`; a bulk error remains attached to the bar. Details loading/error are independent so the issue list remains usable.

`useMyIssuesSelection(groups)` implements this contract locally: one selected-ID set, flattened visible ordering for Shift ranges, automatic pruning when filters remove rows, derived `selectedIssues`, and a preview ID that is cleared if its row disappears. Pass `selectIssue` to `MyIssuesList`, `selectedIssues` to the bulk bar, and `previewIssue` to the details pane.

## Remaining modules

## Module 4: page controller, applied filters, persistence, and visual fixture

Status: standalone page-state contract and reproducible visual fixture implemented.

### View tabs and URL contract

Live Flow renders actual links:

```text
/cleantrack/my-issues/assigned
/cleantrack/my-issues/created
/cleantrack/my-issues/subscribed
/cleantrack/my-issues/activity
```

The active link uses `data-active=true` and `data-disabled=true`; it does not render a visible issue count. `MyIssuesSurface` now uses anchors with the same active attributes and accepts `viewHref(view)`. Optional counts are included only in the accessible label because adding visible count pills would not match the observed UI. `myIssuesViewHref(workspaceSlug, view)` produces the route and `useMyIssuesController.changeView` invokes the injected navigation adapter.

### Applied filter band

Applying Priority = High on live Flow adds a separate band below the 44px toolbar. Measured rectangle: x=252.5, y=96, width=1201, height=45; 10px padding; `lch(9.345 0.85 272)` background; 0.5px bottom/bounding border. The combined `Priority / is / High` block is 170.875 x 24 with 8px radius and a `lch(27.12 1.48 272)` border. Clear and Save controls appear at the right.

`MyIssuesFilterBar` matches this band and supports multiple scrollable filters, independent field/operator/value buttons, removal, add, clear, and save states (`idle`, `saving`, `saved`, `error`). Narrow layouts hide the field text first, then the command group, while preserving filter value and removal.

The local controller currently evaluates exact values for priority, status, assignee, labels, and project. Other captured Flow first-level filter types remain UI contracts until their domain fields are introduced; they are not fabricated in the page DTO.

### Display and filter persistence

`MyIssuesControllerAdapter` makes persistence explicit:

```ts
persistDisplay(view, options): Promise<void>
persistFilters?(view, filters): Promise<void>
```

Display changes write immediately and use a monotonically increasing request ID so a slower prior response cannot overwrite the visible state of a newer save. The controller exposes `displaySaveState`. Filter changes are staged, expose `filterSaveState`, and are saved by an explicit `saveFilters()` matching Flow's visible Save control.

### Summary derivation

Details summary data is derived from currently visible groups, not from an unrelated static list. Labels, priority, and projects are grouped by stable IDs, counted, and sorted by count descending then label. This keeps filter results, rows, and the 350px summary pane coherent.

### Bulk execution and rollback

The controller takes one snapshot before a bulk transaction. If `optimisticBulk` is supplied, replacements render immediately. A successful API result replaces returned issues and clears selection. Failure restores the complete snapshot, keeps selected IDs available for retry, and exposes the actual error message on the bulk bar. Concurrent execution is disabled through `bulkLoading`.

### Reproducible visual fixture

The fixture is a real nested Vite HTML entry and does not modify `App.tsx`:

```text
http://127.0.0.1:5173/src/components/my-issues/fixture.html
http://127.0.0.1:5173/src/components/my-issues/fixture.html?details=1&filters=1
```

Start the existing web server with `npm run dev`, then capture the second URL at 1470 x 754 and 768 x 754. It uses the production components, deterministic issue fixtures, and supports row selection, bulk command opening, details, resize, filters, and tab changes.

Browser verification on 2026-08-13 found no console errors. Desktop horizontal assertions: main x=244/w=1218, filter band x=244.5/w=1217/h=45, details x=1111.5/w=350, and toolbar buttons 28 x 28 at x=1357.5/1391.5/1425.5. At 768 x 754: main 768 x 718, filter band 768 x 45, row 768 x 44, created date `display:none`, and details x=418/w=350. Selecting a row produced a 44px bulk bar; Actions opened 18 command items with the command input focused.

### Verification note

The My Issues directory passes `oxlint` with no warnings and `git diff --check`. The full workspace build was temporarily blocked by concurrent, unrelated TypeScript errors in `components/projects-page/new-project-dialog.tsx` lines 184 and 186. Earlier complete builds passed before those shared files changed; the visual fixture also compiled through Vite and loaded without browser warnings/errors.

## Remaining modules

## Module 5: shared Issue integration adapter

Status: implemented as `MyIssuesPage` in the dedicated module directory. The adapter accepts the shared `BootstrapData`/`Issue` model at its boundary, maps rows locally, and keeps every low-level visual component independent of the shared API.

The production composition now owns these concrete behaviors:

- Assigned, Created, Subscribed, and Activity project from the shared Issue/activity data rather than fixture counts.
- Started workflow states merge into the observed `Other active` group; all other groups keep their real workflow state IDs.
- Row click resolves the shared `Issue` and calls `onOpenIssue(issue)`, so My Issues does not create a second Issue editor.
- Status icon opens a 207px searchable, keyboard-driven anchored picker and emits the real workflow state ID.
- Filter first-level fields remain visually complete, but only Status, Assignee, Priority, Labels, and Project are enabled because those are the fields present in the current Issue contract. Selecting one drills into real workspace values before adding a condition, so an empty filter can never hide every row accidentally.
- Applied-filter `+` reopens the same filter command surface. Summary Labels/Priority/Projects rows add the corresponding real filter.
- Bulk Status, Priority, Assignee, Project, Labels, Due date, and Subscribers drill into a second searchable command list before mutation. Copy actions use the selected shared issues. Labels/Subscribers update each Issue against its own existing ID set rather than overwriting all selected Issues with one guessed set.
- Bulk failures retain selection and use the controller snapshot rollback contract. Successful mutations replace returned shared Issues after mapping them back to row DTOs.

### Mount contract

```tsx
<MyIssuesPage
  data={data}
  onOpenIssue={openIssue}
  onUpdateIssue={(id, input) => updateIssue(id, input)}
  onUpdateIssues={(ids, input) => batchUpdateIssues(ids, input)}
  onDeleteIssues={ids => Promise.all(ids.map(deleteIssue)).then(() => undefined)}
/>
```

Optional callbacks cover create, view URL navigation, display/filter persistence, retryable page error state, and the shared narrow Sidebar. The root application now mounts this component in place of the legacy My Issues `PageShell`; row navigation opens the same full Issue engine, while single and batch mutations use the Go/SQLite Issue APIs.

### Verification

`npm run lint -- src/components/my-issues`, `npx tsc --noEmit -p tsconfig.app.json`, and scoped `git diff --check` pass on 2026-08-13.

The following are deliberately not claimed as complete: domain fields absent from the current DTO (Agent, Agent Session, suggested labels, customers, initiatives), persisted server-side view preferences, and permanent CI image-diff thresholds. Unsupported first-level filters remain visible to match the captured Flow menu but disabled rather than being wired to fabricated values.

## Module 6: top-right view controls

The three 28 x 28 toolbar controls are now separate behavior modules instead of local variants inside the surface component.

- `my-issues-filter-menu.tsx`: the 207px root command menu, grouped Flow field tree, adjacent value menu, search, keyboard navigation, and multi-select that stays open. Status, Assignee, Priority, Labels, and Project use real bootstrap values and issue counts. The applied-filter band can edit `is` / `is not` and reopen a multi-value picker.
- `my-issues-display-menu.tsx`: the measured 301 x 531 panel with List/Board segmented control, Grouping, group ordering, Sub-grouping, Ordering, Completed issues, sub-issue switches, and all 14 display-property chips. Select controls are real listboxes with arrow-key, Enter, Escape, and focus restoration behavior.
- `my-issues-details-pane.tsx`: the 350px summary pane with Labels/Priority/Projects tab keyboard navigation, summary-to-filter actions, 280-620px pointer and keyboard resizing, and narrow-screen overlay behavior. The toolbar exposes the observed Open/Close details labels and `Command-I` shortcut.

Display and filter preferences are serialized per workspace and My Issues route. Grouping, group order, ordering, completed-window filtering, property visibility, Show sub-issues, and Nested sub-issues all affect the rendered list. `Current cycle` is persisted but intentionally does not filter yet because the current Issue domain model has no cycle boundary; no fake cycle data is introduced.

Chrome regression on 2026-08-13 covered Filter -> Status -> Todo/In Progress multi-select, filter clear, Priority grouping, display menu geometry, Details summary, and `Command-I`. `npm run lint`, `npm run build`, `go test ./...`, and `git diff --check` pass; Vite retains its existing chunk-size warning.
