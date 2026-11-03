# Fix Review 1

Review branch: `review/fix-review-1`. UI changes are for owner review, not a push to main. Rebased onto `0c09167ea2d015f5a279ffdbb973a52347f640f0` without conflicts. Prior work was retained; the final continuation repaired the initial-focus race in the workspace shortcut.

## Findings, Measurements and Commits

All evidence paths below are under `/tmp/codex-fix-review-1/shots/` unless otherwise noted. Reference captures are read-only. Numeric values are CSS pixels; L values are CIELCH lightness. Different fixture content is not a claim of whole-page pixel equality.

| Item | Cause and changed files | Reference versus Flow result | Commit |
| --- | --- | --- | --- |
| 1. Avatar dimensions | Inline-size containment could collapse an unsized avatar; consumers also depended on a missing shared class. `ui/user-avatar.tsx`, `ui/user-avatar.css`. | Reported meeting chip was 0x0; restored to 18x18. Update-footer and release-author contexts are 20px and 24px. Browser inventory parses every direct source consumer and mounts its classes, including no-class consumers. | `9b7a4c20` |
| 2. Wide initials | Fixed font sizing could exceed the slot. `ui/fitted-initials.tsx`, `workspace/workspace-initials.tsx`, shared avatar/workspace CSS. | WW was 21.9531px in a 20px slot; measured scale 0.911032 yields 20px. DPR1 sidecar has a 0.0078125px edge displacement, within browser subpixel rounding. WW, MM, CJK, mixed-script, short and long names pass containment checks. | `9b7a4c20` |
| 3. Workspace chevron | The filled path inherited the brighter workspace-name foreground. `layout/sidebar.css`, `styles/tokens.css`. | Dark fill now L=60.621 instead of L=90.155, matching the reference. Light uses the existing muted family at L=40. Rest, focus and open crops are supplied. | `3a522ad1` |
| 4. Workspace cascade | Moving to a sibling did not close the controlled submenu. Closing on every focus then raced initial shortcut focus. `workspace/workspace-menu.tsx`, `e2e/workspace-menu.spec.ts`. | Reference Switch workspace -> Log out closes the cascade and leaves only Log out visually highlighted after 1.6s. Flow checks closed submenu and exactly one highlighted row. Initial focus is excluded from sibling closing; five O/W -> Escape cycles pass. | `5613fe85`, `a2cb7cb2` |
| 5. Label picker | A desktop display:none rule suppressed unchecked boxes; phone tracks reused compact spacing. `styles/picker-parity.css`, `styles/mobile-shell.css`. | Hovered desktop box is 14x14 with 1px border at L=55.92. Phone checkbox x=35.5, icon slot x=62.5, text x=86.5, dot 9px. Original text x=80.3 and dot 8px are corrected. | `e07cdbd7` |
| 6. Shared menu states | Foreground overrides defeated highlight/muted tokens; desktop cascade ignored its leading heading; phone context menus lacked filtering. `layout/sidebar-team-menu.tsx`, `layout/sidebar.css`, `agent/agent-page.module.css`, `ui/menu-submenu.*`, new `ui/menu-filter.*`, `issue/issue-icons.tsx`. | Dark hovered team/skill text L=100; Subscribe headings L=64.714. Phone Subscribe height 302, heading text x=42.5, label x=62.5; root team menu height 416. Original desktop capture is corrected from y=632 to y=602. Fresh reference places the same cascade at y=631 with trigger y=661: the invariant is a 30px leading-heading offset and first option aligned with the trigger, not an absolute screen y. Flow uses measured heading height. Team settings is outline; Slack is monochrome in this menu; configured Feishu/Lark handling is preserved. Phone context filtering has results, empty state, and no cascade chevrons. | `e2abd760` |
| 7. Chinese navigation | Case-sensitive full labels were missing despite lowercase variants. `i18n/translations.ts`, command and navigation coverage tests. | Go to Initiatives/Views/Members and Open navigation have complete Chinese entries. Coverage enumerates command navigation keys. | `e4ed59f3` |
| 8a. Phone sub-issue | The parent breadcrumb did not yield space to a nonshrinking label. `my-issues/my-issues-list.module.css`, row test selector in `my-issues-list.tsx`. | At 375/390, long parent text ends before the label; label ends before the assignee. The label pill is fully visible. | `578d8ff0` |
| 8b. Project events | A fixed 17px event height let wrapped text escape into the next card. `project-detail/project-detail-page.css`. | Height is now intrinsic, with a 17px minimum. Every event's text bottom is within its own bounds at both phone widths, including a long mixed-script rename followed by a card. | `97456f42` |
| 9a. Reviewer prompt | The generic child-span color overrode the muted prompt. `reviews/reviews-page.css`. | Dark Add reviewers is L=61.803 versus reference approximately L=62, instead of approximately L=90. Light uses shared issue-muted L=37.776. Keyboard focus remains visible. | `25c3c605` |
| 9b. Phone update composer | Absolute footer positioning required a redundant 52px bottom reserve. `project-detail/project-detail-page.css`. | Padding-bottom is 0, as in the reference, with the footer in normal flex flow. Reference empty composer height is 257px; Flow retains a 257px minimum and grows for additional change summaries and long text. Empty-disabled and filled-enabled states are checked. | `29690f8b` |
| 9c. Review issue link types | The row menu offered Link issues rather than the association type; metadata had no persistence. `reviews/review-overview.tsx`, `lib/api.ts`, `types/flow.ts`, review translations; `api/cmd/server/reviews_api*`, `api/internal/domain/models.go`, `api/internal/store/{auth.go,issue_query.go,sqlite_test.go}`. | Menu now offers Change link type and Unlink issue from pull request; choices are Resolves, Contributes, Related. Selecting Related, reloading, grouping under Related, and unlinking pass against the scratch API. API rejects invalid types/unlinked issue IDs and redacts hidden-issue metadata. | `aedea9c2` |

