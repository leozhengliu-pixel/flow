# Desktop sweep, 2026-11-04

The sweep found two real shared causes: the full issue layout used fitted columns and gutters, and desktop toolbar icon pills inherited the text-tab palette. Both are fixed. Product rules contain no sampled widths or new specificity tricks. The issue grid's container formulas were read from the foregrounded reference stylesheet and checked at three viewport widths.

## Totals and scope

Baseline: `25505250`, the latest `origin/main` when work started. The two cause commits were subsequently rebased onto `9415df60`. The final validation uses that rebased tree.

| Inventory | Before | After |
| --- | ---: | ---: |
| Manifest screens | 128 | 128 |
| Screens with both reference JSON and PNG | 5 | 5 |
| Missing-reference screens, skipped as inconclusive | 123 | 123 |
| Matched elements | 136 | 136 |
| Raw property differences | 17 | 6 |
| Confirmed real differences among those properties | 11 | 0 |
| Raw FAIL / INCONCLUSIVE / PASS | 1 / 127 / 0 | 1 / 127 / 0 |

The other four measured screens remain inconclusive because of low coverage or unreproduced state. The remaining raw FAIL contains two fixture-title offsets. Neither it nor an inconclusive screen is reported as a parity pass. The old 806-difference report is not comparable with this run because most reference pairs have been deleted and main has changed.

Tooling is a private copy under `/tmp/codex-desktop-sweep/tools`. Shared scratchpad scripts and protected reference directories were read only. The private guard skips a screen if either reference file is missing. Comparisons were tightened to 0.02px geometry and 0.01px CSS/colour quantization; no 1px geometry allowance or one-channel colour allowance remains. Assertions were never loosened. Original routes and steps were retained. The inherited runner's printed "128 of 126" counter counts missing-reference records as runs; the table above uses actual measured records from JSON.

## Causes, Biggest First

| Cause | Properties before / after | Decision | Reference versus Flow before / after |
| --- | ---: | --- | --- |
| Full issue document and property-rail grid | 8 / 0 | Real; fixed in `issue-properties-rail.css` | At 1440px: title x 306.414 vs 306 / 306.414; description x 292.414 vs 292 / 292.414; rail x 1007.852 vs 1007.5 / 1007.852; rail width 375.734 vs 376.5 / 375.734; property text x 1040.852 vs 1040.5 / 1040.852. The description appears as both a semantic control and structural match. |
| Desktop toolbar icon-pill palette | 3 / 0 | Real; fixed in shared toolbar rules and tokens | Resting RGB reference [27,28,29] vs Flow [28,28,29] / [27,28,29], on Add filter, Display options and Open details. The measured colour is `lch(10.149 0.689 272)`. |
| Issue breadcrumb followed by favorite/options | 2 / 2 | Data difference | Reference x 457.070 / 491.070 vs Flow 421.781 / 455.781, unchanged. The two apps show different issue titles. No title or data was hard-coded. |
| My issues topbar wrapper | 2 / 2 | Capture/matching artefact | Reference wrapper spans header plus subheader (87.5px high), with padding 0 and gap 12px; Flow's matched header is 44px high, padding 0 8px and gap 0. Leaf controls retain their measured positions. Changing a shared header to match this different wrapper would be wrong. |
| Cycles inferred trailing header padding | 1 / 1 | Data plus capture/matching artefact | The matcher infers right padding 995.77px versus 902.141px after differently sized team names. Actual Flow header padding is 18px on each side. Current foregrounded reference also omits the older team breadcrumb; the fixture shows disabled-cycle education while the reference shows an enabled empty directory. No settings were changed. |
| Agent composer y | 1 / 1 | Data/state difference; exact matching state unavailable | Captured reference y 296.5 has eight recent chats; Flow y 314.75 has starter examples. Current foregrounded reference y 406 has neither list. Composer dimensions agree at 712 by 104px. No offset or reference chat data was fitted into Flow. |

