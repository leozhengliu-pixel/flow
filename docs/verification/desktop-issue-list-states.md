# Desktop Issue List States

## Baseline And Scope

Measured at 1440 x 900, dark theme, DPR 2, against the section 3/8/10/13 audit in `/tmp/desktop-ref/issues-list.md`. The final clean-main baseline was `16a60106`; earlier measurements against `d6fc59e3` gave the same desktop gaps. Baseline and changed builds used this worktree's Vite and an auth-disabled scratch API/database under `/tmp/codex-desktop-list-states/`. No existing workspace was edited to enable triage.

Rebased onto `c0cc234b` before committing and reran lint, focused tests and the desktop specs. That intervening commit only changes phone settings/team surfaces and the phone footer rule, not these desktop measurements.

Chromium needs `--force-device-scale-factor=2` as well as the context's `deviceScaleFactor: 2` on this machine: context emulation alone reports DPR 2 but rounds a 0.5px border to 1px. The final before/after JSON and desktop crops use both settings.

Already implemented on main, retained rather than rebuilt:

- Column hide/show callbacks and personal display persistence. Missing: the desktop hidden-column panel dimensions, docking, surfaces, and the paged board's equivalent controls. Swimlane restore also passed the wrong compound key.
- Saved-view visibility/owner cards and summary tabs. Missing: default-open desktop state, unassigned bucket, complete paged summary counts, and matching card/list spacing.
- Enabled triage sidebar/count, real issue selection/actions, and the 400px/787px split. The old audit only saw triage disabled. Missing in the enabled comparison: the team glyph and compact row metrics. The disabled-state markup/styles were not changed.

Existing local display preferences retain board visibility; the existing saved-view/team-default API persists shared display defaults. Details use the same best-effort localStorage pattern as other panels, with a separate per-view desktop key so desktop defaults do not open the phone overlay. Paged summaries use the existing grouped issue-records API, including retry and cancellation. No REST route or field changed; no API/SDK generation or Go changes were needed.

## Measurements

Rectangles are `(x, y, width, height)` in CSS pixels. Issue names, status sets, owners, counts, and dates are fixture data, not visual targets.

| Element | Reference | Current-main baseline | After |
| --- | --- | --- | --- |
| Hidden header | (1081.5,96,338,50) | (605,119,147.5,28) | (1081.5,96,338,50) |
| First hidden row | (1081.5,146,338,37) | (605,147,147.5,34) | (1081.5,146,338,37) |
| Second hidden row | (1081.5,193,338,37) | 34px row stride | (1081.5,193,338,37) |
| Hidden row surface | radius 8; gap 10; surface lch(9.232 .85 272) | transparent, no inter-row gap | radius 8; gap 10; matching surface |
| Empty content block | (663,401.25,350,210) | full-area wrapper (244.5,148,1187,743.5), small content | (663,400.75,350,210) |
| Empty illustration | (761.5,401.25,153,90) | (806,480,64,48) | (761.5,400.75,153,90) |
| Empty heading | (730.62,511.25,214.76,23), 15/600 | (776.98,540,122.05,19.5), 13/500 | (730.617,510.75,214.758,23), 15/600 |
| Hidden-by-filter card | (663,554.25,350,57) | absent | (663,553.75,350,57) |
| Clear Filters | (892.45,570.75,104.05,24) | absent | (892.5,570.25,104,24), single line |
| Saved details default | open | closed | open; explicit close/reopen survives reload |
| Saved identity card | (1035.5,96,388,53) | same box only after manual open | (1035.5,96,388,53) by default |
| Saved metadata card | (1035.5,157,388,85) | (1035.5,157,388,87) after manual open | (1035.5,157,388,85) |
| Saved list width | 787 | 1187 closed; 791 after manual open | 787 |
| Triage list | (244.5,8.5,400,855) | already identical | unchanged |
| Triage detail | (644.5,8.5,787,855) | already identical | unchanged, including selected issue |
| Triage row | (252.5,60.5,383.5,59) | (252.5,60.5,384,59) | (252.5,60.5,383.5,59) |
| Triage title | 13/500, normal line height, lch(100 0 272) | 13/500, 16px line height, lch(96 0 272) | matches reference type/color |

