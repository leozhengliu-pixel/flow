# Desktop Projects P1/P2

Rechecked baseline `4c0010f1`, using `/tmp/desktop-ref/projects.md`. Bundled Chromium: 1440 x 900, `--force-device-scale-factor=2`, no context deviceScaleFactor; measured `[1440,900,2]`. Raw evidence: `/tmp/codex-desktop-p12-2/projects.json` and `shots/projects-*.png`. Repeated effects of one saved display option are grouped rather than counted twice.

Display preferences were reproduced through Flow's controls, not encoded as new defaults: No grouping, Manual ordering, Summary off, Milestones on, Timeline -> Year. The same existing project remains visible through those changes. New-workspace defaults, saved personal choices and workspace defaults are distinct sources of state; a captured personal choice is not proof of a different product default. The browser regression now explicitly exercises these transitions (`0c687c93`).

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| 1: Health/Priority/Lead column offsets | Fixed already | Current health cell x809.5, header text x815.5; priority/lead tracks use the corrected grid. | `4c0010f1` |
| 1: Target/Issues/Status offsets | Fixed already | Correct declared tracks, intrinsic labels; Issues x1242.5 and Target control x1145.5. | `4c0010f1` |
| 1/2/3: Grouped band and displaced project rows | Not applicable | Selecting No grouping removes the band in both list and timeline, on the same project. No per-row y offsets or new defaults. | `0c687c93` |
| 1: Missing milestone chip | Fixed already | Actual Alpha milestone is visible with a 16px diamond; property toggle hides/restores it. | `4c0010f1` |
| 1/2: Extra summary | Not applicable | Summary is a persisted display property. Turning it off removes the sub-line without changing project data or phones. | `0c687c93` |
| 1: Health control height/type | Fixed already | 24px, 12/500/normal control. | `4c0010f1` |
| 1: Unset target-date placeholder | Fixed already | 28px calendar-only target; clicking opens the real date picker. | `4c0010f1` |
| 1: Extra progress sparkline | Fixed already | Desktop row omits the sparkline and uses compact status/percentage text. | `4c0010f1` |
| 2: Display-menu width/placement/surface | Fixed already | 331px menu with corrected right inset, 12px radius and menu surface. | `4c0010f1` |
| 2: Manual versus Name ordering | Not applicable | Selecting Manual reproduces the reference choice and removes the ordering-direction control; persisted ordering remains user-controlled. | `0c687c93` |
| 2: Extra grouping arrows/sub-grouping | Fixed already | No grouping omits both its direction and Sub-grouping. Browser asserts absence after selecting the matching state. | `4c0010f1` |
| 2: Show empty groups | Fixed already | Rendered only while grouping is active; absent in the matching ungrouped state. | `4c0010f1` |
| 3: Timeline Today/Zoom controls | Fixed already | Compact Today and a real zoom menu replace the five exposed zoom buttons. | `4c0010f1` |
| 3: Quarter versus Year density | Not applicable | Zoom is stored display state; choosing Year changes the same timeline and survives reload. | `0c687c93` |
| 3: Narrow project rail | Fixed already | 320px rail starting at the content boundary. | `4c0010f1` |
| 3: Month-cell density | Fixed already | Year zoom uses the existing year-scale track; dates and today offset remain real data. | `4c0010f1` |
| 3: Missing day ticks | Fixed already | Secondary numeric axis present; browser asserts day-number text. | `4c0010f1` |
| 3: Short/misplaced project row | Fixed already | 72px timeline row; first ungrouped row starts at y128. | `4c0010f1` |
| 3: Clipped project-bar label | Fixed already | Desktop bar is unlabelled; actual dates determine its length. Final dated fixture visibly renders the bar. | `4c0010f1` |
| 3: Quarter header vocabulary | Fixed already | Year layout displays months and the calendar year, not quarter headings. | `4c0010f1` |
| 4/5: Extra Insights and tool-position cascade | Not applicable | Explicit retained Flow feature; no artificial offsets cancel its actual width. | `4c0010f1` |
| 5: Empty project artwork | Fixed already | Four status-circle illustration, not one dashed ring/document glyph. | `4c0010f1` |
| 5: Empty heading/paragraph placement | Fixed already | 340px intro; heading x468, y454.95 within 1px in the isolated browser fixture. | `4c0010f1` |
| 6: Missing activity month divider | Fixed already | Posting a real project comment shows the month heading; browser test and populated activity screenshot. | `4c0010f1` |
| 8: Initiative Name/Status/Priority columns | Fixed already | Corrected directory grid and normal header type. | `4c0010f1` |
| 8: Initiative Owner/Target/Projects columns | Fixed already | Corrected tracks; no wrapping of the Projects fraction. | `4c0010f1` |
| 8: Extra Lead team column | Fixed already | Not a default directory column; remains an explicit optional display property. | `4c0010f1` |
| 8: Missing project-count ring | Fixed already | 16px progress ring and unbroken `0 / 1` in the linked-project fixture. | `4c0010f1` |
| 9: Missing initiative sidebar and shifted title/properties | Fixed already | 691px main column at x292.5 and docked Properties/Activity rail; sections collapse and reopen. | `4c0010f1` |
| 9: Extra main-row team fields | Fixed already | Team fields no longer displace the main property row; accessible in the sidebar/overflow. | `4c0010f1` |
| 9: Resources/Description shifts | Fixed already | Same corrected main column; real saved update controls downstream vertical position. | `4c0010f1` |
| 9: Missing latest update/false empty state | Fixed already | Saved update displays its real body, health, author and actions; empty prompt absent. | `4c0010f1` |
| 9: Project-table column/row offsets | Fixed already | Compact main-column table below the actual update; row position is not forced to a fixture's update height. | `4c0010f1` |
| 9: Initiative browser title | Fixed already | Initiative name plus Overview appears in the document title; browser assertion. | `4c0010f1` |
| 10: Missing team context in Cycles | Fixed already | Team breadcrumb and Cycles are both present. | `4c0010f1` |
| 10: Empty-cycle heading | Fixed already | Enabled team with auto-create off shows the measured 20px empty message, not the disabled-feature onboarding screen. | `4c0010f1` |
| 10: Empty Cycles sidebar row | Fixed already | Enabling cycles without any cycles does not add an empty sidebar link. | `4c0010f1` |
| 11: Releases workspace entry | Fixed already | Standard sidebar keeps Releases under More rather than always painting a top-level row; custom sidebar preferences remain usable. | `4c0010f1` |
| 11: Release plan gate and associated search/empty-state chrome | Not applicable | Flow has no paid plans. No upgrade card, billing navigation or unavailable paid gate was restored. | `4c0010f1` |

