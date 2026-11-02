# Desktop Projects Round 3

Product commits: `c4efe0f2` and `29fe7c0b`. Both were rebased and pushed to main. Baseline `5064f332` freshly reproduced the previous report's 31 differences. Final verification includes the concurrent phone-triage test commit `5c081c67`.

| Screen | Result | Before | After |
| --- | --- | ---: | ---: |
| List | PASS | 5 | 0 |
| Milestone menu | FAIL | 12 | 9 |
| Overview | FAIL | 5 | 3 |
| Project actions | FAIL | 5 | 3 |
| Project overview | PASS | 3 | 0 |
| Cycles | FAIL: coverage | 1 | 0 |
| Display menu | FAIL | 0 | 5 |
| Initiative detail | PASS | 0 | 0 |
| Initiatives | PASS | 0 | 0 |
| Project activity | PASS | 0 | 0 |
| Project issues | PASS | 0 | 0 |
| Timeline | PASS | 0 | 0 |

Seven measured screens pass; four fail measurements. Cycles remains INCONCLUSIVE in the raw gate, with one body match against the unchanged five-match floor, and is counted as a failure to obtain PASS above. The paid-plan screen remains excluded. Total differences: **31 -> 20**. Sixteen original observations were fixed; five new observations expose conflicting Lead widths between captures. PASS describes measured gate coverage.

## Shared Rules

- `projects-bundle-parity.css`: shared desktop Lead track is 120px, measured directly in List. No screen-specific width or fitted displacement.
- `project-detail-desktop.css`: team icon gap is 8px; natural section heading height is 27.5px; the description-to-milestones space is 16px. The target-date button already measures the reference's 106.62px: its following team control caused the 2px displacement. No placeholder spacer was added.
- `desktop-projects-parity.spec.ts`: updated the shared column expectation and verifies a populated description's block height, following section spacing, and team gap. It creates its own workspace.
- `CHANGELOG.md`: entries under Unreleased.

The rich-text body's 226px height, heading spacing, list spacing and trailing paragraph already match. No shared editor rule changed. The old report's Expand difference concerns the milestone row, not editor folding. All product CSS changes are inside the existing desktop media query. No new UI copy, API shape or Go changes.

## Fixture And Gate Rechecks

All 14 original fixture limitations were rechecked. Four anonymized description-width failures are fixed through PATCH in the owned project workspaces. Neutral final word `layout` measures 383.56px against the captured 383.70px, preserving font and spacing. The captured summary is also persisted through the API. No reference JSON or PNG changed.

The Cycles gap observation was a gate error: its reference outer header has one in-flow child. Its declared 12px gap creates no inter-child spacing. The scratch matcher and browser collector now compare the used zero gap and retain the declaration as evidence. A two-child 2px gap mutation still fails. The body coverage floor is unchanged.

The neutral description is an explicit semantic alias in the scratch gate; the reference bounds and style assertions are preserved. No thresholds, tolerances, properties or coverage requirements were removed. Twenty-six matcher regressions and all twelve live main-frame mutation proofs pass.

Gate tools and fixture scripts are isolated at `/tmp/codex-desktop-r3-projects/`; the supplied shared scratch tools were not overwritten. [The difference inventory](desktop-r3-projects-differences.json) records hashes, every original observation's disposition, current failures, routes and API checks.

## Remaining Limits

- **Milestone menu, nine observations:** rows and glyph boxes are uniformly scaled in the capture: 31.36/32 and 15.19/15.5. The persisted milestone and all six actions reproduce. API data cannot set a transient client capture scale. Production typography was not scaled or offset to fit it. A settled reference capture is needed.
- **Overview and Project actions, three each:** their JSON omits the 32px name and 23px summary editor boxes, while their PNG shows both titles. This places both following sections 55px higher in JSON. The same captures omit the main milestone title width, placing Expand 41.86px left of the populated row. Empty project and milestone creation return HTTP 400. The stable Project overview capture passes the shared layout. No required title UI was removed to reproduce incomplete capture geometry.
- **Display menu, five new observations:** its Lead track is 48px, whereas List's is 120px. The shared 120px rule follows the requested List reference and moves Health/Priority left by 72px in both states. No data or existing display setting explains the two immutable widths. A consistent reference is needed to pass both without a per-screen override.
- **Cycles:** API fixture is the captured empty directory. The body contains only its empty-state sentence, so it cannot meet five body matches. Adding cycles changes the reference state; inventing wrapper matches would inflate coverage. Its measured differences are now zero, but it is not reported as PASS.

