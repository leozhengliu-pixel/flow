# Team Issues / Workspace Issues modules

Status: first list-view milestone implemented. The Team Issues measurements and interaction inventory come from the signed-in Flow workspace at `/cleantrack/team/CLE/all`. Workspace Issues uses the same explorer with workspace scope because the current local domain supports multiple teams even though the reference workspace currently exposes one team.

## 1. Shared Issue Explorer shell

- Header: scope breadcrumb, `Issues`, favorite switch, notification action.
- View toolbar: Active, Backlog, All issues, Add new view, filter, display options, details.
- Canonical routes:
  - `/{workspace}/team/{teamKey}/{active|backlog|all}`
  - `/{workspace}/issues/{active|backlog|all}`
- Team and Workspace pages share `IssueExplorerPage`; only the issue scope, breadcrumb, and route builder differ.
- Opening an Issue records the source route so closing full-screen details returns to the originating explorer view.

## 2. Scope and view projection

- Team scope includes only Issues whose `team.id` matches the route team.
- Workspace scope includes Issues from every workspace team.
- Active includes `started` and `unstarted`; Backlog includes `backlog`; All includes every non-archived workflow state.
- The default status order follows the observed Flow list: In Progress, Todo, Backlog, completed, canceled.

## 3. Grouped issue list

- Uses the same 44px Issue row implementation as My Issues.
- Group headers support collapse/expand, live counts, status glyphs, and create-in-group.
- Rows expose priority, identifier, status, title, labels, project, assignee, due date, created/updated date according to Display options.
- Clicking a row opens the canonical Issue route; checkboxes support shift-range selection.
- Right-click exposes the shared Issue context menu.

## 4. Inline properties and persistence

- Status, priority, assignee, labels, project, and due date use shared searchable property commands.
- Changes are optimistically projected. A status change immediately moves the row between groups; request failure rolls the row back and exposes Retry.
- Mutations use the existing Go API and SQLite workspace aggregate, so refresh preserves changes.

## 5. Filter, display, details, and bulk actions

- Implemented filters: Status, Assignee, Priority, Labels, Project, including multi-value conditions and `is` / `is not`.
- Filter and display preferences persist per workspace, scope, and view.
- Display options support grouping, group order, ordering, completed visibility, sub-issue visibility/nesting, and property visibility.
- Open details shows the shared resizable issue summary pane.
- Selection opens the shared bulk command bar for status, priority, assignee, project, labels, due date, subscribers, and copy commands.

## 6. Create in group

- The group `+` opens the full shared Create Issue dialog.
- Status is inherited from the selected workflow group when no saved draft overrides it.
- Creation, attachments, draft confirmation, keyboard handling, and persistence remain owned by the shared Create Issue module.

## 7. State boundaries

- Loading, empty, error, mutation error, and retry states reuse the My Issues state system.
- Unknown workspace/team routes still resolve through the application route boundary.

## Next complex modules

1. Board layout, including column/sub-group projection and full card property parity.
2. Drag-and-drop ordering and cross-status moves with persisted ordering.
3. Add new view and saved view CRUD rather than a placeholder action.
4. Completed/archived pagination and server-backed view preferences.
5. Full Issue preview content inside the details pane rather than summary-only details.
