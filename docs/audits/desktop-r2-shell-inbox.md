# Desktop R2: Shell And Inbox

The fresh restart gate ran at `48085779` with the carried notification-menu edits present. These results use new measurements, not earlier session logs. Settings is handled separately.

| Area | Before | After | Measured differences |
| --- | --- | --- | --- |
| Shell | 7 PASS, 4 FAIL, 2 inconclusive | 13 PASS, 0 FAIL | 40 to 0 |
| Inbox | 8 PASS, 2 FAIL, 1 finding | 10 PASS, 0 FAIL, 1 finding | 12 to 0 |

All 24 frame-padding mutation proofs detected the 2px change and restored the original state. The full gate exits **1** because the welcome-video finding remains. The six final screenshot screens exit **0**. A gate PASS is limited to its matched elements, recorded properties and coverage floors; it does not establish complete pixel or fixture equivalence.

[Per-screen results, before/after differences, coverage and classified limits](desktop-r2-shell-inbox-differences.json) are recorded separately. Improvements include these commits, fixture/matcher corrections, and concurrent fixes pulled from main.

## Product Changes

| Commit | Files | Before and after |
| --- | --- | --- |
| `1de69f7b` | `web/src/components/inbox/notification-row.tsx`, `web/src/components/inbox/inbox.css`, `web/e2e/desktop-inbox-parity.spec.ts` | Desktop row-menu outer padding becomes zero with a separate 6px options inset; the snooze search gains a separator. The measured menu width is 191px. Dated presets and typed snooze persist. |
| `be75d20b` | `inbox.css`, `desktop-inbox-parity.spec.ts` | The desktop actions menu uses its intrinsic content width instead of 180px; the negative shortcut margin is removed. Screenshot capture waits for menu animation completion. |
| `060d7397` | `web/src/components/layout/sidebar.css`, `web/src/components/ui/tooltip.css`, `web/src/components/issue/issue-header.css` | Desktop navigation right inset is 9px; both issue navigation buttons use 2px horizontal padding; the sidebar search tooltip uses weight 450. |
| `2fa7c4f6` | `web/src/components/issue/label-project-pickers.tsx`, `web/src/components/detail/issue-properties-rail.css` | An explicit empty-estimate state gives the closed desktop placeholder the muted foreground. Selected values and interactive states retain their foregrounds. |
| `c8965680` | `web/src/components/issue-explorer/issue-board.module.css`, `web/src/components/my-issues/my-issues-list.module.css` | Desktop identifiers use -0.24px spacing; identifiers and dates use the board metadata token. Parent separators use the captured 13px type and 6px inset, correcting the first card's date position. |
| `db5ff140` | `inbox.css`, `web/src/components/inbox/inbox-filter-builder.module.css` | The open filter trigger is transparent away from hover. Selected filter labels and leading icons use the highlighted foreground. |

Each product commit includes its Unreleased entry in `CHANGELOG.md`. New visible UI copy was not introduced.

## Gate And Fixture Corrections

Scratch tooling remains in this worktree's `.parity-local/` directory. Original reference JSON, PNGs, routes and step arrays were preserved.

- `desktop-match.mjs`: preserve capture visibility before animation normalization; a clipped 1px accessibility label cannot become a visible shortcut after unscaling.
- `desktop-match.mjs`: avoid applying the same declared scale twice; implicitly scaled floating menus retain their anchored top-left origin.
- `desktop-match.mjs` and `desktop-browser.mjs`: intersect clipping ancestors before applying the existing 1px visibility floor. Collapsed rows exposed only through a subpixel sliver are excluded consistently.
- `desktop-match.mjs`: pair the composite shell header's breadcrumb row with Flow's one-row header. Inbox/Pulse retain their complete header owner.
- `desktop3-states.mjs`: initialize and assert collapsed Workspace/Your teams groups; use repaired issue identifiers for toast/tooltip; reproduce the hovered notification-actions control while filtering.
- `desktop3-data.mjs` and `r2-fixtures.mjs`: match eight recent-chat titles/ages, notification read state, board order, team names, glyphs and sampled team colors through owned fixtures.

Comparison tolerances and numeric coverage floors were not lowered. Seventeen matcher tests pass, including clipped labels, collapsed groups, scale normalization and header ownership.

## Validation

All heavy commands ran through `~/.flow-local/heavy.sh` using the requested Node PATH. Final successful checks:

