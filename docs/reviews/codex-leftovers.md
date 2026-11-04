# Leftover UI Review

Review branch: `review/codex-leftovers`. No PR and no push to main.

## Findings and Changes

| Item | Reference and previous Flow behavior | Result |
| --- | --- | --- |
| Desktop labels | Live reference label offset is exactly 60px at scale 1. Flow's layout is also 60px; the reported 59.41px was measured during its 267ms scale animation. | Wait for the opening animation before the exact `toBe(60)` assertion. No fitted width or offset. |
| Completed cycles | Handover reports a 12px shift caused by the wider success text sizing the scope column. The available live PIC reference has current and upcoming rows only; the FLO route supplied by stored captures has no measurable rows. | Unresolved. Keep the existing layout. A candidate that preserved columns produced overlap and was rejected; no sampled width, hidden capacity control or relaxed assertion is shipped. A completed reference row is still needed to establish its intended metric layout. |
| Document subscribers | Live first row has `aria-selected=true` because it is subscribed, `data-focused=false`, and a transparent highlight layer. | Keep current behavior. The live capture does not show an initial keyboard highlight. No new highlight rule. |
| Phone Teams | Stored reference and previous Flow both have a 504px table; search widths are 72.16px at 375 and 87.16px at 390. This clips the placeholder and puts the group band's end outside the viewport. | Following the explicit clipping request, search takes its own row: 331px at 375 and 346px at 390. Table widths are 375px and 390px, with intrinsic numeric columns and an ellipsized name. Group bands are 359px and 374px. This deliberately differs from the stored geometry. Live Teams checks returned loading or Not found and cannot establish newer reference geometry. |
| Phone API | Previous admin Issued keys copy extended to x373.66 and the control to x706.33 inside a card ending at x368. | Restrict full-width controls and nonshrinking copy to the permission selector row. Issued keys copy wraps alongside its count inside the card. |
| Phone Preferences | Stored reference row heights start 94, 79.5, 68, 65, 79.5px at scale 2. Flow matches those values at scale 2. Scale 1 has different normal font line boxes. | No row-height or font override. Preferences gate verifies the existing layout. |
| Teams Name arrow | Main already renders Down for ascending and Up for descending. | Preserve it. The concurrent main update also supplies stricter sort-announcement tests; those upstream tests are retained. |
| Home-view suffix | Live open menu dims `(default)` separately from the option label. Measured dark menu foregrounds were LCH 91.554 and 66.215 during this visit. | Render the suffix separately in the shared select menu, using the existing secondary text token. Native phone select text remains plain. |
| Project title | Reference uses a wrapping editable title: pre-wrap, break-word, no maximum height, 32px paragraph line height. Previous Flow used a scrolling single-line input. | A textarea grows with its contents, wraps long names and commits with Enter or blur. Block layout prevents an added textarea baseline gap. |
| Phone issue rows | Fixed-width pill contents could extend past their shrinking slots. The session button also shrank below the Error pill and avatar cluster's intrinsic width, especially with a long label. | Let pill contents shrink and ellipsize, reserve space for the badges, and keep the session trigger at its content width. Both My issues and team lists are exercised. |
| Light Project pill | A light-only project/milestone rule overrode the shared mobile property background and border. | Remove that rule so project and sibling property pills share the same styling. Empty and populated project states are checked. |
| Broader light contrast | Live reference theme changes are prohibited. Stored captures and existing semantic tokens do not establish a replacement global tertiary or Backlog palette. | No speculative global palette change. Low-contrast rings and tertiary text remain a limitation of this review. |

## Files and Commits

| Commit | Files and purpose |
| --- | --- |
| `a9185212` | `web/e2e/desktop-issue-detail-properties.spec.ts`: wait for opening motion, then require exactly 60px. |
| `b4dcbcd5` | `web/src/components/settings/mobile-teams.css`, `mobile-api.css`, `settings-primitives.tsx`, `settings.css`: contain phone settings and render the dim suffix in the shared menu. |
| `a35da0c3` | `web/src/components/project-detail/project-overview.tsx`, `project-detail-page.css`: wrapping, growing project title. |
| `2b15ca09` | `web/src/components/my-issues/my-issues-list.module.css`: shrinking label contents and nonshrinking session/avatar controls. No menu-rule changes. |
| `6921ebff` | `web/src/components/issue/issue-project-picker.css`: remove the divergent light-only pill override. |
| `6811fc00` | `web/e2e/mobile-leftovers.spec.ts`: isolated workspace regression across all 12 viewport/theme/language combinations, with a scripted event stream that cannot overwrite the synthetic Error session. |

