# Date and Menu Review Handoff

Review branch: `review/fix-other-ACD`. Implementation head: `764c1aaa`.
The earlier work was retained, including published history merged in `72c508fb`.
Current `origin/main` (`0c09167e` at the final fetch) and the previously published
review head are both ancestors. Publication is to the review branch only.

This report supersedes the earlier date/menu conclusions. The [original A/C/D
report](/tmp/codex-fix-other-acd/report.md) retains the earlier comparison crops,
broader gate results, and original fixtures. Evidence below is local to this
machine under `/tmp/codex-fix-other-acd/continuation/`; it is not bundled into Git.

## Seven Follow-up Items

| Item | Final behavior, reference comparison, and files | Commits |
| --- | --- | --- |
| 1. Due-date submenu | Restores the saved audit baseline of 232px minimum width and no shortcut inside the desktop submenu. The fresh live reference measured 226px with its current preset labels; the requested saved 232px baseline takes precedence. Uses the shared sizing variable in `issue/due-date-control.css` and `issue/issue-options-menu.css`, with the assertion restored in `e2e/desktop-issue-list-menus.spec.ts`. [Reference]( /tmp/codex-fix-other-acd/continuation/shots/ref-due.png), [Flow](/tmp/codex-fix-other-acd/continuation/shots/flow-due-1440-dark-en-US.png). | `62f1d894` |
| 2. Search dates | Updated/Created date offer 1 day, 3 days, 1 week, 1 month, 3 months, 6 months, and 1 year ago, then Custom date or timeframe. Desktop custom dialog is 562px wide, with two months, one chevron pair, blank outside days, five precisions on one row, draft Cancel/Apply, and typed month/quarter/half-year/year/date input. Non-day precisions show `in`. Phone retains one month with the same presets and precisions. Changes are in `search/search-menus.tsx`, `search-date-filter.{tsx,css}`, `search-date-input.ts`, and the shared calendar. [Reference presets](/tmp/codex-fix-other-acd/continuation/shots/ref-presets.png), [reference Day](/tmp/codex-fix-other-acd/continuation/shots/ref-day.png), [Flow Day](/tmp/codex-fix-other-acd/continuation/shots/flow-day-1440-dark-en-US.png). | `8959611e`, `6c9d5e3b`, `f09a4f6a` |
| 3. Project calendar | Desktop panel is 430px tall; at device scale 2 its clientHeight and scrollHeight are both 429. All seven columns fit; weekday/date weekend shading joins and keeps rounded bottoms. Removes duplicate sidebar sizing, and opens phone date popovers vertically to keep columns within the viewport. A 375x667 phone scrolls vertically, as the fresh phone reference does. Files: `projects-page/project-target-date-picker.tsx`, `project-row-menus.module.css`, `project-detail/project-detail-page.css`. [Reference](/tmp/codex-fix-other-acd/continuation/shots/ref-project.png), [Flow dark](/tmp/codex-fix-other-acd/continuation/shots/flow-project-1440-dark-en-US.png), [Flow light](/tmp/codex-fix-other-acd/continuation/shots/flow-project-1440-light-en-US.png). | `919437c5`, `4e642fba`, `3c436340`, `0722f829`, `0c714630` |
| 4a. Fiscal requests | `search/workspace-search-page.tsx` subscribes to workspace preference updates and includes numeric fiscal start in memoized requests. Regression coverage changes loaded settings to April and checks new year/half-year requests with the matching 2025 chip. | `007ce287` |
| 4b. Invalid chips | `search/search-state.ts` validates shared URL filters and advanced Apply with the same date validator used by the request AST. Invalid/impossible dates and timestamp-shaped values disappear from both chips and requests. | `007ce287` |
| 4c. Team filter | Deferred, as permitted by the brief when the existing AST does not support it. Issue queries support Team, but unified search metadata has no Team mapping in `api/internal/store/search_metadata.go` (`searchMetadataFilter`). An issue-only condition would silently discard other resource types. Cross-resource support needs a server change. | No change |
| 5. Menus | Shared issue-menu surfaces use a 0.5px border. At device scale 2 the English menu is 243.023px wide with a 6.5px first-row offset in both themes; Chinese is 242px. Phone dark menus use the five-layer shadow, and Release Back headers clip to 12px top corners. Files: `issue/issue-options-menu.css`, `ui/menu-submenu.css`, shared theme rules. [Reference](/tmp/codex-fix-other-acd/continuation/shots/ref-issue-menu.png), [Flow desktop](/tmp/codex-fix-other-acd/continuation/shots/flow-menu-1440-dark-en-US.png), [Flow Release](/tmp/codex-fix-other-acd/continuation/shots/flow-release-375-dark-en-US.png). | `77086cfb` |
| 6. Weekday preference | Pulse uses the same `firstWeekday` resolver as calendars. All seven named weekdays, case-insensitive names, and legacy numeric values are supported in `lib/runtime-preferences.ts` and `pulse/pulse-model.ts`. Browser coverage saves/reloads Tuesday and opens the custom search calendar. | `5581efd4`, `4440eea1` |
| 7. Chinese typed dates | `lib/natural-date.ts` shares parsing across snooze, reminders, issue due dates, command pages, projects, and search. Covers the requested Chinese week/month/year examples, rejects partial/invalid parses, and retains IME guards. Consumers are `inbox/notification-row`, `triage/triage-model`, `ui/reminder-options`, `issue/due-date-utils`, `due-date-dialog`, `due-date-picker`, `issue-options-menu`, `command/context-commands`, `document-commands`, and the project/search date controls. | `23082636`, `63b77616`, `8959611e` |

