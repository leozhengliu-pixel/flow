# Desktop Projects Resume

Baseline: fresh gate on e69cf0fe, not an old sweep log. Verified product commit: aeaec04a16b833ec0bbe3e787a1b92d44981fca9.

Restart verification on November 2, 2026: no unpushed product commits or tracked edits remained. The area gate was rerun before reviewing the pending report; no old sweep logs were read. It reproduced all 31 listed differences exactly, with the same screen statuses and matching hashes for all five gate tools. This continuation adds the report files only; it makes no product or gate changes.

## Results

Projects: 6 PASS, 5 FAIL, 1 INCONCLUSIVE, 1 paid-plan SKIPPED. Numerical differences: 301 before, 31 after. PASS is scoped to this gate's measured matches, not exhaustive pixel parity.

| Screen | Final | Before | After |
| --- | --- | ---: | ---: |
| cycles | INCONCLUSIVE | 12 | 1 |
| display-menu | PASS | 8 | 0 |
| initiative-detail | PASS | 45 | 0 |
| initiatives | PASS | 16 | 0 |
| list | FAIL | 8 | 5 |
| milestone-menu | FAIL | 50 | 12 |
| overview | FAIL | 43 | 5 |
| project-actions | FAIL | 45 | 5 |
| project-activity | PASS | 17 | 0 |
| project-issues | PASS | 16 | 0 |
| project-overview | FAIL | 37 | 3 |
| releases | SKIPPED | 0 | 0 |
| timeline | PASS | 4 | 0 |

Every remaining numerical difference has an individual classification and reason in [the JSON inventory](desktop-r2-projects-resume-differences.json). Withheld measurements, ambiguous labels, coverage flags, and conservative missing-label candidates are retained separately, not relabeled as missing features or passes.

## Changes

- Initiative directory: measured 12px column gaps and actual column widths, 450-weight muted labels, title contrast, and update-count hit areas.
- Timeline: a flex canvas extends grid and today line through the viewport without fitting individual row heights.
- Initiative overview: content-sized shared label track, heading insets, owner/date glyphs, resources, reactions, and project actions.
- Project metadata and activity: title/summary tracking, initiative chips, resource gaps, date/lead controls, activity-tab tokens, sidebar caption layout, issue-intro keycaps, and open-menu triggers.
- Cycle directory: empty-state contrast/tracking, unshifted header controls, and the measured decorative breadcrumb separator. No new translatable UI copy.
- Files: project-detail/project-detail-desktop.css; projects-page/project-timeline-desktop.css, projects-bundle-parity.css, project-lead-picker.tsx; initiatives/initiative-desktop-parity.css, initiative-detail-page.tsx, initiative-shared.tsx; cycles/cycles-bundle-parity.css, cycles-page.tsx; styles/tokens.css; e2e/desktop-projects-parity.spec.ts; CHANGELOG.md. All UI rules/tokens changed here are desktop-scoped. The added semantic classes have no phone styles.

## Gate Corrections

Scratch tooling only; original routes/steps, evidence hashes, reference JSON/PNGs, tolerance values, and coverage floors were not rewritten.

- Replay the sidebar milestone menu, including the captured highlighted Edit row, rather than the unrelated overview menu.
- Select Year and fix the timeline clock to the captured October 30 date; no product clock was hard-coded.
- Exclude text wholly clipped by overflow ancestors and respect hidden/transparent live ancestors.
- Match document headings separately from outline labels and compare explicit line boxes to line boxes, retaining glyph widths for stretched headings.
- Use deepest equal-box presentation divs/paragraphs for date wrappers and contenteditable fields; normalize their text colors consistently.
- Anchor relative text y to the same semantic row on both sides, not a chip on one side and its enclosing project row on the other.
- Treat isolated dot separators as ambiguous identities, not interchangeable labels from unrelated rows.
- Compare the actual 44px header row rather than an 87.5px combined header, and the outer aside rather than its inner scrolling wrapper. Horizontal chrome insets are measured from visible glyphs/cards so equivalent nested padding and child margins do not create false failures.
- Compare layout-tab boxes/effective inter-tab spacing when the reference wrapper paints the same background as its parent; retain the original wrapper bounds/gap as evidence. No visible border/fill is flattened.
- Twenty-five matcher regressions include deliberate width, gap, line-box, row-offset, and header-inset mutations. The final live sweep also adds 2px of main-frame padding, detects it, restores it, and verifies restoration on every measured screen.

Tool SHA-256 values are recorded in the JSON inventory. Tooling remains in the supplied scratchpad, not product source.

## Remaining Decisions

1. Shared Lead column: approve an authored width contract before changing the current 48px column to the captured 120px column. No fitted width was restored.
2. Shared rich-text editor: agree on folding-affordance placement and block spacing before changing global editor behavior or fitting project-section y positions.
3. Target-date placeholder: recover its missing intrinsic/box contract before compensating a downstream 2px team-label displacement.
4. Capture limitations: anonymized description text bounds, uniformly scaled menu capture, and the sparse cycle reference cannot be made identical by product CSS. These are individually listed, not waived.

## Verification

All heavy jobs used ~/.flow-local/heavy.sh. Successful final checks:

Fresh restart checks, using Node 24.19.0 for web validation:

