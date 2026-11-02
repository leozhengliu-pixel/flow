# Workspace Menu CI Repair

Base: `fd58e5a8`. Review branch: `review/ci-green-13`.

## Diagnosis

Read `gh run view 41457688020 --log-failed`; the saved log is `/tmp/codex-ci-green-13/ci-failed.log`.

| Original test | Cause | Repair |
| --- | --- | --- |
| `mobile-settings.spec.ts:249`, 375px and 390px | Product regression in `89daaeb4`. Phone Settings previously used `avatarInitials`, whose Unicode letter/number match skipped punctuation. The new helper returned `[W` instead of `MW`. The old sidebar helper did retain punctuation, so the two callers had differed. | Shared workspace initials select whole letter/number graphemes. Preserve two CJK initials, mixed scripts, accents, explicit icons and the `W` fallback. Keep the existing `MW` browser assertion. Product commit: `7293efa2`. |
| `workspace-menu.spec.ts:31`, 1440px dark/light | Five names, five navigations and 25 open/close cycles shared one 30-second budget. CI exhausted that budget at different font/style reads. | Generate one test per name. Each compares action positions with `A` in its own fixture workspace and retains all five trigger click targets. |
| `workspace-menu.spec.ts:31`, 390px dark/light | Same accumulated budget, plus drawer and dialog transitions. The reported visible dialog already had `data-state="closed"` when the test budget expired. | Same split. Keep the real Escape/hidden assertions and animations. |
| `workspace-menu.spec.ts:180` | Three viewport workflows, repeated navigation and real logout requests shared one budget. CI failures move between `goto` and drawer clicks. | Generate independent 1440px, 375px and 390px tests with fresh fixture workspaces. |

The original CI-mode local run reproduced both initials failures. Its four menu matrix tests passed in 26.8, 25.1, 28.5 and 28.1 seconds; the combined shortcuts case passed in 15.8 seconds. The CI log shows overall test timeouts, not independent 30-second navigations or 5-second dismissal failures. Exact Linux timeout locations were not reproduced on this Mac.

The repaired name cases take 5.3-9.4 seconds and the shortcut cases 4.6-6.4 seconds locally. No product CSS, timeout, retry setting or assertion tolerance changed.

## Data Independence

Every generated case creates its own workspace through `workspace-fixture`. Position comparisons happen within that workspace. The run accumulates memberships and duplicate display names without selecting a workspace by account-list position. The existing switch test filters account bootstrap to its two real fixture URL keys, then constructs the explicit long-list response; it does not require a global workspace count or database ordering. That test passed separately in 14.4 seconds, including real switching and the 65-entry stress fixture on desktop, 375px and 390px.

The manual state runner also used an accumulating scratch database with dozens of real memberships and duplicate names. It located the current workspace by its selected checkmark, rather than assuming the first matching name was current. Its initial locale captures were rejected because stored account language overrode local storage. The accepted run patches account language and asserts the translated accessible name before capturing each state.

## Verification

All heavy commands ran through `~/.flow-local/heavy.sh`, with the requested Node PATH. API binaries, logs, databases and state runner are under `/tmp/codex-ci-green-13/`. Playwright used `CI=1`, the repository `playwright.config.ts`, bundled Chromium, scratch API/Vite servers and `--retries=0`.

| Command / scope | Exit | Result |
| --- | --- | --- |
| `npm ci` in this worktree's `web/` | 0 | Own dependencies installed, then removed during cleanup. |
| `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-ci-green-13/flow-e2e-api ./cmd/server` in `api/` | 0 | Scratch API binary; no Go source changed. |
| Baseline `npx playwright test -c playwright.config.ts e2e/workspace-menu.spec.ts e2e/mobile-settings.spec.ts --grep 'keeps Chinese and mixed\|workspace menu shortcuts\|stacks workspace fields' --retries=0 --reporter=list` | 1 | Two reproduced `MW` failures; five passes, five intentional project skips. |
| `npm run lint` | 0 | Includes `npm run typecheck`, oxlint and every picker/token/foundation/control/motion gate. |
| `npx vitest run --maxWorkers=2` for workspace initials/menu, sidebar, Settings, invite dialog and OAuth authorization test files | 0 | Six files, 80 tests. Full command paths are listed below. |
| `npx playwright test -c playwright.config.ts e2e/workspace-menu.spec.ts e2e/mobile-settings.spec.ts --grep 'workspace menu\|stacks workspace fields' --retries=0 --reporter=list` | 0 | 27 passes, 25 intentional project skips. |
| `npx playwright test -c playwright.config.ts e2e/workspace-menu.spec.ts --grep 'switches real workspaces' --retries=0 --reporter=list` | 0 | One pass, one intentional project skip. Together these two runs cover every test in the changed spec. |
| `node /tmp/codex-ci-green-13/states.mjs` | 0 | 168 states across 12 viewport/theme/language combinations. |