The fresh reference desktop dialog measures 562 x 550.5px at (439,116.5) in a
1440x900 viewport; Flow is 562 x 550px at the same position. Both input rectangles
are (471.5,191,497,32), both calendar dividers start at y=283, and both calendar
areas end at y=581.5. These measurements do not imply pixel-perfect parity.

The final calendar assertion change `764c1aaa` addresses actual 1x rasterization:
430px outer height, 428px clientHeight, 429px scrollHeight, and a 1px rendered
border. At 2x these are 430/429/429 and a 0.5px border. The test permits only that
one-pixel 1x rounding and retains the real last-day hit-target check. No product
height was changed to satisfy CI. [1x capture](/tmp/codex-fix-other-acd/continuation/shots/flow-project-scale1.png),
[2x capture](/tmp/codex-fix-other-acd/continuation/shots/flow-project-scale2.png).

## Retained Earlier Fixes

These prior logical commits remain in the branch. The final targeted E2E run
covers their changed specs along with the follow-up date/calendar work.

| Area | Retained behavior and files | Commit | Reviewer route and steps |
| --- | --- | --- | --- |
| Reviews | Reachable onboarding sections and responsive columns in `reviews/reviews-onboarding.css`. | `73746ea5` | `/<workspace>/reviews`: open the empty onboarding view; scroll through all features at phone and narrow desktop widths. |
| Inbox | Chinese snooze input and IME handling in `inbox/notification-row.tsx`; now uses the shared parser. | `075aae1f`, `23082636` | `/<workspace>/inbox`: open a notification's Snooze; type a Chinese relative date and verify its result. |
| Releases | Entry remains available before the first assignment; phone picker has a working Back path in `issue/issue-release-picker.tsx`. Preserves pipeline gating. | `2d0a728b`, `5e70b643` | `/<workspace>/issue/<identifier>`: with an unassigned release and an eligible pipeline, open Issue options > Release; select a release, then reopen and go Back. |
| Display properties | Supported desktop properties remain selectable in `my-issues/my-issues-display-menu.tsx`. | `00677512` | `/<workspace>/my-issues/assigned`: Display > Display properties; inspect SLA, Release, and activity properties. |
| Label actions | Rest/hover/focus/open action visibility in `settings/label-settings.css`. | `18c7c68c`, `96629531` | `/<workspace>/settings/issue-labels`: hover a row, Tab to its actions, and open its menu on desktop and phone. |
| Member export | Team CSV export stays reachable; workspace-only cleanup is scoped in `settings/settings-parity.css`. | `0ea03057` | `/<workspace>/settings/teams/<teamKey>/members`: use Export CSV at 375/390px. |
| Team headings | Titles and action shortcuts wrap in normal flow in `settings/settings.css` and `settings-parity.css`. | `47f42ca6`, `e5066094` | `/<workspace>/settings/teams/<teamKey>` and its `/labels` page: use a long team name and inspect heading/actions at 375/390px. |
| Nested properties | Shared phone submenu layering and Back focus/ARIA in `ui/dropdown-menu.tsx` and `use-phone-submenu-level.ts`. | `325c257d`, `3c024c67` | `/<workspace>/projects/all`: row menu > Properties > Labels > a label group; select a child, reopen, and use Back. |
| Shared issue surfaces | Shared typography/menu/date rules replace detail-only overrides in `detail/issue-desktop.css`, `editor/rich-text-body.css`, and shared controls. | `c73a9ea4`, `94ccc2cd`, `00d36f2c` | Open an issue detail and sub-issues, then its options and due-date menus; compare shared titles and controls. |