The filter card has a 0.5px `lch(18.48 1.48 272)` border and radius 5. Saved cards retain the existing 10px radius and now use the reference surface and 0.5px `lch(16.793 1.93 272)` border. Hidden rows use that border/surface and the existing card shadow. The empty block is 0.5px above the reference, within the 1px tolerance.

## Browser Review

All saved PNGs were opened. The baseline shots intentionally show the missing presentation, not passing parity. The final shots show functioning states with no errors or overlapping controls. An initial crop exposed a wrapped Clear Filters label; it was fixed and the desktop test now checks its text height.

All files below are under `/tmp/codex-desktop-list-states/shots/`:

| PNGs opened | What they show |
| --- | --- |
| `board-before.png`, `board-after.png` | Narrow inline hidden statuses before; right-docked filled hidden rows after |
| `empty-before.png`, `empty-after.png` | Small search icon without recovery before; stacked-row art, heading, count and working recovery card after |
| `saved-default-before.png`, `saved-default-after.png` | Closed before; open metadata and assignee breakdown including unassigned issues after |
| `saved-open-before.png`, `saved-open-after.png` | Manually opened old cards versus corrected spacing/surfaces and complete buckets |
| `triage-before.png`, `triage-after.png` | Existing enabled queue/sidebar count; added team glyph and corrected row metrics |
| `triage-selected-after.png` | Selected real issue, full detail content and triage action buttons in the right pane |
| `phone-hidden-390-before.png`, `phone-hidden-390-after.png` | Same horizontally scrolled phone hidden-column panel at 390 x 844 |
| `phone-hidden-375-before.png`, `phone-hidden-375-after.png` | Same phone hidden-column panel at 375 x 667 |

The supplied `desktop-compare.cjs` was run with a DPR-2 launcher preload. These opened crops place the reference above Flow:

| Crop | Region | Observation |
| --- | --- | --- |
| `board-compare.png` | (1080,96,350,294) | Header/row placement, surface, half-filled estimate glyph and gaps match; status set differs with test-team data |
| `empty-compare.png` | (660,395,356,224) | Matching block/card geometry and single-line recovery command; 0.5px vertical offset and different close glyph stroke |
| `saved-compare.png` | (1032,94,397,272) | Matching 388px card stack; actual owner and unassigned buckets; existing three-tab summary differs from reference's four tabs |
| `triage-compare.png` | (244,8,400,238) | Team glyph and row positions match; actual team color, issue text and relative dates differ; Flow keeps its responsibility control |

## Checks

All heavy commands used `~/.flow-local/heavy.sh`; web commands used Node 24.19.0 because current main's dependencies require it. `npm ci` initially completed under the requested 24.7.0 with engine warnings, exit 0. No dependencies were shared with another worktree.

- `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-desktop-list-states/api ./cmd/server` from `api/`: exit 0. This was only the scratch server build; no Go source changed.
- `npm run typecheck`: exit 0 (also run by full lint).
- `npm run lint`: exit 0, including every foundation/design-token/picker/functional-control/motion check.
- `npx vitest run --maxWorkers=2` with the following focused files: exit 0, 15 files / 130 tests.

