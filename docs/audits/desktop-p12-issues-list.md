# Desktop Issues List P1/P2

Baseline `5009a616`; inventory `/tmp/desktop-ref/issues-list.md`. Repeated observations and downstream shifts are grouped by root item. Bundled Chromium ran at 1440 x 900 with `--force-device-scale-factor=2`, no context deviceScaleFactor; measured `[1440,900,2]`. Scratch API/database and screenshots: `/tmp/codex-desktop-p12-2/`; measurements: `issues-list.json`.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| 1: Team breadcrumb chip/switcher | Fixed already | Padded, rounded team chip with live team navigation; topbar browser spec. | `5009a616` |
| 1/8: Favorite control | Fixed already | 28px control; saved-view position follows the actual title rather than a sampled title width. | `5009a616` |
| 1: Notification bell | Fixed already | Real header notification control beside New issue. | `5009a616` |
| 1/8: Header New issue | Fixed already | Present on team and saved views; opens creation. | `5009a616` |
| 1: Filter icon tone | Fixed already | Desktop foreground token, not faint text. | `5009a616` |
| 1/2/8: Extra Insights control and position cascade | Not applicable | Explicit retained Flow feature; no compensating offsets hide its actual space. | `5009a616` |
| 1: Toolbar gaps | Fixed already | 6px action spacing. | `5009a616` |
| 1/2: Group surface | Fixed already | 36px rounded band using the measured group surface token. | `5009a616` |
| 1/2: Group disclosure/status glyph | Fixed already | Filled 16px dim caret and 14px status glyph. Raw reference SVG rows place these at x262.5 and x287.5; the notes incorrectly call the caret a status glyph. | `5009a616` |
| 1/2: Group count/estimate | Fixed already | 13/450 count; estimate-enabled groups expose the real estimate total. | `5009a616` |
| 1: Identifier column | Fixed already | Intrinsic max-content identifier width instead of a fixture-sized block. | `5009a616` |
| 1: Status/title anatomy | Fixed already | 16px status slot, 14px glyph, normal 13/500 title; browser checks the 9px glyph-to-title gap. | `5009a616` |
| 1/2/3: Estimate pill | Fixed already | Desktop estimate is inline, no painted pill. | `5009a616` |
| 1: Sub-issue progress | Fixed already | Compact unspaced `0/1`, nested issue connector. The raw reference screenshot itself has a progress pill, so it was not removed on the basis of the contradictory prose. | `5009a616` |
| 1/2: Right-edge metadata count | Fixed already | Actual relation/review metadata renders compact count pills; screenshot shows the real blocking count. | `5009a616` |
| 1: Frame height | Fixed already | 856px framed surface with bottom Agent area, not 884px. | `5009a616` |
| 1/8: Missing nested children | Fixed already | Enabling Nested sub-issues produces 24px indentation and elbow; saved-view fixture persists it. | `5009a616` |
| 2: Extra Shared tab | Fixed | Standard desktop navigation has Assigned, Created, Subscribed, Activity. Direct shared routes remain usable; phone overflow is unchanged. | `3be7407c` |
| 2: Created grouping | Not applicable | Display preference, not a view-specific layout defect: selecting No grouping through Display options produces the same flat list on the same issues. Browser test waits for rows before checking. | `3be7407c` |
| 2: Right cluster added label fields | Not applicable | Display properties and label data: the matching fixture has no selected labels and shows no label pill; estimates render inline. | `5009a616` |
| 3: Board columns/header | Fixed already | 340px tracks with 8px separation and the measured header treatment. | `5009a616` |
| 3: Board card surface/padding | Fixed already | Correct surface and 8px padding; card height follows actual visible properties. | `5009a616` |
| 3: Hidden columns | Fixed already | Hide/restore controls, right-hand hidden panel and persisted visibility; isolated browser test. | `5009a616` |
| 4: Missing Team/Estimate/Suggested label filters | Fixed already | All three are real filter categories and visible in the tested root menu. | `5009a616` |
| 4: Additional root filter categories | Fixed | Compact desktop root; Triage Intelligence, Releases and Auto-closed remain available in Advanced filter. Phone root unchanged. | `3be7407c` |
| 5: Status submenu width | Fixed already | Menu uses content-aware 175px minimum; the captured width follows the actual status set rather than a sampled label width. | `5009a616` |
| 5: Applied-filter band | Fixed already | Inset rounded filled band, actual border and 24px chips. | `5009a616` |
| 5: Filter-chip height/type | Fixed already | 24px segments, 12px text; targeted row/filter specs. | `5009a616` |
| 5: Clear/Create view commands | Fixed already | Clear control and Save action create a real filtered view, then persist sidebar filters. | `5009a616` |
| 5/13: Filter-empty illustration | Fixed already | 153 x 90 stack artwork, not the small generic glyph. | `5009a616` |
| 5/13: Filter-empty heading | Fixed already | Full wording, 15/600 and muted colour. | `5009a616` |
| 5/13: Hidden-count/Clear Filters card | Fixed already | Counts real filtered-out issues; Clear Filters restores them without losing team scope. | `5009a616` |
| 6: Display-menu anchor | Fixed already | End-aligned to its trigger; any Insights offset remains real, not fitted away. | `5009a616` |
| 6: Display select widths | Fixed | Live settled reference list declares 116px controls. Board capture's 156.8px boxes/23.52px heights are the 0.98 entrance scale of 160px/24px controls; no 156.8px fitting. | `408dd04e` |
| 6: Additional property chips | Fixed | Desktop starts with the reference inventory; a selected saved SLA/Release/activity property stays reachable for deselection. Phone choices unchanged. | `3be7407c` |
| 6: Show sub-team issues | Fixed already | Toggle changes real sub-team membership in results and survives saved defaults. | `5009a616` |
| 7: Extra Dashboards tab | Fixed | Desktop view-directory resource tabs are Issues and Projects; other routes and phone navigation are retained. | `3be7407c` |
| 7: Directory group surface | Fixed already | Inset 36px filled group band. | `5009a616` |
| 7: Saved-view name/owner layout | Fixed already | One-line name without the redundant All issues subtitle; real owner metadata. | `5009a616` |
| 8: Favorite/options x positions | Not applicable | Reproduced with title All issues: controls follow its intrinsic width. Different titles explain the old 42.5px observation. | `5009a616` |
| 8: Default-open details | Fixed already | Docked sidebar opens with visibility, owner and breakdown; toggle persists across reload. | `5009a616` |
| 9: New-view card surface | Fixed already | Filled, bordered, rounded card; no transparent replacement. | `5009a616` |
| 9: Description layout | Fixed already | Full-width second grid row; 32px pitch from name and 1px relative inset. Stale integer-coordinate test now asserts the declared grid relationship. | `3be7407c` |
| 9: Create view button | Fixed already | Existing primary accent and intrinsic 8px inline padding. | `5009a616` |
| 10: Triage nav/queue | Fixed already | Enabling triage shows its real count, 400px queue and 787px detail pane; accept, decline, duplicate and snooze persist. | `5009a616` |
| 12: Context-menu geometry/surface | Fixed | 175px minimum, pointer anchor and viewport-based available height instead of an arbitrary 617px cap. Raw reference screenshot paints a menu surface/border, contrary to the inner-listbox comparison in the notes. | `3be7407c`, `408dd04e` |
| 12: Missing Convert to/Move | Fixed already | Real conversion and team-move submenus appear when conversion/target teams are available; direct browser assertions. | `5009a616` |
| 12: Extra Estimate/Remove entries | Not applicable | Data/configuration: unset estimate scale omits Estimate; a record with no parent/relations omits Remove. Matched record shape reproduces the same inventory. | `5009a616` |
| 12: Extra subscription entry | Fixed | Desktop row menu omits it; issue-page subscription and phone row actions remain usable. | `3be7407c` |
| 12: Group context menu | Fixed already | Collapse, Collapse all, Select all in group, Hide group; bulk Actions and persisted hide verified. | `5009a616` |

