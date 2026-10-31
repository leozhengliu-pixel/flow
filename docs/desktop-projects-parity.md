# Desktop Projects Verification

Measured at 1440 x 900, DPR 2, dark theme, using bundled Chromium. Reference evidence is in `/tmp/desktop-ref/projects/`. Fresh-main baseline was `857039d8`; subsequent phone changes were rebased before pushes. No API contract or database migration changed.

## Comparison State

The evidence does not establish fresh-account defaults. Flow therefore retains Status grouping, Name ordering, Summary visibility and Quarter zoom. Comparisons explicitly select No grouping, Manual, Summary off and, for the timeline, Year on the Flow side. The initiative list comparison explicitly hides Lead team. These are browser-session display choices, not application defaults or changes to workspace defaults.

The reference files are not all the same settled state. In particular, the initiative JSON precedes title/summary hydration: Properties is at y=232, while its PNG has the settled title block and Properties at y=287. The following initiative rows use the settled PNG position, with the original JSON position shown where relevant. Likewise, the reference list and display-popover captures have different column allocations; the list uses the list JSON coordinates.

The reported 363px initiative sidebar is its inner content width, not the outer rail. Raw reference evidence has an outer rail `(1031.5,96,400,767.5)`, a 388px card at x=1035.5 and 363px inner content at x=1048. Flow uses the same 400px rail instead of incorrectly shrinking the entire rail to 363px.

## Already Fixed On Main

- The project overview already had its two-column layout, Properties/Milestones/Activity rail, 24px/600 title and 15px summary. The missing desktop structure was on initiative detail, not project detail.
- Project row menus were already hover/focus-only. The status percentage already rendered without an inserted space.
- Sub-grouping, group direction and Show empty groups were already conditional on grouping; ordering direction was already absent with Manual ordering. Matching display settings removes these apparent menu gaps without deleting controls.
- The initiative update component and project month headings already existed for phones but were hidden on desktop. These are now also styled on desktop.
- Initiative details chrome and its rail already existed, but the rail started closed and used the wrong desktop allocation. Its desktop default and allocation are corrected while saved choices remain respected.

## Measurements

Rectangles are `(x,y,width,height)` in CSS px. Before measurements are from this worktree after the initial main rebase. Audit-only baselines are explicitly marked. Subpixel rasterization can produce a 0.5px coordinate or 1px width difference from the reference browser.