`CHANGELOG.md` records these changes under Unreleased. This task changes no Go
source, REST shape, generated SDK, migration, production service, or model proxy.

## Validation

Every heavy command used `~/.flow-local/heavy.sh`, Node v24.7.0, and this
worktree's own dependencies. Browser checks used bundled Chromium against an
auth-disabled scratch API on 5442 and Vite on 5443, backed by a scratch database
under `/tmp/codex-fix-other-acd/continuation/`. Changed E2E specs create their own
workspaces through `workspace-fixture`; they do not require the saved fixtures.

| Command | Exit | Result and evidence |
| --- | --- | --- |
| `npm run lint` | 0 | Includes `npm run typecheck`, oxlint, and all five UI gates. Passed again immediately before final publication. Previous identical run: [log](/tmp/codex-fix-other-acd/continuation/lint-handover-final.log). |
| `npx vitest run --maxWorkers=2 <19 files below>` | 0 | 19 files, 215 tests. [Log](/tmp/codex-fix-other-acd/continuation/vitest-handover-final.log). |
| `env FLOW_MOBILE_BASE_URL=http://127.0.0.1:5443 npx playwright test -c /tmp/codex-fix-other-acd/continuation/playwright.config.mjs <11 specs below>` | 0 | 43 passed, 17 viewport/project-specific skips. [Log](/tmp/codex-fix-other-acd/continuation/e2e-handover-final.log). |
| `npx playwright test -c /tmp/codex-fix-other-acd/continuation/playwright-scale1.config.mjs e2e/project-calendar-bounds.spec.ts` | 0 | 1x desktop: 2 passed, 4 phone-specific skips. [Log](/tmp/codex-fix-other-acd/continuation/calendar-scale1-final.log). |
| Local browser state matrix, `node /tmp/codex-fix-other-acd/continuation/verify.cjs` | 0 | Twelve viewport/theme/language combinations, including final dependent reruns. [Measurements](/tmp/codex-fix-other-acd/continuation/measurements.json), [log](/tmp/codex-fix-other-acd/continuation/verify-final.log). |
| `git diff --check` | 0 | Clean before report/publication. |

The 19 Vitest paths, relative to `web/src/`, are:

```text
lib/natural-date.test.ts
lib/runtime-preferences.test.ts
components/search/search-date-filter.test.tsx
components/search/workspace-search-page.test.tsx
components/pulse/pulse-model.test.ts
components/inbox/notification-row.test.tsx
components/issue/issue-options-menu.test.tsx
components/issue/due-date-picker.test.tsx
components/issue/due-date-dialog.test.tsx
components/issue/issue-release-picker.test.tsx
components/command/command-menu.test.tsx
components/command/command-menu-zh.test.tsx
components/command/document-commands.test.tsx
components/triage/triage-actions.test.ts
components/projects-page/project-target-date-picker.test.tsx
components/project-detail/project-overview.test.tsx
components/ui/row-context-menu.test.tsx
components/ui/dropdown-menu-mobile.test.tsx
components/my-issues/my-issues-display-menu.test.tsx
```

The 11 Playwright paths, relative to `web/e2e/`, are:

```text
desktop-issue-list-menus.spec.ts
desktop-settings-inbox-shell.spec.ts
label-action-visibility.spec.ts
mobile-week-start-preference.spec.ts
phone-empty-release.spec.ts
project-calendar-bounds.spec.ts
project-property-label-group.spec.ts
search-date-filter.spec.ts
team-label-heading.spec.ts
team-member-export.spec.ts
team-overview-title.spec.ts
```

No full local test suite was run. No Go packages changed, so Go vet/tests and
OpenAPI/SDK generation were not required.

The publication continuation repeated explicit `npm run typecheck`, full
`npm run lint`, all 19 Vitest files (215 tests), and all 11 Playwright specs
(43 passed, 17 intentional skips); every corrected command exited 0. An initial
browser rerun omitted `FLOW_MOBILE_BASE_URL` and failed 12 cases by connecting
to unused port 4173. Setting the variable to the scratch app's 5443 port resolved
those failures without changing product code or tests. The retained crop sheets
and both scale captures were reopened again before publication.

## Browser States and Inspected Captures

The matrix covers 1440x900, 390x844, and 375x667; dark/light; en-US/zh-CN.
Changed controls were checked in resting, hover, focus-visible, active/open,
selected, and applicable disabled states. It covers all five precisions,
invalid-input disabled Apply, month navigation, short/long English and Chinese
input, a two-CJK viewer identity, translated chips, and long Release labels.
The earlier report retains the two-CJK avatar ink measurements and original
label/team/property state matrix.

All twelve retained crop sheets were reopened during the final continuation,
as were the two 1x/2x calendar captures. Their contents are listed below. The
[screenshot manifest](/tmp/codex-fix-other-acd/continuation/screenshot-manifest.txt)
lists the underlying captures; the two scale captures are additional.

| Opened sheet | What it shows |
| --- | --- |
| [01](/tmp/codex-fix-other-acd/continuation/shots/contact-01.jpg) | Desktop/375px Day, both themes/languages, one-row precision labels and blank outside dates. |
| [02](/tmp/codex-fix-other-acd/continuation/shots/contact-02.jpg) | 390px Day and desktop disabled Apply. |
| [03](/tmp/codex-fix-other-acd/continuation/shots/contact-03.jpg) | Disabled Apply at 375/390px in both themes/languages. |
| [04](/tmp/codex-fix-other-acd/continuation/shots/contact-04.jpg) | Desktop due submenu without shortcut, Half-year keyboard focus, desktop issue menus. |
| [05](/tmp/codex-fix-other-acd/continuation/shots/contact-05.jpg) | Remaining issue-menu themes/languages and phone sizes. |
| [06](/tmp/codex-fix-other-acd/continuation/shots/contact-06.jpg) | Month mode, full desktop calendars, and scrolled short-phone calendars. |
| [07](/tmp/codex-fix-other-acd/continuation/shots/contact-07.jpg) | Phone calendar bottoms, 390px calendars, Quarter mode, and Release header. |
| [08](/tmp/codex-fix-other-acd/continuation/shots/contact-08.jpg) | Rounded Release Back headers with long/CJK labels and a parsed reminder. |
| [09](/tmp/codex-fix-other-acd/continuation/shots/contact-09.jpg) | Chinese reminder results, selected Q4, and focused Apply. |
| [10](/tmp/codex-fix-other-acd/continuation/shots/contact-10.jpg) | Remaining selected/focused states, readable Apply labels, Year mode, and reference Day. |
| [11](/tmp/codex-fix-other-acd/continuation/shots/contact-11.jpg) | Reference menus, relative presets, periods, desktop project, and scrolling phone project. |
| [12](/tmp/codex-fix-other-acd/continuation/shots/contact-12.jpg) | Two superseded legacy Quarter captures and fresh reference Year. The legacy file named `ref-search-day.png` is Quarter, and is not Day evidence. |

The 1x/2x captures show the full 430px calendar, all seven columns, final date
row, and rounded weekend bottoms. Final crops show no horizontal clipping or
overlapping controls. Short-phone project crops show the last row after vertical
scrolling. Disabled Apply is an expected validation state, not a runtime error.
Earlier failed captures/logs (off-screen phone picker, wrong period scroll, dark
Apply text) are superseded diagnostics and are not claimed as passes. Broad
desktop parity gates in the original report are not all green and have not been
reclassified as passing.

