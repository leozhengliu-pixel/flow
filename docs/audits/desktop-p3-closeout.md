# Desktop P3 Follow-Up

Baseline: current main `c8348f01`. Rechecked in bundled Chromium at 1440 x 900 with `--force-device-scale-factor=2`, `--window-size=1440,900`, `viewport: null`, and no context scale override. Measured viewport was `[1440,900,2]`. Evidence, scratch database and deterministic provider are under `/tmp/codex-gap-loops/`; the shared API and existing databases were not changed.

Read all eight `/tmp/desktop-ref/*.md` area notes. The table groups repeated observations and cascading positions rather than counting the same root cause several times. Reference dimensions are declarations or structural relationships, never fitted per-row heights, sampled text widths, transforms or compensating offsets. Data-specific differences are not cosmetic defects.

## Item Status

| Notes / Item | Status | Result |
| --- | --- | --- |
| Agent 1: title, empty composer, footer and Skills | Already fine | Title 13/500/normal; 712 x 104 composer; 24px footer and 74px Skills button; examples present. |
| Agent 2: history surface, shadow and empty text | Fixed | Reference menu surface, three-layer control shadow, 13/450/normal empty text with 14px inline padding. Existing New chat action remains functional. |
| Agent 3: Skills row typography | Already fine | Header and compact row are present; no fitted 0.4px correction to a captured floating-menu box. |
| Agent 4-5: conversation and embedded-session comparisons | Skipped | Notes explicitly lack an equivalent reference conversation/session fixture. No desktop sizes inferred from a phone or coding-session surface. |
| Loops 6: tabs, search placement and padding | Fixed | 43.5px toolbar, 6px tab gap, 12px search separation; real 300 x 28 input with 6/25/6/32 padding. |
| Loops 6: table headers, badge and group | Fixed | Runs header internal spacing, normal badge line box and reference group surface. 32px headers, 36px groups and 20px chevron were already present. |
| Loops 6: description height and execution values | Already fine | Rows grow with actual description; names, owner, draft status, run count and age remain real data. |
| Loops 7: empty hub position | Fixed | Restored the actual toolbar, 48/32 scroll padding and 32px section gaps. Heading y144 without a fitted 35.5px offset. |
| Loops 7: prompt, start button, categories and cards | Fixed | Reference surfaces; 104px prompt with 12px padding, 44px editor and 24px footer; removed the redundant template heading. Cards retain reference 12px padding and 10px radius. |
| Loops 7: start caption and template catalogue | Already fine | Button width follows its actual caption; template count follows the available catalogue. No sampled widths. |
| Loops 8: name box and trigger label/card | Fixed | Name box is 32px with 32/500/40 text. Trigger label y140.5, normal 14.5px line box; card `(502,167,672,96)`. |
| Loops 8: schedule control cascade | Fixed | Trigger and schedule are separate rows, gap 8; interval/unit/time 26px. Moved desktop declarations after their base rules so they actually apply. Daily schedules omit empty weekly pills; explicitly configured weekdays remain editable. |
| Loops 8: Instructions and Compose button | Fixed | Label y304.5; Compose uses reference 0/8 padding and intrinsic width. |
| Loops 8: capabilities and Team access | Fixed | Capabilities & access label and 30px, 13/400 value control; Chinese copy included. |
| Loops 8: date/time defaults and downstream section y coordinates | Skipped | Dates/timezones are data. Output-contract review is a real extra section; no fixed Connectors/permissions y offsets conceal it. Unrelated P2 controls were not removed. |
| Loops 9: tabs, run list and Edit loop | Fixed | Pane starts y96; 400px list; Edit loop `(1326.59,112,84.91,24)`; 13/500/16 run title. |
| Loops 9: run question composer | Fixed | `(652.5,751.5,771,104)`, 8px pane insets, normal Skills text, Ask about this run prompt and descriptive run search. |
| Loops 9: row outcomes, footer and bottom Agent button | Already fine | Outcome icons/durations reflect the run; footer controls 24px; Agent button 76.92 x 28 with 12/450 label. |
| Shell 1-4: avatar, nav, frame, footer and pills | Already fine | Current source has measured avatar/nav tokens, actual 0.5px frame border, compact footer and normal pill text. Empty favorites are data. |
| Shell 5-7: command input, rows, headings and shortcut search | Already fine | 112px query right inset, 15.6px item lines, 8px heading padding; shortcut search 318 x 44 with neutral border. |
| Shell 8-11: create issue, workspace menu, toast and tooltip | Already fine | 749 x 261 dialog at y117.5, radius 21; 32px close/expand; normal property text; intrinsic workspace menu; tooltip 11/400/16.5. |
| Docs 1: team title inset | Fixed | 6px icon/name gap makes text x284.5, without moving the frame or fitting a title width. |
| Docs 1-2: tabs, new action, sort/group/row, icon and editor | Already fine | 12px action; intrinsic sort targets, normal text, 24px icon picker; editor retains 10/14/384 padding and 24/600/32 title. |
| Docs 3-9: options, metadata, subscribers, history and selection text | Already fine | Normal menu labels and correct command order; metadata labels not fixed-width; 18px subscribers and 43px subscribed rows. History retains its frame and content-based preview. |
| Docs 10-15: unmatched threads, tables, code, math and images | Skipped | The notes mark these states as Flow-only or lack an equivalent reference fixture; no speculative preview or typography change. |
| Inbox 1: title | Fixed | Reference title padding 0/4/0/10 and 4px action gap replace the fitted 9px group margin; painted title x262.5. |
| Inbox 1-6: snippet, age, filters, action/row menus and snooze | Already fine | Normal text; real notification ages; separate shortcut keys, full labels and current menu surfaces. Filter/list x follows actual list width. |
| Inbox 7: Pulse | Already fine | 23px heading line, left-aligned 340px empty block and compact action at y563.45. |
| Inbox 9: review detail | Skipped | Explicitly Flow-only in the notes. |
| Issues 1-2: groups, priority targets, dates and titles | Already fine | Normal labels and intrinsic date/identifier widths. Invisible interactive hit areas do not move their 16px glyphs; unassigned users use the actual unassigned glyph. |
| Issues 3: board header controls and count | Fixed | Circular 24px buttons, 2px action gap, 12px right padding: menu x534.5, add x560.5. Restored existing estimate glyph and removed extra scroll-bottom padding. |
| Issues 3: card pitch | Already fine | 8px card gap retained; card height follows visible properties, not a fixed 117.5px fixture. |
| Issues 4-7: filters, display, view directory | Already fine | Normal labels, supported group controls, intrinsic headers and current page title tokens. Counts and empty/populated states are data. |
| Issues 8: saved-view count | Fixed | 13/500/normal at y66.5, instead of 12/450/18. |
| Issues 9: creation actions | Fixed | Real transparent 0.5px borders restore the reference Cancel/Save geometry; no padded shadow substitute. |
| Issues 11-12: bulk and context menu | Already fine | Declared 8px bulk gap; centering follows the visible list; menu labels normal. |
| Issue detail 1-13: frame, sub-issues, activity, pickers, comments and parent link | Already fine | Current desktop rules already restore normal labels, 36px sub-issue rows, 26px parent link, 3px add-action gap, 17.1px shortcut keys and 16px comment padding. Actual activity history determines y positions. |
| Issue detail 14: triage peek | Already fine | Header actions align, chips use their desktop line boxes and 14px glyphs. Resize handles expose separator semantics and retain keyboard access; responsibility configuration is supported, not an inert extra button. |
| Issue detail 15: relation hover | Fixed | Omit the unset No priority field. Identifier 12/450 was already restored. Nonzero priorities remain visible. |
| Projects 1-3: list, display and timeline | Already fine | Normal header/row text, reference status formatting, hover menu, measured headings and layout modes; timeline remains functional. |
| Projects 4-7: overview, issues, activity and milestones | Already fine | Natural 40px property rhythm, normal sidebar labels, 36px milestone rows, compact issue action and corrected activity composer. Missing initiative rows and feed lengths are data. |
| Projects 7: milestone shortcut | Already fine | Legend reflects the actual supported command, not an invented single-key action. |
| Projects 8-10: initiatives and cycles | Already fine | Current intrinsic headers, compact pills, single-name initiative breadcrumb and team-context cycle breadcrumb. |
| Settings 1-6: navigation, preferences, profile, notifications, security, workspace | Already fine | Links/14px back glyph, 13px selects, 32px avatar/inputs, reference toggles/rows, 22px security help, workspace section rhythm and docs links already present. |
| Settings 7-8: members/teams | Fixed | Team creation now exposes the reference accessible name while retaining the existing visible caption and translated phone behavior. Role heading, 44px team rows, links, handles and input geometry were already fixed. |
| Settings 7-10: timestamps | Already fine | Display dates and full timestamp metadata are different things; no fixed timestamp text replaces real values. |
| Settings 9-16,18: team overview, labels, statuses, integrations, API, security | Already fine | Full document wording, link semantics, empty table headers, full-width status content, 15/400 search, docs links and API section y206.5/381.5/578.5. |
| Settings and shell: paid plans/billing | Skipped | Flow has no paid plans. No badges, plan-gated actions, invoices or billing pages restored. |
| Provider/account-specific capabilities | Skipped | Real extra providers, unavailable mobile delivery, non-HTTPS passkeys and instance-specific authentication configuration are not cosmetic gaps. |
| Three measurement scripts | Fixed | `git grep` found no tracked references; deleted only crops/gates/measure scripts. |
| Three requested remote branches | Fixed | Deleted only the named branches after content comparison, as detailed below. |