`c864ee18` adds the phone geometry/state coverage. `8e48b231` completes long/short label, workspace-hover, isolated Skills focus, opened reviewer-dialog, pending link mutation and saved selection coverage. Each change has the requested Codex trailer. `CHANGELOG.md` records avatar, menu/localization, phone geometry and link-type behavior under Unreleased.

## Reference and Flow Evidence

| Item | Reference / before crop | Flow crop and what it shows |
| --- | --- | --- |
| 1 | `/tmp/verify-state/crops/meet-chip.png` (reported regression) | `meeting-avatar-1440-Dark-en-US.png`: wide Latin initials beside the attendee name. `meeting-avatar-390-Light-zh-CN.png`: two Chinese initials fit a visible circular avatar beside the two-character name. |
| 2 | `/tmp/verify-ds/ww-zoom.png`, `initials-grid-before.png` | `initials-grid-after.png`, `initials-grid-after-dpr1.png`: WW stays within the 20px workspace tile at both pixel densities. Measurements: `../initials-after.json`, `../initials-after-dpr1.json`. |
| 3 | `ref-workspace-rest.png`, `ref-workspace-hover.png`, `ref-workspace-open.png` | `workspace-rest-1440-dark-en-US.png`, `workspace-focus-1440-light-en-US.png`, `workspace-open-1440-dark-en-US.png`: muted chevron, visible keyboard ring and open panel. |
| 4 | `ref-workspace-switch.png`, `ref-workspace-logout.png`, `ref-workspace-confirm.png` | `flow-workspace-before.png`: intermediate diagnostic capture, not a final cascade verification. The final Playwright case asserts hidden cascade, closed trigger and only Log out highlighted; repeated shortcuts also pass. |
| 5 | `ref-labels-phone-390.png` | `labels-hover-1440-dark-en-US.png`: empty bordered hover box. `labels-hover-390-dark-en-US.png`, `labels-selected-390-light-zh-CN.png`: separate checkbox/icon/text tracks and a checked label. |
| 6 | `ref-team-hover.png`, `ref-team-phone.png`, `ref-skills.png`, `ref-subscribe-desktop.png`, `ref-subscribe-phone.png`, `ref-subscribe-touch-375.png`, `ref-subscribe-confirm.png` | `team-hover-*`/`team-focus-*`: outline settings and monochrome Slack. `skills-hover-*`, `skills-selected-*`: strong hovered text and selected checkmark with long Latin/CJK names. `subscribe-focus-*`, `subscribe-disabled-*`, `subscribe-selected-*`: muted headings, unchecked, pending and saved states. `context-filtered-*`, `context-empty-*`: matching Priority result and deliberate no-results state. |
| 7 | Missing exact translation keys in the original command dictionary | Chinese command/navigation unit coverage plus Chinese workspace and menu browser cases. No separate screenshot proves the command-list entries; this part is covered by unit assertions. |
| 8a | `/tmp/verify-ds/ws5-first-crop.png` (reported regression) | `phone-breadcrumb-{375,390}-{Dark,Light}-{en-US,zh-CN}.png`: long breadcrumb truncates before the fully visible label and avatar. |
| 8b | `ref-project-activity-375.png` | `project-feed-{375,390}-{Dark,Light}-{en-US,zh-CN}.png`: wrapped rename remains above the following update card. |
| 9a | `ref-review-detail.png` | `review-properties-*`, `review-properties-focus-*`: muted reviewer prompt and its keyboard ring. |
| 9b | `ref-project-update-375.png` | `project-composer-*`, `project-composer-filled-*`: empty and long-filled update composer, contained summary/footer and submit state. |
| 9c | `ref-review-issue-actions.png`, `ref-review-link-types.png` | `flow-review-issue-menu-*`, `flow-review-issue-menu-keyboard-*`, `flow-review-link-types-*`: actions, keyboard-selected action and the three types with current checkmark. |

