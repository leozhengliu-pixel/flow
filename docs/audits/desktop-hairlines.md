# Desktop Physical-Scale Audit

Measured Flow at 1440 x 900 in bundled Chromium with `--force-device-scale-factor=2`, `--window-size=1440,900`, and `viewport: null`. No context `deviceScaleFactor` was supplied for those one-off measurements. Reference evidence is `/tmp/desktop-ref/<area>/`; all work used a throwaway API/database and workspace under `/tmp/codex-desktop-halfpx/`.

The automated regression spec now uses `web/playwright.config.ts` and its standard desktop context. Its original custom context was incompatible with the project's inherited device scale and failed all four CI tests before assertions. Scale-sensitive checks now assert active stylesheet declarations and layout relationships, not physical-scale coordinates. The existing project filters collect this desktop spec only in `chromium`, before sharding.

## Measurements

CSS pixels. Before values are current-source measurements, not the older audit's Flow column.

| Area | Measured / Reference | Changed | Left |
| --- | --- | --- | --- |
| Shell | Rail 244 x 900; nav rows 220 x 28; header 44 | Agent button 78.195 -> 76.922 wide, reference 76.92; toast padding 12.5 -> 12 with its real 0.5 border | Matching rail/row geometry unchanged |
| Agent | Composer 712 x 104; reference y314.75 | y406 -> 315; footer y383 versus 382.75; Skills 74 x 24; watermark y107 versus 106.75 | 0.25px offsets, content-dependent example wrapping; secondary Loop surfaces are not claimed fully closed |
| Docs | Rows 48; groups 36; four sort targets | Widths 61.60/114/114/142 -> 59.45/71.36/88.91/63.83, matching reference; group title x314.5 -> 312.5; icon becomes a functional 24 x 24 picker at x286.5 instead of a bare 16px glyph | Dates/names remain real data; document editor already declares 10/14/384 padding |
| Inbox | Header 299.5 x 44; notification row 55 high | No new compensation found on the main list | Header title is 1px left of reference; welcome content is workspace data |
| Issue Detail | Title width 653.5 versus 653.52; property controls 28 high, 32 pitch | Comment bottom padding 15.5 -> 16 | Rail x1007.5 versus 1007.85; activity positions depend on actual sub-issues/comments |
| Issues List | Row 44; group 36 | Frame 884 -> 856 high; group title x310 -> 310.5; bulk gap 7.5 -> 8 | Identifier/title positions follow identifier length, not a fitted column |
| Projects | Flat row `(244.5,128,1187,48)`; tabs 28 high | Header/toolbar insets 8; toolbar 43.5; display modes y103 -> 109; section headings y290/314.5, property grid y341; composer padding 44/12/8 | Display panel 470.5 = reference 430 + Flow's 40.5px modified-view footer; defaults retained |
| Settings | Heading y72.5; preference rows 65 | Top padding 63.5 -> 64; row padding 15.5/14.5 -> 16; table padding 51.5/19.5 -> 52/20; sort targets have declared 0.5 border, -6.5 margin and 2 gap; integration heading 12/15 -> 11/normal | Integration Enabled y266 versus 266.5; unconfigured integrations stay unconfigured |

## Fractional Spacing

A PostCSS inventory found 191 fractional gap/margin/padding declarations, including 46 in phone media rules. The inventory and media ancestry are in `/tmp/codex-desktop-halfpx/inventory.json`. This includes surfaces without supplied reference states; it is not permission to round their values.

Removed reference-backed compensation in comment cards, list/bulk gaps, project chrome/display/composer spacing, settings content/rows/tables/integration spacing, and toasts. Restored actual borders rather than replacing them with inset shadows. Display headings now use the declared 8/2 and 8/4 margins instead of fixed 27.5/21.5 line boxes.

Preserved real fractional values: project milestone gap 7.5 and left padding 2.5; delegated avatar overlap -4.5; entity-link padding 1.5/4.5/1.5/3; 0.5 hairlines; 43.5 toolbar height. Sidebar 13.5 spacing preserves its correctly measured y60.5 first row. Secondary dialogs, hover/code typography and account-only states without equivalent reference evidence were not rounded speculatively.