The old sidebar-label colour, More-row padding and generic icon-padding causes produce no failures in the five surviving screens. Live reference confirms section labels `lch(60.621 1.2 272)`, More right padding 9px and toolbar horizontal padding 2px; current Flow already has those rules.

The toolbar inspection additionally found hover/open/selected palette differences outside the resting gate. These share the icon-pill cause: hover fill `lch(14.777 1.075 272)` replaces `lch(14.006 0.593 272)`; open/selected fill `lch(18.634 1.075 272)` replaces `lch(16.706 0.979 272)`; hovered SVG ink becomes `lch(100 0 272)`. Text tabs retain their palette. The rule applies at the shared desktop toolbar control; no display/filter menu implementation was edited.

Cause commits: `14822373` contains the grid fix and its browser regression. The toolbar fix, its regression, tokens and this report are in the second delivery commit (the branch tip).

The full issue grid uses `1fr minmax(0, 80ch) minmax(0, clamp(280px, 26.087cqw + 66.087px, 400px)) 1fr`, with gap `clamp(16px, 8.696cqw - 55.304px, 56px)`. Its 16px grid font establishes the reference's `ch` basis; the document and property UI retain their 13px base. At 1200 / 1440 / 1600px, the document widths are 552.734 / 667.523 / 779px and rail widths 313.125 / 375.734 / 400px. The browser regression allows only 0.02px reference quantization.

## Evidence

All paths below are under `/tmp/codex-desktop-sweep/`. Reference measurements used `bu.sh`, a newly owned tab whose returned id was saved immediately, `Page.bringToFront`, and read-only navigation/inspection. Only the saved owned id was closed. No reference setting, data or view preference was changed.

- Grid crops: `shots/crop-reference-issue.png`, `shots/crop-before-issue.png`, `shots/crop-after-issue.png`. Opened: the document title and description sit beside the property rail; the content differs by fixture. Subpixel improvements are established by measurements, not by claiming the crops look dramatically different.
- Toolbar reference crops: `shots/reference-toolbar-rest.png`, `shots/reference-toolbar-hover.png`, `shots/reference-toolbar-open.png`. Opened: resting icons, hovered filter, and open filter with selected details.
- Toolbar Flow crops: `shots/toolbar-rest.png`, `shots/toolbar-hover.png`, `shots/toolbar-focus.png`, `shots/toolbar-open.png`, `shots/toolbar-selected.png`. Opened: the corresponding states, including the 1px focus ring. Flow's extra Insights control is visible.
- Reference full captures: `shots/reference-cycles.png` shows the enabled empty directory; `shots/reference-subscribed.png` shows two subscribed issues and details; `shots/reference-issue.png` shows the tutorial issue and its unplayed media; `shots/reference-agent.png` shows an empty composer without examples or history. These establish context, not complete parity or media-playback verification.
- Flow baseline captures: `shots/before-issue-detail-description-image.png` shows different tutorial data; `shots/before-issues-list-empty-subscribed.png` shows grouped tutorial issues; `shots/before-issues-list-filter-status-hl.png` shows a list with no reference filter panel; `shots/before-projects-cycles.png` shows disabled-cycle education; `shots/before-shell-tmp.png` shows starter examples. All were opened. The last three expose state differences and are not passes.
- Flow after captures: `shots/after-issue-detail-description-image.png` and `shots/after-issues-list-empty-subscribed.png` show the same fixture content with the shared fixes. Both were opened.
- Theme/language crops: `shots/review-toolbar-dark-en-US.png`, `shots/review-toolbar-dark-zh-CN.png`, `shots/review-toolbar-light-en-US.png`, `shots/review-toolbar-light-zh-CN.png`. All were opened: selected Details remains distinct in each theme and language. State measurements wait 300ms for colour transitions before recording.
- Text and phone review: `shots/review-issue-dark-zh-CN-1440.png` shows a wrapping long title, two CJK initials and a CJK label beside the property rail; `shots/review-issue-light-zh-CN-390.png` shows that title wrapping above phone property chips and activity; `shots/review-phone-dark-zh-CN-375.png` and `shots/review-phone-light-en-US-390.png` show subscribed lists with long text ellipsized beside the label and avatar. All four were opened; no overlap or horizontal overflow was found.
- Failed-attempt evidence: `shots/failed-grid-readiness.png` was taken after a 5-second visibility assertion expired while the app was loading; the page completed loading before the screenshot. The test now waits for the issue view before asserting geometry. `shots/failed-toolbar-token.png` shows the first icon-fill alias resolving to a darker generated-theme token. The palette token was corrected against the live reference, with the assertion unchanged. Both were opened and are failure evidence, not successful verification.

