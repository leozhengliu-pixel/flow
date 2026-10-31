# Desktop Shell Polish

Measured at 1440x900, DPR 2, with bundled Playwright Chromium, on
`/verify/team/VER/all`. Vite used port 5446 and the shared auth-disabled API at
`http://127.0.0.1:18200`. No existing workspace data was modified; browser specs
create only `[audit]`-prefixed workspaces.

## Measurements

Reference values come from `/tmp/desktop-ref/shell/topbar-ref.json` and
`create-issue-ref.json`. Before/after values use `getBoundingClientRect()` and
`getComputedStyle()`; complete measurements are saved at
`/tmp/codex-oc-desktop-shell-polish/{before,after}.json`.

| Gap | Reference | Before | After |
| --- | --- | --- | --- |
| Add-view diamond | SVG 14x14 at 471.13,67.25; rendered RGB 86,87,89 | 14x14 at 474.64,67.75; fill `lch(61.803 1.2 272)` | 14x14 at 471.64,67.75; fill `lch(36.975 1.2 272)`, matching reference pixels |
| Active pill | x252.50, width57.16, height28 | x253.00, width58.16, height28 | x253.00, width57.16, height28 |
| Backlog pill | x313.66, width67.04, height28 | x315.16, width68.05, height28 | x314.16, width67.05, height28 |
| All issues pill | x384.70, width75.43, height28 | x387.20, width76.44, height28 | x385.20, width75.44, height28 |
| Footer divider | None | `1px solid lch(14 1.2 272)` in emulated Chromium | `0px none`; footer remains 747x51.5 |
| Team text | `lch(91.201 1.65 272)` | `lch(64.805 1.65 272)` | `lch(91.201 1.65 272)` |
| Expand glyph | 16x16 at 1020.5,137.5; filled two-path glyph | 15x15 at 1021,137; 24-unit stroked glyph | 16x16 at 1020.5,136.5; exact reference paths from `openFullPage` |
| Close glyph | 16x16 at 1058.5,137.5; filled one-path glyph | 15x15 at 1059,137; 24-unit stroked glyph | 16x16 at 1058.5,136.5; exact reference path from `closeChat` |
| Attach glyph | 14x14 at 364.5,345.5; filled one-path glyph | 14x14 at 363.5,345.25; 24-unit stroked glyph | 14x14 at 363.5,344.75; exact reference path from `attach` |
| Action icon ink | Rendered RGB 156,157,160 | Bright strokes, `lch(91.201 1.65 272)` | Muted fill, `lch(64.805 1.65 272)`, matching reference pixels |

The raw rows record `color`, not SVG `fill`. The diamond's raw inherited colour
is `lch(90.451 1.2 272)` and the dialog icons' colour is
`lch(91.201 1.65 272)`, but their screenshot pixels show the muted fills above.
The fixes retain those inherited colours and set the actual glyph fill.

Pill text remains 12px/500 with normal line-height and 4px gaps. The width error
is border rounding, not font metrics: emulated Chromium rounds a 0.5px border
to 1px even at DPR 2. Desktop pills now reserve the reference's 21px total
horizontal inset as 10.5px padding per side, with the existing pseudo-element
hairline inset by 0.5px rather than a layout border. All fixed glyph/pill
rectangles are within 1px of the reference. Phone and tablet CSS is unchanged.

## Crops Opened

All files are under `/tmp/codex-oc-desktop-shell-polish/shots/` and show the
reference above Flow, without errors or failed navigation:

- `topbar-before.png`: wider pills and brighter diamond; shared header controls present.
- `topbar-after.png`: corrected pill widths and dim diamond; Flow's Insights remains.
- `create-before.png`: muted VER text, bright stroked icons and footer divider.
- `create-after.png`: bright VER text, reused muted filled icons and no divider.

Remaining visible differences are outside these five gaps: team name/avatar
and list data differ, Flow retains Insights, and the reference has a display
state dot. The composer retains its existing header/title vertical metrics,
team-pill padding, and property-pill outlines. These were not broadened into
unrelated changes. Existing Drafts, Loops and Agent commands remain untouched.

## Phone Gates

The supplied `mobile-gate.mjs` ran through `~/.flow-local/heavy.sh` against
`http://127.0.0.1:5446` with this worktree's `web/` directory. Verbatim results:

```text
mobile-gate shell / topbar-myissues: 36 element checks, 0 failures
mobile-gate issues-list / my-assigned: 46 element checks, 0 failures
mobile-gate issues-list / team-list: 66 element checks, 0 failures
mobile-gate inbox / inbox-list: 44 element checks, 0 failures
```

An additional `shell --screen composer` attempt exited 1 because the supplied
locator file has no composer entry (0 checks, two missing-locator failures).
No reference files were changed. A focused Playwright regression checks the
existing compact composer text colour and visible original icons at 375, 390
and 1023px, including that the desktop glyphs remain hidden.

## Tests

All heavy commands used `~/.flow-local/heavy.sh`. Final successful runs:

- `npm run lint`: exit 0, including typecheck and all five architecture/token/motion checks.
- `npm run typecheck`: exit 0.
- `npx vitest run --maxWorkers=2` over the create-issue team-state, assignee,
  templates, app wiring, i18n coverage, issue-explorer page, view-creation,
  saved-view-editing, issue-explorer surface and route-preload files:
  exit 0, 10 files / 77 tests.
- `npx playwright test --config /tmp/codex-oc-desktop-shell-polish/playwright.config.cjs`:
  exit 0, three tests in `web/e2e/desktop-shell-polish.spec.ts`, using bundled
  Chromium and the local Vite/shared API. Checks pill geometry, fills, glyph
  dimensions, footer border, team colour, expand/collapse/close, attachment
  chooser, and the compact breakpoint.
- Both prescribed `desktop-compare.cjs` commands: exit 0; all four crops opened.
- `git diff --check`: exit 0.

The first lint/typecheck attempt caught an unsupported testing-library option;
it was removed. The first browser run caught an incorrect test locator and a
measurement taken during the entry animation; the spec now uses the actual
accessible label and polls the settled glyph dimensions.

## Reviewer Steps

1. At a desktop width of at least 1024px, open `/verify/team/VER/all`.
   Inspect Active, Backlog and All issues, then the dim add-view diamond.
2. Press `c`. Inspect the bright VER text, muted expand/close/attach glyphs and
   footer without a divider.
3. Click Expand, Collapse, Attach, then Close to check the unchanged actions.
4. At 375px or 390px, reopen the composer and confirm the original compact
   controls remain; the desktop-only glyphs must not appear.
