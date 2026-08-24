# Drafts comparison

## Measured geometry

| Element | Linear | Flow | Result |
|---|---:|---:|---|
| Main surface | 1218 x 649 at 244, 8 | 1218 wide at 244, 8 | width/x/y pass |
| Header | 44px | 44px | pass |
| Empty inner container | x 262.5, y 70.5, width 1181, padding 120px 60px, gap 20px | same x/y/width/padding/gap | pass |
| Empty illustration | 121.844 x 120 | 121.844 x 120 | pass |
| Empty text | 500 13px, 15.5px high | 500 13px, 15.5px high | pass |
| Issue/comment card | 383 x 140 at 262.5, 98 | 383 x 140 at 262.5, 98 | pass |
| Card border/radius | .5px / 10px | .5px / 10px | pass |
| Hover discard | 28 x 28 at 605, 110.5 | 28 x 28 at 605, 110.5 | pass |
| Confirmation dialog | 480 x 169 | 480 x 169 | pass |

## Removed incorrect Flow behavior

- Removed the Draft search toolbar from empty and populated states.
- Removed the invented empty-state helper sentence and generic file icon.
- Removed horizontal generic operation rows and three-dot deletion menu.
- Replaced immediate deletion with the measured confirmation dialogs.
- Replaced client delete loops with atomic `DELETE /api/drafts`.

## Evidence

- `linear-empty-dom.json`, `flow-empty-dom.json`
- `linear-list-dom.json`, `flow-list-dom.json`
- `linear-comment-dom.json`
- `model-api-mapping.json`, `state-transitions.json`, `coverage.json`
- `screenshots/linear-*.png`, `screenshots/flow-*.png`
- `screenshots/empty-overlay.png`, `screenshots/empty-diff.png`
- `screenshots/card-overlay.png`, `screenshots/card-diff.png`

The full-page screenshots use different Chrome window heights. Component crops and DOM coordinates are used for the direct comparisons instead of claiming a misleading full-page difference percentage.