Raw evidence: `before.json`, `final.json`, `reference-issue-layout.json`, `reference-issue-widths.json`, `reference-toolbar.json`, `reference-toolbar-states.json`, `reference-toolbar-hover-values.json`, and `verification.json`.

## Validation

Every heavy command used `~/.flow-local/heavy.sh`. Vite used a free private port. Browser regressions used bundled Chromium and a scratch API on private ports 18571/18572, with `reuseExistingServer:false`; review data uses a separate scratch API on 18592. The fixture API on 18200 was used read-only for the supplied gates.

- `GOMAXPROCS=2 go build -p 1 -o /tmp/codex-desktop-sweep/server ./cmd/server`: exit 0. No Go product changes.
- `npm run lint`: exit 0, including `npm run typecheck` and all foundation, picker, token, functional-control and motion checks.
- `npx vitest run --maxWorkers=2 src/components/content-view/content-view.test.tsx src/components/detail/detail-feature-gates.test.tsx src/lib/issue-detail-boot.test.ts`: exit 0, 15 tests.
- `npx playwright test e2e/desktop-detail-grid.spec.ts e2e/desktop-toolbar-pills.spec.ts --config playwright.desktop-sweep.local.ts`: exit 0, 2 tests. The temporary config is removed before delivery.
- Desktop gate on all 128 manifest entries: exit 1 before and after, because raw data/structure differences and inconclusive coverage remain. Measured differences 17 to 6; confirmed real differences 11 to 0. The affected issue screen was separately rerun, 10 to 2 differences, exit 1 for the two fixture-title offsets.
- Phone gates, using the exact supplied preload command without `--seed`: issue-detail `top` 58 checks, `sub-issues` 28, `activity` 34; issues-list `my-subscribed` 36. Each exit 0; 156 checks, zero failures. Both 375px and 390px are included.
- `node /tmp/codex-desktop-sweep/review-session.mjs`: exit 0. Checked all four light/dark and English/zh-CN combinations at 1440x900, 375x667 and 390x844, with short and long titles, a long Latin assignee name, two CJK avatar initials and a CJK label. Toolbar resting, hover, focus-visible, open and selected states were checked. The toolbar exposes no disabled state; the issue screenshots include the disabled empty-comment send action. A second review run settled colour transitions before collecting values; it also exited 0.
- Initial attempts: API compilation exceeded the browser runner's 120-second startup deadline (exit 1); API was built separately. A subsequent issue readiness failure and the first toolbar token failure also exited 1, were inspected, and were corrected without weakening geometry or colour assertions.

Owned API/Vite processes were stopped, installed `web/node_modules` was removed, and only the private temporary browser config was deleted. Captures, scratch databases, exact commands and logs remain under `/tmp/codex-desktop-sweep/` for review.

## Reviewer Clicks

1. Open `/<workspace>/issue/<identifier>`. Resize to 1200, 1440 and 1600px. Check the title, description, and Properties rail together. Use a short title, a wrapping long title, a long assignee name, and two CJK characters in a person avatar or label.
2. Open `/<workspace>/my-issues/subscribed`. Inspect Add filter, Display options and Details at rest; hover Add filter; Tab to it for the focus ring; open and close its menu; open Details for selected paint. Repeat in light/dark and English/zh-CN, then at 375px and 390px.

The workspace menu, agent skills pages, issue-explorer menu implementations, board files, avatar props, estimate icon and other explicitly owned files were not edited. No PR was opened. Missing reference screens and the unreproduced reference states remain undone; no whole-desktop parity claim is made.
