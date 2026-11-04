# CI guard delivery

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
