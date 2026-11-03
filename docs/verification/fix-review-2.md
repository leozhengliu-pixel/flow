# fix-review-2 review report

Review branch: `review/fix-review-2`. Main was not changed.

Base: main `f58bc59f`, merged in `3a29158e`. Existing published review history is retained without force-pushing. Source commits are listed per finding below.

The earlier [CI run 41582760525](https://github.com/leozhengliu-pixel/flow/actions/runs/41582760525) failed, including stale nesting-control expectations and the missing cycle phone toggle. Those failures prompted the follow-up fixes and local reruns below. Final branch CI is reported in the completion message; no CI polling or wait is required.

## Findings and review steps

Use a workspace with a parent issue, matching and completed children, empty workflow states, several projects, and team documents. Routes below use `/fix-review-2` as the scratch workspace. Equivalent routes work in any review workspace.

| Item | Reference and result | Commit | Where to click |
| --- | --- | --- | --- |
| 1 | Reference compact select is 116 x 24 CSS px; its chevron sits 6.5px from the right edge. Flow keeps `No grouping` on one line and uses the 9 x 5 glyph with a 6px right inset. Bundled Chromium reports a 7px outer-edge distance because it computes the declared .5px border as 1px. | `9c7f2e6d` | `/fix-review-2/team/FIX/active` > Display options > Sub-grouping. Hover, focus and open the select. |
| 2 | Reference offers Show all, Show matching and Hide, with matching children joined under their parent by an elbow. The explorer now exposes its supported Show matching/Hide choices and preserves server-backed nesting. My issues retains its existing desktop toggle and phone Show all behavior. **Explorer Show all, including descendants outside the view/filter, remains unsupported.** It was not mapped to matching behavior under a false label. | `9c7f2e6d`, `69381344` | Active > Display options > Nested sub-issues > Show matching; close the menu and inspect the child under the parent. Reopen and choose Hide. Also inspect `/my-issues/assigned` on desktop and phone. |
| 3 | Reference Active has no Completed issues control and no default footer before a display change. Flow now follows that structure. Property inventory order puts Due date and Milestone before Cycle. Reset/save-default appear after a change. Saved ordering and selected-property defaults were not proven to be product defaults and were preserved. | `9c7f2e6d` | Active > Display options; verify the initial footer is absent, toggle a property, then Reset. Compare All issues, where Completed issues remains available. |
| 4 | The sampled reference team has no phone sub-team row. The finding is conditional: a sub-team control is relevant only when a child team exists. Flow uses that condition on both desktop and phone; it keeps SLA and Release choices. | `9c7f2e6d` | Team Active > Display options. Repeat with a team that owns a child team. |
| 5 | Team and Estimate now use their shared domain glyphs; External source uses the measured overlapping-circle glyph. The sampled reference team filter does not expose every workspace-level field, so Team/Estimate were checked against the shared glyph inventory rather than claimed as new pixel measurements. | `326a929d` | Active > Add filter; inspect Team, Estimate and External source on desktop and phone; scroll the menu as needed. |
| 6 | Reference row submenus use filled triangles; Status reflects the issue state. Assignee uses an assignment glyph, not an avatar or unassigned-person glyph. More properties and Open in now use the measured paths. Subscription copy remains driven by the actual subscription: the sampled reference issue says Subscribe, while the subscribed scratch issue correctly says Unsubscribe. | `326a929d` | Active > right-click rows with different statuses; hover Status, Assignee, More properties and Open in. |
| 7 | Fresh reference measurement: divider at x306.414, width653.523, height .5 CSS px, 16px after the sub-issue content. Flow's final span starts at x306.5 and is 653.5px wide, with a declared .5px border and 16px top space. The border follows the content's 6px inset. Bundled Chromium computes the half-pixel border as 1px; it remains a real border. | `80c9f606`, `5d85a15a` | Open a parent issue > inspect the boundary between Sub-issues and Activity. |
| 8 | The handover reports a reference Hidden columns panel. The fresh reference All issues view is currently saved as a list, so the board panel could not be re-measured without changing its saved settings. Flow now retains empty columns for both collection and server-backed boards, keeps complete counts, collapses the panel, and reveals a chosen column. Those behaviors were checked in unit tests and the real browser. | `6ddbee3e` | `/fix-review-2/team/FIX/all` > Display options > Board; leave Show empty columns off; scroll to Hidden columns, collapse/expand it and click an empty state. |
| 9 | Stored Todo color `#e2e2e2` remains unchanged. On light surfaces, its rendered stroke uses the existing light-surface limit, LCH lightness 70.313. Dark rendering preserves the configured color. Workflow and composer glyphs react to theme changes. | `42f3db3b` | Open a parent issue with a Todo child; compare light and dark themes. Also open the issue composer status picker. |
| 10 | The row's negative-z-index paint lacked a stacking context. `isolation:isolate` restores hover, focus-visible and selected paint using the existing theme tokens. | `2bfa589a` | `/fix-review-2/projects/all` > hover a row, Tab to it, then select its checkbox. |
| 11 | Reference All projects is ungrouped in the sampled workspace, but its saved default is not proof of a product default. Flow defaults remain intact. An explicit No grouping selection now removes group strips consistently on desktop and phone. | `4cd19d69` | Projects > Display options > Grouping > No grouping; compare desktop and phone. |
| 12 | Reference empty target-date cells are blank at rest. Flow reveals the calendar on cell hover, focus-visible and open state. The browser spec now checks hidden-at-rest followed by visible-on-hover behavior. | `88aa0c3f` | Projects > move the pointer away from an empty Target date; hover it, Tab to it, then click it. |
| 13 | **The original comparison mixed enabled and disabled states.** Fresh reference inspection shows a disabled Add dependency button: its workspace has no other project. Main already contains a disabled-specific Flow rule. The pending change that dimmed enabled buttons was removed. | No product change | Open project Overview > details rail > Properties +. Compare a one-project workspace with a workspace containing another project. |
| 14 | Reference unsorted arrows reserve layout space but are hidden at rest; they appear on hover/focus. Inspecting only the SVG's own opacity initially missed its ancestor's visibility. Flow preserves glyph tracks, reveals unsorted arrows on interaction, uses LCH90.451 for the active dark arrow, restores the team tint gradient over the measured LCH9.232 dark base, and removes the toolbar hairline. The existing light base remains LCH94.44. | `810e3583`, `c9835cdf` | `/fix-review-2/team/FIX/documents` > inspect headings at rest, hover/focus Created, then click it; inspect both team and project group bands. |
| 15 | Fresh reference property controls are 28px high with 8px gaps. Flow now uses that structure. Activity actor and description share inline text with a natural space instead of an 8px flex gap plus 4px actor margin. Fractional font/border rounding remains visible between the two browser builds; no sampled-width or high-specificity fitting overrides were added. | `35462aa0` | Initiative Overview > open details > inspect Properties and Activity, including a long actor name or translated text. |

Changelog commit: `69aeca19`.

Follow-up commits: `ff7526b4` retains the generic phone cycle nesting toggle and tests its behavior; `f9eb4083` updates explorer browser interactions to the supported select. The final scope safeguard, `714e1daf`, keeps the existing phone My issues select alone, with no duplicate toggle. Review it at `/{workspace}/team/{team}/cycle/1` > Display options > Nested sub-issues on a phone, and compare `/{workspace}/my-issues/assigned` > Display options. The former has a toggle; the latter keeps its existing select.

## Files

- Issue display: `issue-explorer-page.tsx`, `my-issues-display-defaults.ts`, `my-issues-display-menu.tsx`, its CSS module and tests, `my-issues-icons.tsx`, and `i18n/translations.ts`.
- Board: `issue-board.tsx`, `issue-grouping.ts`, `paged-issue-list.tsx`, and their focused tests.
- Issue glyphs/context: `context-menu-icon.tsx`, `my-issues-filter-menu.tsx`, `my-issues-list.tsx`, `ui/menu-glyphs.tsx`, `ui/submenu-chevron.css`.
- Issue detail/theme: `detail/issue-desktop.css`, `issue-icons.tsx`, `composer-status-glyph.tsx`, `project-status-color.ts`, `status-default-colors.ts`, and its tests.
- Projects: `projects-page.css`, `projects-data-view.tsx`, `projects-bundle-parity.css`, `e2e/desktop-projects-parity.spec.ts`.
- Documents: `team-documents.css`, `team-documents.test.tsx`, and the shared `styles/tokens.css` group-base token.
- Initiative/activity: `initiative-desktop-parity.css`, `panel/activity-sidebar-section.tsx` and its CSS.
- Browser follow-ups: `e2e/desktop-issue-list-rows.spec.ts`, `e2e/mobile-issues-list.spec.ts`, `e2e/mobile-projects.spec.ts`.
- `CHANGELOG.md` and this report.

## Final Local Validation

Completion checks after merging main `f58bc59f`:

- `~/.flow-local/heavy.sh sh -c 'npm run typecheck && npm run lint && npx vitest run --maxWorkers=2 <the 22 focused paths in validation-commands.md>'`: exit 0; 22 files and 196 tests passed. Output: `/tmp/codex-fix-review-2/checks-completion-final.log`.
- `~/.flow-local/heavy.sh npx playwright test -c /tmp/codex-fix-review-2/playwright.config.mjs desktop-issue-list-rows.spec.ts desktop-projects-parity.spec.ts mobile-issues-list.spec.ts mobile-projects.spec.ts desktop-hairlines.spec.ts`: exit 1; 42 passed, 7 failed during loading, navigation or animation. First `--last-failed` rerun: exit 1, 6 passed and the Views directory remained on Loading. Second `--last-failed` rerun: exit 0, the remaining test passed in 2 seconds. All 49 tests have a passing result on the final product tree without test or product changes between attempts. Logs: `browser-completion-final.log`, `browser-completion-retry.log`, `browser-completion-final-retry.log`.
- Two initial visual-probe attempts exited 1 before taking a new screenshot, at navigation and at the first sub-issue locator. A diagnostic browser pass loaded the parent issue with both children and no page errors. Those failed attempts are not screenshot evidence.
- The next visual pass completed geometry, CJK and disabled-dependency probes, then exited 1 during the state matrix. Eight configurations passed; three captured incomplete steps and the last sheet timed out while capturing. The incomplete sheets were rejected. The scratch capture script now waits for `main` instead of assuming a 350ms load, and uses the normal 30-second browser action timeout. Only the four incomplete configurations are repeated; no product or test assertion was relaxed.
- Final visual results: all 12 configurations passed across the initial pass and targeted retries. `audit-completion-retry.log`: exit 1 (one passed, two still on Loading); `audit-completion-390-retry.log`: exit 0 (one passed); `audit-completion-last.log`: exit 0 (the remaining two passed, one after a logged page reload). The final aggregate is `/tmp/codex-fix-review-2/audit-completion-results.json`. `~/.flow-local/heavy.sh node /tmp/codex-fix-review-2/cycle-states.mjs`: exit 0, 12 configurations, in `cycle-completion-final.log`. All final screenshots were opened and inspected after regeneration. The earlier incomplete sheets were replaced and are not counted as passes.

The resumed pass ran the following through the machine-wide lock, using Node v24.7.0, the scratch API on 18322, Vite on 18323, and bundled Chromium. API and provider output remains under `/tmp/codex-fix-review-2`. Browser specs create independent workspaces through `workspace-fixture`; the visual probes use only the scratch fixture.

- `npm run typecheck`: exit 0.
- Full `npm run lint`: exit 0, including all architecture, design-token and motion gates.
- The 22 focused Vitest files listed in `validation-commands.md`: exit 0, 195 tests before the final additional phone scope test.
- Final display-menu/list/explorer Vitest rerun: exit 0, 25 tests, recorded in `checks-phone-scope.log`.
- Full changed browser specs: `desktop-issue-list-rows.spec.ts`, `desktop-projects-parity.spec.ts`, `mobile-issues-list.spec.ts`, `mobile-projects.spec.ts`; plus `desktop-hairlines.spec.ts`: exit 0, 49 tests. The four changed specs were repeated after the final phone scope correction: exit 0, 45 tests, in `checks-phone-scope.log`.
- `node /tmp/codex-fix-review-2/final-probes.mjs`: exit 0, geometry probes and 12 viewport/theme/language configurations. Final rerun: `probes-phone-scope.log` and `audit-results.json`.
- `node /tmp/codex-fix-review-2/cycle-states.mjs`: exit 0, 12 cycle nesting configurations. `cycle-nesting-states.png` shows resting, hover, keyboard focus-visible and checked states; checkbox open state is its checked state, and it has no disabled state.
- `git diff --check`: exit 0.

The interrupted 48-test browser attempt passed 45 and failed three. Two failures exposed the missing generic phone nesting control; the third used a scratch API without the scripted agent provider. The restored cycle toggle and the repository's `e2e/agent-provider.mjs` resolved those failures. Screenshot review then found that an intermediate broad fallback duplicated the phone My issues control. That intermediate capture failed review; the fallback is now scoped to generic menus, and the final sheets replace those captures.

## Retained Validation

All heavy local commands ran through `~/.flow-local/heavy.sh`; Node was selected from `~/.nvm/versions/node/v24.7.0/bin`.

The complete command lines and exit codes are in [validation-commands.md](/tmp/codex-fix-review-2/validation-commands.md).

| Command | Result |
| --- | --- |
| `npm run lint` | Exit 0; includes `tsc -b`, oxlint and all five architecture/design/motion checks. |
| `npx vitest run --maxWorkers=2` with 22 relevant files after rebase | Exit 0; 193 tests passed. Paths: `validation-commands.md`; output: `checks-rebased.log`. |
| Final `npm run lint` plus display-menu, issue-list and explorer-page tests | Exit 0; 23 tests passed. Output: `checks-nesting.log`. |
| Final paint `npm run lint`, document tests and document-heading browser check | Exit 0; 33 unit tests and 1 browser test passed. Output: `checks-final-paint.log`. |
| `npx playwright test -c /tmp/codex-fix-review-2/playwright.config.mjs desktop-projects-parity.spec.ts` | Exit 0; all 3 tests in the changed spec passed with isolated workspace fixtures and bundled Chromium. |
| Targeted document-heading and project/initiative browser checks | Exit 0; 3 passed. Output: `e2e.log`. |
| Browser state matrix | 12 configurations: 1440x900, 390x900 and 375x900, each in light/dark and English/zh-CN. Theme is asserted from the actual root attribute. Final output: `audit-results.json`. |
| Final geometry, light-ring, document-token and CJK probes | Exit 0; `final-probes.json` records both themes and two actual CJK initials at all three widths. |
| `git diff --check` | Exit 0. |

Go, REST, migrations and SDK shapes were unchanged, so no Go build/test/vet or generation commands were required. No model-provider behavior changed, and no real-model request was needed.

## Browser Evidence

Screenshots are in `/tmp/codex-fix-review-2/shots`. Each state sheet embeds real Chromium crops rather than recreating the controls.

The sheets cover resting controls, pointer hover, keyboard focus-visible, open menus, selected layout/properties/status/order, disabled Board/Ordering under Focus grouping, long English and Chinese titles, CJK fixture names and project chips. Native-disabled states do not exist for row hover bands, document heading sorts or the target-date button; those are not reported as disabled-state checks.

The original state script contained incorrect locators and an account-theme fixture error. Those attempts were failures, not evidence of a passing light-theme run. Final sheets were regenerated after checking the actual theme. The inherited `ref-projects-rest.png` contains a loading screen and is excluded from verification.

Reference comparison captures: `ref-active-display-before.png`, `ref-nesting-options.png`, `ref-phone-recheck.png`, `ref-context-recheck.png`, `ref-filter-recheck.png`, `ref-projects-recheck.png`, `ref-projects-hover.png`, `ref-docs-recheck.png`, `ref-docs-hover-recheck.png`, `ref-initiative-panel.png`, `ref-project-properties-disabled.png`, `ref-issue-divider.png`.

| Item | Reference capture | Flow capture/crop label |
| --- | --- | --- |
| 1 | `ref-active-display-before.png` | `states-1440-dark-en-US.png`: Display / Nesting |
| 2 | `ref-nesting-options.png`, `ref-phone-recheck.png` | All state sheets: Nesting open / Nested list |
| 3 | `ref-active-display-before.png` | Desktop state sheets: Display resting |
| 4 | `ref-phone-recheck.png` | Phone state sheets: Display resting |
| 5 | `ref-filter-recheck.png` | State sheets: Filter glyphs and open state |
| 6 | `ref-context-recheck.png` | Desktop state sheets: Row context / Status submenu |
| 7 | `ref-issue-divider.png` | `final-issue-dark.png`, `final-issue-light.png` |
| 8 | `ref-all-issues-list.png` documents the current saved list state; the inherited `/tmp/review-issues/keep/fd-board-all.png` is a Flow before-capture, not a reference image | State sheets: Board / Hidden columns expanded and collapsed |
| 9 | Original pale-ring finding; no reference theme change was made | `final-issue-light.png`; stroke values in `final-probes.json` |
| 10 | `ref-projects-hover.png` | Desktop state sheets: Project row hover / focus / selected |
| 11 | `ref-projects-recheck.png` | Phone state sheets: Projects; explicit desktop No grouping covered by the browser spec |
| 12 | `ref-projects-recheck.png` | Desktop state sheets: Projects resting / Empty target date hover, focus and open |
| 13 | `ref-project-properties-disabled.png` | `flow-project-properties-disabled.png`; enabled controls in desktop state sheets |
| 14 | `ref-docs-recheck.png`, `ref-docs-hover-recheck.png` | State sheets: Documents / Heading hover, focus and selected ordering |
| 15 | `ref-initiative-panel.png` | Desktop state sheets: Initiative |

All reference calls went through the required `bu.sh` lock wrapper and used their own temporary tab, closed at the end of each call. Reference interaction stayed read-only.

### Screenshot Review Inventory

The prior pass opened every retained screenshot for visual inspection. The completion pass reopened all 46 retained screenshot files, including the final state sheets, geometry/CJK crops, cycle sheet, reference comparison captures, and the explicitly excluded historical/loading captures. Historical discovery captures and the loading-screen failure remain excluded below. The final state sheets show the same named control states in each configuration:

| Viewport | Dark English | Dark Chinese | Light English | Light Chinese |
| --- | --- | --- | --- | --- |
| 1440x900 | `states-1440-dark-en-US.png` | `states-1440-dark-zh-CN.png` | `states-1440-light-en-US.png` | `states-1440-light-zh-CN.png` |
| 390x900 | `states-390-dark-en-US.png` | `states-390-dark-zh-CN.png` | `states-390-light-en-US.png` | `states-390-light-zh-CN.png` |
| 375x900 | `states-375-dark-en-US.png` | `states-375-dark-zh-CN.png` | `states-375-light-en-US.png` | `states-375-light-zh-CN.png` |

- Desktop sheets show display-select hover/focus/open/selection, the matching child list, hidden-column expansion/collapse, filter/context glyphs, project-row hover/focus/selection, empty-date hover/focus/open, document-heading hover/focus/ordering, initiative spacing and disabled Focus controls. No broken layout or failed-step screen is accepted as a pass.
- Phone sheets show the narrower display controls in both languages, matching child indentation, hidden-column controls, long English/CJK project titles, document groups and the existing My issues controls. The menus scroll naturally below the viewport.
- `cjk-1440-dark.png`, `cjk-390-dark.png`, `cjk-375-dark.png`, `cjk-1440-light.png`, `cjk-390-light.png`, `cjk-375-light.png` show the parent issue row with two actual CJK initials fitting inside its avatar. Chinese property chips are also visible in the state sheets.
- `final-issue-dark.png` and `final-issue-light.png` show both child statuses, the inset Activity divider, and the visible light-theme Todo ring. `flow-project-properties-disabled.png` shows the existing disabled dependency plus.
- `ref-active-page-inspect.png` shows the reference Active list. `ref-active-display-before.png` and `ref-active-recheck.png` show its display menu; `ref-nesting-options.png` shows the three reference nesting choices; `ref-phone-recheck.png` shows the phone menu without a sub-team row.
- `ref-context-recheck.png` shows the reference row menu. `ref-filter-recheck.png` shows its filter inventory and External source glyph.
- `ref-projects-rest-loaded.png` and `ref-projects-recheck.png` show the reference project list at rest; `ref-projects-hover.png` shows row hover; `ref-target-date-hover.png` shows the revealed 16x16 calendar on target-cell hover.
- `ref-docs-recheck.png` shows the sorted Name arrow and hidden unsorted arrows. `ref-docs-hover-recheck.png` shows Created's hover arrow alongside the sorted Name arrow and tinted group bands.
- `ref-initiative-rest.png` shows the reference overview with its details rail closed. `ref-initiative-panel.png` shows the open Properties/Activity rail and inline actor text. `ref-issue-divider.png` shows the reference sub-issue divider.
- `ref-all-issues-list.png` shows the reference All issues view in its current saved list layout; it does not verify reference board geometry.
- `ref-project-properties-disabled.png` shows the open project rail and disabled plus. `ref-project-detail-recheck.png` and `ref-project-panel-buttons.png` are earlier discovery captures with the rail closed, so they do not substantiate the plus-state comparison.
- `flow-display-before.png` captured an opening animation with low opacity; `flow-projects-before.png` and `flow-initiative-panel-before.png` are historical intermediate views. They are not final verification. `ref-projects-rest.png` is a loading screen and is explicitly excluded.

## Limits

- Explorer Show all nesting is not implemented; Show matching remains backed by the existing server query. My issues' existing Show all behavior is retained.
- The reference Hidden columns panel could not be re-measured in this pass because All issues is currently saved as a list; its saved layout was not changed.
- Ordering, selected display properties, project Summary and default project grouping were not changed based on one workspace's saved settings.
- Some geometry differs by fractional font metrics and Chromium's minimum rendered border width. The report distinguishes authored .5px hairlines from the bundled browser's computed 1px border.
- Review-1-owned avatar, picker and phone sub-issue-row work was not changed. CJK data was used only in browser fixtures.

## Final CI and Cleanup

The branch is pushed only to `review/fix-review-2`; main is left for the lead to merge after review. The completion message records the exact pushed commit and a single CI status snapshot without waiting for branch CI. Scratch API, Vite and the scripted provider are stopped by their recorded PIDs, and this worktree's `web/node_modules` is removed after verification. No production service, database, container, or model proxy is changed.


Final screenshot addition: `cycle-nesting-states.png` shows the generic cycle toggle in twelve width/theme/language configurations. Each case includes resting, hover, keyboard focus-visible and checked crops. There is no separate popup or disabled state for this binary toggle.
