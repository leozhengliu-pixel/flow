# Behaviour confirmation and archived issue editing

Date: 2026-11-03. Task branch: `review/codex-behaviour`. This report distinguishes confirmed behaviour from unavailable reference states. It does not claim archive visual parity.

## Results by item

| Item | Reference evidence | Flow before / after | Decision |
| --- | --- | --- | --- |
| Sidebar order | The live personal sidebar shows Pulse, Inbox, My issues, Reviews, Agent. The read-only customization dialog lists Inbox, Reviews, My issues, Pulse, Agent, Drafts; Drafts is shown when badged. Its Workspace list starts Initiatives, Projects, Views. The public personalization changelog documents customization but gives no authoritative default sequence. | Flow's default remains Inbox, Reviews, My issues, Pulse, Drafts, Agent; Workspace remains Initiatives, Projects, Loops, Views. | Default order remains unconfirmed. The rendered account order is insufficient evidence to change the product default. No sidebar settings, order or visibility changed in the reference. |
| Sidebar destinations | Live Initiatives href is `/flow-app12/initiatives/active`, contradicting the brief's bare `/initiatives` premise. Team Home href is `/flow-app12/team/FLO/documents`. The team-pages documentation describes Overview and Documents; it does not require Documents as the default. | Initiatives already uses `/initiatives/active`. Flow's Home link already reopens the viewer's remembered team Home tab, with Overview as the fallback. No changes. | No confirmed destination defect. |
| Archived title, description and properties | Both visible reference teams have **0 archived issues**. Public archive/team-page documentation requires restoration before changing an archived issue. | Brief's original content/property PATCH result: **200**. Now both `/api/issues/{id}` and `/api/issue-records/{id}` reject changes with **400**. A mixed restore-plus-edit also returns **400**. A separate restore returns **200**, then edits return **200**. The UI changes `contenteditable=true` to `false`, disables native property buttons and provides Restore in issue options. Opening, hovering and keyboard inspection send **0** issue PATCHes; Restore sends **1**. | Implemented the documented requirement. Archived-description sockets join read-only and follow archive/restore immediately. |
| Archived banner and discussion | No archived issue could be opened in either reference team. The available documentation does not specify the banner geometry/copy or the exact comment/reaction/attachment/relation permissions. | No banner was invented. Comment, reaction, attachment and relation behaviour was not changed. The new guard covers `IssueUpdateInput` content/property writes, their batch path and collaborative descriptions; it is not a universal prohibition on every issue-related endpoint. | These details remain unconfirmed and need a populated archive or more precise public evidence. |
| Create-issue failure | The task permits read-only reference interaction. No reference failure could be reproduced without attempting a mutation, and no authoritative error-presentation example was found. | The existing dialog catches the error inline while App's mutation wrapper emits the failure toast. The diagnostic result is recorded below. Product behaviour is unchanged. | Unconfirmed; no speculative removal of either error channel. |
| Offline mutation | The existing Offline pill is already on main. A reference mutation was not attempted under the read-only restriction. Available public evidence did not establish its mutation queue/revert/toast contract. | Property mutations use an optimistic update and revert on failure with an error toast. The per-issue queue serializes writes; it is not an offline persistence/replay queue. Title autosave separately retains a failed edit for Retry. Product network behaviour is unchanged. | Unconfirmed; do not generalize the property diagnostic to every mutation. |

The public sources inspected were the reference site's `/docs/delete-archive-issues`, `/docs/default-team-pages` and `/changelog/2024-12-18-personalized-sidebar`. The web-search service returned HTTP 503; public pages were read directly. All live reference interaction used the supplied `bu.sh`, an owned tab id saved immediately after creation, URL assertions before input and `finally` cleanup. No reference data or settings were changed.

## Implementation

