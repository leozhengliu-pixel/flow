# Desktop Residual Review Corrections

Rebased on `origin/main` (`c722cbf4`); the workspace-menu changes are already on main and are absent from this product diff. Product source before that test-only rebase: `5f7de69d`. The authoritative matcher, tolerances, fixtures and reference captures were not edited.

## Lead Findings

| Finding | Live reference evidence | Correction |
| --- | --- | --- |
| Table shifts when Display opens | Settled Lead is 48px and Health x=881.5 both closed/open, in dark and system-emulated light. Immediately after reload, an initial 120px track changes later without opening the menu. | Use the shared 48px track in both states. Remove the menu-dependent selector. The browser spec now asserts column invariance instead of a fitted x coordinate. |
| OAuth avatar at 5px | The reference paints three initials at 5px; Flow's shared avatar paints two initials. | Remove the application-kind override and retain existing shared sizing/palette. The remaining avatar differences stay counted. |
| Global metadata state | The metadata trigger's own open/hover styles are sufficient. | Remove the global portal selector. Opening Subscribers no longer forces the unrelated metadata trigger active. |
| Loop heading offsets | The actual location option declares 12px horizontal row padding and 13px/15.6px label text; its live text x is 402.5. It is a different owner from the editor's level button. | Remove the 17px editor-button adjustment. Leave the location comparison unresolved. |
| Document action width | Live declared panel width is 264px. | Use 264px as a desktop minimum with content sizing, rather than fixing the width. English naturally measures 268.328px; its eight shortcut x differences remain counted. Chinese actions fit. Preserve phone sizing. |
| Milestone inset | Live row padding is `0 18px 0 14px`, icon 16px, gap 8px. | Use 14px, not 16px. Let the shared menu size to content within the viewport. This also fixes the observed phone truncation of “Show description history”. |

Retained typography is measured: document title 24px/600, 32px line height, -0.16px letter spacing; metadata/history avatars 9px/400; published-loop trigger 12px. Typography adjustments are desktop-scoped.

## Unified Gate

Numbers are PASS / FAIL / INCONCLUSIVE / FINDING; differences are never waived.

| Area | Before | After | Differences |
| --- | --- | --- | --- |
| projects | 6 / 4 / 1 / 1 | 6 / 5 / 1 / 0 | 19 -> 19 |
| docs | 1 / 8 / 1 / 0 | 4 / 5 / 1 / 0 | 52 -> 31 |
| agent | 6 / 7 / 1 / 0 | 6 / 7 / 1 / 0 | 26 -> 24 |
| settings | 18 / 2 / 0 / 0 | 18 / 2 / 0 / 0 | 4 -> 4 |
| issue-detail | 20 / 6 / 0 / 0 | 20 / 6 / 0 / 0 | 31 -> 31 |

The supplied `desktop-sweep.sh projects,docs,agent,settings,issue-detail` ran 82 screens: PASS 54, FAIL 25, INCONCLUSIVE 3, FINDING 0, differences 109; exit 1. Output/JSON: `/tmp/codex-desktop-r4/review-gates.{out,json}`. Projects were rerun at `46d6363c`: the same 6/5/1/0 and 19 differences, exit 1, in `review-projects-final.{out,json}`. The later shared-menu edit preserves these desktop computed dimensions while allowing the phone history label to fit.

Projects/display-menu now passes. The five 72px observations move to the older List capture, which expects the initial 120px track. Four neutral-sentence glyph-width differences, four description/milestone y differences and six milestone-menu x differences remain. No string was changed or width fitted to erase them. Docs retain subscriber/avatar paint/metrics, the 1.5px submit position and content-sized shortcut positions. Agent retains session offsets, location geometry, avatar metrics and capture text widths. Settings retain feature-card surface/ink and heading text widths; the dark-ink override was removed because it harmed contrast on Flow's dark card. All 31 issue-detail differences remain, including URL/avatar associations and list-header geometry; no issue-list product code changed.

## Browser Review

Own Chromium + Vite used the shared API `127.0.0.1:18200`; reference inspection used only the task's own tab, read-only. The reference light theme was inspected through system media emulation, without changing saved preferences. Flow covered 1440x900, 375x844 and 390x844; light/dark; English/zh-CN; short and long names/titles; two CJK initials and a two-character document chip. Resting, hover, focus-visible, open/active and selected states were inspected where applicable. Current-version restore and empty submit provide natural disabled states; subscriber disabled paint is explicitly labelled simulated. Noninteractive text/avatar states have no active/disabled behavior to assert.

The final state ledger has 108 surface combinations (including both milestone variants), zero interaction/page errors, four unchanged desktop column checks, and eight phone submenu checks with the parent hidden and the child inside the viewport. Long subscriber names use existing ellipsis while retaining their complete accessible names. Reference/Flow raster crops were opened in contact sheets and representative 2x crops. Initial wrong-readiness, blocked-hover and exit-animation captures were diagnostic failures and were rerun; they are not verification passes.

Artifacts: `/tmp/codex-desktop-r4/states-final.json`, `reference-readings.json`, `state-review.mjs`, and `shots/index.html`. The `shots/sheets/` contact sheets label every retained screenshot. The gallery contains the raw reference captures and each Flow state crop, including CJK avatars/chips, inset hovers, keyboard focus, readable milestone actions and the stable table.

## Reviewer Routes

| Route | Clicks |
| --- | --- |
| `/desktop3-projects-display-menu/projects/all` | Open Display options, then close it; watch Health/Priority stay fixed. |
| `/desktop3-docs-document-options/document/document_1793535771347283000_jmvt6n9vmn` | Open document ellipsis; hover/focus Rename; on phone open Move to and return with Back. |
| `/desktop3-docs-document-metadata/document/document_1793535771311358000_f1kxzw4q2b` | Focus title; open Edited; inspect author avatars; close before focusing title again. |
| `/desktop3-docs-document-history/document/document_1793535771275464000_1njcme3gsf` | Edited -> Show document history; inspect selected current version, disabled Restore and author avatar. |
| `/desktop3-docs-document-comment-composer/document/document_1793535771246391000_fskveey6xx` | Select paragraph text -> Comment; inspect draft avatar and empty disabled submit. |
| `/desktop3-projects-milestone-menu/project/project_1793535813919008000_b4gzn8mx47/overview` | Open Alpha actions in the sidebar. Close project details, scroll to Alpha and open its overview menu; inspect the complete history label. |
| `/desktop3-agent-loop-run-history/loop/triage-agent-83b199d9f54f` | Inspect the published loop's Trigger row. |

## Checks

All heavy commands used `~/.flow-local/heavy.sh`. Full `npm run lint` (including typecheck), exit 0. Six targeted Vitest files (documents page/history/inline comments, loop trigger, projects data view, milestone menu): 112 tests, exit 0. The changed `desktop-projects-parity.spec.ts`, bundled Chromium against shared API with isolated workspace fixtures: all three tests, exit 0. The unified entry point's 96 matcher tests passed. No Go changes. Owned servers and installed web dependencies are removed after verification.
