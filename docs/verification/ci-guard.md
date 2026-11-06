# CI guard delivery

## Review delivery: run 41839544845

Run [41839544845](https://github.com/leozhengliu-pixel/flow/actions/runs/41839544845) failed at `e6e6d358`, after the previous green main run `41814667117` at `25505250`. The downloaded failed logs contain browser failures exposing three product regressions and two test assumptions. This delivery includes product/UI changes and therefore follows the owner's review-branch requirement: push only `review/ci-guard`, for the lead to review and merge. It is not a direct-main CI-guard push.

| Failure | Cause and correction |
| --- | --- |
| All eight phone filter state cases timed out hovering or removing chips. | The new Add Filter tooltip overlapped the chip below and intercepted input. The ordinary tooltip surface and its positioning wrapper now ignore pointer events; rich customer-summary tooltip cards explicitly retain pointer interaction. Both layers matter: a diagnostic run with only the surface fixed still failed because the wrapper intercepted the pointer. |
| `]` no longer toggled My issues details after closing help; a new focused unit test also reproduced F being blocked with a tooltip visible. | The shortcut-scope checks treated any visible positioning wrapper as a blocking popover. Exclude wrappers whose immediate child is a tooltip, while preserving dialog/menu/popover blocking. Existing keyboard assertions remain unchanged. |
| The selected saved-view details button used the generic fill instead of the measured fill. | `e6e6d358` replaced its native title, but its CSS selected `[title]`. Use an explicit details-trigger attribute, and key menu-open styling to `aria-expanded` so opening a tooltip does not masquerade as opening a menu. Keep the existing exact fill assertion. |
| The new tooltip spec expected Mac modifier caps on Linux. | Exercise both explicit Mac and Linux platform values and require the exact corresponding keycaps. Retain every gap, height and edge assertion; replace the old fixed delay with an actual animation wait. |
| Phone issue-menu geometry intermittently measured about 357.35px instead of 358px. | Five normal local repetitions passed; CPU throttling then reproduced the same partial-scale width on the due-date sheet (357.36px). Wait for actual animations and a paint frame on the menu, due-date sheet and status sheet before exact geometry checks. Three throttled confirmation cases passed. Temporary throttling instrumentation is removed; no dimensions, tolerances, retries or timeouts were relaxed. |

The baseline bundled-browser reproduction exited 1 with three failures (filter pointer interception, saved-view fill and shell shortcut) and two passes. The new F-shortcut unit regression failed before the scope correction, then passed with it. All fixtures create their own workspaces; the browser uses an auth-disabled scratch API, scripted provider and Vite. Unique ports 24773/24780/24790 and `reuseExistingServer:false` avoid other agents' servers. The current API was built under `/tmp/codex-ci-guard/flow-server-41839544845` through the heavy lock (exit 0); no Go source or REST contract changes are included.

Required local checks use Node 24.7.0 and `~/.flow-local/heavy.sh`, from `web/` unless stated:

| Command | Exit | Result |
| --- | --- | --- |
| `npm ci` | 0 | Worktree-local installation; no dependency changes |
| `npm run typecheck` | 0 | Final explicit check |
| `npm run lint` | 0 | Complete lint including every architecture, design-token, picker, foundation, functional-control and motion gate |
| `npx vitest run --maxWorkers=2 src/components/issue-explorer/issue-explorer-surface.test.tsx src/components/ui/tooltip.test.tsx src/components/ui/title-tooltip-bridge.test.tsx src/components/my-issues/my-issues-summary-filter.test.tsx` | 0 | Four files, 27 tests |
| Focused bundled Chromium reproduction after both tooltip-layer fixes | 0 | Five cases: phone filter, saved-view fill, shell shortcuts, Mac caps and Linux caps |
| Phone issue-menu confirmation with 4x CPU throttling, three repeats, zero retries | 0 | Three passes with exact geometry; the pre-fix throttled run exited 1 at the due-date sheet width |
| `npx playwright test --config=playwright.ci-guard.tmp.config.ts --retries=0 --reporter=list` with the five affected specs and temporary review probe | 0 | 61 tests: 37 browser regression cases plus 24 review-state checks; 6.4 minutes, zero retries |
| Repository root: `git diff --check` | 0 | No whitespace errors |

Logs use `/tmp/codex-ci-guard/*-41839544845*.log`. The browser commands set `CI=1`, `FLOW_E2E_API_BIN`, a task-specific scratch database and API log, `--config=playwright.ci-guard.tmp.config.ts --retries=0 --reporter=list`, and output under `/tmp/codex-ci-guard/results-41839544845-*`. The temporary config imports the repository configuration, changes only ports/provider binding and server reuse, and selects bundled Chromium. It and the one-off review probe are removed before delivery. No real-model request or user's Chrome was used.

Review evidence is under `/tmp/codex-ci-guard/shots/tooltip-review/`. The details probe checks resting/unselected, hover/tooltip, keyboard focus-visible, selected/open, deselected, enabled and overflow states in English/Chinese and light/dark at 1440x900, 375x667 and 390x844. The existing filter matrix additionally checks pending-save disabled state, selected short/two-CJK/long labels, a two-CJK avatar, full accessible descriptions and text containment. The details action has no disabled product state; disabled coverage belongs to the filter's held AI request. No new avatar or chip styling is introduced.

The full browser command runs `e2e/desktop-issue-filter-states.spec.ts e2e/desktop-saved-view-details.spec.ts e2e/keyboard-shortcuts.spec.ts e2e/desktop-tooltips.spec.ts e2e/mobile-issue-detail.spec.ts e2e/ci-guard-review.tmp.spec.ts`. Screenshot hooks use `FLOW_FILTER_STATE_SHOTS` and an optional `FLOW_FILTER_STATE_SHOTS_MATCH` to capture selected states without hundreds of duplicate images. All 51 initial crops were opened and inspected; after rebasing those originals are archived under `shots/tooltip-review-9415df60/`.

| Screenshot group | What it shows |
| --- | --- |
| `details-*` (24 crops, mouse and touch contexts) | Selected/focused details action, its custom tooltip and modifier caps, and the long English/Chinese saved-view name at all three widths and both themes. |
| `{1440-dark-en-US,375-dark-zh-CN,390-light-zh-CN}-{resting,hover,focus-visible}` | Nine filter-button crops: resting border/fill, hovered fill and visible keyboard ring. |
| The same prefixes with `{open-root,disabled,selected-long-option}` | Nine menu crops: open root, muted disabled pending AI action, and checked long label alongside A and two-CJK labels. |
| The same prefixes with `{long-label-tooltip,value-hover,value-focus}` | Nine text/value crops: complete long label in a wrapped tooltip, complete large-number tooltip, and focused numeric chip. A narrow chip ellipsizes its painted value while retaining the complete accessible description and tooltip. |

Seven diagnostic screenshots were also opened: three baseline failures (blocked phone chip, wrong saved-view fill, help tooltip while the shortcut fails), three from the interrupted surface-only fix (the wrapper still blocks two phone cases; the last light phone case was interrupted), and the throttled due-date sheet before its animation finished. They remain under the corresponding `results-41839544845-{baseline,fixed,mobile-slow2}` directories and are failure evidence only, not verification. Earlier harness-only failures generated no screenshots. No assertion, retry, timeout or gate was relaxed.

After rebasing onto `a4202413`, the explicit typecheck, complete lint, four related Vitest files (27 tests), and all five affected browser specs plus the temporary review probe (61 tests, 6.8 minutes, zero retries) were rerun and exited 0. The only rebase conflict was the changelog; both entries were retained. Refreshed logs use `-41839544845-rebased.log`. All 51 refreshed crops were opened: 24 `details-{mouse,touch}-*` crops and 27 filter-state crops, with the same states and contents described above. No error, broken step or overflowing text is claimed as verification. Changed files are `web/src/App.tsx`, `web/src/components/ui/tooltip.css`, the issue-explorer surface/CSS/unit test, `web/e2e/{desktop-issue-filter-states,desktop-tooltips,mobile-issue-detail}.spec.ts`, `CHANGELOG.md`, and this report. No Go package or REST shape changed, so Go tests/vet and API/SDK generation are not applicable.

Because menu-open styling also applies to Display, final targeted checks additionally ran `npx vitest run --maxWorkers=2 src/components/my-issues/my-issues-display-menu.test.tsx` (exit 0, 13 tests) and `npx playwright test --config=playwright.ci-guard.tmp.config.ts --retries=0 --reporter=list e2e/desktop-issue-list-menus.spec.ts --grep 'shows every display property'` (exit 0, one browser case). Both used the heavy lock; their logs end in `-41839544845-display.log`. No new screenshots were generated by those checks. The harness stopped normally, and task-owned ports had no listeners afterward. Temporary source/config files and installed dependencies are removed before handoff. Full suites were not run; no main CI success after this UI correction is claimed until the lead reviews and merges the branch.

Reviewer steps:

1. At `/:workspace/team/:teamKey/all` at 375/390px, add a Labels filter with a long name or a Customer count filter with a large number. Let the Add Filter tooltip appear, then hover the selected value and remove the chip. These actions must work without dismissing a tooltip first. Keyboard F must also open filtering while its tooltip is visible.
2. At `/:workspace/my-issues/assigned`, open help with ?, close with Escape, then press ] twice. Details must open and close even if the help tooltip remains visible. Open a real menu and confirm page shortcuts remain scoped to that menu.
3. At `/:workspace/view/:id`, toggle the details button, then hover/focus it. The selected desktop fill must retain the measured dark/light token while the tooltip is open. Repeat in Chinese and on a phone; the tooltip and long view name stay within the viewport.
4. On a team issues page, hover Search, Create new issue, details and Add Filter. Check keycaps and spacing on both Mac and Linux. On `/:workspace/issue/:identifier` on a phone, open Issue options, return from Create related, set Tomorrow, reopen the due-date chip, then open Status; the settled sheet geometry remains exact.

