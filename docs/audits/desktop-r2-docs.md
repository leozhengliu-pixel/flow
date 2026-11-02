# Desktop Documents Round Two

Verified on `f0641d64` after rebasing onto main. No product code changed in this
closeout. Earlier document changes are already on main in `e1a80e8c`,
`4da8d30b` and `981d5da7`. Agent has a separate `desktop-r2-agent` owner; this
inventory closes out documents only and does not claim Agent parity.

## Results

| Screen | Before differences | After differences | Current status |
| --- | ---: | ---: | --- |
| document-comment-composer | 7 | 1 | FAIL |
| document-history | 14 | 0 | PASS |
| document-metadata | 15 | 0 | PASS |
| document-options | 42 | 16 | FAIL |
| document-subscribers | 12 | 6 | FAIL |
| document-view | 5 | 0 | INCONCLUSIVE |
| documents-list | 9 | 0 | PASS |
| editor-block-menu | 9 | 0 | PASS |
| editor-slash-menu | 24 | 0 | PASS |
| editor-toolbar | 8 | 0 | PASS |

Round-two baseline: 0 PASS, 9 FAIL, 1 INCONCLUSIVE, 145 differences. Current:
6 PASS, 3 FAIL, 1 INCONCLUSIVE, 23 differences. The original sweep-three count
was 272; shared fixes, fixture alignment and matcher corrections make that
count unsuitable as a pure CSS improvement metric. The resumed session's
initial and post-rebase runs both produced the current results above.

Every residual comparison is individually recorded in
`desktop-r2-docs-differences.json`, including 45 inherited withheld foreground
comparisons and the sparse-body coverage flag. Withheld is not a parity claim.
No reference JSON/PNG, comparison tolerance or coverage floor was changed.

## Gate Corrections

The owned scratch gate compares the document editor wrapper, not the padded
article; the framed history dialog, not its full-viewport overlay; and the
selection toolbar frame with its effective inner padding. Padded/wrapped text
uses corresponding element boxes, not a glyph Range against an element box.
Breadcrumb x is withheld only when its preceding fixture name actually differs.
These changes predate the resumed session and were rechecked with 24 matcher,
surface and fixture-policy tests. Scratch tooling is not part of the product.

## Remaining Limits

- Composer: action x is 750.5px versus 749px. Existing padding, margin and control
  sizes match. Recover the source group's sizing contract or approve retaining
  responsive sizing; no fitted 1.5px offset was added. Product decision needed.
- Options: 16 text-width/shortcut-position differences are consistent with a
  captured 98% menu scale. Scale origin and animation timing were not recorded;
  no transform was fitted to the screenshot. Fixture cannot match reliably.
- Subscribers: six differences cover scaled text/heading bounds, a badge after a
  different built-in application name, and JSON/PNG trigger-paint disagreement.
  Human fixture names and roles match; product identity is preserved. Fixture
  cannot match all captured evidence simultaneously.
- Document view: four eligible body matches, below the unchanged floor of five.
  Numeric fixture content does not create additional UI-label matches. Fixture
  cannot establish coverage; INCONCLUSIVE remains distinct from PASS.

Agent reasoning effort remains with its owner: it needs persisted configuration
and provider capability support, not a decorative selector.

## Verification

All heavy commands used `~/.flow-local/heavy.sh` with Node 24.7.0. Working
directory for npm commands: `web/`.

- `npm run typecheck`: exit 0.
- `npm run lint`: exit 0, including every repository lint gate.
- `npx vitest run --maxWorkers=2` targeting document-page, team-overview-page,
  selection-toolbar and issue-description-editor-blocks tests: exit 0, 107 tests.
- Owned `desktop3-session.mjs e2e-client.mjs desktop-p12-docs.spec.ts
  desktop-p3-secondary.spec.ts`: exit 0, four tests. Uses bundled Chromium,
  owned Vite, auth-disabled scratch API/database and independently created
  workspaces; servers stop by their owned PIDs.
- `node --test` targeting desktop-match, desktop-r2 and desktop3 tests: exit 0,
  24 tests.
- Owned `desktop3-session.mjs desktop-gate.mjs docs --repaired --compact
  --no-shots`: exit 1 before and after rebase, reflecting the table above.

No phone rules changed in this closeout; the phone gate was not rerun. No real
model requests were needed for document geometry or deterministic fixtures.

## Browser Review

All five screenshots were opened and inspected under
`/tmp/codex-desktop-r2-docs-agent/shots/e2e/`:

- `docs-list.png`: Created and Last edited values align beneath their headings.
- `docs-metadata.png`: intrinsic metadata panel, aligned author avatars and the
  full-width history action.
- `docs-subscribers.png`: search filtered to the intended user and its Agent badge.
- `docs-comment.png`: a posted inline comment inside the viewport, without moving
  the document title or article.
- `docs-history.png`: centered history dialog, current version and disabled
  Restore version action.

These verify working interactions and layout, not a pass for the three residual
comparison screens. There are no error pages or broken layouts in these images.

Review `/W/team/T/documents`: compare the date headings and first row, then open
a document. On `/W/document/D`, click Edited for metadata and Show document
history for the dialog; open the bell and search for a member. Select paragraph
text, choose Comment, enter text and submit. In an empty paragraph, type `/` to
inspect the insert menu, or select text to inspect the formatting toolbar.