- `api/cmd/server/issue_archive.go`, `main.go`: enforce archive restoration in the shared issue-update path; accept existing no-op updates and a separate restore with its version precondition.
- `api/cmd/server/realtime_collaboration.go`, `collaboration_access.go`: archived descriptions are read-only, and an archive field change forces an access recheck even when the issue stays in the same team. This prevents restoration from waiting for the periodic socket sweep.
- `api/cmd/server/issue_archive_test.go`: legacy and paged PATCHes, mixed restore/edit refusal, atomic batch rollback, restoration and live socket access transitions.
- `web/src/components/detail/detail-pane.tsx`, `issue-properties-rail.css`: read-only editors, disabled property fieldsets, disabled assignment shortcut and no description selection mutation actions while archived. Fieldsets use `display:contents`, retaining the existing layout. Disabled buttons and their phone trigger wrappers ignore pointer events, preventing a trapped hover tooltip or a wrapper click from opening a picker.
- `web/src/components/issue/issue-title-editor.tsx`: a reusable read-only prop. Changing editability emits no editor update, so it cannot schedule a phantom autosave.
- `web/src/components/issue/issue-options-menu.tsx`, `.css`: Restore and disabled property actions use the existing shared command-row implementation; disabled rows are dimmed and cannot open property submenus.
- `detail-feature-gates.test.tsx`, `issue-options-menu.test.tsx`, `web/e2e/archived-issue.spec.ts`: editor/field gating, separate restoration, disabled hover, real keyboard selection and restoration without a reload.
- `web/src/i18n/translations.ts`, `CHANGELOG.md`: Chinese refusal text and Unreleased entry. Restore already has a Chinese translation.

No REST schema, routes, migrations, SDK, production hosts or production databases changed. No real model requests were needed, and the lead-managed model proxy was untouched.

## Browser coverage and image ledger

Bundled Chromium used an auth-disabled scratch API at `127.0.0.1:18463`, Vite at `127.0.0.1:5463`, and `/tmp/codex-behaviour/flow.db`. Each permanent e2e case creates its workspace through `workspace-fixture`; it does not depend on `/verify` or retained local data. The temporary config imported the repository config, used unique ports and `reuseExistingServer:false`, and selected bundled Chromium. Port 4173 was checked before the run.

The 12 cases cover **1440x900, 390x844 and 375x844**, light/dark and English/zh-CN. They use short mixed-script text and a realistic long English/CJK title/project name, plus two CJK avatar initials supplied as fixture responses. Checked states: archived rest, disabled hover, focus-visible on the options trigger, open menu, pointer-selected Restore, keyboard-selected Restore, restored editors/properties and an open property picker after restoration. Restore is a command, so a persistent checked state is not applicable. Disabled restore-while-saving is not separately claimed.

All screenshot paths below are under `/tmp/codex-behaviour/shots/`.

| Images | What was opened and what it shows |
| --- | --- |
| `reference-customize-open.png` | Actual read-only customization dialog, whose list order differs from the rendered sidebar. |
| `reference-archive-current.png`, `reference-picker-archive.png` | The two team archive pages, each showing count 0 and no matching issues. |
| `reference-sidebar-menu.png`, `reference-customization.png`, `reference-customization-current.png` | Context-menu captures only. They do not show the customization dialog and are not evidence of its defaults. |
| `reference-sidebar.png` | Earlier loading shell. Rejected as settled-sidebar evidence. |
| `reference-archive.png` | Wrong plural archive URL showing Not found. Rejected as archive evidence; the actual route is singular `/archive`. |
| `reference-archive-resume.png` | Resume-session capture has the archive shell and live sidebar, but no settled issue count or empty-state message. Rejected as archive-count evidence. Its DOM confirms Initiatives `/initiatives/active` and team Home `/documents`. |
| `{1440,390,375}-{dark,light}-{en-US,zh-CN}-archived.png` | The archived issue page with readable short/long content and fixed CJK initials. Editor attributes and native disabled states are checked by the browser test, not inferred from pixels. |
| The corresponding `-open.png`, `-hover.png`, `-keyboard.png` | Restore at rest/open, pointer highlight, and actual keyboard highlight. Property actions are disabled and dimmed. The keyboard screenshot contains the deliberately typed English search query, including in Chinese cases. |
| The corresponding `-inspection.png` | Four source images placed next to each other at native resolution with filenames. All 48 final source captures were inspected through these 12 sheets. |
| The corresponding `-focus.png` | Native crop of the issue options trigger after keyboard focus-visible verification. |
| `diagnostic-create-failure.png` | Flow's deliberately injected HTTP 500: the dialog retains its draft and inline error, with the failure toast at the bottom. This is a failure diagnostic, not successful creation or reference parity. |
| `diagnostic-offline-mutation.png` | Flow's deliberately offline priority mutation: Offline pill, reverted priority and error toast. This is failure feedback, not successful mutation or reference parity. |