Counts: fixed already 38; fixed 7; not applicable 5; still open 0. No product decision required.

Verification: full lint/typecheck exit 0; 57 tests across nine targeted Vitest files exit 0; eleven focused browser tests exit 0, including the changed menu spec rerun after the last control edit. No API or Go files changed. The phone team-list gate also passed 66 checks with zero failures.

Phone gate: 580 checks, two failures. Temporarily restoring all six desktop rendering/CSS edits reproduced exactly the same failures (40 board checks), then the desktop edits were restored. No phone offset was changed to satisfy these baseline coordinates:

```text
mobile-gate issues-list: 580 element checks, 2 failures
  FAIL 375 team-board.column-menu x: want 224 got 218.00
  FAIL 390 team-board.column-menu x: want 224 got 218.00
```

Screenshot review: opened all nine `issues-list-*.png` images. Team shows real rows/counts; Assigned shows the four tabs and actual empty assignment state; Created was recaptured after selecting No grouping; Views shows the two resource tabs and real saved view; filter shows the compact root; display shows complete controls; context shows actual conversion/team actions without subscription; new-view shows the card and full description; saved-view shows the docked details and nested child. A first context capture exposed the old height cap clipping Delete; that was fixed and recaptured rather than reported as a pass.

Reviewer: `/W/my-issues/created` for four tabs, then Display options -> Grouping -> No grouping. `/W/team/T/all` -> Add filter; Advanced filter retains the additional fields. Display options -> Board for wider selectors and the compact property inventory. Right-click an issue in a workspace with two teams for Convert to and Move, without Subscribe. `/W/views/issues` for two resource tabs; New view for the corrected editor. `/W/view/V` for default-open details and nested children.
