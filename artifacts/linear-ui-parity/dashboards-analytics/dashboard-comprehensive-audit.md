# Dashboard comprehensive audit

Audited against the current Linear dashboard route chunks and Flow's frontend,
API handlers, domain model and browser behavior on 2026-08-30.

## Coverage

- Workspace and team directories, grouping, ordering, columns and empty states.
- New dashboard deep links, location selection and persistence.
- Detail breadcrumbs, title/description, owner, location and destructive actions.
- Favorites, subscriptions, sharing, export and refresh.
- Dashboard-level filters and hidden saved-filter access.
- New/edit insight routes, URL state, dirty protection and reset.
- Measures, aggregations, slices, segments, date buckets and filters.
- Live preview, graph/table/metric display and issue drill-down.
- Widget removal, ordering, drag placement and persisted width.
- Permissions, visibility, validation, pagination and stale-request handling.
- Portal semantics, I18n, design tokens, mobile layout and console state.

## Gaps found and corrected

| Area | Defect | Correction |
| --- | --- | --- |
| Directory | Only the first dashboard API page was loaded. | Cursor pagination now loads the full directory. |
| Results | A slow previous request could overwrite the newly selected dashboard. | Result requests now use a monotonic request guard. |
| Create | Linear new-dashboard deep links were missing. | Added workspace and team `/dashboards/new` routes. |
| Favorite | Header star was a no-op. | Connected generic favorite APIs with optimistic rollback. |
| Share | Share copied the private editor URL and never enabled sharing. | It now creates/reuses a share token and copies the public API URL. |
| Filters | The `Saved filters` control was a no-op. | It now reopens the dashboard filter editor. |
| Mutations | Failed patches silently left optimistic local values on screen. | Shared dashboard mutation errors now surface a failure toast. |
| Insight | Modal, native selects and hard-coded widget types diverged from Linear. | Replaced by routed fullscreen generic insight editor and themed menus. |
| Drill-down | Widgets did not open their filtered underlying issues. | Added filtered issue-explorer drill-down. |
| Layout | Creation-time Half/Full was invented and ordering was static. | Width moved to card actions; drag and keyboard-accessible menu ordering persist. |

## Verification

- Browser: workspace create route, detail, new/edit insight routes, Portal menus,
  live preview, URL reload restoration, filters, save, reset, width persistence,
  sharing and filtered issue drill-down.
- DOM: no native `select` in dashboard UI; Add insight is not a dialog.
- Backend: lifecycle, preview, global/widget filters, subscription, sharing and
  export tests.
- Automated: `npm run lint`, `npm run build`, `go test ./...`, `go vet ./...`
  and `git diff --check`.