Each product/test cause includes its Unreleased entry in `CHANGELOG.md`. All commits use the requested Codex coauthor trailer. Preferences heights, the existing Teams arrow and subscriber highlighting require no product commit. Completed-cycle alignment and broad light contrast remain undone.

Additional bounds checked by the regression: the old session cluster reached x395 at 375px; the final cluster ends inside its row at both 375 and 390. The old project title had scrollWidth 1408 versus clientWidth 691; the final field has scrollWidth <= clientWidth and scrollHeight <= clientHeight at all three widths. API row children now end inside the row, rather than at x706.33. These are containment checks, not invented reference coordinates. The light Project pill now has the same computed background as the status pill; the removed override supplied rgba(0,0,0,.03) plus a 1px border.

## Reviewer Click Paths

Replace `:workspace`, `:team`, `:issue`, and `:project` with records in your workspace.

1. Desktop `/:workspace/issue/:issue`: click Add label. Check an unchecked row, hover it, select it, close and reopen the menu. The text starts 60px into the option after opening completes.
2. Desktop `/:workspace/team/:team/cycles`: use a team with completed and upcoming cycles. Check status, capacity and success text on the same heading line; compare column positions with a current cycle.
3. Desktop `/:workspace/document/:document`: click the bell. The subscribed row has its check without an added initial keyboard highlight.
4. Phone `/:workspace/settings/teams`: read the full search placeholder, type a matching and nonmatching name, clear it, and inspect Members and Issues at 375 and 390. Click Name to change direction. Long team names ellipsize while identifiers remain visible.
5. Phone admin `/:workspace/settings/api`: scroll to API keys, read the whole Issued keys description and count, then click the row to open `/settings/api/keys`.
6. `/:workspace/settings/account/preferences`: inspect the existing phone row heights; on desktop open Default home view and compare the dim suffix with the option label.
7. `/:workspace/project/:project/overview`: on phone close project details if it is open. Edit the title to a long English/CJK name, then a short name. Press Enter and reload to confirm persistence. Hover and focus the field.
8. Phone `/:workspace/my-issues/assigned` and `/:workspace/team/:team/all`: inspect issues with parent titles, labels, a sub-issue counter and an assigned agent with an Error session. Hover/focus a label and open its picker. Parent text and label text ellipsize; pill borders and avatar clusters stay whole.
9. Phone light `/:workspace/issue/:issue`: inspect an empty Project pill, open it, choose a long project name, then reopen it. Its resting fill and border match sibling property controls.

## State Coverage

The browser regression creates an isolated workspace per case. It covers 1440x900, 375x667 and 390x844; Dark and Light; English and zh-CN. It uses short and long title values, long team and label text, two CJK initials, empty and populated project selections, and a scripted Error session. It checks resting, hover, keyboard focus, active editing, open menus, selected options and empty search results. These controls do not expose a disabled state in the exercised routes; this is not a claim that every disabled application control was audited.

The initial 12-case matrix passed before the longer label/session stress was added. That stronger case exposed the session trigger shrinking. The matrix with the session correction passed all 12 cases in both the scratch desktop context and the repository's mobile Chromium context. A post-rebase repeat then exposed live event replay overwriting the synthetic session; its failed screenshot shows the missing fixture pill and is not a verification pass. The fixture now intercepts the event stream and both bootstrap routes, with a 90-second budget for this multi-route journey. Geometry assertions remain unchanged. Earlier failures are retained in scratch logs and are not counted as successful verification.

## Validation and Artifacts

