# Tooltip Review Refresh

`review/ci-guard` preserves published `b2bbc5ad` ancestry and merges main through
`f8c4491e`. The remaining product change is the previously published `13944e00`
correction: ordinary tooltips do not intercept pointers, tooltip-only wrappers
do not block page shortcuts, and the details button has an explicit marker for
its selected fill. Rich hover cards retain pointer interaction.

Product files are `web/src/App.tsx`, `issue-explorer-surface.tsx`,
`issue-explorer.module.css`, and `ui/tooltip.css`. The surface unit test and
`desktop-issue-filter-states.spec.ts` cover shortcuts with a visible tooltip.
No user-facing copy is added. There is no Go/API/schema/SDK diff against main.

The older review grid rule from `f20e578c` is removed. The phone stylesheet and
all phone timing/platform corrections now match main exactly; merging this
branch preserves main's deliberate `e7750182` geometry. The merges retain both
changelog histories and the published branch ancestry, allowing a normal push.

Final validation uses the heavy lock, Node 24.7.0, bundled Chromium, a freshly
built API at `/tmp/codex-ci-guard/flow-server-41958540300-review`, an isolated
scratch database and private ports 24773/24780/24790. No real model or user's
browser is used. All checks below exit 0:

| Command | Result |
| --- | --- |
| API: `GOMAXPROCS=2 go build -p 1 -o /tmp/codex-ci-guard/flow-server-41958540300-review ./cmd/server` | Fresh merged API |
| `npm run typecheck` | Explicit review-tree typecheck |
| `npm run lint` | Full lint and every repository gate |
| `npx vitest run --maxWorkers=2` for issue-explorer-surface, tooltip, title-tooltip-bridge, my-issues-summary-filter, inbox-header, inbox-filter-builder and search-team-filter | Seven files, 46 tests |
| `npx playwright test --config=playwright.ci-guard.tmp.config.ts --retries=0 --reporter=list e2e/desktop-issue-filter-states.spec.ts e2e/desktop-saved-view-details.spec.ts e2e/keyboard-shortcuts.spec.ts e2e/desktop-toolbar-pills.spec.ts e2e/desktop-tooltips.spec.ts e2e/mobile-issue-detail.spec.ts` | Six complete files, 38 cases, zero retries, 6.3 minutes |
| `git diff --check origin/main` | No review whitespace errors |

Logs end in `-41958540300-review.log` under `/tmp/codex-ci-guard/`. The browser
harness stopped normally and all six task ports, including the earlier capture
harness's 24873/24880/24890, were clear. Earlier broad validation had 59 passes
and one context-filter reset race; the separate main correction `f8c4491e`
fixes that race and the entry-animation races. Its checks and diagnostics are
recorded in `ci-guard-41958540300.md`.

The browser checks cover resting, hover, focus-visible, active/open, disabled
and selected states at 1440x900, 375x667 and 390x844; light and dark; English and
Chinese; short and long labels; and two CJK characters in chips and avatars.
All 12 filter combinations pass their functional and containment assertions.

## Screenshot Inspection

All 67 fresh screenshots were opened individually. The 65 usable crops are in
`/tmp/codex-ci-guard/shots/tooltip-review-41958540300/`. Two phone numeric-chip
focus crops and their metadata were moved to the sibling
`tooltip-review-41958540300-diagnostic/` directory: their lower browser focus
outline is clipped. Those states are visual failures, not passing evidence.
The numeric-chip focus styling is unchanged by this review. This remains a
visible limitation for the reviewer, independently of the passing DOM checks.

The three captured prefixes are `1440-dark-en-US`, `375-dark-zh-CN` and
`390-light-zh-CN`; the functional run checks every combination, while these
prefixes provide representative crops. Each `{prefix}-{state}.png` shows:

| State suffix | What the crop shows |
| --- | --- |
| `resting`, `hover`, `focus-visible` | Add Filter trigger in the three interaction states |
| `open-root`, `open-status`, `nested-submenu` | Open root, status and date menus; constrained scrollable menus retain their partial-row affordance |
| `disabled` | AI filter row while its scratch request is held |
| `cjk-avatar`, `selected-avatar` | Two-character avatar and its selected row |
| `selected-chip-0`, `selected-chip-1`, `selected-chip-2` | Short, two-character CJK and long selected label chips |
| `selected-long-option` | Checked long-label option with text truncation |
| `number-empty`, `number-submenu` | Empty numeric search and extreme-value comparison options |
| `selected-number`, `selected-number-open` | Extreme numeric value in its chip and open selection menu |
| `long-label-tooltip`, `value-hover` | Wrapped long label and complete numeric tooltip text |
| `value-focus` | Desktop numeric focus ring; the two phone versions are the diagnostic failures described above |

`toolbar-{rest,hover,focus,open,selected}.png` shows toolbar pill states.
`desktop-saved-view.png` shows the selected details button and its visible rail.
`shortcut-help.png` shows the filtered shortcut-help dialog. These seven crops
were also opened. Earlier screenshots remain historical evidence under their
original task directories and are not substituted for this refresh.

## Reviewer Clicks

1. At `/{workspace}/team/{teamKey}/all`, hover Add Filter until its tooltip is
   visible, then press F. Open a Labels filter, select a long label, hover its
   value chip and remove it. Repeat in both themes/languages at the three widths.
2. On the same route, open Add Filter, hover Status and its nested menus, then
   open Display options. Hover/focus the toolbar icons while menus are open;
   their open fills must remain stable while ordinary hints remain visible.
3. At `/{workspace}/my-issues/assigned`, press ?, dismiss with Escape and press ]
   twice while a tooltip remains visible. Details opens and closes. At
   `/{workspace}/view/{viewId}`, toggle the details button and hover/focus it to
   inspect its selected fill.
4. For the known diagnostic state, apply Customer count with an extreme value
   on the team issue route at 375/390px, then keyboard-focus its value chip.
   Inspect the lower outline using the two diagnostic crops.

Run `41958540300` completed red; main correction run `41966748316` was queued at
the latest poll. The separate palette-opening flake was reproduced by holding
the lazy Agent module: its initial composer focus could arrive after the test's
header click. Main commit `63651212` waits for that intended focus before
clearing it; three controlled cases and three full-spec runs pass, alongside
42 related unit tests, explicit typecheck and full lint (all exit 0). See the
[focus diagnosis on main](https://github.com/leozhengliu-pixel/flow/blob/main/docs/verification/ci-guard-command-focus.md).
This review delivery does not claim green main. Product UI changes must remain
on `review/ci-guard` until the lead reviews and merges them.
