# UI Fit Audit

Audit date: 2026-10-31. Baseline: `6486bf39`. Started with
`git diff 6486bf39..origin/main --stat -- web/src`: 340 changed files,
12,589 insertions and 1,621 deletions. Followed with targeted searches of
added CSS/TSX, then small surrounding reads. This is a targeted audit of that
change set, not a claim that every pre-existing control in the application
has been audited. Concurrent main changes were rebased normally.

All e2e specs were left untouched. No API, database
schema, production infrastructure, reference expectations, or gate locators
were changed. Browser mutations used a private scratch database only.

## Confirmed Findings

Paths in this table are relative to `web/src/components/`. Line numbers point
to the corrected implementation, or to the adjacent surviving rule when a
rule was deleted. All rows below were judged fitted or functionally incomplete.

| File:Line | What It Was | Verdict | Fix |
| --- | --- | --- | --- |
| `agent/agent-page.module.css:123` | Empty-state examples and composer independently absolutely positioned | Fitted; longer input could meet the examples | Retain the existing grid rows and normal composer flow |
| `agent/agent-page.module.css:1046` | Desktop skills menu fixed to 43.1px and search hidden | Fitted to an empty skill list; real skills were clipped | Content-height menu, visible search, scrollable options |
| `agent/agent-page.module.css:356` | Composer and footer nudged down 2px and 8px | Layout offsets | Remove the nudges; editor and footer occupy normal flex/grid space |
| `agent/agent-page.module.css:1224` | 275.69px title cap and actions at left 323.69/355.69px | Fitted to a particular phone/title; favorite selector also assumed the unfavorited label | Flex title truncation with fixed-size actions; restore Move to toolbar |
| `agent/agent-chat-actions.css:7` | Absolute menu items positioned by nth-child, fixed list height, header shifted when menu opened | Fitted; filtering changed the number/order of rows | Normal-flow command groups and separator; no page/header movement |
| `agent/agent-chat-actions.tsx:30` | Phone actions did not close their sheet; feedback absent | Phone lost session feedback and could stack dialogs | Close before dispatch; offer feedback under the same feature flag as desktop |
| `create-issue/create-issue-dialog.tsx:522` | Extra assignee-name span, fixed at 183px/182.5px and opacity zero on phones | Measurement-only duplicate; also duplicated desktop name content | Remove the span; regression asserts one real assignee label |
| `create-issue/create-issue-dialog.module.css:137` | Label options 1/2/3 absolutely positioned in a 179px list | Fitted to three labels; fourth and later labels overlapped | Normal-flow options in the shared scrolling picker |
| `create-issue/create-issue-dialog.module.css:199` | Phone title editor forced to 24px high | Fitted to a one-line placeholder | Keep minimum height only; a long real title grows |
| `create-issue/create-issue-dialog.module.css:206` | Property group fixed to two rows, overflowing chip labels | Fitted to a particular set of names | Growing property area, wrapping chips, bounded label ellipsis |
| `create-issue/create-issue-dialog.module.css:230` | Create-more translated 6px; team glyph translated 1.5px; background copy restyled when labels opened | Fitted to screenshots, not state semantics | Remove offsets and menu-open typography changes |
| `detail/issue-mobile.css:7` | Breadcrumb capped at 179.09px | Fitted to one breadcrumb | Shrink within available header space |
| `detail/issue-mobile.css:20` | Work-control group hidden | Phone lost coding-tool selection and starting coding sessions | Restore the working group in the command strip |
| `detail/issue-mobile.css:103` | Sub-issue checkboxes invisible until focus/selection | Touch users had to discover an invisible control | Keep selection visible |
| `detail/issue-mobile.css:115` | Activity text at top -2px / left -4.5px with compensating width | Layout offsets | Normal row padding; text stays in its grid track |
| `detail/issue-desktop.css:14` | Desktop activity text at left -4.5px and top -1px | Same fitted row layout | Remove offsets and compensating width |
| `documents/document-page.css:747` | Breadcrumb label width calculated as 75vw - 261.8px; absolute header clusters | Fitted interpolation between two phones | Flex breadcrumbs and action clusters, available-space truncation |
| `documents/document-page.css:788` | Metadata panel fixed to 360.19px and shifted left 10px | Could extend outside narrower viewports | Viewport-bounded panel without an offset |
| `documents/document-page.css:814` | Owner fixed to 141.04px; last-edit timestamp fixed to 102.77px; Edited button fixed to 77.36px | Data-dependent names/dates | Flex owner and intrinsic dates/button |
| `documents/document-page.css:866` | Only first paragraph changed to inline display | Positional document-content fitting | All paragraphs retain normal block layout |
| `documents/document-history-dialog.css:44` | Document title could not shrink | Long document names could escape history header | Shrinkable title group with normal ellipsis |
| `documents/document-history-dialog.css:220` | Header children translated 3.38px | Layout offset | Center in the actual header row |
| `documents/document-history-dialog.css:250` | Restore toolbar/button fixed to 124.3/106.3px, despite optional diff controls | Fitted to current-version-only state | Content-sized, wrapping toolbar in normal flow; responsive version selector |
| `documents/inline-comments/document-inline-comments.css:248` | Draft fixed to 298px and Send translated 23.5px | Fitted text/send alignment | Existing flex composer owns the available card width |
| `team-overview/team-documents.css:48` | Column headings shifted 6/8px; date values shifted 2px | Layout offsets | Use the table's date/owner track widths and normal padding |
| `team-overview/team-documents.css:582` | Selected-documents Agent button hidden | Phone lost asking about selected documents | Restore the same contextual Agent action |
| `my-issues/my-issues-surface.tsx:152` | Overflow-pill measurement assumed 74.96px | Fitted to one count/locale | Measure each rendered count pill |
| `my-issues/my-issues.module.css:44` | Third toolbar action hidden by index | Phone lost Insights; the supposed alternate route was not a trigger | Restore Insights; tabs overflow naturally |
| `issue-explorer/issue-explorer.module.css:37` | Third toolbar action hidden by index | Team/saved-view phone users lost Insights | Restore the working control |
| `issue-explorer/issue-board.module.css:388` | Different card padding except first-of-type | Item-position fitting | One card-body padding rule for every card |
| `issue-explorer/saved-view-panels.tsx:44` | Phone Teams summary tab removed and selected Teams silently replaced | Phone lost a filter/drill-down dimension | Same summary dimensions on both layouts |
| `issue-explorer/saved-view-panels.tsx:66` | Estimate aggregation suppressed on phones | Phone saw counts instead of the configured estimate measure | Use the same real local/server aggregation |
| `my-issues/my-issues-display-menu.module.css:139` | Fixed sheet heights, translated layout buttons/chevrons, offset section | Fitted to a reduced set of options | Bounded scrolling surface and normal alignment |
| `my-issues/my-issues-display-menu.module.css:286` | Fourteen property controls, recency/empty-group switches and reset/save footer hidden; rows reordered by index | Phone lost settings that existed and worked on desktop | Restore them; retain the equivalent compact nested-sub-issue select |
| `settings/settings-parity.css:568` | Select widths and row/description heights chosen by row number | Fitted to English values and specific wrapping | Intrinsic select widths and content-height rows |
| `settings/settings-parity.css:609` | First personal row hidden | Phone lost Language, with no equivalent setting elsewhere | Restore the row; let it contribute to section height |
| `settings/mobile-profile.css:12` | Different fixed heights for five profile rows | Fitted to particular profile values | Keep existing stacked layout, remove per-row heights |
| `settings/mobile-notifications.css:15` | First section rows forced to 79.5px | Fitted description wrapping | Allow existing minimum-height rows to grow |
| `settings/mobile-workspace.css:14` | Positional heights, 187px fiscal description, subdued working onboarding rows | Fitted to one workspace/settings state | Semantic stacked-input selectors, normal copy flow, natural select width |
| `settings/feature-settings.css:209` | Only Slack's copy translated 8px | Provider-specific fitting | Shared row centering |
| `settings/team-workflow-settings.tsx:619` | Security and Project statuses omitted on non-desktop overview | Phone could not navigate to working team settings | Show both links on phones too |
| `settings/workspace-teams-settings.tsx:158` | Export CSV rendered only outside phones | Phone lost export | Same permission check on both layouts; wrapping toolbar |
| `settings/settings-parity.css:951` | Members export hidden by CSS | Phone lost export | Restore it in a two-row toolbar; verified a real members.csv download |
| `settings/settings-parity.css:969` | 154px role column plus 76px leading reservation squeezed real names | Fitted to sampled names/statuses | Shared content-sized subgrid columns; fixed sizes only for checkbox/menu controls |
| `settings/settings-parity.css:1051` | Member menus depended on hover | Invisible touch actions | Visible phone row menus in their own grid track |
| `cycles/cycles.css:19` | 12px header gap canceled by -12px subheader margin; Insights hidden | Layout cancellation and lost phone Insights | Normal adjacent rows; Insights remains reachable. Concurrent main now supplies the alternate Insights command inside Display options |
| `cycles/cycles.css:32` | Cycle switcher fixed to 81px with absolute glyphs | Fitted to short cycle names | Intrinsic flex name, fixed glyphs, available-space ellipsis |
| `project-detail/project-detail-page.css:1564` | Milestone issue count/link widths 101.22/94.66px; description height 50px | Data-dependent fitting | Natural count/link widths and minimum-height description |
| `project-detail/project-detail-page.css:1679` | Write with Agent and project Insights hidden | Phone lost working Agent writing and Insights | Restore controls; wrapping update footer |
| `project-detail/project-detail-page.css:1717` | Diff label fixed to 80.08px, update editor fixed to 70px | Fitted to particular labels/text length | Content-sized grid label track and growing editor |
| `project-detail/project-detail-page.css:1791` | Project-created event pulled up 24px | Layout offset | Use its intended 12px padding instead of negative margin |
| `team-overview/team-overview-page.tsx:327` | Team tab overflow pill assumed 74.96px | Fitted count/locale | Measure the actual rendered count variants |
| `initiatives/initiatives-mobile.css:38` | Selection and row actions hidden, name/health offset to fill their space | Phone lost bulk selection and discoverable row commands | Real grid tracks for select, name, health and menu |
| `reviews/reviews-page.css:705` | Guide tab hidden; 12px header gap canceled with negative margin | Phone lost a whole review view | Restore Guide, scrollable tabs, normal adjacent header rows |
| `coding-sessions/coding-sessions.css:234` | Inline code shifted up 2px; Latest hidden | Fitted baseline and lost phone jump-to-latest | Normal code baseline; restore working Latest |
| `loops/loops-page.css:494` | Name input fixed at 596px / flex-basis 596px | Editor overflowed narrow windows and crowded its level control | Shrinking flex input with a proper line box |
| `views-page/views-page.module.css:178` | Count shifted -12px to cancel a 16px gap | Layout cancellation | Express icon/title/count separation with actual gaps/margins |
| `inbox/hosts/workspace-welcome-inbox-view.tsx:12` | Decorative play/progress/time controls for no video; mobile hid unavailable state | Invented playback UI | Honest unavailable state at every width; real resource links remain |
| `inbox/hosts/workspace-welcome-inbox-view.css:1` | Desktop welcome shifted left 3px | Layout offset | Existing centered bounded column |
| `workspace/workspace-menu.tsx:106` | Desktop-download command opened a hard-coded domain with no native app release | Invented product capability | Remove the unsupported command on phone and desktop |