An early light-desktop open-menu capture included a lingering property tooltip. A follow-up exposed that hovering a native disabled trigger could trap its tooltip; the shared disabled-fieldset pointer rule fixes that behaviour. Those captures were rejected and replaced. Initial automatic failure screenshots were also opened: the setup failures were blank pages, the keyboard-sequence failure showed the second fuzzy-search result, and the restoration failure showed an editor that had not regained edit access. They are failures, not successful verification; the underlying defects were fixed and the final matrix rerun.

Phone gate: the unchanged `issue-detail/top` measurements are run at **390x844 and 375x667**, through the supplied setup import without `--seed`. A process-local locator hook points only the routes to a fresh scratch fixture; it never changes expectations or shared files. Result: **58 element checks, 0 failures**. The retained expectations no longer contain the `actions` screen: its invocation returns **0 element checks**, which is explicitly not an actions parity pass. The archive command's exact reference pixels remain unavailable.

## Reviewer steps

1. In a scratch workspace, archive an issue through `PATCH /api/issues/{id}` with `{"archived":true}`, or use an existing archived issue. Open `/<workspace>/issue/<identifier>` directly.
2. Try the title, description, status, priority, assignee, project and label controls. The editors are read-only and the property controls cannot open. Repeat at desktop and phone widths, both themes and languages.
3. Open the issue's ellipsis menu. Inspect Restore at rest, on hover and using the filter plus ArrowDown/Home. Disabled Team/Due date/Convert/Subscribe actions cannot change properties.
4. Choose Restore. Title, description and property controls become editable without a reload. Open Priority to see the normal picker again.
5. To verify the server boundary, try title, description or priority PATCHes while archived, and a mixed `{"archived":false,"title":"changed"}` payload. They return 400 without changing the issue. Restore separately, then repeat the edit successfully.

## Validation

All heavy commands were run through `~/.flow-local/heavy.sh`; Go commands used `GOMAXPROCS=2` and `-p 1` where applicable. Node used the required v24.7.0 PATH. `npm ci` exited 0 with engine-version warnings; no dependency or lockfile changes were made.

| Command | Exit / result |
| --- | --- |
| `npm ci` in `web/` | 0; local dependencies, no symlink. |
| `go test -p 1 ./cmd/server -run '^TestArchivedIssueContentRequiresSeparateRestoration$' -count=1` | 0. |
| Targeted Go run for archive content, batch deduplication, record create/update and collaboration join/broadcast | 0. |
| Targeted Go run for archive content, `TestOpenIssueDescriptionFollowsArchiveAndRestore`, immediate permission changes and reparenting | 0. |
| `go vet ./...` | 0 after the socket fix. |
| `go build -p 1 -o /tmp/codex-behaviour/server ./cmd/server` | 0; tracked `api/server` untouched. |
| Targeted vitest run of detail feature gates, issue options, customer commands, inbox unread/paging, triage actions, deploy previews and entity description/comments | 0; 9 files, 66 tests. |
| Full `npm run lint` | 0 before the final capture cleanup; final result recorded below. |
| `npm run typecheck` | Final result recorded below. |
| Bundled Chromium archive matrix | 0; 12 cases passed. Final capture rerun recorded below. |
| Phone gate `issue-detail --screen top` | 0; 58 checks, 0 failures. |
| Phone gate `issue-detail --screen actions` | 0 but zero checks; coverage unavailable, not a parity pass. |

Earlier runs exited 1 for a new test selector/setup mistake, the keyboard sequence and the real socket restoration defect; the first lint attempt exited 2 for an invalid testing-library option. These were corrected, with no geometry assertions or expected values relaxed. The cold-start editor wait was increased to wait for the actual editor rather than the static loading surface.

Final diagnostics, commit ids, final checks and cleanup are recorded in the completion entry below.

## Completion after outage

The continuation inspected the original brief, prior log tail, uncommitted patch and upstream delta. There were no task-local commits at the handover. The existing implementation and reference findings were retained. The branch was rebased on `origin/main`; the only conflict was the Unreleased changelog insertion, resolved by keeping both entries.