Settled reference check: the title/summary editors do not initially mount in a background tab. After read-only focus emulation and waiting for them, Properties/Resources/Latest update/Description measured y287/327/379.75/563. Flow measured y286.5/326.5/379/562. The audit's earlier y232/272/324.75/508 omitted that mounted title block; no 55px compensating offset was added.

Counts: fixed already 33; fixed 0; not applicable 6; still open 0. No product decision required.

Verification: full `npm run lint` (includes typecheck) exit 0; 35 tests across four targeted project Vitest files exit 0; all three `desktop-projects-parity` browser tests exit 0. Only the browser regression and this report changed in this area; no product rule, default, API, phone stylesheet or phone state was changed, so there is no new shared phone rule requiring a mobile gate.

Screenshot review: every project PNG was opened. List/display show the same project after matching display choices; timeline shows year/month/day axes, a 320px rail and a dated bar; overview shows the properties rail and real milestone; Issues shows the four-circle empty intro; Activity shows an actual comment and month section; Initiatives shows the compact columns/progress ring; initiative overview shows its persisted update and linked project; Cycles shows the enabled, empty state. The earlier undated timeline and disabled-cycle onboarding captures were not treated as evidence for a bar or enabled empty-state parity; fixtures were corrected and those images replaced.

Reviewer: `/W/projects/all` -> Display options -> No grouping, Manual, Summary off; toggle Milestones. Select Timeline -> Year, reload, then Center timeline on today. Open `/W/project/P/overview`, then Issues and Activity. Open `/W/initiative/N/overview` with a saved update, collapse/reopen Properties and open its Update action. Enable cycles with auto-create off in a throwaway team and open `/W/team/T/cycles`. Scratch project is `compare-test-ed7555dbe21a`; initiative is `delivery-goals-d1cb024344df` in workspace `desktop-p12-1793532276117`.
