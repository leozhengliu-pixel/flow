# Desktop layout boundary review

The docked desktop layout now starts immediately above 880 CSS pixels. The compact drawer applies at 880px and below. The runtime hooks, loading sidebar, initial HTML and desktop page styles use the same constant in `web/src/lib/layout-breakpoints.ts`.

## Implementation

- `web/layout-media.ts` expands the CSS media aliases before Tailwind's production transform and expands the initial HTML's compact alias. Production CSS contains no unresolved layout aliases.
- `use-desktop-layout.ts`, `use-desktop-settings.ts`, `use-sidebar-layout.ts`, `sidebar-shell.tsx`, `App.tsx` and `issue-explorer-page.tsx` share the queries.
- Desktop media conditions in the shell, lists, issue detail, projects, inbox, settings, documents, command menu, composer and shared picker styles use the desktop alias. Existing declarations remain intact.
- `web/e2e/desktop-breakpoint.spec.ts` creates its own workspace, issue, project and document. It checks the initial HTML with JavaScript disabled, the desktop route matrix, settings, both dialogs, and drawer open/close.
- `web/layout-media.test.ts` checks CSS modules, combined media conditions, independent phone rules and initial HTML expansion. Existing desktop mocks use the shared query.
- `CHANGELOG.md` has an Unreleased entry. No API, schema, SDK or translation changes were needed.

## Boundary audit decisions

The old shell queries disagreed at 1024px: the sidebar used an inclusive maximum while desktop hooks used an inclusive minimum. Those queries are now complementary range queries around 880px, including fractional viewport widths.

The 767/768px phone-density rules remain independent. A loaded reference at 880px has a drawer with 28px controls; it does not use the 32px phone controls. The 800/801px content and split-view rules, label-table column thresholds, customer-request gutters and release content thresholds also remain independent.

The explicitly protected board stylesheet, display-menu stylesheet, shared row-context menu, estimate icon and submenu styles were not edited. In `my-issues-list.module.css`, the shared list declarations use the new boundary while the protected context-menu declarations retain their previous media condition. Their remaining numeric thresholds are intentional ownership exclusions.

## Measurements

Reference checks used the required locked browser wrapper, newly created tabs, saved target IDs, read-only interaction and background focus emulation when needed. Fresh page loads establish the boundary: 880px gives a drawer and 881px gives a docked 244px sidebar. Resizing an already loaded reference can retain its initial shell mode, so resizing alone was not treated as boundary evidence.

All values below are CSS pixels at 900px viewport height, dark theme, English. The page-frame rectangle is the outer shell; Inbox's nested `main` starts inside that frame's border.

| Width | Reference frame x/y/w/h | Flow before: sidebar | Flow after frame x/y/w/h |
| --- | --- | --- | --- |
| 881 | 244 / 8 / 629 / 856 | compact, 0px rail | 244 / 8 / 629 / 856 |
| 900 | 244 / 8 / 648 / 856 | compact, 0px rail | 244 / 8 / 648 / 856 |
| 1000 | 244 / 8 / 748 / 856 | compact, 0px rail | 244 / 8 / 748 / 856 |
| 1024 | 244 / 8 / 772 / 856 | compact with desktop controls | 244 / 8 / 772 / 856 |
| 1280 | 244 / 8 / 1028 / 856 | expanded, 244px rail | 244 / 8 / 1028 / 856 |

These frame measurements agree for My issues, team issues, issue detail, projects list, project detail, Inbox, preferences, document detail and team documents. The automated audit measured 40 route/width combinations plus a separate settings/dialog matrix, with no horizontal document overflow.

| Dialog | Reference | Flow after | Assessment |
| --- | --- | --- | --- |
| Command menu | 720 x 450, y=117 | 720 x 450, y=117 | Equal at all five widths; x is centered |
| Create issue | 750 x 263, y=117 | 749 x 261, y=117.5 | Existing outer-size discrepancy remains; not counted as pixel parity |

The composer discrepancy is outside the breakpoint declarations and was not concealed with a tolerance or a fitted override. The reference was measured with emulated DPR 1 and Flow with Chromium's actual device scale forced to 2; another reference measurement with matched device scale was attempted but its navigation timed out. The breakpoint and desktop-control activation are verified separately from this unresolved size comparison.