The temporary offline diagnostic initially timed out with a blank page because it disabled networking before the issue's lazy editor/picker loading had settled. Waiting for the description editor, opening/closing the picker once and waiting for network idle made the diagnostic pass. It confirms the priority is reverted with `Could not update issue` after a failed offline write. Vite also logged an unhandled `Failed to fetch` rejection from the existing mutation queue. That existing rejection is a remaining limitation; this patch does not alter network-error handling.

An archive matrix rerun without capture delays exposed animation timing in the new geometry test: the before box was sampled during the opening transform. The test now awaits the menu's animations before taking its baseline. The exact before/after equality assertion is unchanged. No product dimensions or reference expectations were adjusted. An attempted pending-Restore browser capture failed because the optimistic update immediately removes Restore; that test-only interception was removed. The failure image showed the restored issue, not a disabled Restore, and is not claimed as disabled-state verification. Disabled property actions are covered; Restore while saving is not separately claimed.

Final command results, screenshots and cleanup are recorded before publication. The task is pushed only to `refs/heads/review/codex-behaviour`; no PR is opened and branch CI is not awaited.

Implementation commit: `6f046c85` (`fix(issues): require restoration before editing archived content`). All commands below ran through `~/.flow-local/heavy.sh` in this worktree, after rebasing. The temporary Chromium config used bundled Chromium with isolated ports and `reuseExistingServer:false`.

| Final command | Exit / result |
| --- | --- |
| `env GOMAXPROCS=2 go test -p 1 ./cmd/server -run 'TestArchivedIssueContentRequiresSeparateRestoration\|TestOpenIssueDescriptionFollowsArchiveAndRestore\|TestCollaborationIssueAccess\|TestIssueRecordsCreateUpdateAndContext\|TestBatchUpdatesDeduplicateIDsAndSkipIdenticalRetries' -count=1` | 0. |
| `env GOMAXPROCS=2 go vet ./...` | 0. |
| `npm run typecheck` | 0; also included in final full lint. |
| `npm run lint` | 0; full typecheck, oxlint, pickers, design tokens, UI foundations, functional controls and motion checks. |
| `npx vitest run --maxWorkers=2 src/components/detail/detail-feature-gates.test.tsx src/components/issue/issue-options-menu.test.tsx src/components/customer/customer-request-commands.test.tsx src/components/inbox/inbox-unread-only.test.tsx src/components/inbox/paged-inbox.test.tsx src/components/triage/triage-header-actions.test.tsx src/components/detail/entity-description-selection.test.tsx` | 0; 7 files, 60 tests. These consumers were found with `git grep` for the changed components. |
| `env FLOW_ARCHIVE_SHOTS=/tmp/codex-behaviour/shots npx playwright test --config playwright.behaviour.tmp.ts e2e/archived-issue.spec.ts e2e/behaviour-gate.tmp.spec.ts --workers=1` | 0; 14 cases, including 12 archive combinations, phone gate and failure diagnostics. |
| Supplied phone gate `issue-detail --screen top` | 0; 58 element checks, 0 failures at 390x844 and 375x667. |
| Supplied phone gate `issue-detail --screen actions` | 0; 0 checks, not coverage. |
| `git diff --check` | 0. |

Final capture review opened all twelve `-inspection.png` sheets, which contain the 48 page/menu source captures, plus all twelve `-focus.png` crops and both diagnostic captures. The desktop sheets show intact title wrapping, property text truncation and dimmed menu actions. Phone sheets show wrapped long titles, contained property chips and menus inside both 375px and 390px viewports. CJK avatar initials render as complete characters. Focus crops show the options trigger's accent outline. None of these claims establish unavailable reference archive pixels. The phone 375 layout deliberately compacts empty priority/label chips; it does not indicate an archive-specific regression.

Cleanup removes the temporary gate hook, temporary diagnostic spec/config, collage helper and `web/node_modules`. Playwright's owned scratch API/Vite processes have exited; the scratch ports have no listeners. No other agent's server, container or dependency directory was stopped or removed. Scratch screenshots and gate logs remain under `/tmp/codex-behaviour/` for review.
