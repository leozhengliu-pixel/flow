# Linear Add insight production bundle audit

Inspected 2026-08-30 from the current `static.linear.app/client/assets`
production build. Raw production bundles were inspected outside the repository;
only the findings are retained here.

## Linear implementation

Relevant route chunks and shared modules:

- `DashboardPage.B146zZFy.js`
- `DashboardWidgetPage.CdKZB3o0.js`
- `InsightsFullscreen.C56cNhw5.js`
- `WidgetActions.FdHAFmP9.js`
- `useWidgetInsight.Ce4rGHdf.js`
- `RegisterFilterValues.MOycM2yX.js`
- `Select.CT-hcwKI.js`

Linear's **Add insight** action does not open a modal. Both the dashboard empty
state and the dashboard toolbar navigate to
`/:orgKey/dashboard/:dashboardId/widget/new`. Editing an existing widget uses
`/:orgKey/dashboard/:dashboardId/widget/:widgetId`.

`DashboardWidgetPage` creates or clones a generic insight widget and mounts the
standard fullscreen Insights editor. The editor provides:

- Dashboard and widget breadcrumbs.
- Editable insight name and description.
- The shared measure, aggregation, slice, segment, display and filter controls.
- Live graph/table/number preview and drill-down behavior.
- Universal filters plus issue filters.
- Settings and filters serialized into URL search parameters.
- Unsaved-change protection for a new insight.
- `Add to dashboard`, `Save`, and `Reset` states.
- The same themed menu/select primitives used elsewhere in Linear.

Dashboard layout is a row/column model with drag-and-drop placement. Widget size
is derived from that layout; it is not a `Half`/`Full` field in an Add insight
modal.

## Flow findings

| Severity | Finding | Evidence in Flow |
| --- | --- | --- |
| Resolved | Add insight was an invented modal instead of the routed fullscreen editor. | Flow now uses `/dashboard/:dashboardId/widget/new` and `/widget/:widgetId`. |
| Resolved | The widget model was seven hard-coded result types rather than a generic insight definition. | `insight` widgets persist typed measure, aggregation, slice, segment, interval, display and filters. |
| Resolved | Slice, segment, aggregation, date aggregation and reusable filters were missing. | The fullscreen editor exposes and persists each control. |
| Resolved | The modal had no live preview, URL state, deep link, reset state or unsaved-change guard. | Preview API, URL serialization, reload restoration, Reset and navigation guards are implemented. |
| Resolved | `Half`/`Full` was an invented creation-time size control. | Size was removed from creation; cards support persisted drag ordering and post-create width changes. |
| Resolved | Issue count incorrectly disabled chart and table. Linear can display issue counts as graph/table/number when dimensions are configured. | The artificial disabled condition was removed. |
| Resolved | Filters were fixed checkbox boxes rather than the shared filter builder. | The editor reuses searchable `PropertyMenu` selectors, including label groups. |
| Resolved | Insight description was missing from the editor. | Name and description are editable and persisted. |
| Resolved | Several labels were literal English strings and bypassed I18n. | Display buttons and filter group names now use I18n. |
| Resolved | Native `<select>` controls delegated the opened menu to the browser, so Portal theme, keyboard focus, item layout, animations and z-index could not match Linear. | Measure, Location and Team fields were migrated in this audit. |

## Immediate Flow correction

All native `<select>` elements were removed from the dashboard implementation.
Measure, dashboard location and team now use one reusable Radix menu-backed
`DashboardSelect` with:

- Portal rendering and Flow theme tokens.
- `menuitemradio` semantics and selected checkmark.
- Arrow-key navigation, Escape close and trigger focus restoration.
- Trigger-width matching, collision padding, scrolling and z-index.
- Option descriptions for insight measures.
- Open/close icon state and measured 120 ms popover animation.

## Completed parity work

- Added routed new/edit widget pages and dashboard/widget breadcrumbs.
- Added the generic insight configuration model and server-side validation.
- Added a debounced server preview endpoint with a single-pass aggregation path.
- Added issue count, estimate, cycle time, lead time and SLA breach measures.
- Added count, sum, average, minimum and maximum aggregations.
- Added status, team, assignee, label, project, cycle, priority and date slices.
- Added status, team, assignee, project and priority segments.
- Added day, week, month, quarter and year date buckets.
- Added graph, table and number previews using the shared Insights graph layer.
- Added drill-down from widgets and previews into the issue explorer with the
  dashboard widget's team, status, assignee and label filters preserved.
- Added URL state, reload restoration, dirty-state reset and navigation guards.
- Added persisted widget ordering and width changes outside the creation flow.
