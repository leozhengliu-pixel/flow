# Keyboard focus, phone skip link, and startup hint

Review branch: `review/codex-focus`. UI changes are submitted for owner review, not pushed to main.

## Measurements and changes

All dimensions below are CSS pixels. Reference browsing used a private task tab through the supplied browser lock wrapper, with keyboard Tab navigation on lists, projects, and settings. Network throttling and focus emulation were restored before closing each owned tab. No reference settings or data were changed.

| Control | Reference | Flow before | Flow after |
| --- | --- | --- | --- |
| Command buttons, property triggers, settings selects | 1px, offset 0 | Usually 2px, offsets -1/1/2; some local colors | Shared 1px, offset 0 |
| Navigation and view tabs | 1px, offset -1 | Mixed 2px outlines and shadows | Shared 1px, offset -1 |
| Checkboxes and toggles | 1px, offset 2 | 2px shadow without a gap; issue row checkbox had no explicit rule | Shared 1px outline, offset 2 |
| Profile inputs | 1px, offset -1 | Local border color and 2px translucent shadow | Shared 1px inset outline |
| Project property triggers | 1px inset shadow | No keyboard ring on several triggers | 1px inset shadow, transparent outline at -1 |
| Project header, health, icon, and target date controls | 1px, offset 0 | Missing ring or local color | Shared 1px, offset 0; hover alone does not draw a ring |
| Phone skip link | First Tab stop at 390px; 1px outline, offset 1, plus 1px shadow | Hidden below 768px | Available to hardware keyboards; Enter focuses main |
| Startup hint | Trigger at about 8s after DOM readiness; 0.8s transition delay, then 0.8s ease-out fade | 8s delay and 0.4s fade, restarted by React | 8.8s to fade start, 0.8s ease-out fade; elapsed time survives React takeover/remounts |

The dark reference ring is `lch(47.918 59.303 288.421)`. Flow previously used `lch(67.6 34.2 283)` in many components and now uses the shared activity highlight token. Light Flow uses the existing light activity highlight token, `lch(53 52.26 286.91)`; the read-only reference session was dark, so the light reference color was not independently measured.

The final cold-load reference sample had DOM readiness at 7383.2ms, opacity 0 at 16027ms, 0.321346 at 16381ms, 0.826923 at 16735ms, and 1 at 17094ms. Computed transition was `opacity 0.8s ease-out 0.8s`. Its current visible text is `Loading...` with an ellipsis glyph. Flow retains the task's existing localized workspace-loading text. An earlier severely throttled attempt reached a network error and is excluded from passing evidence.

Implementation is in `web/src/styles/tokens.css`, the existing component focus rules, `web/src/styles/micro-audit-overrides.css`, `web/src/styles/mobile-shell.css`, `web/src/components/layout/skip-to-content.css`, `web/index.html`, and `web/src/components/layout/app-startup.tsx`. The component changes consume shared width, flush, inset, and toggle-offset tokens. No new specificity escape or important declaration was added. Board and display-menu files owned by the other task were left alone.

## Browser review

The browser specs create their own workspaces through `e2e/workspace-fixture.ts`. They cover 1440x900, 390x844, and 375x667 in dark/light and English/zh-CN. The control sheets include long names, a one-character valid profile name, long project and issue titles, and two CJK characters in avatars and project chips.

Checked states: resting and hover; selected/unselected tabs; focused and selected checkboxes; property trigger focus, open picker, and Escape focus return; project header hover versus keyboard focus; project icon/health/property focus; settings select resting/hover/focus/open and Escape return; toggle resting/hover/focus/on/off; profile input resting/hover/focus; Update resting/hover/focus/pressed and disabled/disabled-hover with no focus. Startup is checked while auth is held, before entry readiness, and after bootstrap completes. Disabled states apply to controls that expose them naturally in these flows.

These are representative shared control families. Peripheral component focus declarations were migrated to the same tokens, but this report does not claim a separate screenshot of every button in every settings/integration/editor screen.

### Reviewer clicks

1. `/<workspace>/my-issues/created`: create an issue if the list is empty, reload, then press Tab. Inspect Skip to content; press Enter and confirm main receives focus. At desktop width, Tab to the row checkbox and press Space twice; Tab to Priority and press Enter, then Escape. Inspect selected and unselected view tabs.
2. `/<workspace>/projects/all`: use List layout with a project present. Tab to Name, the project icon, Health, Priority, Lead, Target date, Issues, and Status. Hover Name after clearing focus: hover must not paint a keyboard ring.
3. `/<workspace>/settings/account/preferences`: Tab to the default home view select, open it with Enter, close with Escape, then Tab to a toggle and press Space twice.
4. `/<workspace>/settings/account/profile`: Tab to Full name. Enter a long name, then clear it to inspect disabled Update; enter `A`, Tab to Update, and inspect hover/focus/pressed states. Cancel instead of saving on a real workspace.
5. At 375px and 390px, reload a workspace route, press Tab then Enter, and open/close the navigation drawer. Repeat in both themes/languages.
6. For loading timing, hold `/api/auth/session` in browser tooling and reload a workspace route. The disc appears immediately; the hint stays transparent until the 8.8s fade start and reaches full opacity at 9.6s after DOM readiness. Release the request to finish loading.

## Evidence and validation

Screenshots are under `/tmp/codex-focus/shots`. The final control contact sheets are `controls-{1440,390,375}-{dark,light}-{en-US,zh-CN}.png`; startup frames are `loading-{1440,390,375}-{dark,light}-{en-US,zh-CN}.png`. Each sheet labels its actual control state. `reference-loading-confirmed.png` shows the hatched reference disc with its visible loading hint; `reference-phone-skip-handover.png` shows the hardware-keyboard skip link. Exact test results and screenshot inspection are recorded in the final handover.