The 320 source captures are indexed in `/tmp/codex-fix-review-1/evidence-index/manifest.txt` and were visually reviewed using all 20 contact sheets, reopened during final delivery. These include `reviewers-open-*` (the opened selector), `flow-review-link-pending-*` (disabled issue actions during save), `flow-review-link-selected-*` (the saved Related checkmark), `workspace-hover-*` (muted chevron over the hover background), and labels with long English/Chinese names plus the one-character A. Existing contact images are redundant previews, not additional passing states.

Failed capture steps are excluded: `ref-project-composer-375.png` and `ref-project-composer-verified-375.png` show the details overlay; `ref-subscribe-measured-375.png` and `ref-subscribe-confirmed-375.png` show the sidebar drawer. `flow-subscribe-before.png`, `flow-workspace-before.png` and `initials-grid-before.png` are diagnostic/before evidence. Refreshed Skills focus crops move the pointer away before keyboard selection and show one focused row. No failed capture is counted as parity verification.

## State Coverage

Flow matrix: 1440x900, 375x667 and 390x844, dark/light, English/zh-CN. Real bundled Chromium used Vite port 5412 and an auth-disabled API on 5411, with the database under `/tmp/codex-fix-review-1/`. The specs create workspaces through `workspace-fixture`, not a fixed local workspace.

