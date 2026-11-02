# Unified Desktop Sweep 5

Source main revision: `78ba116193200b5a097e243ee11e0677b8c5a577`. No product code changed. One authoritative executable gate is in the supplied scratchpad. All 128 inventory entries are retained; two pre-existing paid-plan exclusions leave 126 runnable screens. The JSON records the exact source revision, tool/fixture hashes, every matched pair, withheld comparison, flag and numerical difference.

`screens run 126 of 126, PASS 76, FAIL 44, INCONCLUSIVE 4, FINDING 2, differences 448`

| Area | Reported PASS | Sweep 4 PASS | Unified PASS | FAIL | INCONCLUSIVE | FINDING | Differences |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| agent | 7 | 6 | 6 | 7 | 1 | 0 | 26 |
| docs | 6 | 0 | 1 | 8 | 1 | 0 | 52 |
| inbox | 10 | 0 | 10 | 0 | 0 | 1 | 0 |
| issue-detail | 20 | 15 | 20 | 6 | 0 | 0 | 31 |
| issues-list | 0 | 1 | 2 | 17 | 1 | 0 | 316 |
| projects | 6 | 6 | 6 | 4 | 1 | 1 | 19 |
| settings | 18 | 11 | 18 | 2 | 0 | 0 | 4 |
| shell | 13 | 0 | 13 | 0 | 0 | 0 | 0 |

The issue-detail comparison uses the later 20-PASS handoff in desktop-r3-issue-detail.result.md. The earlier round-two report was 2 PASS / 24 FAIL before its supported avatar/profile fixtures. Projects' supplied comparison is the six-PASS round-two report; main now also contains the subsequent shared-column work. Issues-list reported zero PASS in its closeout.

## Accepted Corrections

- Keep one matcher/browser/state/data/policy implementation. Retain all existing matcher tests and recover the shell, issue-detail, document and project matcher tests: 96 tests, exit 0. Coverage remains 15 total matches, five distinct body matches and 35% of at least ten eligible body UI labels; all numeric comparison tolerances remain unchanged.
- Restore shell history ages, collapsed groups, menu hover states, notification read state, board/team metadata, issue navigation context, account/profile projections and supported avatar image fixtures. Measure the covering avatar image's modal opaque paint rather than the hidden fallback color. Restore the document non-owner/author/roster/history fixture and its captured project parent context.
- Match clipped visibility consistently; normalize recorded capture scale once while retaining original capture visibility. Match actual form/editor/header/history/toolbar/rail surfaces and equal-box text presentation. Distinguish headings, authors, avatars, link text, portal previews and body/header create controls. All prior style/geometry properties stay in the comparisons.
- Preserve original routes/steps and reference JSON/PNG hashes. Repaired steps remain separately recorded. Refresh fixtures, index both manifests, run matcher tests, launch owned Vite and bundled Chromium, record per-area/global counts, and stop child processes by PID through the single entry command. Installed web dependencies are removed by the entry command when it installed them.
- The reviews onboarding and backlog educational empty state now exist. Remove their stale missing-feature declarations; retain the unavailable welcome-video finding. Sparse screens remain inconclusive.

## Rejected Corrections

- layoutChildren replaces padding/gap assertions with child x/y/w assertions. This skips the padding and gap properties. Reject it even when child positions match. The retained regression test asserts both differences remain. Source: `desktop-r3-issue-detail/.parity-local/desktop-match.mjs (recovered recorded diff)`.
- Choose layout as the replacement word because its glyph width matches the anonymized reference, then rename the reference item. This fits data to obsolete glyph bounds and changes the semantic expectation. The original neutral reference sentence is restored through the owned fixture API; its real width difference remains measurable. Source: `/tmp/codex-desktop-r3-projects/repair-neutral-fixtures.mjs and gate alias`.
- document-canvas maps the captured editor wrapper to the padded article. Wrong semantic owner. The recovered editor-shell rule replaces this mapping and retains all box properties; the canvas is not added as a second coverage credit. Source: `older area copies of the docs surface map`.

No candidate changed a numeric tolerance or coverage floor. The padding/gap substitution above is the concrete property-skipping correction rejected. Existing evidence limits remain explicitly withheld; they are not new waivers or passes.

## PASS Reconciliation