All saved screenshots were opened for visual inspection. This inventory distinguishes passing evidence from exploratory captures:

| Files under `/tmp/codex-focus/shots` | What they show |
| --- | --- |
| `controls-{1440,390,375}-{dark,light}-{en-US,zh-CN}.png` (12 sheets) | Labeled actual control crops in the states described above. Desktop adds checkboxes, project controls and open pickers; phones show skip link, tabs, native select, toggle, profile input and Update. Long text truncates within its control; CJK avatars and chips remain legible. |
| `loading-{1440,390,375}-{dark,light}-{en-US,zh-CN}.png` (12 frames) | Hatched disc and fully visible localized workspace hint; desktop rail and card, full-viewport phone surface, no error or overflow. |
| `reference-loading-confirmed.png`, `reference-loading-hint-final.png` | Reference disc and visible loading hint during a cold throttled load. |
| `reference-phone-first-tab.png`, `reference-phone-skip.png`, `reference-phone-skip-handover.png`, `reference-phone-skip-resumed.png` | First keyboard Tab stop revealing Skip to content on phone project/team/My issues surfaces. |
| `reference-profile-input.png` | Full-name input with the reference's inset keyboard ring. |
| `reference-project-icon-handover.png`, `reference-project-property.png` | Project icon keyboard ring and surrounding project row. |
| `reference-project-inset.png` | Project priority trigger with the inset ring. |
| `reference-toggle.png` | Checked preference toggle with a separated keyboard ring. |
| `reference-settings-menu.png`, `reference-settings-select.png` | Preferences screen with focus on the submission-key select and settings navigation, respectively; the filenames are historical and these are not open-menu captures. |
| `gate-drawer-375.png`, `gate-drawer-390.png` | Final measured drawer at its top scroll position; navigation stays within the phone viewport. |
| `drawer-parity-375.png`, `drawer-parity-390.png`, `drawer-gate-debug-375.png` | Earlier exploratory drawer frames, including crowded lower navigation under the fixed Help control. Excluded from final passing layout evidence. |
| `reference-list-focus.png` | Exploratory team list surface with focus on the bottom Agent command, not evidence of an issue checkbox ring. |
| `reference-loading.png` | Initial startup disc before the hint; not evidence of the hint's final state. |

Local browser runs use bundled Chromium and isolated API/Vite ports 18480/15480. Port 4173 was occupied and was not reused. The temporary Playwright config and gate adapter are not shipped. The drawer adapter only scrolls the drawer navigation to the top specified by the measurement manifest before comparing it; it does not change expected measurements or product styles.

Validation used Node 24.7.0 and the machine-wide heavy-command lock. On the rebased branch, `npm run typecheck` and the full `npm run lint` both exited 0. The targeted `npx vitest run --maxWorkers=2` invocation covered the 78 files listed in `/tmp/codex-focus/vitest-files.txt` plus startup, icon picker, header search, settings primitives, and project surface/data-view tests: 84 files and 952 tests passed, exit 0. The complete command and output are in `/tmp/codex-focus/checks-rebased.log`.

Before rebase, `npx playwright test -c .focus-playwright.config.ts e2e/keyboard-focus.spec.ts e2e/startup-hint.spec.ts e2e/startup.spec.ts` passed all 28 tests, exit 0 (`/tmp/codex-focus/e2e-finish.log`). After the target-date distinction was added, the four desktop focus cases also passed, exit 0 (`/tmp/codex-focus/project-focus-finish.log`). The final rebased run of the complete `e2e/keyboard-focus.spec.ts` matrix passed all 12 cases, exit 0 (`/tmp/codex-focus/focus-rebased.log`). All 12 final contact sheets were reopened after this run.

The supplied phone gate passed drawer (58 checks), preferences (38 checks), and My issues top bar (36 checks), exit 0. Commands use `FLOW_SWEEP_API=http://127.0.0.1:18480 FLOW_SWEEP_WEB=<worktree>/web ~/.flow-local/heavy.sh node --import <scratchpad>/mobile-sweep-setup.mjs <scratchpad>/mobile-gate.mjs <area> --screen <screen> --base http://127.0.0.1:15480 --web <worktree>/web`; drawer additionally imports the temporary scroll adapter described above. Logs are `/tmp/codex-focus/gate-drawer-finish.log` and `/tmp/codex-focus/gates-shell-settings-finish.log`.

An initial focus run exited 1 because its selector referred to a removed project picker wrapper; the selector was corrected to the actual shared trigger and the geometry/color assertions were retained. The profile gate initially exited 1 with four email-value geometry failures because the scratch workspace viewer had an empty email, producing a zero-height span. The local fixture email was populated in `/tmp/codex-focus/verify.db`; no product CSS or expected measurements were changed for that failure.

With the fixture complete, the final profile gate passed all 42 element checks, exit 0 (`/tmp/codex-focus/gate-profile-finish.log`). All four required phone screens are green, totaling 174 element checks. The first two review commits are `95163372` (shared focus rules) and `83f63fea` (phone skip link plus browser state coverage); the loading-hint commit contains this report. All commits use the required Codex co-author trailer.

The branch was rebased onto `origin/main`; the changelog conflict retained both sets of entries, and concurrent issue-pill/avatar and settings-select suffix changes were preserved. Temporary browser configs and adapters were removed, worktree dependencies were deleted, and the scratch API/Vite processes were stopped by their own PIDs after validation. Screenshots and test logs remain under `/tmp/codex-focus` for review. No PR was opened and branch CI was not awaited.

No API, schema, SDK, model-provider, or Go source changes are included. Real model requests are unnecessary for these UI changes. Remaining known scope: the explicitly excluded board/display-menu focus rules, independent light-reference measurement, and exhaustive individual screenshots of peripheral controls.