| Control | Checked states and content | Evidence |
| --- | --- | --- |
| Workspace trigger/menu | Rest, hover, focus-visible, open, hovered sibling, keyboard selection, current workspace, close/reopen shortcuts; A, WW, MM, CJK and long mixed names; actual logo, Settings, Invite and OAuth contexts. No disabled trigger product state. | `workspace-*`, `initials-grid-after*`; workspace spec |
| Team/Subscribe | Hover, focus-visible, open, unchecked, selected, pending-disabled during a held API save, disabled Leave team. Long localized subscription copy. | `team-*`, `subscribe-*` |
| Skills | Open, hover, focus-visible assertion, selected check; one-character, long Latin and long CJK names. No independently disabled Skills row was fabricated. | `skills-*` |
| Labels/context | Open, hovered unchecked checkbox, selected label; phone filter input/result/empty/reset and hidden chevrons. Existing Bug/Feature/Improvement plus A and long English/Chinese labels. | `labels-*`, `context-*` |
| Meeting avatar/chip | Nonzero size, wide Latin and two CJK characters beside a short/long attendee name. Decorative avatar has no separate active/disabled state. | `meeting-avatar-*`; full consumer inventory test |
| Phone breadcrumb/event | Long mixed-script issue and project names; label/assignee non-overlap; multiline event containment. These are layout surfaces, not toggles. | `phone-breadcrumb-*`, `project-feed-*` |
| Review prompt/link menu | Rest, hover, focus-visible, opened reviewer selector, open action and type menus, keyboard-selected action, pending-disabled mutation, current type, saved Related checkmark after reload, unlink. | `review-properties-*`, `reviewers-open-*`, `flow-review-*` |
| Update composer | Empty/disabled submit, update tab selected, focused long text, enabled submit and contained footer. | `project-composer-*` |

These checks establish the listed states, not every possible cross-product. The live reference remained in its existing dark English theme; no reference theme/account settings or entities were edited. Flow light/Chinese checks are not a claim of live light/Chinese reference parity. Controls without a disabled product state are identified above rather than artificially disabled for a screenshot.

## Reviewer Clicks

1. At `/:workspace/my-issues/assigned`, inspect the workspace trigger at rest, hover and Tab focus, then open it. Hover Switch workspace, then Log out; the cascade must close. Escape, press O then W, and repeat five times. Use a review workspace named WW/MM or two CJK characters; repeat at 390 and 375 using the sidebar drawer.
2. At `/:workspace/meeting/:id`, add an attendee in a review workspace and inspect the chip. Also inspect project-update authors and release-note authors for nonzero avatars. Use a two-character Chinese name and a wide Latin name.
3. At `/:workspace/issue/:identifier`, open Labels. Hover an unchecked row, select it, reopen, and inspect the checkmark. Repeat at 390/375 and in both languages/themes.
4. At `/:workspace/agent`, open the sidebar team ellipsis and hover Team settings; inspect its outline icon and the Slack notification icon. Open Subscribe, Tab/arrow through options, toggle one and reload. At phone width, inspect the filter and heading/row indentation. Open Skills and inspect short, long and selected skills.
5. At `/:workspace/my-issues/assigned` on a phone, right-click an issue row. Type Priority or its Chinese translation in Filter; then type a string with no matches; clear it. Rows should filter/reset and have no cascade chevrons.
6. Set zh-CN in account preferences, open the command menu and search the navigation commands for initiatives, views and members. At `/:workspace/agent` on a phone, inspect the navigation button accessible label: it must be fully Chinese.
7. At `/:workspace/my-issues/assigned` on a phone, inspect an assigned sub-issue with a long parent title and label. The label and assignee must remain separate from the breadcrumb.
8. At `/:workspace/project/:slug/activity`, inspect a long rename event followed by a comment/update. Select Update, inspect the empty submit state, and enter a long update. The footer must remain inside the composer with no extra 52px bottom reserve.
9. At `/:workspace/review/:slug` on a phone, inspect Add reviewers, Tab to it, then open it. For a linked issue, use its ellipsis -> Change link type -> Related. Reload, confirm the Related grouping, then reopen the issue actions and unlink.

## Checks and Exit Codes

All heavy checks use `~/.flow-local/heavy.sh`. Node commands use the requested Node 24.7.0 path. No full Go or browser suites were run.

