# Linear Dashboards, Insights, and Usage UI audit

Audit date: 2026-08-30. Source workspace: `leozhengliu`, authenticated Chrome session, Linear Business trial.

## Verified entry points

| Capability | Linear entry and route | Observed state | Flow mapping |
| --- | --- | --- | --- |
| Insights | Any issue view -> `Open view insights`; `Ctrl/Cmd+Shift+I` is documented | Live right-side panel | `/:workspace/analytics` |
| Insights fullscreen | Panel top-right expand icon | Button measured at 24 x 24 | Full analytics page |
| Insights options | Panel sliders icon | Show archived issues; Hide No Priority | Functional display menu |
| Insights actions | Panel `...` | Copy link; Export insights as CSV; documentation; Refresh | Copy, CSV export, refresh |
| Dashboard index | Workspace/team Views -> Dashboards tab -> New dashboard | Enterprise-only; absent from this Business workspace | `/:workspace/dashboards`, shared Views header and grouped list |
| Usage | Settings -> Administration -> Usage & limits | `/leozhengliu/settings/usage` | `/:workspace/settings/usage` |
| Usage history | Usage -> All sessions | `/leozhengliu/settings/usage/history` | Usage event chart and persisted activity data |
| Spend limits | Usage -> Spend limits | `/leozhengliu/settings/usage/spend-limits` | Persisted workspace spend limit control |
| Paid plan | Settings -> Billing | `/leozhengliu/settings/billing` | Paid subscription summary and plan usage |

## Linear Insights state inventory

- Header: issue count, expand, display options, three-dot menu.
- Controls: Measure, Slice, Segment. Default measured values: Issue count, Status, Priority.
- Chart and table are linked representations of the same result.
- Menu order: Copy link; Export insights as CSV; Insights documentation; separator; Refresh.
- Display options: Show archived issues and Hide No Priority checkboxes.
- The panel is a complementary landmark; opening changes the toolbar button from `Open view insights` to `Close view insights`.
- Icon buttons are 24 x 24 with circular `9999px` radius. Select controls are 30px high with 8px radius.

## Linear Usage state inventory

- Header: Usage & limits and explanatory text.
- AI credits: balance, Add credits, automatic reload, Spend limits link.
- Analytics: Week period selector, previous/next buttons, total spend, chart series, session rows, empty state.
- Current observed empty state: `$0.00 available`, no limits, no usage this week; next-period and auto-reload Manage buttons disabled.
- The Flow deployment has no payment-provider purchase endpoint, so it intentionally does not render a non-functional Add credits button. Existing persisted reload and spend-limit controls are wired.

## Dashboard documentation implementation

Linear Dashboards requires Enterprise. The authenticated workspace is on a Business trial, so the Dashboards tab and dashboard create/detail UI could not be accessed. Direct `/leozhengliu/views/dashboards` returned `View not found`. Dashboard structure and behavior are now implemented from Linear's official list, detail, and Add-to-dashboard images plus the verified Insights controls. See `dashboard-docs-reference.md`. This remains documentation-derived and is not claimed as authenticated Enterprise DOM evidence.

## Evidence files

- `linear-insights-default.png`
- `linear-insights-menu.png`
- `linear-insights-display-options.png`
- `linear-insights-measurements.json`
- `linear-usage.png`
- `linear-usage.dom.txt`
- `linear-usage-measurements.json`
- `parity-map.json`