## Examined And Kept

| Candidate | Verdict | Reason / Phone Reachability |
| --- | --- | --- |
| Negative letter spacing in composer, document, project, issue, activity and numeric identifiers | Real measured typography | Reference raw rows contain -0.1px, -0.16px, -0.24px, -0.26px, -0.0866667px and -0.08px. Kept rather than normalizing to a generic style |
| Text scaleX / font-stretch | No remaining added match | Targeted added-line scan found no remaining text distortion of these kinds |
| scaleY(.5) divider and rotated/flipped glyphs | Real drawing | Hairline or directional icon, not compressed text |
| translateX/Y(-50%) centering and animation entrance transforms | Real positioning/motion | Center overlays or animate entry, not final-state text fitting |
| Half-pixel coding-panel/header border adjustments and <=1px glyph/badge baseline adjustments | Optical | Bounded optical corrections, not whole-row layout |
| Agent history absolute row positions | Data-driven list layout | Positions derive from group counts, actual row index and fixed control-row height, not sample titles or nth-child CSS |
| Standard/virtual property option row indexing | Data-driven control layout | Counts/indexes cover every option; virtual rows retain their own layout. Unlike the removed three-label rules |
| Board column/card widths and horizontal scrolling | Real board design | Stable lanes/cards, with wrapping titles; not widths fitted to a sampled name/count |
| Menu/panel/reading-column maximum widths | Real bounded tools | Menus scroll, text truncates/wraps, viewport constraints remain; project side panel additionally capped to 100% |
| Fixed icons, avatars, checkboxes and icon buttons | Real controls | Fixed-format targets rather than variable-content boxes |
| nth-child selecting table columns, first/last edge radii, first paragraph placeholder, sibling separators | Real structural selectors | Select columns or edges, not particular data rows |
| Label-chip truncation and compact row property visibility | Real compact list design | Full values and edits remain in the property picker or detail view |
| Hidden desktop issue properties rail | Alternate phone UI | Status, priority, assignee, labels, project/date and relations have phone pickers; not a disabled feature flag |
| Hidden review sidebar | Alternate phone UI | Phone property rows expose reviewers, linked issues, status and branch data |
| Hidden review Favorite / Copy / repository controls | Alternate commands | Actions menu retains Favorite and Copy URL; PR link opens host; Diff tab remains reachable |
| Short phone ready-for-review label | Same action | Same mutation; no capability removed |
| Hidden initiative update shortcut and empty team pills | Alternate UI | Activity tab and details/properties panel remain reachable |
| Hidden desktop welcome-video chrome | Honest fallback | No video source exists; no simulated playback control is retained |
| Full-width negative-margin bands and update-card bleed | Real container edges | Match parent gutters; they do not displace individual rows according to their content |
| Phone native selects / dropdown sheets | Alternate input presentation | Preserve options, mutations, disabled/permission state and navigation |
| Conditional empty/archived/permission controls | Real application state | Archived coding composer is not rendered for prompts on either layout; permission checks remain authoritative |
| New URLs and handlers | No new inert placeholder found | Added-line scan found no bare href="#" or empty click/select handler; added reference-brand text scan returned zero. Docs/support URLs point to this repository |

