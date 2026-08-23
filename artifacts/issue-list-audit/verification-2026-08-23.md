# Issue list Linear/Flow verification - 2026-08-23

## Scope exercised in signed-in Chrome

| Surface | Linear route | Flow route | Result |
| --- | --- | --- | --- |
| My Issues | `/leozhengliu/my-issues/assigned` | `/cleantrack/my-issues/assigned` | Shared list verified; missing Insights fixed |
| Saved issue view | `/leozhengliu/view/all-issues-4084658ef443` | temporary `/cleantrack/view/view_1787456277318997000` | List/board, view menu, Filter, Display, Insights, Details exercised |
| Release issues | `/leozhengliu/pipeline/test/release/test3-bbd6eacb1010/issues` | `/cleantrack/pipeline/flow-qa-releases/release/qa-1-7e5bf757a578/issues` | Hand-written list removed; shared list and menus verified |

## Measured comparison

| Element | Linear | Flow | Difference |
| --- | --- | --- | --- |
| Release issue row bounding box | `x=244.5`, `width=1217`, `height=44` | `x=244.5`, `width=1217`, `height=44` | `0px` size/x |
| Release issue row radius | `8px` | `8px` | `0px` |
| Release toolbar icon buttons | `28x28` | `28x28` | `0px` |
| Release options outer menu | `230x305`, `12px`, `0.5px`, `z=600` | `230px` target, `12px`, `0.5px`, `z=600` | rechecked after off-screen filter fix |
| Issue list row implementation | Linear grid/subgrid | shared `MyIssuesList` grid | Flow uses explicit tracks; outer size matches |

Raw computed-style snapshots are in `dom/release-linear.json` and
`dom/release-flow.json`. Ordered Release menu snapshots are in `menus/`.

## Ordered menus and behavior

Release options was measured from live DOM and aligned to:

1. `Edit...`
2. `Stage` submenu
3. separator
4. `Add issues to release...` (`Option R`)
5. `Add document...`
6. `Add link...` (`Ctrl L`)
7. separator
8. `Favorite` (`Option F`)
9. `Copy URL` (`Command Shift ,`)
10. separator
11. `Delete`

The menu keyboard filter exists as an off-screen searchbox, matching Linear's
measured off-screen input rather than adding visible height.

Saved-view column menu was aligned to exactly:

1. `Select all in column`
2. `Hide column`

Both actions now have effects. Hidden columns persist in display configuration
and appear in a recoverable `Hidden columns` area.

## Sub-issue behavior

- `showSubIssues` controls whether matching child issues remain in the result.
- `nestedSubIssues` controls indentation only.
- Child rows expose their parent trail.
- Parent rows expose completed/total progress and a hover/focus child Portal.
- Linear My Issues live data confirmed parent `LEO-14` at `0/3` while matching
  children remained separate rows.
- Linear Release live data confirmed parent `LEO-6` at `4/5`.

## Evidence

- `screenshots/linear-my-issues.png`, `flow-my-issues.png`
- `screenshots/linear-release-issues.png`, `flow-release-issues.png`
- `screenshots/linear-saved-view-board.png`, `flow-saved-view-board.png`
- `screenshots/overlay-*.png`, `screenshots/diff-*.png`
- `dom/release-linear.json`, `dom/release-flow.json`
- `menus/release-options-linear.json`, `menus/release-options-flow.json`

## Remaining gate

The Linear and Flow accounts do not contain the same issues, labels, projects,
language, or counts. Whole-page overlays therefore are evidence artifacts, but
their pixel difference is not a valid parity score. A defensible `<=1%` page
gate remains blocked until both applications are supplied with an equivalent
fixture and fixed locale/theme/viewport. No `<=1%` completion claim is made.

## Automated checks

- `npm run build`: passed
- `npm run lint`: passed with the pre-existing unused variable warning at
  `src/App.tsx:2007`
- `npm run check:pickers`: passed
- `git diff --check`: passed after whitespace cleanup

## Test data cleanup

The temporary Flow saved view `Issue list audit` was created only to exercise
saved-view list/board menus. Its delete action was invoked and the browser
confirmation was accepted; the browser session timed out while waiting for the
post-delete navigation, so cleanup should be confirmed on the next bootstrap.