| Command | Exit | Coverage |
| --- | --- | --- |
| `npm run typecheck` | 0 | Before every web push |
| `npm run lint` | 0 | Full lint, including all design/control/motion checks |
| `npx vitest run --maxWorkers=2 <targeted files>` | 0 | Twelve distinct component test files, listed in the JSON report |
| Playwright `desktop-inbox-parity.spec.ts` | 0 | All 6 tests |
| Playwright `desktop-settings-inbox-shell.spec.ts -g toast` | 0 | Toast interaction |
| Playwright `desktop-issue-detail-properties.spec.ts` | 0 | Both issue-property flows |
| Playwright `desktop-issue-board.spec.ts` | 0 | Board layout and controls |
| Playwright `desktop-p12-inbox.spec.ts` | 0 | All 3 tests |
| Scratch phone row-menu/read-state test | 0 | 390px compact menu and persisted read state |
| Phone geometry/text gates: inbox list, inbox actions | 0 | 44 + 24 geometry checks, both text gates |
| `node --test .parity-local/desktop-match.test.mjs` | 0 | 17 matcher tests |
| Desktop shell/inbox gate with `--mutation` | 1 | 23 PASS, video finding, zero measured differences, 24/24 proofs |
| Six-screen final capture gate | 0 | Command menu, collapsed groups, toast, tooltip, board and inbox filter |

Browser flows used bundled Chromium, an auth-disabled scratch API and owned Vite processes. Harnesses stopped their servers by PID. Dependency cleanup interrupted earlier attempts; successful checks used temporary protection on installed package files with writable caches. The protection was released afterward. Initial animation captures and failed setup probes are not counted as passes.

## Reviewer Clicks

Use an existing workspace in place of `:workspace` and a team key in place of `:team`.

| Route | Steps and expected change |
| --- | --- |
| `/:workspace/inbox` | Click the title's ellipsis. Labels and shortcut fit the actions menu. Press Escape, right-click a notification, then hover Snooze. Inspect the separate list inset, search separator and dated presets; enter `in 3 hours` and press Enter. |
| `/:workspace/inbox` | Click Add filter, then move the pointer over the title's ellipsis without clicking. The filter trigger becomes transparent while the selected Notification type label/icon remain highlighted. |
| `/:workspace/agent` | Expand Workspace to inspect navigation insets. Click Workspace and Your teams to collapse both groups. Hover the sidebar search button to inspect its tooltip weight. Open the command menu with the existing shortcut to inspect keycaps and scrolling. |
| `/:workspace/team/:team/all` | Open an issue from the list to establish navigation context. Inspect the up/down buttons, click Copy issue URL to show the toast, and hover sidebar Search. |
| `/:workspace/issue/:identifier` | Use an issue whose team has estimates enabled and whose estimate is empty. The Set estimate placeholder is muted at rest; hover/open it and choose a value to inspect its interactive/selected foreground. |
| `/:workspace/team/:team/all` | Open Display options, choose Board, and enable identifier/created-date metadata. Include a sub-issue with a parent. Inspect identifier spacing, date foregrounds and the parent separator. |

## Screenshots Opened

Files are under `/tmp/codex-desktop-r2-settings-inbox-shell/shots/`. Every retained screenshot below was opened and inspected.

| File | What it shows |
| --- | --- |
| `desktop-inbox-actions.png` | Settled light-theme actions menu; opaque surface with complete labels and shortcut. |
| `desktop-notification-snooze.png` | Light-theme row menu plus complete dated snooze submenu and search separator. |
| `phone-notification-actions.png` | Compact 390px phone menu; read action works. The existing Delete-label ellipsis remains. |
| `shell-command-menu.png` | Dark command menu with preserved agent-chat commands, Issues group, keycaps and scrolling. |
| `shell-sidebar-collapsed.png` | Workspace and Your teams are both collapsed; history contains the captured eight titles/ages. |
| `shell-toast.png` | Issue detail, muted empty-estimate label, navigation controls and visible copied-URL toast. |
| `shell-tooltip.png` | Same issue detail with the sidebar Search tooltip visible. |
| `shell-topbar.png` | Board with correctly ordered cards, parent trail and metadata typography. Fixture-count limits remain below. |
| `inbox-inbox-filters.png` | Selected Notification type row, transparent filter trigger and hovered notification-actions control. |

## Remaining Limits

Each item is also classified individually in the JSON report.

- **Product decision needed:** the welcome notification needs an owner-approved introductory-video asset. Its unavailable-video state remains a finding, not a PASS.
- **Fixture cannot match:** the captured board's icon/count metadata pills and Todo value of 9 are not reproduced by the three-card fixture. The original metadata payload and complete issue projection are absent from the captures.
- **Fixture cannot match:** the reference sidebar includes a third team and cycle entries; the isolated fixture lacks the original cycle/membership graph. Complete resource contents are not equivalent.
- **Product decision needed:** reference Try suggestions offer Import issues, while Flow's current onboarding offers Invite people, Cycles and Connect GitHub. The onboarding model was preserved.
- **Product decision needed, phone:** a separate 390px probe exposed the existing cascading snooze submenu at `x=-172.64px`. A responsive cascade or single-layer interaction needs a decision. Desktop changes leave the phone rules intact; this failing submenu probe is not counted as verification.