Reference inspection in the earlier continuation used the required `bu.sh`
wrapper. It was read-only. No fresh reference calls were necessary to finish
publication; the retained measurements and captures were reused.

## Reviewer Clicks for Follow-up

1. `/<workspace>/issue/<identifier>`: Issue options > hover Due date on desktop.
   Inspect 232px minimum width and absence of the submenu shortcut. Tab/hover the
   parent rows; repeat with light and dark themes.
2. `/<workspace>/search`: Add filter > Updated date (repeat Created date).
   Inspect seven relative presets, choose one, and inspect its chip. Reopen >
   Custom date or timeframe. Switch Day/Month/Quarter/Half-year/Year; browse
   months; type `May 2027`, `Q4`, `H2 2027`, `2027`, and `05/20/2027`.
   Invalid input disables Apply; Cancel discards the draft and Apply adds a chip.
   Repeat at 1024/1280 and 375/390px, both languages/themes.
3. `/<workspace>/project/<slug>/overview`: open Target date; inspect the final
   row and both weekend bottoms. Desktop is 430px tall; short phones scroll
   vertically with all columns still reachable.
4. `/<workspace>/search?filters=...`: use an April fiscal start and a shared
   year/half-year filter; reload and inspect the chip plus network ranges.
   An invalid value such as `2026-02-30` must leave neither chip nor condition.
5. Issue options > Release at 375/390px: inspect both top corners; search and
   Back preserve the rounded header. Reopen Issue options to inspect its shadow.
6. `/<workspace>/settings/account/preferences`: save Tuesday through Friday as
   First day of the week. Open a calendar and `/<workspace>/pulse`; both use it.
7. `/<workspace>/inbox`: Snooze > type the requested Chinese week/month/year
   examples. Repeat Issue options > Remind me, a project/document reminder menu,
   and command-palette due-date/reminder pages. IME candidate confirmation must
   not submit. Fixture identifiers are in the local continuation `fixture.json`.

## CI, Cleanup, and Limits

Earlier CI [41556785726](https://github.com/leozhengliu-pixel/flow/actions/runs/41556785726)
succeeded. Published-head CI [41582600142](https://github.com/leozhengliu-pixel/flow/actions/runs/41582600142)
failed; it is not a verification pass. Its calendar assertion assumed no 1x
rounding. That was reproduced locally, measured against the retained reference
geometry, and fixed by `764c1aaa`; the scale-1 and scale-2 checks pass. The prior
weekday spec now opens Custom date (`4440eea1`), and the Apply assertion accepts
the shared white color serialization (`3de97001`). The complete changed-spec
run above includes those fixes. Cancelled runs `41578621320`/`41579531542` are
also not passes. Review-branch pushes do not automatically trigger CI: the
workflow listens for main pushes, pull requests, and manual dispatch. The final
continuation explicitly dispatched [41604671516](https://github.com/leozhengliu-pixel/flow/actions/runs/41604671516)
against published revision `208d6b67`. The user explicitly requested no wait for
branch CI, so its result is unverified. The subsequent commit only updates this
report; it changes no implementation or test files.

Final cleanup stopped owned scratch API/Vite PIDs `68320`, `68364`, and `68333`;
ports 5442 and 5443 have no remaining listeners. `web/node_modules` was removed.
The clean published revision and retained test logs were checked, and all twelve
crop sheets plus both scale captures were reopened in the final continuation.
No product changes or additional test runs were needed after those passing runs.
Other agents' processes, shared browser daemon,
model proxy, production hosts, and production databases remain untouched.

Team filtering remains deferred as described above. Reference light/Chinese
states cannot be obtained without changing that account's settings, so those
comparisons are local Flow state checks. Due-date preset suffixes still use
English weekday/month abbreviations in zh-CN. Remaining subpixel, font, icon,
and phone-anchor differences are not a pixel-perfect parity claim. No model
behavior changed, so this frontend follow-up needs no real-model requests.