Confirmed P3 fixes cover Agent footer/Skills metrics, document headers/group/row text/icon targets, selected-issue toolbar gap, project display headings and settings sort/integration headings. This is not a claim that every secondary editor/run/menu observation in the older notes is resolved. The empty Loop hub remains y108.5 versus reference y144 because its empty state omits the list toolbar; a 35.5px fitted offset was deliberately not added. Reference-unobserved conversations and data-dependent activity remain unverified.

## Verification

All heavy commands used `~/.flow-local/heavy.sh`; targeted runs only.

- `npm ci`: exit 0; requested Node 24.7 emitted engine-version warnings.
- `npm run lint` (including `npm run typecheck` and all repository checks): exit 0.
- `npx vitest run --maxWorkers=2 <targeted files>`: runs of 143, 23, 32 and 40 tests, each exit 0. Files cover Agent empty state/tooltips, issue rows/bulk/assignee/activity, issue explorer/content view, project activity/surface, settings/members/integrations, documents/actions/icons.
- Regression rerun: `npx playwright test --config=playwright.config.ts e2e/desktop-hairlines.spec.ts`, both with `CI=1` and in normal local mode: 4 passed each, exit 0. Fixtures create their own workspaces; the tests do not depend on local verification data. `FLOW_E2E_DATABASE_PATH` allows the repo config's API server to use an isolated scratch database without changing its default CI path.
- Existing browser specs `desktop-issue-detail-properties`, `desktop-projects-parity`, `desktop-shell-remaining`, and `desktop-settings-parity`: all passed in targeted runs, exit 0.
- After rebasing the concurrent phone cleanup: full lint/typecheck, 111 targeted unit tests and 12 targeted browser tests passed again, all exit 0. Phone gate evidence below was collected before that independent cleanup was merged.
- `mobile-gate`: 21 distinct screens, 932 element checks, zero failures; affected Docs/Issues screens were rerun after their final changes.
- `text-gate`: 16 of 18 screens passed. Two failures reproduce byte-for-byte against untouched baseline `64db1c70`, served through a read-only Vite source overlay; no phone styling was changed to conceal them:

```text
text-gate projects / project-overview: 17 shared texts, 25 not shown in Flow there, 1 differences
  "Add customer request" y=645: size 13.3333px vs 13px, weight 400 vs 500, lightness 100 vs 37
text-gate shell / composer: 12 shared texts, 41 not shown in Flow there, 1 differences
  "Skyler Anderson" y=293: size 8px vs 12px, weight 500 vs 450
```

Commands, full outputs, baseline overlay and measurement JSON remain in `/tmp/codex-desktop-halfpx/`. No Go source, API contract, database migration or SDK changed.

## Screenshot Review

Every saved screenshot was opened. Under `/tmp/codex-desktop-halfpx/shots/`: `shell` and `agent` show the sidebar, corrected empty composer/examples and compact Agent footer; `docs` shows aligned headers/group/row and the icon target; `inbox` shows the notification list; `issue-detail` shows properties, activity/comment and composer; `issues-list` shows the corrected bottom clearance; `projects` shows the flat project row; `project-display` shows the complete display menu and retained footer; `settings` shows evenly sized preferences; `settings-teams` and `settings-members` show sort controls and real rows; `settings-integrations` shows truthful empty-enabled state and discovery; `project-overview` shows the property/milestone rail; `project-activity` shows its composer/feed; `document-view` shows the editor. All are `.png`, with no sign-in/error page or broken layout.

## Reviewer Routes

Use any real workspace `W`, team `T`, document `D`, issue `I` and project `P`.

1. `/W/agent`, with no chats: inspect composer/Skills/examples and bottom Agent button.
2. `/W/team/T/documents`: inspect sort headers and group caret; click the row icon, choose an icon, reload and verify persistence without leaving the list.
3. `/W/issue/I`: inspect a comment's padding; click Copy issue URL and inspect the toast.
4. `/W/team/T/all`: inspect bottom-bar clearance; select an issue and inspect the bulk toolbar.
5. `/W/projects/all`: Display options -> No grouping -> Manual -> Summary off; inspect modes, headings and properties. Open `/W/project/P/activity` for its composer footer.
6. `/W/settings/account/preferences`, `/W/settings/workspace`, `/W/settings/teams`, `/W/settings/members`, `/W/settings/integrations`: inspect row rhythm and sort targets; click column headings to verify ordering.