| Surface / Element | Reference | Before | After |
| --- | --- | --- | --- |
| Project row, same flat settings | `(244.5,128,1187,48)` | `(245,164.5,1186,48)`, including an incorrect All projects band even with No grouping | `(245,128.5,1186,48)` |
| List milestone | 16px diamond and Alpha | hidden by CSS | 16px diamond and Alpha; chip `(459.89,144.5,52.69,16)` includes the wider `[audit]` project name before it |
| Health / Priority / Lead header x | `815.5 / 951.5 / 1019.5` | `868 / 1004 / 1072` | `815 / 951 / 1019` |
| Target / Issues / Status header x | `1151.5 / 1242.5 / 1303.5` | `1138 / 1230 / 1291` | `1151 / 1242 / 1303` |
| Health button | `(809.5,140,106.9,24)`, 12px/500 | `(862,174.5,101.58,28)`, 12px/450 | `(809,140.5,105.88,24)`, 12px/500 |
| Empty target date | `(1145.5,138,28,28)` | `(1132,174.5,92,28)` | `(1145,138.5,28,28)`, calendar icon, working picker |
| List Summary / sparkline | off / absent | summary ignored its toggle; sparkline present | Summary follows display setting; desktop sparkline absent |
| Display surface | `(1088.5,92.5,331,430)` | audit `(1068.5,92.5,330,486)` | `(1088,93,331,471)`: 430px shared content plus the existing 41px modified-view footer |
| Display mode widths / gap | `99.3 / 6` | `94 / 8` | `99.33 / 6` |
| Display List options text | `(1105,290,298,14.5)` | audit section-sized heading | text starts `(1104,289.75)`; normal line height |
| Display properties text | `(1105,314.5,298,14.5)` | audit oversized line box | text starts `(1105,314.25)`; normal line height |
| Timeline project label | 320px, y=128, 72px row | 240px, y=212, 36px row | `(245,128.5,320,72)` with No grouping |
| Timeline header | y=96, secondary day-number axis | toolbar adds 36px; no day axis at this scale | y=96.5, 32px two-tier month/day header |
| Timeline Today / Year | `(1308.5,101,52.1,24)` / `(1364.6,101,56.9,24)` | five 27-28px controls | two 24px controls, fixed widths `52.1 / 56.9`; all zoom levels remain available in the menu |
| Timeline Year density | about 75px/month | Year 1.2px/day, Quarter 3.5px/day | Year 2.5px/day; 30-day month 75px; localized month labels, January year context, fortnight day ticks |
| Timeline bar label | outside, above the bar | clipped inside short bar | outside, above; drag/resize/dependencies retained |
| Project main column | x=292.5, width=691 | x=293, width=690 | unchanged structural parity |
| Project Properties row | `(292.5,287,67.73,28)` | narrow label column | `(293,287,67.72,28)` |
| Project Initiatives / Resources / Customers y | `327 / 367 / 407` | `329 / 371 / 413` | `327 / 367 / 407` |
| Project Description y | `554` | `560` | `554` |
| Project sidebar milestone rows | compact rows; Alpha y=524.3 | 42px rows; Alpha +2.5px; Activity +12.2px | 36px rows and compact header; Activity and Alpha rhythm corrected |
| Empty Issues illustration | `(468,350.95,86.5,80)` | unrelated document/ring artwork | `(468,350.69,86.5,80)`, four-status artwork |
| Empty Issues heading | `(468,454.95,340,23)` | audit `(484,317.5,308,21)` | `(468,454.69,340,23)` |
| Empty Issues first paragraph | `(468,485.95,340,18.2)` | audit x=484, y=345.5 | `(468,485.69,340,18.2)` |
| Empty Issues second paragraph | two lines at 340px | 308px column | `(468,519.89,340,36.41)` |
| Empty Issues create button | x=468, y=580.54, 12px/500 | audit y=428.11, 13px/450 | x=468, y=580.30, 12px/500; outlined shortcut key |
| Issues Filter / Display / rail tools x | `1327.5 / 1361.5 / 1395.5` | `1293.5 / 1327.5 / 1395.5` | `1327 / 1361 / 1395`; Insights preserved at x=1293 |
| Activity month heading | 18px/500, between month groups | desktop display:none | visible, localized 18px/500; browser-tested with a real persisted comment |
| Initiative list Name / Status / Priority header x | `296.5 / 814.5 / 926.5` | audit `284.5 / 756.5 / 862.5` | `297 / 814 / 926` with matching properties |
| Initiative Owner / Target / Projects header x | `1000.5 / 1056.5 / 1174.5` | audit `924.5 / 1086.5 / 1192.5` | `1000 / 1056 / 1174` with matching properties |
| Initiative Health / Active Projects header x | `1239.5 / 1309.5` | audit `1257.5 / 1315.5` | `1239 / 1309` |
| Initiative Projects count | `(1174.5,142,57.4,24)`, progress icon, single-line count | audit `(1164.5,142.5,35.4,24)`, wrapped count, no icon | `(1174,143,57.39,24)`, icon and single-line count |
| Initiative title | x=292.5, width=691, 24px/600 | `(436,204.5,804,32)`, 23px/500 | `(293,208,690,32)`, 24px/600 |
| Initiative summary | x=292.5, 15px | `(436,240.5,804,23)`, 14px | `(293,244,690,23)`, 15px |
| Initiative Properties / Resources y | settled `287 / 327`; JSON `232 / 272` | `273 / 313`, wrong horizontal allocation | `287 / 327`, x=293 |
| Initiative Latest update card | settled `(276.5,371,723,170)`; JSON y=316 | hidden; false empty-state button `(420,363,836,66)` | `(277,371,722,170.5)`; health, author, time, body, comments, reactions and Update action |
| Initiative Description y | settled `563`; JSON `508` | `465` | `563.5` |
| Initiative project columns x | `292.5 / 621.5 / 677.5 / 745.5 / 911.5` | audit `437 / 744 / 859 / 949 / 1134` | `293 / 621 / 677 / 745 / 911` |
| Initiative project header text y | settled `841.25`; JSON `786.25` | audit `772.5` project row | `841` header text, normal line height |
| Initiative right rail | outer `(1031.5,96,400,767.5)` | closed, single 804px content column | outer `(1031,96,400,767)`; Properties and Activity first, Flow Progress/Teams below |
| Initiative browser title / breadcrumb | initiative name and tab / name only | workspace name / extra parent crumb | initiative name and translated tab / desktop parent crumb hidden |
| Cycle empty message | `(725.3,445.8,225.4,24.5)`, 20px/500 | audit `(761.8,462.2,152.3,19.5)`, 13px/450 | `(723.36,446,229.28,24)`, 20px/500 |
| Cycles team context / sidebar | team breadcrumb / no empty Cycles link | no breadcrumb / always-visible enabled link | team breadcrumb restored; desktop link appears once the team has cycles; phones/tablets unchanged |