Vitest paths: `src/components/workspace/workspace-initials.test.tsx`, `src/components/workspace/workspace-menu.test.tsx`, `src/components/layout/sidebar.test.tsx`, `src/components/settings/settings-page.test.tsx`, `src/components/workspace-directory/invite-members-dialog.test.tsx`, `src/components/auth/oauth-authorize-page.test.tsx`.

Browser command environment: `FLOW_E2E_API_BIN=/tmp/codex-ci-green-13/flow-e2e-api`; each run has a distinct `FLOW_E2E_DATABASE_PATH` and `FLOW_E2E_API_LOG` under the same scratch directory. `FLOW_CI_SHOTS` and `FLOW_MOBILE_SHOTS` point to its `shots/` directory. Logs: `baseline.log`, `lint.log`, `vitest.log`, `fixed.log`, `stress.log`, `states-corrected.log`.

## Reviewed Screenshots

Every saved screenshot was opened. The two baseline failure images show the broken `[W` logo and are failure evidence only (`baseline-results/`). Initial incorrectly localized sheets are retained in `rejected-locale-captures/` and excluded from accepted verification.

Each accepted sheet shows six resting names (shortest `A`, punctuation prefix, two CJK characters, both mixed-script orders and a long mixed name), trigger hover/focus-visible/open, the checked current workspace, and Settings Logo resting/hover/focus-visible/busy-disabled. Initials are decorative; there is no separate chip or disabled trigger state to invent. The logo's actual upload request was held to exercise its disabled state. Both translations were verified from rendered accessible names.

| Viewport | Dark English | Dark Chinese | Light English | Light Chinese |
| --- | --- | --- | --- | --- |
| 1440x900 | [14 states](/tmp/codex-ci-green-13/shots/states-1440-dark-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-1440-dark-zh-CN.png) | [14 states](/tmp/codex-ci-green-13/shots/states-1440-light-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-1440-light-zh-CN.png) |
| 375x667 | [14 states](/tmp/codex-ci-green-13/shots/states-375-dark-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-375-dark-zh-CN.png) | [14 states](/tmp/codex-ci-green-13/shots/states-375-light-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-375-light-zh-CN.png) |
| 390x844 | [14 states](/tmp/codex-ci-green-13/shots/states-390-dark-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-390-dark-zh-CN.png) | [14 states](/tmp/codex-ci-green-13/shots/states-390-light-en-US.png) | [14 states](/tmp/codex-ci-green-13/shots/states-390-light-zh-CN.png) |

Other accepted images in `/tmp/codex-ci-green-13/shots/`:

- `workspace-general-375.png`, `workspace-general-390.png`: repaired `MW` Settings logos in the actual phone tests.
- `cjk-1440-dark.png`, `cjk-1440-light.png`, `cjk-390-dark.png`, `cjk-390-light.png`: both CJK initials contained in the focused trigger, with stable adjacent actions.
- `collapsed-workspace-menu.png`: the desktop menu remains visible over a collapsed sidebar.
- `cjk-settings-1440.png`, `cjk-settings-390.png`: two CJK initials in the Settings Logo control with Chinese UI.
- `cjk-invite-1440.png`, `cjk-invite-390.png`: two CJK initials in the invite dialog header.
- `cjk-oauth-1440.png`, `cjk-oauth-390.png`: two CJK initials in the consent workspace trigger.
- `uploaded-switch-1440.png`, `uploaded-switch-390.png`: decoded uploaded logo in the trigger and checked workspace row among duplicate workspace names.
- `many-workspaces-desktop.png`, `many-workspaces-375.png`, `many-workspaces-390.png`: bounded scrolled workspace lists with the current workspace visible and checked.

## Review Clicks

1. At `/:workspace/settings/workspace`, set the name to `[mobile] Workspace`. Blur the name field, reload, and inspect Logo: it must show `MW`. Repeat at 375px and 390px. Hover and keyboard-focus Logo; an in-flight upload disables that same control.
2. Open `/:workspace/my-issues/assigned`. On phones first click the header sidebar button. Inspect the top workspace avatar; hover, Tab-focus and open its menu. Open Switch workspace and inspect the checked row. Repeat with `A`, a CJK name, mixed scripts and a long name in light/dark and English/Chinese. Both initials must fit, and Search/New issue must retain their positions.
3. On desktop, collapse the sidebar using its resize separator, reveal it from the header, and open the workspace menu. Use `G` then `S` to reach workspace Settings. Menu Log out and Alt+Shift+Q must send the logout request. Repeat the phone workflows at 375px and 390px.

The product commit awaits lead review and merge. Local targeted verification does not establish the result of the next Linux main CI run. No full suite was run.