Heavy commands use `~/.flow-local/heavy.sh`; browsers use bundled Chromium against an auth-disabled scratch API and Vite on isolated ports 15381 and 15380. Reference calls use the supplied `bu.sh`, owned tabs and read-only navigation. The fixture database is a SQLite backup of the existing scratch review database, stored under `/tmp/codex-leftovers/gate.db`; the original database was read only while taking that backup. A fresh reference cycle visit during the resumed session remained at Loading, including after an 8-second wait, and adds no parity evidence.

No Go, database schema, REST contract or translation copy changes. No model calls are needed for these layout changes.

### Commands

Run from `web/`, through the machine-wide heavy wrapper:

```sh
npm run typecheck
npm run lint
npx vitest run --maxWorkers=2 \
  src/components/settings/personal-settings.test.tsx \
  src/components/settings/workspace-teams-settings.test.tsx \
  src/components/settings/desktop-settings.test.tsx \
  src/components/settings/settings-primitives.test.tsx \
  src/components/project-detail/project-overview.test.tsx \
  src/components/project-detail/project-overview-mentions.test.tsx \
  src/components/my-issues/my-issues-list.test.tsx \
  src/components/my-issues/my-issues-estimates.test.tsx \
  src/components/my-issues/issue-row-actions.test.tsx \
  src/components/cycles/cycle-capacity.test.tsx
FLOW_CI_SHOTS=/tmp/codex-leftovers/shots npx playwright test \
  -c .leftovers.playwright.config.ts \
  desktop-issue-detail-properties.spec.ts \
  desktop-cycles-timeline.spec.ts mobile-leftovers.spec.ts
```

The temporary config imports the repository config, keeps its desktop/mobile projects and device contexts, removes the local Chrome channel, selects bundled Chromium and points to the owned servers at `http://127.0.0.1:15380`. One worker runs the tests. The desktop launch uses scale 2; the mobile project retains the repository's Pixel 7 context. The temporary config is removed after verification. The new regression imports `workspace-fixture`; it creates its own workspace and has no dependency on local `/verify` data.

| Check | Exit code and result |
| --- | --- |
| `npm run typecheck` | 0. |
| Full `npm run lint` | 0, including all design/control/motion gates. |
| Targeted Vitest command above | 0; 10 files, 67 tests. |
| Three Playwright specs above | 0; 17 tests before rebase. After rebase the five unchanged desktop cases pass; the corrected `mobile-leftovers.spec.ts` rerun exits 0 with all 12 phone/matrix cases passing after isolating the scripted session. |
| Phone gates: API, Preferences, project overview, My issues, team list, issue detail top | Each 0; no assertion changes. |
| Phone Teams geometry gate | 1; 60 expected differences across 48 element checks, because its stored layout requires the clipped 504px table and one-row toolbar. The containment regression passes; this geometry gate is explicitly not a pass. |
| `git diff --check` | 0. |

Saved resumed-session output: `/tmp/codex-leftovers/{typecheck,lint,vitest}-resume.txt`, `e2e-resume.txt`, `e2e-final-review.txt`, `gate-teams-resume.txt` and `gate-<area>-<screen>.txt`. Rebased validation output uses `{typecheck,lint,vitest,e2e}-rebased.txt`; `e2e-rebased.txt` records the one fixture failure. Its corrected rerun is `e2e-stable-fixture.txt`, and the final lint run is `lint-final-fixture.txt`. No Go files changed, so no Go test/vet or generated-contract step applies.

Phone gates use the supplied `mobile-sweep-setup.mjs` import and `mobile-gate.mjs`, without `--seed`. Screens: `settings/api`, `settings/account-preferences`, `projects/project-overview`, `issues-list/my-assigned`, `issues-list/team-list`, `issue-detail/top`, and `settings/teams`.

### Screenshots

All files are under `/tmp/codex-leftovers/shots/`. Each listed capture was opened and visually reviewed. The two phone suffixes below are `375-Light-zh-CN` and `390-Dark-en-US`; the regression checks all 12 viewport/theme/language combinations even when it does not save a screenshot.