## Branch Evidence

`c3993203` and `b62c9b15` have identical workflow/config content; main's matching change is `a5893fe8`. Files touched by `36ea2e9c` match main's `d51513d2` exactly. Compared the branch tips with current main as well: remaining differences are later isolated-test-database support, later phone assertions, and removal of paid-plan UI. The follow-up branch has no unique commits. Ancestry alone was not used for the rebased/cherry-picked branches.

Deleted `oc-task-ci-e2e-shards`, `oc-task-ci-e2e-shards-followup`, `oc-task-ci-e2e-shards-verify2`. No other remote branch was changed.

## Reviewer Routes

Use workspace W, team T, draft L, published loop P, run R, saved view V and an issue I with a related issue.

1. `/W/agent`: open the title history switcher; inspect empty text, menu surface and shadow.
2. `/W/loops` in an empty workspace: inspect tabs, hub and prompt. In a populated workspace inspect search, group and Runs header; New loop opens the dialog version.
3. `/W/loops/new?draftId=L`: inspect Trigger/Instructions, daily schedule and Compose; scroll to Capabilities & access and Team access. Choose weekly for editable weekday pills.
4. `/W/loop/P/run/R`: inspect the toolbar/panes, Edit loop and full-width Ask about this run composer; type a question to enable send.
5. `/W/team/T/documents`: inspect the team icon/name gap. `/W/inbox`: inspect title and adjacent options.
6. `/W/team/T/all`: Display options -> Board; inspect count glyph and round menu/add controls; hide and restore a column.
7. `/W/view/V`: inspect the issue count. `/W/views`: Create new view -> inspect Cancel/Save.
8. `/W/issue/I`: hover the related issue with no priority; preview shows identifier, title and status, not No priority.
9. `/W/settings/teams`: inspect the Create team button's accessible name; invoke it to confirm creation still opens.