| Command | Result | Log |
| --- | --- | --- |
| `npm run lint` (includes `npm run typecheck`, oxlint, pickers, design tokens, UI foundations, functional controls and motion) | Exit 0 | `/tmp/codex-fix-review-1/handover-lint.log` |
| `npx vitest run --maxWorkers=2` with menu-filter, menu-submenu, agent-glyph, workspace-initials, workspace-menu, sidebar-team-menu, reviews-page, command-menu-zh, command-navigation-coverage, translation-key-coverage, my-issues-list, project-activity, agent-page and issue-header paths | Exit 0; 13 discovered files, 162 tests | `/tmp/codex-fix-review-1/handover-vitest.log` |
| `npx playwright test -c /tmp/codex-fix-review-1/playwright.config.ts desktop-avatar-consumers.spec.ts desktop-menu-review-states.spec.ts mobile-review-geometry.spec.ts mobile-review-linked-issue.spec.ts workspace-menu.spec.ts` | Exit 1 initially: 63 passed; two OAuth setup calls used the default API port 4180 instead of the scratch port | `/tmp/codex-fix-review-1/handover-e2e.log` |
| `env FLOW_MOBILE_API_URL=http://127.0.0.1:5411 npx playwright test -c /tmp/codex-fix-review-1/playwright.config.ts workspace-menu.spec.ts --grep 'routes and shared avatars work in Chinese'` | Exit 0; remaining 2 passed. All 65 cases passed across these runs. | `/tmp/codex-fix-review-1/handover-workspace-zh.log` |
| `env GOMAXPROCS=2 go test -p 1 ./cmd/server ./internal/store ./internal/domain -run 'TestReview\|TestFilterBootstrapTeams\|TestPagedWorkspaceMetadata\|Test.*FieldConflict'` from `api/` | Exit 0; server/store pass, domain has no matching test | `/tmp/codex-fix-review-1/handover-go-test.log` |
| `env GOMAXPROCS=2 go vet ./...` from `api/` | Exit 0 | `/tmp/codex-fix-review-1/handover-go-vet.log` |
| `gofmt -l` on the six changed Go files | Exit 0, no files listed | Tool output |
| `node scripts/generate-openapi.mjs`, `node sdk/typescript/scripts/generate.mjs`, then both with `--check` | Exit 0; generated artifacts unchanged | Tool output |
| `git diff --check` | Exit 0 | Tool output |

Final continuation checks also passed: full `npm run lint` (exit 0), targeted Vitest for command navigation/Chinese commands, reviews, project activity, menu submenu/filter, workspace initials/menu, team menu and issue list (exit 0; 11 discovered files, 131 tests), and the three modified specs `desktop-menu-review-states.spec.ts`, `mobile-review-geometry.spec.ts`, `mobile-review-linked-issue.spec.ts` against the same scratch API/Vite (exit 0; 28 tests, 2.8 minutes). The latter adds the missing long-label, reviewer-open and pending/selected link-type states; its earlier screenshot-enabled pass is `/tmp/codex-fix-review-1/continuation-e2e.log` (exit 0; 28 tests). An initial Vitest invocation used nonexistent `__tests__` paths and exited 1 with no tests; corrected source-adjacent paths produced the passing result. An accidentally broad invocation was stopped and is not counted as validation.

The new association field is on the internal review API/domain JSON; the public generated contract does not expose it. No new route or schema migration is required. Persistence tests use SQLite and local fake providers; no real model request or external repository mutation was needed. MySQL/Postgres were not separately exercised.

Branch CI is recorded after push without waiting for completion. The report's final delivery includes the pushed SHA and the observed run IDs/statuses. Main is unchanged by this task.

## Limits and Cleanup

Implementation work covers all nine findings. Exact whole-screen parity, a live light/Chinese reference comparison, and the additional states explicitly omitted above are not claimed. Link types are local workspace metadata and do not rewrite external pull-request descriptions. No search-date filter, DayPanel, issue-options menu or weekday-preference edits are included.

Scratch servers are stopped by their recorded PIDs and worktree `web/node_modules` is removed before delivery. Evidence and test logs remain in `/tmp/codex-fix-review-1/` for the reviewer.
