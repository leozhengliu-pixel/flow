# Linear Dashboard documentation reference

Source: <https://linear.app/docs/dashboards>, inspected 2026-08-30.

The authenticated Business workspace cannot open Enterprise Dashboards, so this
implementation uses Linear's three official documentation images for the
Dashboard-only layout while retaining real DOM evidence for Insights.

## Source images

- Dashboard list: <https://webassets.linear.app/images/ornj730p/production/71f0619607a00ecd4486a66340fa0ec12f38fd97-2016x1036.png>
- Dashboard detail: <https://webassets.linear.app/images/ornj730p/production/bd1540bfabec98d610d36770d521b116d8538299-2246x2266.png>
- Add an existing insight: <https://webassets.linear.app/images/ornj730p/production/644a864b6ba70c142ae19d2a1713daf78092582a-2258x1512.png>

## Implemented observations

- Dashboards are a third tab beside Issues and Projects in Views.
- The list uses full-width flat rows grouped by workspace/team/personal scope,
  with Name and Owner columns and a plus action in each group header.
- The detail header contains a Dashboards breadcrumb, colored dashboard glyph,
  dashboard name, context menu, favorite, refreshed time and link action.
- The content begins with a large glyph/title and editable description, followed
  by dashboard filters and the Add insight command.
- Insights use a responsive two-column grid. Metric blocks, charts and tables
  share the same bordered 8px-radius surface.
- Dashboard filters are persisted separately from insight filters. Hidden saved
  filters remain active.
- Context menus provide owner transfer, Move to workspace/team/personal, copy,
  CSV export, public-link control, refresh and deletion.

This evidence is documentation-derived rather than authenticated DOM evidence;
that distinction remains explicit in the main parity report.

## Flow verification

- `flow-dashboard-docs-index.png`: workspace/team grouping, Name/Owner columns.
- `flow-dashboard-docs-detail.png`: title, description, filters, metric/chart/table grid.
- `flow-dashboard-docs-mobile.png`: 390 x 844 responsive index.
- `flow-views-dashboard-tab.png`: Dashboard entry integrated into the Views tab group.
- `flow-dashboard-shared-views-header.png`: production-bundle-derived shared Views/Dashboards chrome, search, display controls and zero state.
- `flow-dashboard-nivo-detail.png`: shared Nivo bar/line rendering in Dashboard cards.
- A temporary `Flow dashboard docs QA` dashboard exercised three insight display
  modes and was deleted after capture.
- `linear-graph-library-audit.md` records the authenticated DOM and production
  bundle evidence for Linear's Nivo/D3 graph implementation.
- `linear-dashboard-bundle-audit.md` records the production route, shared Views
  header, grouping, sizing and list-provider implementation.