- agent/loops-all: reported PASS, now INCONCLUSIVE. Gate difference; unified gate is right. LOW_MATCH_COVERAGE: >=15 total and >=5 body matches required. Only 3 distinct body nodes matched, below five. The prior PASS does not meet the unchanged measured-body coverage floor with the merged semantic matching and distinct-node counting. Properties: none.
- docs/document-history: reported PASS, now FAIL. Gate difference; unified gate is right. It measures the equal-box paragraph that paints the captured title against the live input, retaining lineHeight and letterSpacing. The area gate compared the inert textbox wrapper. Labelled avatars are also measured explicitly instead of being paired to same-name author text. The document-page CSS blob is unchanged from the area report revision f0641d64, so these are additional/corrected comparisons, not a later document regression. Properties: letterSpacing, lineHeight, fontWeight, backgroundColor.
- docs/document-metadata: reported PASS, now FAIL. Gate difference; unified gate is right. It measures the equal-box paragraph that paints the captured title against the live input, retaining lineHeight and letterSpacing. The area gate compared the inert textbox wrapper. Labelled avatars are also measured explicitly instead of being paired to same-name author text. The document-page CSS blob is unchanged from the area report revision f0641d64, so these are additional/corrected comparisons, not a later document regression. Properties: letterSpacing, lineHeight, fontWeight, backgroundColor.
- docs/editor-block-menu: reported PASS, now FAIL. Gate difference; unified gate is right. It measures the equal-box paragraph that paints the captured title against the live input, retaining lineHeight and letterSpacing. The area gate compared the inert textbox wrapper. Labelled avatars are also measured explicitly instead of being paired to same-name author text. The document-page CSS blob is unchanged from the area report revision f0641d64, so these are additional/corrected comparisons, not a later document regression. Properties: letterSpacing, lineHeight.
- docs/editor-slash-menu: reported PASS, now FAIL. Gate difference; unified gate is right. It measures the equal-box paragraph that paints the captured title against the live input, retaining lineHeight and letterSpacing. The area gate compared the inert textbox wrapper. Labelled avatars are also measured explicitly instead of being paired to same-name author text. The document-page CSS blob is unchanged from the area report revision f0641d64, so these are additional/corrected comparisons, not a later document regression. Properties: letterSpacing, lineHeight.
- docs/editor-toolbar: reported PASS, now FAIL. Gate difference; unified gate is right. It measures the equal-box paragraph that paints the captured title against the live input, retaining lineHeight and letterSpacing. The area gate compared the inert textbox wrapper. Labelled avatars are also measured explicitly instead of being paired to same-name author text. The document-page CSS blob is unchanged from the area report revision f0641d64, so these are additional/corrected comparisons, not a later document regression. Properties: letterSpacing, lineHeight.
- projects/display-menu: reported PASS, now FINDING. Later regression: The shared Lead track changed from 48px to 120px. The previously passing Display menu reference uses 48px, so adjacent columns move by 72px. Properties: x.

The document CSS blob check is `589b156dd9c494c86267ecbd418697186e6723da / 589b156dd9c494c86267ecbd418697186e6723da` (reported revision/current main). Equal hashes rule out a later change to that file. The full discrepancy ledger retains exact reference/current values and selectors instead of treating the older PASS labels as authoritative.

## Later Regression

- projects/display-menu: `c4efe0f2bc727b3831faeac5fec85f7a5406ca5e` sets the shared Lead track to 120px; the formerly passing Display-menu capture uses 48px. The five adjacent-column x observations shift by 72px. Unified status is FINDING and all numerical differences remain counted. The shared rule improves List, but the reported Display-menu PASS no longer reproduces. Main's round-three report independently records the conflict.

## Commands And Artifacts

Run from any directory:

```sh
/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-sweep.sh [area]
```

Omit area for all areas. Exact completed full run: `/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-sweep.sh`. This fetches/fast-forwards the task worktree to origin/main before pinning the source revision, installs its own dependencies if needed, seeds/refreshes owned fixtures and executes under the machine-wide heavy-command lock. The existing shared fixture API is http://127.0.0.1:18200. Vite uses a free local port and is stopped by its runner. The user's Chrome is untouched.

- `/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-sweep5.out`: full sweep text, final line above; exit 1 because measured failures/inconclusive/findings remain.
- `/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-sweep5.json`: complete screen evidence and hashes.
- `/private/tmp/claude-501/-Users-liuzheng-src-flow/412a45aa-8326-428d-9ef9-334bda30c694/scratchpad/codex/desktop-sweep5-reconcile.json`: every prior PASS that did not reproduce, rejected corrections and regression evidence.
- `/tmp/codex-desktop-gate-unify-matcher.out`: 96 matcher/policy tests, exit 0; syntax checks exit 0. Dependency installation exit 0. No product web/Go/E2E files changed, so product suites were not rerun.
- Matcher command: `~/.flow-local/heavy.sh node --test "$S"/desktop-match*.test.mjs "$S/desktop3.test.mjs"`, exit 0. The shared gate's shell/sidebar live mutation run (`desktop3-session.mjs desktop-gate.mjs shell --screen sidebar --repaired --compact --no-shots --mutation`) exited 0: clean baseline, one detected 2px padding difference, clean restored result. Evidence is `/tmp/codex-desktop-gate-unify/mutation.json`.
- `desktop-sweep5-shots.md`: all retained diagnostic screenshots were opened in labelled contact sheets; these earlier captures are not the final numerical sweep and do not independently certify parity. The unavailable-video image and mismatched fixture states are explicitly not verification passes. The 18px avatar fixture image was also opened: white SA initials on a red circular image.

Scratch changes: desktop-sweep.sh, desktop-gate.mjs, desktop-match.mjs and its retained area tests, desktop-browser.mjs, build-desktop-fixtures.mjs, generated expectation/fixture manifests, desktop3-states.mjs, desktop3-data.mjs, desktop3-session.mjs, desktop3-index.mjs, and recovered fixture refresh/avatar/account helpers. Product code, reference captures, production services and databases were untouched.