Full computed styles and exact rectangles, including menu items, are in `/tmp/codex-desktop-projects/final-measurements.json`. Baseline measurements are in `before.json` in the same directory.

## Retained Differences And Limits

- Saved grouping, ordering, summary and zoom preferences are not evidence of fresh-account defaults. They were not copied into application defaults. Lead team remains an available initiative-list property; matching settings removes it for the comparison.
- The modified-view footer is functional Flow behavior, not unexplained popover height. Removing it would remove Reset and Set default for everyone.
- Flow Insights, Progress, Teams, Drafts, Loops and agent features remain available. Shared project toolbar controls keep the reference positions; initiative-specific extra cards follow Activity.
- Releases is intentionally ungated in Flow. The reference evidence shows only a paid-plan gate, not a releases list. No reference billing entitlement, navigation restriction or plan configuration was encoded.
- Data differences include `[audit]` names, dates, authors, icons/colors, resource contents and history. Timeline bar start/end positions follow actual dates, not the reference account's schedule.
- Some P3 glyphs and text metrics remain different: milestone menu icons/shortcut rendering, the cycle message's 3.88px wider text metric, and the No updates button's approximately 1px width difference. The six milestone commands remain present and functional; the shortcut legend follows the actual command rather than the audit's conflicting single-key description. These are not missing features.
- The reference PNGs and JSON have documented state/timing differences. No claim of a zero-pixel screenshot diff is made.

## Screenshots

All crops use `desktop-compare.cjs`, reference above Flow, and were opened. Files are under `/tmp/codex-desktop-projects/shots/`:

- `list-crop.png`: flat 48px row, milestone, health, target-date control and compact percentage. The audit-name prefix changes the milestone's x position.
- `overview-crop.png`: Properties, Initiatives, Resources and Customers now maintain 40px row rhythm; Description aligns. Resource/name/glyph data differ.
- `display-crop.png`: matching flat/manual display settings, wider layout buttons and corrected row rhythm. The modified-view footer remains.
- `timeline-crop.png`: two controls, month/day axis, 320px overlaid label rail, 72px rows and external bar label. Current day, date range, focus state and dates differ.
- `issues-crop.png`: four-status artwork, centered 340px text column and matching paragraph rhythm.
- `initiatives-crop.png`: matching visible columns, single progress glyph and unwrapped count. Name, owner and health data differ.
- `initiative-crop.png`: settled title block, latest saved update, docked Properties/Activity and retained Flow cards below.
- `milestone-crop.png`: all six commands, corrected text alignment; icon designs and shortcut glyph remain different.
- `cycles-crop.png`: enabled, empty `[audit] Desktop cycles` team; correctly sized and centered message. An initial missing-fixture capture was rejected and replaced, not counted as verification.
- `activity-crop.png`: actual persisted comment with its month divider in an isolated scratch workspace; dates and history differ from the reference September feed.

## Tests And Gates

All heavy commands ran through `~/.flow-local/heavy.sh`, with targeted Vitest files and at most two workers. Go was only built to `/tmp/codex-desktop-projects/server` for the scratch API; no Go source changed.