## Verification

Private API/Vite: `127.0.0.1:5381` / `127.0.0.1:5382`. Private DB,
uploads, scripted model provider and screenshots: `/tmp/codex-fit-audit/`.
The source verification DB was read using SQLite backup, never mutated.
No real model requests were necessary. The review-panel development fixture
was enabled explicitly for that screen; its unimplemented sync/artifact
requests return 404, so that screen verifies layout/Latest, not provider sync.

Heavy commands ran through `~/.flow-local/heavy.sh` with the requested Node
path. Final targeted test command from this worktree's `web/`:

```sh
~/.flow-local/heavy.sh npx vitest run --maxWorkers=2 $(cat /tmp/codex-fit-audit/test-targets.txt) \
  src/components/coding-sessions/coding-session-panel.test.tsx \
  src/components/coding-sessions/coding-session-conversation.test.tsx \
  src/components/coding-sessions/review-agent-panel.test.tsx
```

After the final rebase: exit 0, 48 files / 607 tests passed. Full `npm run lint`
(including typecheck and every architecture/design/motion check) and explicit
`npm run typecheck` passed before pushes. `git diff --check` passed. API was
built to `/tmp/codex-fit-audit/server` with `GOMAXPROCS=2 go build -p 1` through
the lock, exit 0. No Go source changed, so no Go tests/vet were needed.