- `node /tmp/codex-desktop-r2-projects/resume-session.mjs resume2-baseline --mutation`: exit 1; 6 PASS, 5 FAIL, 1 INCONCLUSIVE, 1 SKIPPED, 31 differences; all 12 live mutation proofs passed. Gate launch used the requested Node 24.7.0 path.
- `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-desktop-r2-projects/api ./cmd/server` from api/: exit 0.
- `FLOW_CI_SHOTS=/tmp/codex-desktop-r2-projects/shots/resume2 npx --no-install playwright test --config .codex-projects.playwright.config.ts e2e/desktop-projects-parity.spec.ts` from web/: exit 0, 3 passed.
- `node --test <scratchpad>/desktop-match.test.mjs`: exit 0, 25 passed.
- `npx --no-install vitest run --maxWorkers=2` for the project grid/display/view-state/timeline/lead, cycle capacity/menus, and project overview/sidebar/activity/issues/menu/detail files: exit 0, 17 files / 155 tests. Exact paths are in the JSON inventory's restartVerification field.
- `npx --no-install vitest run --maxWorkers=2 src/components/initiatives/initiative-shared.test.tsx src/components/initiatives/initiative-page-chrome.test.tsx src/components/initiatives/initiative-latest-update.test.tsx`: exit 0, 3 files / 9 tests.
- `npm run lint`: exit 0, including typecheck and all repository UI/design/motion checks.
- A structured comparison of fresh gate failures against the inventory confirmed each name, property, wanted value, actual value, classification, and reason; all five recorded gate-tool hashes still match.

Earlier-session verification retained below as historical evidence, not commands rerun on restart:

- npm run lint, including npm run typecheck and all five repository UI/design/motion checks: exit 0.
- npx playwright test --config .codex-projects.playwright.config.ts e2e/desktop-projects-parity.spec.ts: 3 passed, exit 0. Specs create independent workspaces using workspace-fixture.
- Targeted Vitest files for every changed component were run before each product push. The final broad menu/cycle pass covered 9 files / 93 tests, exit 0; the project metadata pass covered the overview/sidebar/activity/issues/grid/lead files, exit 0. Exact successful command arguments and counts are in /tmp/codex-desktop-r2-projects/*-unit.log and checks.mjs.
- node --test scratchpad/codex/desktop-match.test.mjs: 25 passed, exit 0.
- Area gate with --repaired --mutation: exit 1; remaining statuses above are intentional report outcomes, not runner crashes. All measured-screen mutation proofs passed.
- Scratch Go API build to /tmp/codex-desktop-r2-projects/api: exit 0. No Go source, REST shape, migration, OpenAPI, or SDK changes were made by this task, so Go tests/vet were not required for its commits.

Browser verification used bundled Playwright Chromium at 1440x900, a task-owned auth-disabled API on 18293 with a scratch SQLite database, and Vite on 5494. Gate Vite used 5493 and the supplied fixture API on 18200. Servers were owned/stopped by their runners by PID. No user Chrome, production host, production database, or unrelated process/container was touched.

Intermediate dependency-removal interruptions and failed selector/layout assertions were not counted as verification. They were corrected and rerun. No full test suite or phone sweep was run; there are no changed phone CSS/token rules. The lead's complete phone sweep and main CI remain external checks.

## Reviewer Routes

Use any workspace with a project and initiative; fixtures in the browser specs are created automatically.

- /<workspace>/initiatives: create an active initiative with one linked project, inspect column labels, status alignment, and the update-count control; open it.
- /<workspace>/initiative/<slug>/overview: inspect Properties/Resources alignment and the latest update; toggle Properties, scroll to Projects, and open Display options or Add a project.
- /<workspace>/projects/all: create a project with a milestone, open Display options, toggle Initiatives, and choose Timeline. Select Year, reload, and click Today. Inspect the full-height grid and today line.
- /<workspace>/project/<slug>/overview: inspect the title, summary, initiative/resource chips, and sidebar. Open Project actions, Escape, then the sidebar milestone's actions menu and hover Edit.
- /<workspace>/project/<slug>/activity: switch Comment to Update and back; inspect tab fills and attachment control.
- /<workspace>/project/<slug>/issues: use a project with no issues and inspect the introductory heading and C keycap.
- /<workspace>/team/<team>/cycles: enable cycles with auto-create disabled and leave the directory empty; inspect breadcrumb, favorite icon, and empty-state text.

## Screenshots

All ten fresh restart screenshots were opened individually and inspected. [The screenshot manifest](desktop-r2-projects-resume-shots.md) lists every fresh file and what it shows. These captures show no error page, broken overlap, or failed action. The initiative Projects capture shows its heading and actions only; rows below the viewport are not visual verification. Screenshot coherence does not turn the remaining gate failures into passes. Earlier captures and logs were not used as restart verification.

Exact argv arrays, exit codes, and scoped file lists are recorded in the JSON inventory's validation field. Node 24.19.0 was used to satisfy the current web package engine requirement. The temporary browser config and installed node_modules were removed after verification.

## Product Commits

- 9c57f284 fix(web): align desktop initiative directory columns
- f07e576e fix(web): extend the desktop timeline grid through the viewport
- 285b5890 fix(web): align initiative overview metadata and controls
- 5d27da01 fix(web): align desktop project metadata and activity controls
- 18b9fc82 fix(web): align desktop directory and menu chrome
- 924e6941 fix(web): align desktop header content edges
- aeaec04a fix(web): keep desktop cycle separators muted

Each product commit was rebased and pushed to origin/main after its scoped checks.

## File Inventory

- CHANGELOG.md
- web/e2e/desktop-projects-parity.spec.ts
- web/src/components/cycles/cycles-bundle-parity.css
- web/src/components/cycles/cycles-page.tsx
- web/src/components/initiatives/initiative-desktop-parity.css
- web/src/components/initiatives/initiative-detail-page.tsx
- web/src/components/initiatives/initiative-shared.tsx
- web/src/components/project-detail/project-detail-desktop.css
- web/src/components/projects-page/project-lead-picker.tsx
- web/src/components/projects-page/project-timeline-desktop.css
- web/src/components/projects-page/projects-bundle-parity.css
- web/src/styles/tokens.css
