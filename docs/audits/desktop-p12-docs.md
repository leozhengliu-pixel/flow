# Desktop Documents P1/P2

Baseline: main `255c1056`. Checked in bundled Chromium at 1440 x 900 with `--force-device-scale-factor=2`, no context scale option, and asserted `[1440,900,2]`. The table preserves all four P1 and 23 P2 entries from `/tmp/desktop-ref/docs.md`, including related entries sharing a cause.

| Item | Status | Evidence | Commit |
| --- | --- | --- | --- |
| P1 subscriber search and shortcut | Fixed already | Searchable Change subscribers field and working header shortcut are present. | `062914ac` |
| P1 comment submit off-screen | Fixed already | Browser posted a real comment; submit stays within the viewport. | `ec98abab`, `a73b01b3` |
| P1 composer overflow | Fixed already | New comment opens below selection; article x does not move and composer right edge is inside 1440. | `ec98abab` |
| P1 posted thread overflow | Fixed already | Posted card ends at x1417, not x1505. | `7340cf21` |
| P2 list metadata-column positions | Fixed already | Created/Last edited labels measure x1021.5/x1147.5; shared 120px columns plus 6px gaps. | `ec98abab` |
| P2 group create action hidden | Fixed already | 24px group create button visible at rest and opens the actual composer. | `062914ac` |
| P2 row time cells | Fixed already | Row cells use the same column tracks; normal text line boxes. | `ec98abab` |
| P2 row owner position | Fixed already | Owner uses the rightmost 140px column, aligned with its heading. | `ec98abab` |
| P2 document icon tile | Fixed already | Transparent 24px action with 18px glyph. | `062914ac` |
| P2 extra People with access row | Not applicable | Reproduced an editor who is neither creator nor workspace admin: the row is absent, as in the reference account. Creator/admin access controls remain functional. | `062914ac`; `82046b80` browser proof |
| P2 metadata panel width | Fixed | Removed the sampled 359px width/minimum. Intrinsic layout reproduces the reference-sized panel with the same owner name and timestamp, while short names produce a smaller panel. | `82046b80` |
| P2 metadata owner width | Not applicable | Same-data reproduction uses Skyler Anderson; owner target sizes to name plus avatar and padding. No sampled name width retained. | `82046b80` browser proof |
| P2 metadata history-pill width/fill | Fixed | Fills the intrinsic panel and uses measured 17.349/1.139/272 control fill; 28px height. | `82046b80` |
| P2 subscriber-panel width/start | Fixed already | 246px panel, search first, subscribed rows then other users. | `062914ac` |
| P2 Other users heading | Fixed already | Visible grouping and query filtering verified in browser. | `062914ac` |
| P2 agent badge | Fixed already | Application members receive the Agent badge. | `062914ac` |
| P2 subscriber row hitbox | Fixed already | Full-width row buttons in the 246px panel. | `062914ac` |
| P2 history dialog placement/height | Fixed already | Viewport-centered 1020 x 721 dialog at x210, independent of sidebar. | `062914ac` |
| P2 history header wording/padding | Fixed | History for retained; desktop header now has declared 20px top, 12px bottom and 20px left padding. | `82046b80` |
| P2 history preview/date heading | Fixed | Date heading already present; restored desktop 52/16/84/24 preview padding. | `82046b80` |
| P2 history footer extras/current restore | Not applicable | Reproduced one stored snapshot identical to current: only disabled Restore is shown, with no diff switch or pager. Controls appear only when another version exists; current restore was already disabled. | `062914ac`; `82046b80` browser proof |
| P2 Move selection to new document | Fixed already | Selection toolbar exposes the existing implemented action. | `062914ac` |
| P2 insert-menu height/placement | Fixed | Removed the stale desktop 442px placement cap; uses the surface's 482.5px desktop limit. Phone placement limit unchanged. | `82046b80` |
| P2 extra default Heading 4/Callout entries | Fixed | Default desktop document list has the measured 13 entries. Advanced blocks remain searchable; phone and issue default lists are unchanged. | `82046b80` |
| P2 composer placement | Fixed already | Inline draft opens below its selected text, not in a shifting right gutter. | `ec98abab` |
| P2 composer panel anatomy | Fixed already | Fresh read-only measurement: outer card is 360 x 49, border 0.5px, radius 8; the note's 298 x 48 box is the inner contenteditable, not the panel. Current outer card matches. | `ec98abab`; fresh read-only check |
| P2 article reflow on comment | Fixed already | Browser compares title x before/after opening and posting; no movement. | `7340cf21` |

## Verification

Full lint/typecheck: exit 0. Targeted tests: 143 passed across document page, collaboration, history, inline comments, editor blocks, selection toolbar and rich-text rules (exit 0). Three browser cases passed (exit 0), each creating its own workspace through `workspace-fixture`; they cover list geometry, same-name metadata, subscriber search, non-owner actions, single-version history, insertion and real comment creation.

Phone gates on shared fixtures: `mobile-gate docs / document-history: 20 element checks, 0 failures`; `mobile-gate docs / document-subscribers: 22 element checks, 0 failures` (both exit 0). The document-view fixture does not render a document: 33 missing-element/surface failures, including `document-view.top-bar: not found: .document-header`. Metadata likewise times out waiting for `.document-edited`. These unavailable fixture runs are not parity passes. No phone expectation or data was altered. The new document insert mode is disabled on phones; spacing/color changes are desktop-scoped.

All saved screenshots were opened. `/tmp/codex-desktop-p12/shots/docs-metadata.png` shows intrinsic metadata with the reproduced name/date and the brighter history action. `docs-comment.png` shows a posted thread inside the right edge with the article unmoved. `docs-history.png` shows the centered preview, date heading and disabled current-version restore. No screenshot shows an error page.

## Review

`/W/team/T/documents`: inspect date columns and group plus. `/W/document/D`: click Edited, then Show document history; open the bell and filter users. Select body text, click Comment and post; the article must stay put. In a blank paragraph type `/`: the default list has 13 entries; type `callout` to find the advanced block.

No backend or product decision remains in this area's scored inventory.