Real browser checks included 375/390 phone layouts, 1440 desktop layouts,
long composer text, five actual labels with nonoverlapping row rectangles,
menu filtering, feedback closure, a CSV download, team setting navigation,
review Guide navigation, Agent writing with a scripted provider, and an inline
comment whose Send button stayed within its card.

### Gates And Checks To Retire

Both supplied gates were invoked by absolute path, with absolute `--web` and
explicit `--base http://127.0.0.1:5382`, on 63 touched/affected screen IDs.
Raw output: `/tmp/codex-fit-audit/gates.log`. Latest results are mixed, not a
clean gate pass: 21 mobile passes / 42 failures; 55 text passes / 8 failures.
No expectations were changed to conceal these differences.

| Failing Screen(s) | Why The Failure Was Kept |
| --- | --- |
| Agent home/conversation/options | Intrinsic title/action positions, restored actions, intrinsic menu height; backend enabled state differs from reference |
| Document view/metadata/history | Removed sampled breadcrumb/name/date/toolbar widths and offsets; current dates/version data differ |
| Document subscribers | Agent subscriber is absent from the copied fixture; no invented person added |
| My issues views, team list/board, saved view and project issues | Restored Insights changes toolbar space and which tabs fit; overflow counts are now measured |
| Display options | Restored working controls/footer, natural panel contents and alignment instead of a reduced fitted sheet |
| Issue top/activity | Natural breadcrumb space and activity grid alignment replace sampled coordinates |
| Composer / label / assignee | Removed invisible 8px duplicate name, fixed three-label viewport and conditional background-copy restyling. Five labels correctly make a taller list |
| Preferences/profile/notifications/workspace | Language restored; real row/copy/value heights no longer pinned to row numbers |
| Teams/members/team overview | Restored exports/settings and content-sized columns change row/header positions |
| Integrations | Removed Slack-only translation; nearby repeated text causes different nearest-text matches |
| Cycle detail | Intrinsic name width; text gate also reports existing Cycles 16px/400 versus 13px/500 mismatch |
| Project milestones | Real content/scroll state and natural description/count geometry differ from the sampled state |
| Project overview | Text gate reports existing Add customer request size/weight/lightness mismatch; mobile geometry gate passes |
| Review detail / Agent panel | Guide/header structure is no longer hidden/canceled; code chip no longer shifted up 2px |
| Workspace menu | Unsupported download row removed, so panel/row heights change |
| Views | Same visible count spacing, expressed by a real 4px gap rather than 16px minus 12px |
| Documents tabs menu | All available tabs now fit; obsolete click locator has no overflow trigger. Corresponding 390 text-reference file is also absent |
| Loop editor | No loop expectations/reference files exist. Both attempted gate calls fail ENOENT; checked directly at 375/1440 instead |
| Initiative list / selected documents | Restored real selection, actions and Agent affordances change the compact layout |