```text
src/components/issue-explorer/focus-paged.test.tsx
src/components/issue-explorer/issue-board.test.tsx
src/components/issue-explorer/issue-explorer-page.test.tsx
src/components/issue-explorer/issue-explorer-surface.test.tsx
src/components/issue-explorer/issue-view-creation.test.tsx
src/components/issue-explorer/paged-issue-list-cache.test.ts
src/components/issue-explorer/paged-issue-list.test.tsx
src/components/issue-explorer/saved-view-editing.test.tsx
src/components/issue-explorer/status-group-lists.test.tsx
src/components/issue-explorer/saved-view-panels.test.tsx
src/components/project-detail/project-issues.test.tsx
src/components/triage/triage-page.test.tsx
src/components/workspace-directory/member-profile-page.test.tsx
src/i18n/view-filters-coverage.test.ts
src/lib/route-preload.test.ts
```

- `npx playwright test -c /tmp/codex-desktop-list-states/playwright.config.cjs`: exit 0, four desktop specs, one per feature. They verify hide/restore plus reload, real filter recovery plus reload, default details and persisted toggle plus phone resize, and enabled triage selection/acceptance with queue/sidebar count changing from 3 to 2 followed by disabled-state verification.
- `node /tmp/codex-desktop-list-states/measure.cjs before` and `after`: exit 0; JSON at `before.json` and `after.json` in the scratch directory.
- `node /tmp/codex-desktop-list-states/measure-phone.cjs before` and `after`: exit 0; all seven hidden-panel element rectangles and computed styles match exactly at both phone sizes.
- Four supplied mobile gates against worktree Vite at port 5442, proxied to the shared API, exit 0. Output verbatim:

```text
mobile-gate issues-list / team-list: 66 element checks, 0 failures
mobile-gate issues-list / my-assigned: 46 element checks, 0 failures
mobile-gate shell / topbar-myissues: 36 element checks, 0 failures
mobile-gate inbox / inbox-list: 44 element checks, 0 failures
```

The extra `issues-list --screen team-board-scrolled` gate exits 1 before measurement: its setup tries to click the now-hidden phone Display options Groups section (`Hide group Backlog`). This is a stale setup on current main, not a claimed passing gate. Its reference/locator files were not modified. The direct clean-main versus changed-build phone comparison above verifies the touched hidden panel without that setup.

## Reviewer Clicks

The scratch workspace is `[audit] Verify`, team `[audit] Queue` (`VER`), and all created issues/views have `[audit]` titles. The API/database are at `/tmp/codex-desktop-list-states/api` and `scratch.db`; temporary servers are stopped after verification. Use the retained scratch data with a restarted scratch API/Vite, or follow the equivalent route in a throwaway workspace on a running instance.

1. Board: `/verify/team/VER/all`; Display options > Board > Show empty columns, close the popover, open a column's menu > Hide column. Click its hidden row to restore, then reload to check persistence. Hide all but Todo to see the panel docked at x=1081.5.
2. Filter empty: `/verify/team/VER/all`; choose List, Add filter > Priority > Urgent (the three test-list issues are High). Click Clear Filters in the empty card; the three issues return and remain after reload.
3. Saved details: `/verify/view/audit-desktop-view-view-1793415411846060000-kxr8f0esqx`; the panel starts open. Click No assignee to filter, Clear in the filter band to restore, then toggle Close/Open view details and reload each time. Desktop preference is separate from phones.
4. Triage: `/verify/team/VER/triage`; verify sidebar count and the two panes, click a queue row, then Accept issue from triage > Accept. The desktop spec uses its own new workspace and verifies the count decrement without changing the retained screenshot fixture.

## Remaining Differences

- Existing Flow-only controls remain, including Insights, Drafts, Loops and triage responsibility.
- The saved summary retains its existing three tabs; adding a Teams tab and estimate totals was outside the requested visibility/owner/assignee feature. Metadata's lock glyph remains the existing outline icon.
- The triage no-selection illustration remains Flow's existing vector, not the reference illustration. The enabled queue, navigation count, selection and acceptance are functional; the disabled state is untouched.
- The explorer's outer frame is still 28px taller than the audited frame; this shared-shell difference was not changed. The targeted widths and empty-state block are within tolerance.
- No API, production host, production database, container, real-model provider, or user browser was changed.
