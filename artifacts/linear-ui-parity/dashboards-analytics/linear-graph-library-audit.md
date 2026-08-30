# Linear Insights graph implementation audit

Measured 2026-08-30 in the authenticated Linear `My issues -> Assigned -> Open
insights` panel. Asset hashes are deployment-specific and can change.

## Conclusion

Linear uses **Nivo** for its standard Insights distribution charts, backed by
D3 utilities. It does not use Recharts, Chart.js, ECharts, or a canvas-only
chart package for the normal bar/line views.

Observed production assets:

- `vendor-nivo.BDH21C4V.js` (133,586 bytes)
- `useNivoTheme.Nl4335_C.js`
- `vendor-d3-scale-chromatic.kM9Khm2g.js`
- `vendor-d3-shape.DlmbA9kv.js`
- `ChartHover.DgewVNsk.js`
- `GraphHelpers.C8X81mr0.js`
- `ScatterPlot.worker-BRS2xutX.js`

The Insights bundle imports the Nivo vendor module and renders Nivo `Bar` and
`Line` components with explicit width/height, `animate: false`, right-side
numeric axes, hidden left axes, disabled labels, custom layers, and a shared
`useNivoTheme` theme.

## DOM evidence

The measured Issue count / Status graph renders an SVG:

- SVG: 345 x 250 CSS pixels, `role="img"`
- Plot transform: `translate(16,10)`
- Plot width: 301px
- Horizontal grid: 1px lines with `3 3` dash pattern except the baseline
- Five 60.2px dimension groups in the measured state
- Bars, grid, axes, hover groups, and table are distinct DOM layers

There was no canvas inside the distribution chart.

## Latency exception

Latency/scatter Insights use a Linear-specific renderer rather than Nivo's
public ScatterPlot component. The bundle labels its data shape
`nivoScatterPlot`, but renders points onto a canvas, optionally transferred to
an OffscreenCanvas worker. Axis, aggregation, hover, drill-down, and tooltip
layers are custom React/SVG/HTML overlays.

## Flow implementation decision

Flow now uses `@nivo/bar` and `@nivo/line` through one shared
`LinearInsightBar` / `LinearInsightLine` component for both Issues Insights and
Dashboard insight cards. The shared component mirrors Linear's SVG rendering,
right axis, grid, disabled animation, tooltip, palette, and responsive sizing.
Flow does not currently expose a latency metric, so duplicating Linear's custom
worker scatter renderer would create an unused implementation.

Verification artifacts:

- `flow-insights-nivo.png`
- `flow-dashboard-nivo-detail.png`