- `npm run lint` (includes `npm run typecheck` and all five repository checks): exit 0 before each push.
- Targeted project/timeline/detail/sidebar/cycle tests: 110 passed, exit 0. Additional initiative/display/detail regression selection: 78 passed, exit 0. Final touched-component reruns passed.
- `npx playwright test --config /tmp/codex-desktop-projects/playwright.config.cjs`: three tests passed, exit 0, bundled Chromium. The committed spec imports `workspace-fixture` and creates its own workspaces; it does not depend on verify.
- Project mobile geometry gates: all seven exit 0. Project text gates: six exit 0; Overview exits 1 due to the existing parent-versus-leaf reference discrepancy below.
- Common shell, assigned issues, team list and inbox geometry gates: all exit 0.

The Overview text-gate discrepancy is not a phone regression. The reference contains BOTH a white 13.3333px/400 button `(40,644.5,170.13,28)` and its dim 13px/500 inner span `(66,650.75,138.13,15.5)` for Add customer request. Flow's leaf matches that span. The gate also compares the inherited parent-button style to the same leaf and reports one difference. The phone CSS is unchanged from main; changing the visible span to the parent style would introduce a visual regression. The gate and reference expectations were not modified to hide this failure.

Verbatim gate summaries:

```text
mobile-gate projects / projects-list: 42 element checks, 0 failures
text-gate projects / projects-list: 3 shared texts, 9 not shown in Flow there, 0 differences
mobile-gate projects / project-overview: 56 element checks, 0 failures
text-gate projects / project-overview: 17 shared texts, 25 not shown in Flow there, 1 differences
  "Add customer request" y=645: size 13.3333px vs 13px, weight 400 vs 500, lightness 100 vs 37
mobile-gate projects / project-properties: 58 element checks, 0 failures
text-gate projects / project-properties: 26 shared texts, 63 not shown in Flow there, 0 differences
mobile-gate projects / project-activity: 40 element checks, 0 failures
text-gate projects / project-activity: 7 shared texts, 24 not shown in Flow there, 0 differences
mobile-gate projects / project-updates: 42 element checks, 0 failures
text-gate projects / project-updates: 11 shared texts, 26 not shown in Flow there, 0 differences
mobile-gate projects / project-issues: 28 element checks, 0 failures
text-gate projects / project-issues: 6 shared texts, 10 not shown in Flow there, 0 differences
mobile-gate projects / project-milestones: 36 element checks, 0 failures
text-gate projects / project-milestones: 19 shared texts, 18 not shown in Flow there, 0 differences
mobile-gate shell / topbar-myissues: 36 element checks, 0 failures
mobile-gate issues-list / my-assigned: 46 element checks, 0 failures
mobile-gate issues-list / team-list: 66 element checks, 0 failures
mobile-gate inbox / inbox-list: 44 element checks, 0 failures
```

## Review Routes

1. `/verify/projects/all`: Display options, List, No grouping, Manual, Summary off. Inspect the milestone chip and column alignment; toggle Milestones/Summary; open the target-date calendar.
2. The same route: Display options, Timeline; choose Year from Timeline zoom, use Today, reload to verify the chosen zoom persists. Drag/resize a dated bar as before.
3. `/verify/project/audit-compare-test-9c10770b6ca3/overview`: inspect Properties through Description and the right rail; open Alpha actions. Switch to Issues for the centered empty state, and Activity for the composer/feed.
4. `/verify/initiatives`: Display options, turn Lead team off to match reference properties. Inspect the Projects ring/count, then open the initiative.
5. `/verify/initiative/audit-test123-b8ab1f505a11/overview`: inspect the saved Latest update and docked rail, collapse/reopen Properties, click Update or Comments to enter Activity, and inspect the browser title.
6. `/verify/team/DPC/cycles`: inspect the team breadcrumb and enabled-empty message; the empty team has no desktop Cycles navigation row. Releases remains at `/verify/release-pipelines` without a copied paid-plan gate.

Scratch API and Vite processes are stopped after verification, and installed dependencies are removed. Logs, measurement JSON and crops remain under `/tmp/codex-desktop-projects/`.
