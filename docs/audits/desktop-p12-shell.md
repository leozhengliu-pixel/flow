# Desktop Shell P1/P2

Baseline: main `462bd76d`. Checked at 1440 x 900 in bundled Chromium with `--force-device-scale-factor=2`, no context scale option, and asserted `[1440,900,2]`. Grouped summary entries in `/tmp/desktop-ref/shell.md` are separated below when they have different outcomes; cascaded positions are not counted again.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| P1 top-bar New issue | Fixed already | Working 28px primary action in team Issues header. | `77875bfa` |
| P1 command query buries commands | Fixed already | Project commands precede optional entity quick results for pro. | `a2672c5b` |
| P1 six missing General shortcuts | Fixed already | Save/submit, comment, Back, Help, workspace switch and logout are listed and wired. | `9c3bb22b`, `b5c4a23e` |
| P2 workspace-name typography | Fixed already | 13/550/23, declared -0.1px tracking. | `5a7a1502` |
| P2 section-header typography | Fixed already | 12/500 with 14.5px text line. | `5a7a1502` |
| P2 extra Drafts | Not applicable | Retained Flow feature. | `5a7a1502` |
| P2 extra Loops | Not applicable | Retained Flow feature. | `5a7a1502` |
| P2 missing Triage/Cycles/Current/Upcoming | Not applicable | Same-data browser reproduction enables triage and automatic cycles in the owned workspace; all corresponding links appear. Current/upcoming navigation works. Defaults with no enabled cycle data legitimately omit them. | `5c90326e` browser proof |
| P2 plan pill/footer action | Not applicable | Flow has no paid plans; no plan badge, upgrade or billing action restored. | `4c579d06` |
| P2 footer container/help placement | Fixed already | 244 x 44 footer, 10px padding, help x10. | `5a7a1502` |
| P2 collapsed section gaps | Fixed already | 8px between collapsed sections; browser geometry test passes. | `5a7a1502` |
| P2 breadcrumb icon/inset | Fixed already | 16px icon at the measured desktop inset; name width remains data-driven. | `77875bfa` |
| P2 header favorite | Fixed already | 28px functional toggle, persisted in the browser test. | `77875bfa` |
| P2 header notifications | Fixed already | Functional team-notification setup link. | `77875bfa` |
| P2 view-pill gaps | Fixed already | Shared tabs have 4px gaps. | `77875bfa` |
| P2 extra Insights | Not applicable | Retained Flow feature; shared filter/display/details cluster stays aligned. | `77875bfa` |
| P2 fullscreen issue command | Fixed already | Real write verified; V opens the expanded composer. | `77875bfa` |
| P2 create-label command | Fixed already | Creates an actual label, not a placeholder action. | `77875bfa` |
| P2 create-view command | Fixed already | Creates and opens an actual saved view. | `77875bfa` |
| P2 extra Agent chat commands | Not applicable | Retained Flow commands. | `a2672c5b` |
| P2 project navigation commands | Fixed | Open project now opens a searchable picker, including server-only results. Go to projects and recently deleted projects retain their separate destinations. | `5c90326e` |
| P2 Settings/touch command | Still open | No persisted forced-touch preference or cross-surface input-mode contract exists. Requires an account preference in API/storage, responsive-mode resolution independent of width, pointer/menu behavior rules and a desktop/phone regression matrix. Removed the misleading desktop command that merely opened Preferences; real Go to settings remains. | `5c90326e` |
| P2 Help/performance commands | Fixed | Performance reporting opens a prefilled support destination, not Preferences. Contact support and changelog open their actual destinations. Nothing is submitted automatically. | `5c90326e` |
| P2 missing navigation shortcut help/behavior | Fixed | Added real team/archive/all-issues/Loops/current/upcoming/initiative/dashboard sequences and searchable project/cycle/team/user/view/initiative/favorite pickers. Favorites reuse sidebar routing; project search includes server results. Display and toast shortcuts are documented; dismiss-all is implemented. | `5c90326e` |
| P2 issue actions beyond New issue | Fixed | Desktop help lists supported issue bindings. Added actual property-picker, assignment, rename/focus, relation, sub-issue and copy bindings; existing reminder/favorite/subscriber/delete bindings remain. G/O sequences are explicitly deferred so P/I/S do not consume navigation. | `5c90326e` |
| P2 create dialog vertical placement | Fixed already | y117.5, 749px width and 21px radius; no fitted compensating offset added. | `77875bfa` |
| P2 property order | Fixed already | Status/Priority/Assignee/Project/Estimate/Labels/Cycle/More, subject to real team capabilities. | `77875bfa` |
| P2 default assignee | Fixed already | Unassigned unless the viewer's real auto-assignment preference requests otherwise. | `77875bfa` |
| P2 Add to cycle chip | Fixed already | Working cycle selector; no invented cycle data. | `77875bfa` |
| P2 sidebar search shortcut hint | Fixed already | Search tooltip advertises /, not command-menu keys. | `9c3bb22b` |

## Verification

Full lint/typecheck: exit 0. Targeted component tests: 72 passed across command menu/navigation, shortcut help, issue feature gates, sidebar/favorites, hotkey sequencing and toast controls (exit 0). Ten browser cases passed across the new four-case P1/P2 spec and existing shell/sidebars/top-bar specs (exit 0). They create owned workspaces through `workspace-fixture`, complete real issue/label/view/preference writes, and verify real navigation and reporting behavior.

Phone gates (exit 0): `shell / command-menu: 38 element checks, 0 failures`; `shell / composer: 48 element checks, 0 failures`; `shell / help-sheet: 34 element checks, 0 failures`; `issue-detail / top: 58 element checks, 0 failures`; `agent / agent-home: 44 element checks, 0 failures`; `docs / document-history: 20 element checks, 0 failures`. The issue-detail actions manifest has zero element checks, so its zero-failure output is not meaningful coverage and is not counted as a verification pass. New issue hotkeys and expanded help are desktop-gated; existing phone menu content remains unchanged.

All PNGs under `/tmp/codex-desktop-p12/shots/` were opened: `shell-topbar.png` and `desktop-topbar.png` show the usable team header; `shell-commands.png` shows command-first pro results; `shell-shortcuts.png` shows the searchable new issue binding; `command-fullscreen.png`, `command-label-saved.png` and `command-view-saved.png` show the working expanded composer, saved label and saved view. A transient development-server import-cache failure after renaming the navigation module was rejected, the owned server restarted, and the complete browser run repeated successfully.

## Review

`/W/agent`: open the command menu, query pro, then choose Open project. O then P/F/T/C/U/V/N opens the corresponding picker; choose an existing item. Open shortcut help and search Assign issue to user. Query Report performance problem and click it; it must not go to Preferences or submit a report.

`/W/team/T/all`: click New issue, favorite and notification setup. With triage/cycles enabled, inspect Triage/Current/Upcoming; G then V/W opens current/upcoming. `/W/issue/I`: press P/S/A to open real property controls, I to assign/unassign yourself, and O then P to navigate without opening Priority. Phone screens must retain their previous controls.

Product decision: define forced-touch preference persistence and behavior before implementing that capability. No other scored shell item needs a new backend design.