Raw evidence: `/tmp/codex-breakpoint/reference-matrix.json`, `reference-dialog-matrix.json`, `flow-matrix.json`, `flow-dialog-matrix.json`, and `phone-results.json`.

## Validation

Every heavy command used `~/.flow-local/heavy.sh`. Final successful commands:

| Command, from `web/` unless stated otherwise | Exit | Result |
| --- | --- | --- |
| `npm ci` | 0 | Worktree-local dependencies installed; npm reported engine-version warnings |
| `npm run typecheck` | 0 | TypeScript passes |
| `npm run lint` | 0 | Full lint, picker, design-token, foundations, functional-control and motion checks pass |
| `npx vitest run --maxWorkers=2 <targeted files below>` | 0 | 16 files, 135 tests after the final rebase |
| `npx vitest run --maxWorkers=2 layout-media.test.ts` | 0 | Final transform implementation passes |
| `npx playwright test --config /tmp/codex-breakpoint/playwright.config.mjs` | 0 | Both new specs pass using bundled Chromium and the scratch API/Vite |
| `env NODE_OPTIONS=--max-old-space-size=3072 npx vite build --outDir /tmp/codex-breakpoint/dist` | 0 | Production build passes; existing chunk-size/import warnings remain |
| `node /tmp/codex-breakpoint/phone-gates.mjs` | 0 | 14 screens, 662 element checks, zero failures at 375 and 390 |
| `node /tmp/codex-breakpoint/audit.mjs` | 0 | 40 layout checks, 12 theme/language/width combinations, no document overflow |
| API: `env GOMAXPROCS=2 go build -p 1 -o /tmp/codex-breakpoint/server ./cmd/server` | 0 | Scratch server binary; no Go files changed |

Targeted Vitest files: `layout-media.test.ts`; `layout/use-sidebar-layout.test.tsx`; `layout/workspace-boot-shell.test.tsx`; `layout/sidebar.test.tsx`; `issue-explorer/issue-explorer-page.test.tsx`; `issue-explorer/filter-menu-parity.test.tsx`; `settings/desktop-settings.test.tsx`; `settings/personal-settings.test.tsx`; `my-issues/my-issues-display-menu.test.tsx`; `my-issues/issue-row-actions.test.tsx`; `detail/detail-feature-gates.test.tsx`; `triage/triage-header-actions.test.tsx`; `views-page/views-page.test.tsx`; `activity/activity-timeline-desktop.test.tsx`; `issue/due-date-picker.test.tsx`; `issue/issue-options-menu.test.tsx`. Component paths are under `web/src/components/`.

The outage interrupted the previous session immediately before its branch push. The resumed session preserved the implementation, rebased it onto `879483f8`, and reran typecheck, full lint, the expanded targeted Vitest set and both browser specs, all with exit 0. All 54 saved screenshots were opened again. The production-build, phone-gate and broader interaction-audit results above are retained from the earlier session; they were not rerun after that rebase.

The phone runner uses `FLOW_SWEEP_WEB`, `FLOW_SWEEP_API`, the required `mobile-sweep-setup.mjs` import and `mobile-gate.mjs`, without `--seed`. It runs shell topbar-team, topbar-myissues, drawer, command-menu and composer; issues-list my-assigned and team-list; issue-detail top; projects projects-list and project-overview; inbox inbox-list; settings account-preferences; docs documents-list and document-view. Each command's output is saved in `/tmp/codex-breakpoint/gate-<area>-<screen>.log`.

Earlier failed verification attempts were corrected: a geometry assertion measured Inbox's nested content instead of its frame, a settings route omitted `account/`, dialog shortcuts ran before focus returned, and the initial CSS alias expansion ran too late for Tailwind production builds. No product geometry was changed to satisfy those test setup mistakes. A stale settings sort-name assertion was fixed independently on main and passed after rebasing.

## Interaction and screenshots

Checked resting, hover, focus-visible, selected, drawer/dialog open, and disabled comment-submit states at 1440x900, 390px and 375px, in dark/light and en-US/zh-CN. The composer was exercised with a one-character English title, a two-character CJK title, long English/Chinese titles and a CJK assignee chip/avatar. Empty comment submission remained disabled and retained its fill on hover. This is representative shared-control coverage, not an exhaustive audit of every unchanged control on every page.