Of the former 17 product-decision observations, 11 are fixed and six are the incomplete Overview/Project actions captures above. Of the 14 fixture observations, four are fixed through API data, one is the gate correction and nine remain capture-scale limits. **No owner product decision is requested.**

## Verification

Every heavy command used `~/.flow-local/heavy.sh`, with Node 24.19.0. Successful checks:

- `npm run typecheck`: exit 0 before both product pushes.
- Full `npm run lint`: exit 0 before both product pushes, including the repository design/control/motion checks.
- `npx --no-install vitest run --maxWorkers=2 <component test paths>`: exit 0; Lead/grid/display/state files: 4 files, 36 tests; additional customer/timeline consumers: 2 files, 3 tests; project overview/detail/sidebar/activity/issues/menu and embedding consumers: 13 files, 114 tests. Exact argv and logs are in `checks.mjs`, `lead-unit*.log` and `metadata-final-unit.log` under the task directory.
- `node --test /tmp/codex-desktop-r3-projects/gate/desktop-match.test.mjs`: exit 0, 26 tests, including used-gap and deliberate geometry mutations.
- `node /tmp/codex-desktop-r3-projects/browser-session.mjs`: exit 0, all 3 tests in `e2e/desktop-projects-parity.spec.ts`. Bundled Chromium, own auth-disabled API on 18294 with `/tmp` SQLite, own Vite on 5594. Eleven final test screenshots opened individually.
- `node /tmp/codex-desktop-r3-projects/browser-session.mjs --scroll`: exit 0; scrolled to Milestones, verified the 16px gap, expanded persisted notes and restored collapse. Its additional screenshot was opened.
- `node /tmp/codex-desktop-r3-projects/session.mjs phone --phone`: exit 0. Both mobile and text gates pass Projects list, Overview, Properties and Milestones, on own Vite port 5595. No whole phone sweep was run.
- Area gate after rebase, with `--no-shots --mutation`: exit 1 for the reported measurements/coverage, not a runner crash; 7 PASS, 4 FAIL, 1 INCONCLUSIVE, 1 excluded. All 12 live mutation proofs pass.
- Scratch API build: `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-desktop-r3-projects/api ./cmd/server`: exit 0. No Go source changed, so package tests/vet were not required.

An early run used a stale gate snapshot and was discarded after comparison with the supplied tools. A phone runner port collision was corrected by assigning port 5595. Neither failed attempt counts as verification. Runners stop their own API/Vite/browser PIDs. Temporary web dependencies and browser config are removed before handoff.

## Browser Review

- `/<workspace>/projects/all`: keep Lead, Health and Priority displayed; inspect the 120px Lead track. Open Display options to see the same shared track.
- `/<workspace>/project/<slug>/overview`: use a start date, no target date and a team; inspect the team icon spacing. Add a paragraph, H2 and two list items; scroll to Milestones and inspect the 16px section gap. Open Project actions, close it, then open the sidebar milestone actions and hover Edit.
- `/<workspace>/team/<team>/cycles`: enable cycles with auto-create off and leave it empty; inspect the empty sentence and breadcrumb. This is visually coherent but still lacks gate coverage.

All twelve screenshots are under `/tmp/codex-desktop-r3-projects/shots/` and were opened after their final browser runs: `desktop-project-list.png` shows the shared columns; `desktop-project-overview.png` shows populated rich text and team properties; `desktop-project-actions.png` shows the open project menu; `desktop-milestone-menu.png` shows all six actions and highlighted Edit; `desktop-milestones-expanded.png` shows the scrolled section and persisted milestone notes; `desktop-project-activity.png` shows the comment composer and persisted comment; `desktop-project-issues.png` shows the issue empty state; `desktop-project-timeline.png` shows the full-height grid/today line; `desktop-initiative-list.png` shows the directory row; `desktop-initiative-overview.png` shows the update and docked sidebar; `desktop-initiative-projects.png` shows the Projects heading/actions at the viewport bottom, not offscreen row validation; `desktop-cycles.png` shows the empty cycle directory. None shows an error page, failed action or broken overlap. These images do not waive the remaining gate failures.
