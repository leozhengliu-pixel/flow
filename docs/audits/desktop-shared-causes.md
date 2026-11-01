# Desktop Shared Causes

## Scope And Results

Shared rules only; no fixture-specific widths, row positions or screen overrides were added. The widely used phone `--issue-muted` token was not changed. Sidebar labels and More now reuse `--sidebar-nav-fg`; the phone drawer's existing higher-specificity rules retain its original foreground and zero padding.

| Cause | Before | Verified After |
|---|---|---|
| Section-label foreground | 144 observations; RGB 149/149/151 instead of 145/146/148 | 0; existing sidebar foreground token |
| More foreground and padding | 72 colour + 75 padding observations; RGB 153/153/157 and 8px right | 0; RGB 145/146/148 and 9px right |
| Top-bar container padding/gap | 54 observations | 34 remain; Agent, Loops and coding-session hosts now have zero padding and 12px gap, with insets owned by inner rows or title/action groups |
| Fixed-size icon-button padding | 164 observations of zero instead of 2px horizontal padding | 0 across the full sweep plus seven targeted follow-ups; dock, Help, explorer, list-group, cycle, loop and settings controls |
| Foreground/content controls | White loop titles, muted Agent switcher, filled settings Help, 3px label sort gaps, 12px/500 Docs links | Desktop titles use the existing text token; open switcher uses its open state; Help is transparent with a transparent border; sort gaps are 2px; Docs links are 13px/450 |

The initial shell sample measured 95 differences. Section labels reduced it to 68, then More to 48. Fixed-size padding changes preserve the controls' measured bounds. Browser tests assert the Agent title's 10px inset, Loops title's 18px inset, and the collapsed sidebar trigger's original 8px title separation.

Raw final sweep: `/tmp/codex-desktop-causes/final-all.{out,json}`. It measured 3,774 matches and 496 differences, with 1 PASS, 21 FAIL, 105 INCONCLUSIVE and 1 SKIPPED. Seven later screen reruns verify the final ten fixed-size padding corrections, giving 486 differences when those reports replace their corresponding full-sweep reports. This is not another full sweep or a claim of complete parity.

The starting sweep had 2,547 matches and 806 differences. A companion task repaired matching/readiness during this session, so the total reduction must not be attributed entirely to these CSS edits. The named shared observations above are the useful comparisons.

## Phone Gates

The following are verbatim final mobile-gate lines. Each checked both phone widths. Logs are under `/tmp/codex-desktop-causes/`.

```text
mobile-gate shell / drawer: 58 element checks, 0 failures
mobile-gate shell / drawer-bottom: 26 element checks, 0 failures
mobile-gate shell / help-sheet: 34 element checks, 0 failures
mobile-gate issues-list / my-assigned: 46 element checks, 0 failures
mobile-gate issues-list / team-list: 66 element checks, 0 failures
mobile-gate issues-list / team-display-options: 44 element checks, 0 failures
mobile-gate issues-list / display-options: 38 element checks, 0 failures
mobile-gate issues-list / views: 32 element checks, 0 failures
mobile-gate agent / agent-home: 44 element checks, 0 failures
mobile-gate projects / cycles-list: 42 element checks, 0 failures
```

All completed text gates for those shell, issue-list and Agent-home screens report zero differences. The chat-switcher text gate also reports zero differences. Unavailable text is not a parity pass.

Broader diagnostic checks exposed these unchanged baseline problems:

```text
mobile-gate issues-list / team-board: 40 element checks, 2 failures
  FAIL 375 team-board.column-menu x: want 224 got 218.00
  FAIL 390 team-board.column-menu x: want 224 got 218.00
mobile-gate agent / chat-switcher: 26 element checks, 26 failures
```

The chat-switcher fixture has aged: its Yesterday group is missing and later date groups move. The entire 26-line failure output is byte-identical on unchanged `origin/main`. The board's two failure lines are also byte-identical on unchanged main. Baseline source was extracted inside this worktree, using this worktree's own installed dependencies, not another worktree's dependencies.

Conversation and chat-options mobile/text gates time out waiting for `main details > summary`. Unchanged main reproduces that missing conversation step. These states are not passes and no fixture dates, steps or expected values were rewritten. The separate full-Agent-area diagnostic likewise stops on unavailable conversation steps.

## Tests

