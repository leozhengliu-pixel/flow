# Desktop Issue Detail P1/P2

Rechecked main `591c1de7` and the fixes below at 1440 x 900 in bundled Chromium, launched with `--force-device-scale-factor=2`, without a context deviceScaleFactor. The measured viewport was `[1440,900,2]`. Source inventory: `/tmp/desktop-ref/issue-detail.md`; repeated focus/rich-description observations and downstream coordinates are grouped with their root item, not counted twice.

Evidence: `/tmp/codex-desktop-p12-2/issue-detail.json`, `shots/issue-detail-*.png`, and the isolated-workspace `desktop-issue-detail-properties`, `desktop-issue-detail-activity`, and `desktop-triage-queue` browser specs. Reference access was read-only in a task-owned tab. Matching relation titles and parent/child structure reproduced the extra similarity section; it was not dismissed as data.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| 1: Title column | Fixed already | Title width 653.5; x306, within 1px of the measured x306.4. | `1e92e1a7` |
| 1-3: Empty/focused/rich description column | Fixed already | Same content column; declared 10/14/16 padding and 15/450/24 paragraphs. | `1e92e1a7` |
| 1-3: Description action size, padding and colour | Fixed already | 28px targets, 14px glyphs, 0/2 padding, issue foreground. | `c29c48da` |
| 1: Sub-issues header position and type | Fixed already | 12/500/normal header; original extra 9px removed. | `c29c48da` |
| 1: Sub-issues count type/colour | Fixed already | 12px count with issue-muted colour. | `c29c48da` |
| 1: Activity heading colour/position | Fixed already | 15/600/23, bright foreground; text y371.25 on matching parent structure. | `c29c48da` |
| 1: Activity issue-reference chips | Fixed already | Plain 13/450 links, no background or repeated identifier; browser assertions. | `c29c48da` |
| 1: Composer box | Fixed already | 83px minimum height; content extends 8px to each side of the title. | `c29c48da` |
| 1: Composer placeholder inset | Fixed already | 16px composer inset and 15/450/24 placeholder. | `c29c48da` |
| 1: Properties column | Fixed already | x1007.5, within 1px of reference; proportional grid, not a sampled width. | `1e92e1a7` |
| 1: Properties/Labels/Project heading typography | Fixed already | 13/500/normal, 15.5px box, issue-muted colour. | `1e92e1a7` |
| 1: Team property and row order/pitch | Fixed already | Status, team, priority, assignee, estimate, cycle; first rows have 32px pitch. | `1e92e1a7` |
| 1: Unset priority/assignee colour | Fixed already | Both use issue-muted instead of faint text. | `1e92e1a7` |
| 1: Extra unset estimate | Fixed already | Disabled estimates omit the row; enabling fibonacci exposes the real estimate picker. | `1e92e1a7` |
| 1/9: Missing cycle property | Fixed already | Existing CyclePicker renders only for explicitly enabled teams; feature-gate tests cover unset/false/true. | `591c1de7` |
| 1: Redundant parent-of activity event | Fixed already | Child creation remains functional but adds no redundant desktop history row. | `c29c48da` |
| 4-6/9: Picker anchors cascading from sidebar | Fixed already | Status, priority, assignee and estimate use the corrected rail and row pitch. | `1e92e1a7` |
| 7: Labels placement and header cascade | Fixed already | 207px panel opens left of its trigger, not below/right. | `1e92e1a7` |
| 7: Extra checkbox column | Fixed already | Label rows toggle directly; unchecked checkbox has no painted column. | `1e92e1a7` |
| 8: Project popup width | Fixed already | 186px for the matching short list; longer lists retain searchable 339px mode. | `1e92e1a7` |
| 8: Extra short-list search field | Fixed already | Short menu omits the painted search header; typing reveals search. | `1e92e1a7` |
| 10: Actions anchor/width | Fixed already | 243px menu starts at the trigger, within 1px; no fixed text-width offset. | `1e92e1a7` |
| 10-11: Missing Team action/submenu | Fixed already | With two active teams, hovering Team lists targets; selecting one persists team, identifier and compatible status. | `1e92e1a7` |
| 10-11: Empty Release action | Fixed | No release/pipeline data now means no empty action; configured release functionality remains. Phone condition is unchanged. | `09fd5a7c` |
| 10-11: Missing Remove action/submenu | Fixed already | Direct navigation loads related targets; selecting Remove deletes the real relation and refreshes the page. | `1e92e1a7` |
| 11: End-of-week due-date preset | Fixed already | Visible and persists a real due date. | `c29c48da` |
| 11: Three-hour reminder preset | Fixed already | Posts a timestamp three hours ahead; browser request assertion. | `c29c48da` |
| 12: Comment action targets | Fixed already | Hover actions are 28 x 28 with pill radius. | `c29c48da` |
| 12: Inline reply typography/inset | Fixed already | 46.5px row, 15/450/24, 43px left padding and 18px avatar; reply posts successfully. | `c29c48da` |
| 12: Collapsed event groups | Fixed already | Three successive events collapse; expansion preserves comments and restores all rows. | `c29c48da` |
| 13-14: Extra Similar issues and its Activity cascade | Fixed | Same relation titles reproduced the unwanted section; ordinary desktop child and peek no longer mount it. Phone suggestions remain. | `09fd5a7c` |
| 14: Peek title/content/description inset | Fixed already | Title x = pane x +14, y112.25, width = pane width -28; browser assertions. | `c29c48da` |
| 14: Peek property-chip row | Fixed already | 24px row, 12px line, 14px glyphs and reference padding; label widths remain intrinsic. | `c29c48da` |

Counts: fixed already 31; fixed 2; not applicable 0; still open 0. No product decision required.

Verification (all exit 0, heavy commands through `~/.flow-local/heavy.sh`): full `npm run lint` including typecheck and all five checks; 55 tests across eight targeted Vitest files; six targeted browser tests. Phone gate: `mobile-gate issue-detail: 120 element checks, 0 failures`; shared inbox notification: `44 element checks, 0 failures`. The retired actions screen has no expectations and is not counted as coverage.

Screenshot review: opened every saved issue-detail PNG. Page shows the corrected columns, sub-issues, plain history links and composer; labels opens left without checkbox boxes; project shows the narrow short-list menu; actions shows the anchored real actions without an empty Release item; child omits the similarity section; comments shows rich text, replies and a collapsed event group; triage shows a selected issue in the split pane. The initial unselected triage capture was not used as peek verification; it was replaced after selecting a row. No final capture is a loading/error screen.

Reviewer: open `/W/issue/I` with similarly named related issues and a child, then open the child. There is no automatic similarity section. Open Issue options in a workspace without releases/pipelines: Release is absent. With two teams, hover Team; with relations, hover Remove. Open `/W/team/T/triage`, select a row, and inspect the full-width title and compact chips. Scratch routes use workspace `desktop-p12-1793532276117`, issue `DES-1`, child `DES-2`, team `DES`.