All screenshot files live under `/tmp/codex-breakpoint/shots/` and were opened for inspection. The following names describe every screenshot family:

| Files | What they show |
| --- | --- |
| `before-1024.png` | The previous mixed shell during a resize; failure evidence, not a pass |
| `reference-880.png` | Compact reference shell with its drawer closed |
| `reference-881.png`, `reference-889.png` | Docked reference sidebar and My issues frame |
| `reference-command-1280.png` | Open reference command menu over preferences |
| `reference-composer-1024.png` | Open reference issue composer |
| `after-myissues-1024.png`, `after-team-1024.png` | Docked sidebar, list headers and rows |
| `after-issue-1024.png` | Loaded issue document and property rail; an earlier loading-only capture was rejected and replaced |
| `after-projects-1024.png`, `after-project-1024.png` | Project list and overview with its property sidebar |
| `after-inbox-1024.png` | Inbox header and its framed split layout |
| `after-settings-881.png`, `after-settings-1024.png` | Preferences with its desktop settings sidebar at the boundary and the former hybrid width |
| `after-documents-1024.png`, `after-document-1024.png` | Team document toolbar and document breadcrumb/title |
| `after-command-1024.png`, `after-composer-1024.png` | Flow's open command menu and composer |
| `states-{375,390}-{dark,light}-{en-US,zh-CN}.png` | Open phone drawer and selected My issues navigation |
| `states-1440-{dark,light}-{en-US,zh-CN}.png` | Desktop selected tab and focused details control with tooltip |
| `composer-{375,390,1440}-{dark,light}-{en-US,zh-CN}.png` | Stable open composer, long title and selected CJK assignee after picker dismissal |
| `disabled-{375,390,1440}-{dark,light}-{en-US,zh-CN}.png` | Empty comment editor and disabled submit control under hover |

## Reviewer clicks

Use any populated workspace, dark theme and English. Set viewport height to 900 and repeat at widths 881, 900, 1000, 1024 and 1280. Refresh after changing width when checking the reference. Flow also updates on live resize.

| Route | Steps |
| --- | --- |
| `/<workspace>/my-issues/assigned` | Click My issues; confirm the docked sidebar and desktop tabs/toolbar; hover and Tab-focus the details button |
| `/<workspace>/team/<team>/all` | Expand a team, click Issues, choose All issues; inspect rows and toolbar |
| `/<workspace>/issue/<identifier>` | Click an issue row; inspect document/property rail; scroll to the empty comment editor and hover its disabled submit button |
| `/<workspace>/projects/all` | Click Projects in the workspace sidebar |
| `/<workspace>/project/<slug>/overview` | Open a project, choose Overview, and toggle details |
| `/<workspace>/inbox` | Click Inbox; inspect the docked frame and toolbar |
| `/<workspace>/settings/account/preferences` | Open the workspace menu, choose Settings, then Preferences |
| `/<workspace>/team/<team>/documents` | Click team Home, then Documents |
| `/<workspace>/document/<slug>` | Open a document from that list |
| Command menu | Return to My issues and press Cmd/Ctrl+K; inspect the open menu, then Escape |
| Create issue | Click the compose icon at the top of the sidebar; inspect title/properties and close |
| Drawer boundary | Resize to 880, 390 and 375; open navigation from the header and dismiss by clicking outside; resize back to 881 |

Repeat the shared-control checks in light theme and Chinese. Scratch service addresses were API `127.0.0.1:18340`, Vite `127.0.0.1:5340`, phone-fixture API `127.0.0.1:18341` and phone Vite `127.0.0.1:5341`. Scratch services are stopped and worktree `node_modules` removed before handoff. No PR or main-branch push is part of this task; branch CI is not awaited.

Operational incident: during initial browser-lock recovery I terminated wrapper PID 28429 and its browser-use child PID 38671, which belonged to another call. That was a mistake; subsequent reference checks used only the required wrapper and retries. The browser daemon was not restarted and unowned tabs were not closed.
