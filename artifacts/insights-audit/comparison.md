# Insights comparison

## Measured geometry

| Element | Linear | Flow | Result |
|---|---:|---:|---|
| Panel outer | 346 x 552.5 at 1115.5, 96 | 346 wide at 1115.5, 96.5 | Width/x pass; y differs 0.5px |
| Header | 345 x 42 | 345 x 42 | Pass |
| Header icon buttons | 24 x 24 | 24 x 24 | Pass |
| Header button x positions | 1361 / 1391 / 1421 | 1361 / 1391 / 1421 | Pass |
| Select buttons | 93.664 x 30 | 93.664 x 30 | Pass |
| Select x positions | 1132 / 1241.664 / 1351.328 | 1132 / 1241.664 / 1351.328 | Pass |
| Select font | 500 13px / 19.5px | 500 13px / 19.5px | Pass |
| Fullscreen surface | x 244.5, y 8.5, width 1217 | x 244, y 8, width 1218 | 0.5-1px difference |

## Interaction structure

- Measure order and descriptions match the measured Linear menu.
- Slice order matches Status through Burn-up, including Label group and Project label group submenus.
- Segment order matches No Value through Added to cycle.
- Display uses the measured toggle plus Colors combobox structure.
- Actions match Copy link, Export insights as CSV, Insights examples, separator, Refresh.
- Fullscreen is a two-column workspace; chart/table selection drives the issue list.

## Evidence

- `dom-measurements.json`
- `menu-snapshots.json`
- `state-transitions.json`
- `model-api-mapping.json`
- `exported-insights.csv`
- `screenshots/linear-*.png`, `screenshots/flow-*.png`
- `screenshots/panel-overlay.png`, `screenshots/panel-diff.png`

The screenshot data sets differ between Linear and Flow, so the full-panel pixel diff is evidence of layout comparison, not a same-data percentage gate.