Known stale e2e assertion, deliberately not edited:
`web/e2e/mobile-shell.spec.ts:387` still requires the unsupported desktop-app
download row. Existing reference-geometry specs may similarly encode the
removed fitted coordinates/hidden-control sets. They are not claimed green;
the lead's spec task needs to update those checks, rather than restoring fake
features or sample-specific layout code.

## Reviewer Routes

Use an existing workspace in place of `:workspace`, at 375px and 1440px.
The exact seeded routes are also in `/tmp/mobile-ref/*.locators.json`.

| Route | Clicks / What To Verify |
| --- | --- |
| `/:workspace/agent` | Open a chat; favorite/unfavorite, options, filter Copy; Feedback; Move to toolbar. Open Skills with several skills |
| `/:workspace/my-issues/assigned` | New issue; type a multiline title, assign a person, open Labels with at least five labels |
| `/:workspace/issue/:identifier` | Inspect header/activity; select a sub-issue; open Choose coding tool |
| `/:workspace/my-issues/assigned` and `/:workspace/team/:team/all` | Open Insights; Display options; scroll to all properties and reset/save footer |
| `/:workspace/view/:view` | Open details; use Teams summary and estimate-enabled breakdown |
| `/:workspace/team/:team/documents` | Select a document; Ask agent; resize tabs to exercise overflow; inspect desktop columns |
| `/:workspace/document/:document` | Edited metadata; history/version selector; select text and Comment; verify Send is inside its card |
| `/:workspace/settings/account/preferences` | Language, long descriptions and changing select values |
| `/:workspace/settings/account/profile` and `/:workspace/settings/account/notifications` | Inspect varying copy/value row heights |
| `/:workspace/settings/workspace` | Name/URL, fiscal month, onboarding; verify wrapping without row clipping |
| `/:workspace/settings/teams` and `/:workspace/settings/members` | Export CSV; member names, visible ellipsis menus and sorting |
| `/:workspace/settings/teams/:team` | Security and Project statuses both navigate on a phone |
| `/:workspace/settings/integrations` | Slack aligns by the same row layout as other providers |
| `/:workspace/team/:team/cycle/:cycle` | Open cycle with a long name; Display options, then Insights |
| `/:workspace/project/:project/overview` | Insights; milestones with varying issue counts/descriptions |
| `/:workspace/project/:project/activity` | Update, Write with Agent; long update and diff values |
| `/:workspace/initiatives` | Select initiative, use bulk bar, open row actions |
| `/:workspace/review/:review` | Guide; Diff; show Agent, expand a long transcript, scroll away, click Latest |
| `/:workspace/loops/new` | Start from scratch, choose Workspace, type a long Loop name; inspect at 375/1440 |
| `/:workspace/inbox` | Open a welcome notification: unavailable video state, no pretend play/time controls |
| Any app route | Open sidebar/workspace menu: settings, member management, switching and logout; no fictitious download |