## Continuation: run 41805518838

Run [41805518838](https://github.com/leozhengliu-pixel/flow/actions/runs/41805518838) failed at `983b0b92` in the shared menu state matrix: phone label text intermittently started at x=86 or 85.5 instead of the asserted 86.5. The previous green run, `41796501428`, passed at `c50eba6b`. Commit `e5ed54d5` deliberately made the phone property list viewport-wide inside its bordered sheet to match the measured rows. The newer main commit `bf194bf9` corrects unrelated timeline locators and does not address this failure.

The local reproduction failed twice in three attempts. Instrumentation showed the sheet at scale 1 after hover, with `scrollLeft=0.5`, while the same passing case had `scrollLeft=0`. Hovering before the opening scale animation finished caused browser scrolling to retain that offset. Awaiting the sheet's actual animation before hover, using the existing pattern in `desktop-issue-detail-properties.spec.ts`, passed three consecutive confirmation cases, with `scrollLeft=0` and label x=86.5. The diagnosis captured pre-wait scales of 0.993612 and 0.999247; both finished at 1 before hover.

The correction adds that animation wait and an explanatory comment to `web/e2e/desktop-menu-review-states.spec.ts`. All exact coordinate, size, color, focus, disabled and selected assertions remain unchanged. No retries, arbitrary delay, longer timeout, product styling or UI copy changes are included. Other changed files are `CHANGELOG.md` and this report; this test-only correction follows the task's direct-main delivery instruction.

Validation uses Node 24.7.0 and the machine-wide `~/.flow-local/heavy.sh` lock. An untracked temporary config imports the repository config, changes its three ports to 24773/24780/24790, selects bundled Chromium through `CI=1`, and sets every `reuseExistingServer` to false. Port 4190 was occupied by another agent, whose process was left alone. The scripted provider, auth-disabled scratch API, database and Vite are isolated under `/tmp/codex-ci-guard/`. The current API was built with `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-ci-guard/flow-server-41805518838 ./cmd/server` through the heavy lock (exit 0). No Go source or REST shape changed.

Both diagnostic screenshots were opened and inspected: `/tmp/codex-ci-guard/results-41805518838-diagnosis/desktop-menu-review-states-efe8e-menu-states-375-light-zh-CN-chromium/test-failed-1.png` and its `chromium-repeat1` counterpart. Each shows the light Chinese phone label sheet, issue context chip, short and long labels, and hovered unchecked label. These screenshots accompany a failed coordinate assertion and are failure evidence only. No successful screenshot or full product UI review is claimed for this test-only change.

Final commands, from `web/` unless stated, all through the heavy lock:

| Command | Exit | Result |
| --- | --- | --- |
| `npm ci` | 0 | Worktree-local dependencies, no lockfile change |
| `npm run typecheck` | 0 | Explicit TypeScript check |
| `npm run lint` | 0 | Full lint including all design, picker, foundation, control and motion gates |
| `npx vitest run --maxWorkers=2 src/components/issue/label-project-pickers.test.tsx src/components/ui/phone-menu.test.tsx src/components/property/use-property-command.test.tsx src/components/layout/sidebar-team-menu.test.tsx` | 0 | Four files, 21 tests |
| `npx playwright test --config=playwright.ci-guard.tmp.config.ts --retries=0 --reporter=list --output=/tmp/codex-ci-guard/results-41805518838-fixed e2e/desktop-menu-review-states.spec.ts` | 0 | All 12 cases, 2.2 minutes, zero retries |
| Repository root: `git diff --check` | 0 | No whitespace errors |

The browser matrix covers 1440x900, 375x667 and 390x844, light/dark and English/Chinese, retaining the existing resting, hover, focus-visible, open, disabled, selected, long/short label and CJK skill-name assertions. The final run generated no screenshots. The browser environment also sets `FLOW_E2E_DATABASE_PATH=/tmp/codex-ci-guard/e2e-41805518838-fixed.db`, `FLOW_E2E_API_BIN=/tmp/codex-ci-guard/flow-server-41805518838` and `FLOW_E2E_API_LOG=/tmp/codex-ci-guard/api-41805518838-fixed.log`. Logs are `/tmp/codex-ci-guard/{npm-ci,typecheck,lint,vitest,e2e}-41805518838*.log`. The diagnostic reproduction exited 1 (two failures, one pass); the animation-wait confirmation exited 0 (three passes). No failure is counted as verification. No full suite, Go tests/vet or generation was needed for this test-only change. The temporary config and installed dependencies are removed before handoff.

After rebasing onto `739c37b5`, which includes concurrent agent-skill settings work, the explicit typecheck, full lint, all four related Vitest files (21 tests) and the complete changed browser spec (12 cases, 1.7 minutes, zero retries) were rerun and exited 0. Those logs use the suffix `-41805518838-rebased.log`; the scratch database, API log and browser results likewise use `-rebased` instead of `-fixed`. The harness stopped normally, and all three task-owned scratch ports had no listeners afterward. The latest poll before delivery listed `41807655181` in progress, `41809849449` pending, and `41805518838` failed. No post-push CI success is claimed.

## Continuation: run 41782547112

The handover contained clean commit `84492ea9`, the placeholder deadline correction below, based on main `5771bac1`. It was retained. The previous session had also downloaded failed logs for runs `41780533623` and `41782547112` and started a desktop-boundary reproduction, but had not recorded or committed that follow-up.

Run [41782547112](https://github.com/leozhengliu-pixel/flow/actions/runs/41782547112) has the same whole-source placeholder deadline failure and three browser failures following `e6dee935`, which deliberately moved the desktop layout boundary to above 880px:

- `desktop-shell-polish.spec.ts` still used 1023px as a compact composer. It now uses 880px, retaining the separate 767px phone-density color assertion and both glyph assertions.
- `desktop-shell-remaining.spec.ts` still expected the compact clipboard icon at 1023px. It now checks compact icons at 375/390/880 and desktop icons at 881/1023, retaining padding and desktop background assertions.
- `desktop-breakpoint.spec.ts` placed 40 route/width combinations, five settings/composer/command-menu checks, and compact drawer interactions inside one 120-second case. CI exhausted that deadline during navigation/loading; the inherited local reproduction also reached it partway through the matrix. Each desktop width now has its own fresh-workspace case with the same 120-second deadline, all eight routes and every original assertion. The original resize sequence remains a separate drawer case. No retry, longer timeout, removed route or product workaround was added.

Changed files in this follow-up are the three specs above, `CHANGELOG.md`, and this report. The preceding commit changes `web/src/i18n/placeholder-replace.test.ts` and those two documentation files. No product, Go, REST, schema, translation or UI code changed; this test-only CI correction follows the task's direct-main delivery instruction.

The resumed diagnostic run exited 1: the toast reproduced the stale desktop icon expectation, while the composer stopped at the startup loader during the first cold load. Its failure screenshot shows only that loader, not a verified composer. The inherited boundary screenshot shows a loaded document at the point the aggregate deadline expired; the toast screenshot shows the desktop copy icon at 1023px. All three diagnostic screenshots were opened and are failure evidence only, under `/tmp/codex-ci-guard/results-{stale-resume,boundary-baseline}/`. The inherited trace was incomplete after the outage and is not claimed as verification.

Final validation used Node 24.7.0 and the machine-wide `~/.flow-local/heavy.sh` lock. All web commands ran from `web/`:

| Command | Exit | Result |
| --- | --- | --- |
| `npm run typecheck` | 0 | Explicit final check |
| `npm run lint` | 0 | Complete lint command and all architecture, token and motion gates |
| `npx vitest run --maxWorkers=2 layout-media.test.ts src/components/layout/use-sidebar-layout.test.tsx src/components/create-issue/create-issue-team-state.test.tsx src/components/ui/toast.test.tsx` | 0 | Four related files, 50 tests |
| `npx vitest run --maxWorkers=2 src/i18n/placeholder-replace.test.ts --coverage --coverage.thresholds.branches=0 --coverage.thresholds.functions=0 --coverage.thresholds.lines=0 --coverage.thresholds.statements=0 --coverage.reportsDirectory=/tmp/codex-ci-guard/placeholder-final-resume-coverage --reporter=json --outputFile=/tmp/codex-ci-guard/placeholder-final-resume.json` | 0 | 1,306 cases; maximum individual duration 230ms. Targeted coverage floor overrides are explained in the preceding fix's section below; repository/CI floors remain unchanged. |
| `npm run test:e2e -- --retries=0 --reporter=list --output=/tmp/codex-ci-guard/results-boundary-fixed e2e/desktop-breakpoint.spec.ts e2e/desktop-shell-polish.spec.ts e2e/desktop-shell-remaining.spec.ts` | 0 | All 13 tests, bundled Chromium, zero retries, 3.1 minutes |
| Repository root: `git diff --check` | 0 | No whitespace errors |

Browser environment: `CI=1`, `FLOW_E2E_DATABASE_PATH=/tmp/codex-ci-guard/e2e-boundary-fixed.db`, `FLOW_E2E_API_LOG=/tmp/codex-ci-guard/api-boundary-fixed.log`, and `FLOW_E2E_API_BIN=/tmp/codex-ci-guard/flow-server-resume` (the inherited scratch binary). The harness starts an auth-disabled scratch API, scripted provider and Vite. The inherited task-owned server PIDs were stopped before the resumed diagnostic; ports 4173/4180/4190 were checked free before the successful run and were clear after harness shutdown. No user's browser, reference browser, real model, production database or other agent's server was used.

The final browser run covers dark/English desktop widths 881, 900, 1000, 1024 and 1280; compact drawer/composer at 375, 390 and 880; return to docked mode at 881; and toast icon changes through 1023. There are no new UI controls or product changes, so a full UI state matrix and reviewer click walkthrough are not applicable. No successful screenshots were requested from this test-only run; its assertions passed. The three opened diagnostic screenshots described above are explicitly not pass evidence.

Final logs: `/tmp/codex-ci-guard/{typecheck-final-resume,lint-final-resume,vitest-final-resume,placeholder-final-resume,e2e-boundary-fixed}.log`. Only affected/related tests were run. No Go package, REST contract or browser spec outside these three changed, so Go checks and generation do not apply. Dependencies are removed before handoff. No post-push CI result is claimed; the user requested delivery without waiting for CI.

## Continuation: run 41777655254

Commit `879483f8` added a TypeScript AST check for unsafe translation placeholder replacements. Run [41777655254](https://github.com/leozhengliu-pixel/flow/actions/runs/41777655254) exceeded the 15-second test deadline in the critical and whole-source coverage jobs because one test synchronously parsed all 1,305 non-test source files. The unchanged test passed locally under coverage, but spent about 11.5 seconds in that single case, consistent with the timing failure under CI load.

The check now uses one case per source file, retaining the same source discovery, AST traversal, unsafe-replacement detection and empty-results assertion. No assertion, source file, timeout or retry was removed or relaxed. Failures identify the affected source in the test title. The accompanying phone toolbar failure was already corrected on main by `055ab0e9`, after `11c00893` intentionally made the Display dot conditional on modified options.

Changed files are `web/src/i18n/placeholder-replace.test.ts`, `CHANGELOG.md` and this report. There are no product or UI changes, so browser screenshots and a UI state matrix do not apply.

Validation uses Node 24.7.0 and `~/.flow-local/heavy.sh`, from `web/`:

| Command | Result |
| --- | --- |
| `npm ci` | Exit 0; no dependency changes |
| `npx vitest run --maxWorkers=2 src/i18n/placeholder-replace.test.ts --coverage` with the targeted coverage options below | Exit 0 before and after the correction; 2 baseline cases, 1,306 corrected cases |
| `npm run typecheck` | Exit 0 |
| `npm run lint` | Exit 0; full command, including every architecture, token and motion check |
| `git diff --check` (repository root) | Exit 0 |

The targeted coverage command sets `--coverage.thresholds.branches=0 --coverage.thresholds.functions=0 --coverage.thresholds.lines=0 --coverage.thresholds.statements=0` because this static check does not exercise the unrelated product modules included by the repository-wide coverage floors. Repository configuration and CI floors are unchanged. Reports are directed to `/tmp/codex-ci-guard/placeholder-{baseline,fixed}-coverage`. The corrected run additionally uses `--reporter=json --outputFile=/tmp/codex-ci-guard/placeholder-fixed.json`; all 1,306 cases passed, the maximum case duration was 383 ms, and no case took over one second. Total scan time was about 11.2 seconds.

Logs are `/tmp/codex-ci-guard/{npm-ci-placeholder,placeholder-baseline,placeholder-fixed,typecheck-placeholder,lint-placeholder}.log`. No Go, REST, schema or browser spec changed; Go checks, generation and browser runs are not applicable. Only the affected test file was run, not the full unit suite.

## Continuation: runs 41755652456 and 41761872413

The resumed worktree contained five modified files and no unpublished commits. The earlier delivery below, commit `1eefe567`, was already on main and was retained. This continuation finishes the inherited corrections rather than repeating that fix. The worktree was updated from `cb8578f9` to `11c00893` before final validation; both changelog entries were preserved.

### Failure causes

| Failure | Evidence and correction |
| --- | --- |
| Run [41755652456](https://github.com/leozhengliu-pixel/flow/actions/runs/41755652456): the shared menu state test never observed disabled notification options. | Commit `3c213c3a` added the disabled-state check while intercepting `**/api/subscriptions`. The first option saves through `addSubscription`, which sends `PUT /api/subscriptions/team/:id`; the intercepted URL never matched. Hold that exact team's request, explicitly verify its PUT method, retain the disabled assertion, release in `finally`, then retain enabled and checked assertions. No retries or longer timeouts were added. |
| Run [41761872413](https://github.com/leozhengliu-pixel/flow/actions/runs/41761872413): all three unit coverage jobs and the desktop teams browser test could no longer locate a sorted header. | Commit `ac1e3fe49` intentionally adds the sort direction to the accessible name. Match the complete supported names and explicitly assert ascending and descending announcements. Keep the browser `aria-sort` checks and the unit test's actual row-order checks. |
| Run `41761872413`: light and dark phone startup tests expected a 244px sidebar and received 0. | Commit `babf90e64` intentionally hides the startup rail at widths up to 1024px. Assert zero rail/offset and full viewport card dimensions on phones, while preserving desktop geometry, the 32px disc, centering, no overflow, theme persistence, all four delayed startup stages, and eventual Projects rendering. Use the shared fresh-workspace fixture instead of a hard-coded API port. |

Changed files: `web/e2e/desktop-menu-review-states.spec.ts`, `web/e2e/desktop-settings-parity.spec.ts`, `web/e2e/startup.spec.ts`, `web/src/components/settings/desktop-settings.test.tsx`, `CHANGELOG.md`, and this report. Optional screenshot hooks capture the corrected disabled and startup states. There are no product, UI copy, REST, migration, or Go source changes. Direct main delivery follows the CI-guard task's instructions; this is test/documentation work rather than a UI change awaiting design review.

### Final validation

All heavy commands used `~/.flow-local/heavy.sh`. Web commands used the requested Node 24.7.0 on PATH. `npm ci` exited 0 with engine warnings because that requested local Node/npm combination is older/newer respectively than the package engine ranges; no dependencies or lockfile were changed.

| Command, from `web/` unless stated | Result |
| --- | --- |
| `~/.flow-local/heavy.sh npm ci` | Exit 0 |
| `~/.flow-local/heavy.sh npm run typecheck` | Exit 0 after final test edits |
| `~/.flow-local/heavy.sh npm run lint` | Exit 0 after final test edits; full lint command, no disabled checks |
| `~/.flow-local/heavy.sh npx vitest run --maxWorkers=2 src/components/settings/desktop-settings.test.tsx src/components/settings/workspace-teams-settings.test.tsx src/components/layout/sidebar-team-menu.test.tsx src/lib/startup.test.ts` | Exit 0; 4 files, 31 tests |
| From `api/`: `~/.flow-local/heavy.sh env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-ci-guard/flow-server-delivery ./cmd/server` | Exit 0; scratch server binary only |
| `CI=1 ~/.flow-local/heavy.sh npm run test:e2e -- --retries=0 --reporter=list --output=/tmp/codex-ci-guard/results-delivery e2e/desktop-menu-review-states.spec.ts e2e/desktop-settings-parity.spec.ts e2e/startup.spec.ts` | Exit 0; 24 tests, zero retries, 3.8 minutes |
| `git diff --check` | Exit 0 |

The browser command additionally sets `FLOW_CI_SHOTS=/tmp/codex-ci-guard/shots-delivery`, `FLOW_E2E_DATABASE_PATH=/tmp/codex-ci-guard/e2e-delivery.db`, `FLOW_E2E_API_LOG=/tmp/codex-ci-guard/api-delivery.log`, and `FLOW_E2E_API_BIN=/tmp/codex-ci-guard/flow-server-delivery`. Ports 4173, 4180 and 4190 were checked free before launch. `CI=1` selects bundled Chromium, with `reuseExistingServer:false`; the configured harness starts an auth-disabled scratch API, Vite, and a scripted provider. No reference browser or user's Chrome was accessed. No real-model requests are needed for these deterministic test failures.

Logs are `/tmp/codex-ci-guard/{typecheck,lint,vitest,e2e,api-build,api}-delivery.log`. Earlier inherited diagnostic failures are not verification. Go tests/vet and API/SDK generation are not applicable because no Go package or REST shape changed. Only the affected Vitest files and all three modified browser specs were run; full suites were not run. CI will not be awaited after push.

### Browser evidence and reviewer steps

All seven screenshots were opened and inspected. No error state or overlapping layout was visible. The held-save crops show the pending selected option; the test verifies the disabled DOM state and recovery after release. Startup crops intentionally show a loader while workspace bootstrap is held; every test subsequently releases the request and verifies Projects renders without browser errors. The images were captured under `shots-delivery` and moved into `/tmp/codex-ci-guard/shots/continuation/` for delivery.

| Screenshot in `/tmp/codex-ci-guard/shots/continuation/` | What it shows |
| --- | --- |
| `subscribe-disabled-390-dark-zh-CN.png` | Dark Chinese phone subscription menu, held notification save, selected first row, intact headings and checkbox tracks. |
| `subscribe-disabled-390-light-zh-CN.png` | The same held-save state in light theme; labels remain within the menu. |
| `startup-desktop-dark.png` | Dark desktop startup rail, inset loading card, centered disc and loading hint. |
| `startup-desktop-light.png` | The same desktop shell in light theme. |
| `startup-phone-dark.png` | Dark phone loading surface fills the viewport without the sidebar or desktop inset. |
| `startup-phone-light.png` | The same full viewport phone shell in light theme. |
| `desktop-mcp-connectors.png` | Existing desktop settings regression navigates to MCP connectors with selected navigation, enabled toggle, and connector selector. |

The menu matrix uses 1440x900, 375x667, and 390x844; startup uses the repository's Desktop Chrome (1280x720) and Pixel 7 (393x727) projects. No additional UI state matrix is claimed for startup or MCP controls. The harness exited normally and ports 4173/4180/4190 had no listeners afterward (the expected `lsof` exit code is 1 for no matches). Dependencies are removed before delivery. At the last poll, run `41766578507` was in progress and `41764413244` had completed red. The latter's failed logs were downloaded successfully and contain the same three causes repaired here (subscription interception, sorted-header names, and phone startup geometry); no additional failure category was found. No post-push CI result is claimed.

1. At `/:workspace/settings/teams`, click Visibility, Members, Issues, Created, and Name twice each. Check the sort arrow, announced direction, and row order. Members and Issues first sort descending; the others first sort ascending when newly selected.
2. At `/:workspace/agent`, open the sidebar on a phone, open a team menu, then Subscribe. Toggle the first notification option. While its PUT is held by the test, the menu options remain disabled; after release, they are enabled and the option remains selected. The automated menu matrix runs 1440x900, 375x667, and 390x844, dark/light and English/Chinese; it retains resting, hover, focus-visible, open, disabled, selected, long/short label, and CJK skill text checks. This change does not introduce or alter an avatar/chip control, and does not claim new avatar/chip coverage.
3. At `/:workspace/projects/all`, delay entry script, auth session, account bootstrap, and workspace bootstrap requests as in the startup spec. Inspect light and dark shells on desktop and phone. The phone card fills the viewport with no sidebar; the desktop retains the sidebar column and inset card. Release requests and verify Projects replaces the shell without errors. `/join` also verifies the shell exists when entry JavaScript is aborted.

## Previous delivery: run 41708613690

Main CI run [41708613690](https://github.com/leozhengliu-pixel/flow/actions/runs/41708613690) failed at `1052e86d2`. The previous completed run, [41690686971](https://github.com/leozhengliu-pixel/flow/actions/runs/41690686971), passed at `5eada7ea7`. This delivery repairs the two failing browser specs without changing product behavior or adding retries/timeouts.

Before delivery, rebased onto `31ec07b4`. Concurrent main commit `91d7dfb2` already restored layout visibility hiding for resting checkboxes; the final test preserves that assertion on the specific created label and adds opacity/hover/selected checks. Newer failed main run [41751007407](https://github.com/leozhengliu-pixel/flow/actions/runs/41751007407) still fails on the removed phone Back control, addressed here. At the final CI poll, run `41754146825` was queued, and run `41752282777` was cancelled.

## Causes and changes

| Failure | Intentional behavior change | Correction |
| --- | --- | --- |
| Desktop issue properties expected the first label checkbox to be hidden in Playwright's layout visibility sense, but it had a box even at opacity zero in the failing run. | `77b5662fd` restored checkbox tracks and unchecked hover affordances. The review records the bordered hovered box in `docs/verification/fix-review-1.md`. Concurrent fix `91d7dfb2` further hides resting boxes through visibility. | Target the explicitly created unchecked label and assert hidden/opacity zero at rest, visible/opacity one on hover and after selection. Retain label geometry, checked state, border and persistence assertions. |
| Phone subscription test attempted to click a Back button that no longer exists. The inherited unfinished edit also retargeted `.first()` to the remaining visible option after filtering. | `fe755e0ac` enables `phoneFilter` on Subscribe, replacing the Back header. Shared parent-input focus restoration was introduced in `325c257db`. | Keep the same row by exact accessible name with `includeHidden`; verify a triage query leaves exactly one visible option, clear it, navigate by ArrowDown, then return by ArrowLeft. Assert submenu dismissal and focus on the parent filter. Retain 358px menu width and 46px row checks. |

Changed files: `web/e2e/desktop-issue-detail-properties.spec.ts`, `web/e2e/mobile-interactive-state.spec.ts`, `CHANGELOG.md`, and this report. Both specs use their existing fresh-workspace fixture. No application source, UI copy, database schema, REST contract, provider, or deployment changes are included.

The handover's uncommitted mobile test was retained and completed. There were no unpublished local commits at resumption. The prior session log ended during the filter locator failure and an API outage; its generated browser report was preserved under `/tmp/codex-ci-guard/inherited-playwright-report`.

## Validation

Commands ran from `web/`, using `~/.nvm/versions/node/v24.7.0/bin` first on PATH and the machine-wide heavy-command lock.

| Command | Result |
| --- | --- |
| `~/.flow-local/heavy.sh npm run typecheck` | Exit 0 |
| `~/.flow-local/heavy.sh npm run lint` | Exit 0; typecheck, oxlint, picker, design token, UI foundation, functional control and motion checks |
| `~/.flow-local/heavy.sh npx vitest run --maxWorkers=2 src/components/layout/sidebar-team-menu.test.tsx src/components/ui/menu-filter.test.tsx src/components/property/use-property-command.test.tsx src/components/property/use-label-selection.test.tsx` | Exit 0; 4 files, 21 tests |
| `CI=1 ~/.flow-local/heavy.sh npm run test:e2e -- --retries=0 --reporter=list e2e/desktop-issue-detail-properties.spec.ts e2e/mobile-interactive-state.spec.ts` | Exit 0 after rebase; all 7 tests; bundled Chromium, zero retries, 31.3 seconds |
| `git diff --check` | Exit 0 |

The initial passing bundled-browser run used a temporary config importing the repository config and only removing its local `channel: 'chrome'` override. The final post-rebase run used the committed config with `CI=1`, selecting the bundled browser directly. The temporary config was removed. The final run also set `FLOW_CI_SHOTS=/tmp/codex-ci-guard/shots`, `FLOW_E2E_DATABASE_PATH=/tmp/codex-ci-guard/e2e-rebased.db`, and `FLOW_E2E_API_LOG=/tmp/codex-ci-guard/api-rebased.log`. The harness started an auth-disabled scratch API, scripted provider and Vite, and stopped them after the run. Ports 4173, 4180 and 4190 were checked afterward and had no listeners. Dependencies were reinstalled in this worktree through `~/.flow-local/heavy.sh npm ci` after the rebase (exit 0); npm emitted engine-version warnings for the required local Node 24.7.0, and all validation passed on that version. Post-rebase logs are `typecheck-rebased.log`, `lint-rebased.log`, `vitest-rebased.log` and `e2e-rebased.log` under `/tmp/codex-ci-guard/`.

Earlier diagnostic browser runs exited 1 while isolating the stale assertions, the visible-role locator retargeting, and Escape versus ArrowLeft behavior. Those failures are not counted as verification. The initial lint attempt exited 1 because the inherited untracked Playwright report contained generated JavaScript; moving the artifact out of the source tree allowed the complete unchanged lint command to pass. No lint rules were disabled. Go validation was not needed because no Go package changed.

## Browser evidence

All six final crops were opened and inspected. They show working controls without errors or overlapping text. They are local artifacts under `/tmp/codex-ci-guard/shots`.

| Crop | What it shows |
| --- | --- |
| `issue-labels-desktop-unchecked.png` | Full desktop label menu; the created unchecked row keeps its checkbox track without painting the box. The initial Bug row is highlighted independently with its empty box hidden. |
| `issue-labels-desktop-hover.png` | Created label hovered with its empty bordered checkbox and highlight visible. |
| `issue-labels-desktop-checked.png` | Selected label in Frequently used with a visible check and separate label/icon tracks. |
| `team-subscription-filtered.png` | Phone filter contains triage; only the triage option and its relevant heading remain. |
| `team-subscription-keyboard.png` | Cleared filter, first notification row keyboard-highlighted, and checked Pulse option visible. |
| `team-menu-parent-filter.png` | Parent team menu restored after ArrowLeft, with search focus asserted by the test and disabled Leave team visible. |

The changed assertions were checked in English/dark at 1440x900 for desktop labels and 390x844 at DPR 3 for the phone menu. This is a test correction, not a new UI delivery; a full light/Chinese/375px control-state matrix is not claimed.

## Reviewer steps

1. On `/:workspace/issue/:identifier` at desktop width, choose Add label in the property rail. Inspect an unchecked label at rest, hover it, then select it. Its empty box should appear on hover and its check after selection; close the picker to confirm its chip persists.
2. On `/:workspace/team/:teamKey/all` at 390px width, open the sidebar, open the team's ellipsis menu, then Subscribe. Type triage into Filter; only the triage row should remain. Clear it, press ArrowDown to highlight the first notification row, then ArrowLeft to return to the parent filter.

## Scope and limits

Only the failing specs and related component tests were run, not the entire browser suite. The latest observed completed non-cancelled main run was `41751007407` (failed), with `41754146825` queued. The task calls for a direct main CI-guard fix; the changes here are tests and documentation only. CI is not awaited after push. Logs, scratch databases and the six screenshots remain in `/tmp/codex-ci-guard/`; dependencies and temporary repository artifacts are removed before delivery.