All heavy commands ran through `~/.flow-local/heavy.sh` with the requested Node path. The browser used bundled Chromium and the shared auth-disabled API on port 18200, never the user's browser.

- `npm run lint`: exit 0, including `npm run typecheck` and every architecture/design/motion check.
- `npx vitest run --maxWorkers=2` with the sidebar, Agent page, loop list/editor, coding-session page, issue-explorer page/surface, issue-list, cycle menu, ContentView, label settings and personal settings test paths: post-rebase exit 0, 12 files / 152 tests. An earlier run also discovered the temporary baseline's duplicate files; that run is not the final test count.
- `npx playwright test e2e/desktop-shared-causes.spec.ts --config playwright.desktop-causes.local.ts`: exit 0, 3 tests. The temporary config uses port 5488, bundled Chromium, and no unrelated servers. Each test creates its workspace with `workspace-fixture`.
- `desktop-gate.mjs all --base http://127.0.0.1:5488 --web <worktree>/web --compact --output /tmp/codex-desktop-causes/final-all.out --json /tmp/codex-desktop-causes/final-all.json`: exit 1 for remaining differences/coverage limits, not a runner crash.
- Targeted desktop reruns: issues-list `group-collapsed`, `my-assigned`, `new-view`, `team-active`; projects `cycles`; settings `issue-labels`, `project-labels`. Exit 1 for their other remaining differences, with zero remaining 2px icon-padding differences.
- After rebasing the concurrent desktop changes, `desktop-gate.mjs shell --screen sidebar` reports 24 matches and zero differences, still INCONCLUSIVE for body coverage; exit 1. The post-rebase lint/typecheck and all three browser tests also pass.
- `mobile-gate.mjs` and `text-gate.mjs` were run individually with `--screen`, `--base http://127.0.0.1:5488` and this worktree's `--web`; the passing mobile lines above exit 0. Diagnostic limits are listed separately.

## Browser Review

All four final screenshots were opened and inspected under `/tmp/codex-desktop-causes/shots/`:

- `shared-agent.png`: desktop Agent home, full sidebar, title inset, composer and dock, without overlap.
- `shared-loops.png`: desktop loop hub, title/actions, toolbar and template grid, without overlap.
- `shared-phone-drawer.png`: fully settled phone drawer, including section labels, More and Help. An earlier animation-in-progress capture was rejected and replaced only after asserting the drawer reached x=0.
- `shared-settings.png`: desktop issue-label table, sort controls, label foregrounds and transparent Help.

The earlier gate proof images in `/tmp/codex-desktop-gate-2/shots/` were also opened. They show the original five measured defects, Agent home, a tutorial list rather than the reference board, and the light member directory. They are baseline difference evidence, not passing parity screenshots.

Reviewer routes, using any workspace key:

1. `/<workspace>/agent`: inspect Workspace/Your teams and More, the bottom-right history button, then open and close the New chat switcher.
2. `/<workspace>/loops`: inspect the title and toolbar, hover the sidebar's right resize edge and click the Collapse sidebar control, then inspect the navigation trigger's title separation.
3. `/<workspace>/settings/api`: inspect Docs text and the bottom-left Help control. Go to `/<workspace>/settings/issue-labels` and `project-labels` to inspect sorting gaps, label foregrounds and filter/display button insets.
4. Open an issue list with a nonempty group and inspect its add-issue button. Open a team's Cycles page and inspect its favorite button.
5. At 390px or 375px, open Agent, then the navigation drawer; inspect More, section labels and Help.

## Remaining Limits

This is a partial shared-cause closeout, not a clean desktop gate. The remaining 34 top-bar padding/gap observations include ContentView, document-list, review and cycle hosts, plus newly measurable issue-header wrappers. Those hosts have different slot/direct-child contracts; they were not flattened or given blanket margins to make a wrapper metric pass.

Remaining leaf typography, backgrounds and borders are spread across controls with different semantics; no global text/surface token was changed to repair one screen at the expense of phone gates. There are still 18 top-bar colour observations in the full report. Ten original label-sort 3px-versus-2px observations are now absent.

Horizontal content differences remain, including fixture-name-dependent positions, different board/list states, and template description Range widths versus stretched reference boxes. No text width, per-row height or data-dependent position was fitted to a fixture. The raw reports retain all failures, withheld observations and inconclusive states for the next pass.