| Files | What they show |
| --- | --- |
| `teams-375-Light-zh-CN.png`, `teams-390-Dark-en-US.png` | Search on its own row, all table columns and the whole group band inside the phone width; long team name ellipsized. |
| `teams-focus-375-Light-zh-CN.png`, `teams-focus-390-Dark-en-US.png` | Keyboard-focused search control crops in both themes/languages. |
| `api-375-Light-zh-CN.png`, `api-390-Dark-en-US.png` | Complete Issued keys copy and count inside the API card. |
| `api-focus-375-Light-zh-CN.png`, `api-focus-390-Dark-en-US.png` | Focused Issued keys row with its existing outline and wrapped description. |
| `project-375-Light-zh-CN.png`, `project-390-Dark-en-US.png` | Saved long project title wrapping onto multiple complete lines. |
| `project-hover-375-Light-zh-CN.png`, `project-hover-390-Dark-en-US.png` | Long title under the pointer, with its existing hover fill. |
| `project-focus-375-Light-zh-CN.png`, `project-focus-390-Dark-en-US.png` | Focused long title with complete lines inside the growing field. |
| `issue-row-375-Light-zh-CN.png`, `issue-row-390-Dark-en-US.png` | Team list parent trails, labels, sub-issue counters and whole CJK avatar clusters. |
| `session-row-375-Light-zh-CN.png`, `session-row-390-Dark-en-US.png` | Team list Error session pill, long CJK/English label, parent trail and full agent/human avatars. |
| `row-label-open-375-Light-zh-CN.png`, `row-label-open-390-Dark-en-US.png` | Open row label picker, including selected and unchecked rows. |
| `issue-properties-375-Light-zh-CN.png`, `issue-properties-390-Dark-en-US.png` | Crops of empty Project and sibling property pills. |
| `project-pill-selected-375-Light-zh-CN.png`, `project-pill-selected-390-Dark-en-US.png` | Focused populated project pill with a long name, ellipsis, and the milestone control. |
| `home-menu-1440-Dark-en-US.png`, `home-menu-1440-Light-en-US.png` | Desktop home-view menu with a dim `(default)` suffix. |
| `issue-labels-desktop-unchecked.png`, `issue-labels-desktop-hover.png`, `issue-labels-desktop-checked.png` | Desktop label menu at rest, hovered checkbox and checked state. |
| `desktop-cycles-timeline.png`, `desktop-cycle-details.png` | Existing current-cycle timeline and details regression captures; no cycle product change is shipped. |
| `ref-labels.png` | Live unchecked label picker; measured offset 60px. |
| `ref-document-subscribers.png` | Live subscribed row with a check, transparent initial highlight layer and the following separator. |
| `ref-home-menu.png`, `ref-home-menu-open.png` | Live default-home control at rest and with the dim suffix in its open menu. |
| `ref-project.png` | Live editable overview title used to inspect wrapping and line-height rules. |
| `ref-cycles.png` | Live PIC current/upcoming cycles. |
| `ref-completed-cycles.png` | Live FLO empty cycle list. This cannot verify a completed row. |
| `ref-teams-390.png`, `ref-teams-retry.png` | Failed reference navigation showing Not found. Not counted as parity verification. |
| `ref-teams-admin-390.png` | Failed reference load showing only a loading indicator. Not counted as parity verification. |
| `cycles-completed.png` | Rejected exploratory layout: capacity falls into the chart. This is a failed capture, not verification of shipped code. |

The final reference limitation is specific: the completed-cycle metric layout remains unresolved, and broad light contrast changes lack an approved light reference. Teams is a deliberate responsive correction to the supplied clipped geometry, so its old geometry gate is expected to fail and has not been weakened.

Representative opened crops: [phone Teams](/tmp/codex-leftovers/shots/teams-390-Dark-en-US.png), [focused API row](/tmp/codex-leftovers/shots/api-focus-375-Light-zh-CN.png), [wrapped project title](/tmp/codex-leftovers/shots/project-375-Light-zh-CN.png), [session and avatars](/tmp/codex-leftovers/shots/session-row-390-Dark-en-US.png), [selected Project pill](/tmp/codex-leftovers/shots/project-pill-selected-375-Light-zh-CN.png), [home-view suffix](/tmp/codex-leftovers/shots/home-menu-1440-Light-en-US.png), [unchecked desktop label](/tmp/codex-leftovers/shots/issue-labels-desktop-unchecked.png).
