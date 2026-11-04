# CI guard delivery

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