## Screenshots

All images below are under `/tmp/codex-fit-audit/shots/`; each was opened and
visually inspected. Initial animation-frame captures and an initial Agent
configuration-error capture were failures, retaken after settling/configuring
the scratch provider. They are not counted as successful verification.

| File(s) | What They Show |
| --- | --- |
| `agent-375.png` | Flex chat title and all header actions fit on a phone |
| `agent-menu-375.png`, `agent-filter-375.png` | Settled four-action menu and one filtered row with no holes |
| `agent-skills-1440.png` | Desktop Skills menu includes search and its actual options |
| `composer-labels-375.png`, `composer-five-labels-375.png` | Three and then five actual label options flow in distinct rows |
| `composer-long-375.png`, `composer-1440.png` | Long issue title and wrapped property chips do not collide with footer |
| `docs-metadata-375.png` | Metadata stays within the phone viewport; real owner/date widths |
| `docs-history-375.png`, `docs-history-1440.png` | Version controls fit at phone/desktop widths |
| `docs-list-1440.png` | Real date/owner columns remain aligned |
| `docs-selected-375.png` | Selected-document action bar includes Ask agent; clicking opens its contextual chat |
| `docs-comment-1440.png` | Inline draft text wraps; Send remains inside the comment card |
| `issue-375.png`, `issue-1440.png` | Issue activity layout and phone-visible sub-issue selection/work tools |
| `issue-display-390.png` | Restored display switches, properties and footer |
| `settings-preferences-375.png`, `settings-preferences-1440.png` | Language and naturally wrapping preferences; desktop unchanged |
| `settings-teams-375.png` | Export/create controls fit on two rows; table scrolls horizontally |
| `settings-workspace-375.png` | Full-width input rows and naturally wrapped fiscal/region copy |
| `settings-members-375.png` | Real member names use remaining width; row menus are visible without hover |
| `project-milestones-375.png`, `project-milestones-1440.png` | Content-sized milestone controls on both layouts |
| `project-agent-375.png` | Restored Agent action receives a scripted response, not a configuration error |
| `initiatives-selected-375.png` | Visible selection and row-menu controls, with a working bulk-action bar |
| `review-guide-375.png`, `review-guide-1440.png` | Guide navigation works on both layouts; this fixture has no guide items |
| `review-agent-375.png` | Expanded transcript, normal inline-code baseline and visible Latest |
| `loop-editor-375.png`, `loop-editor-1440.png` | Long single-line name stays in its flex input with level control alongside |
| `welcome-375.png`, `welcome-1440.png` | Honest unavailable-video fallback and real documentation links; not playback verification |
| `workspace-menu-375.png`, `workspace-menu-1440.png` | Real workspace commands, without fictitious native download |