## Screenshot Review

Every saved PNG was opened. In `/tmp/codex-gap-loops/shots/`, `before-*` shows the usable baseline; `after-agent`, `after-loops`, `after-loop-editor`, `after-loop-run` show the corrected composer, table, schedule and run pane. `history-final`, `empty-hub-final`, `loop-hub-final`, `loop-access-final` show the history menu, inline empty hub, category/template dialog and access control. `docs-final`, `issues-board`, `saved-view`, `inbox-final` show the title, board and count corrections. `issue-final` shows real sub-issues/comments/relations; `issue-hover-final` shows a status-only unset-priority preview. None is a sign-in/error page or broken layout.

## Verification

All heavy commands used `~/.flow-local/heavy.sh`. Full `npm run lint`, including typecheck and all repository checks, passed. Targeted component tests: 177 passed across 17 files. Targeted bundled-Chromium browser run: 20 passed across `desktop-loops-secondary`, `desktop-p3-secondary`, `desktop-settings-parity`, `desktop-saved-view-details`, `desktop-issue-board-hidden`, `desktop-inbox-parity` and `desktop-hairlines`. Each new/changed spec uses a fresh workspace fixture. Earlier failures exposed a translated accessible-name assertion, a strict hover locator and an old integer-border expectation; those tests were corrected and rerun, not hidden with product offsets.

## Limits

This closes the reproducible P3 defects above, not the notes' unrelated P1/P2 feature inventory. No production data, API shape, Go source, migrations or SDK changed. Schedule defaults remain timezone/data driven; populated conversation/session states without an equivalent reference remain unverified.
