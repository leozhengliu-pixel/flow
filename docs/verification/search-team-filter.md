# Search Team Filter Verification

The search Team filter supports exact team IDs with `is`, `isNot`, `in`, and
`notIn`, including compound filters. Parent and child teams are separate choices.
The general issue AST already had a team column on the starting main branch;
this change adds strict team validation, metadata search support, MCP filter-tree
arguments, and the missing search UI rather than introducing a second AST.

## Changes And Measurements

All sizes below are CSS pixels, measured after opening animations finish.
No fitted team names, workspace settings, or sampled menu widths enter product code.

| Item | Live reference | Flow before | Flow after |
| --- | --- | --- | --- |
| Team entry, desktop | Row 206 x 32; text 13px, weight 450, normal line height | Missing | Row 206 x 32; matching text metrics |
| Team choices, desktop | 32px rows; 13px / 450 text; icon and checkbox slots | Missing from search | 32px rows; icons, keys, checkboxes, membership groups, ancestor breadcrumbs |
| Team search, desktop | 36px input; 13px / 400 | Missing | 36px input; 13px / 400 |
| Team entry and choices, phone | 46px rows; widths 346 at 390 and 331 at 375; 15px / 400 / 22.5px | Missing; shared sheet content was 1px narrower | Matching row sizes and typography |
| Team search, phone | 40px input; widths 346 / 331; 13px / 400; padding 11px 12px | Missing; shared empty shortcut track consumed 8px | Matching sizes, type, and padding |
| Team chip | Not measured: selecting a temporary reference filter was held pending clarification of the menu-only access constraint | Missing | 32px high; editable four operators, searchable values, multi-value summary, removal, and URL reload |

Submenu width follows the actual names and keys within the viewport. The reference
sample contained different names and no visible key column, so those widths are
not asserted equal. The reference has a separate breadcrumb entry for a child team
and no subtree toggle in this menu; no implicit descendant expansion was added.
The reference root menu's existing search header and its other sections were not
rebuilt as part of adding Team.

## Browser Review

The real-browser review used bundled Chromium, a scratch SQLite API on 5280, and
Vite on 5273. The e2e used separate private ports 5373 / 5380 and its own workspace
from `e2e/workspace-fixture`. No reference data was changed or saved filters applied.

Reviewed 1440x900, 390x844, and 375x667 in light and dark, English and zh-CN:
resting, hover, focus-visible, open, selected, all four operators, single and multiple
values, long names, keys, and a two-character CJK team name in rows and chips.
The new Team controls have no disabled state or avatars. Names and keys remain
entity text in both languages; all system copy uses existing Chinese translations.

Every saved screenshot was opened. The final Flow crops show readable controls,
selected checkboxes, long names contained in their rows, focus rings, and CJK chips
without overlap. The first e2e failure screenshot showed clipped chip buttons; that
failed capture is not verification. The initial double-Escape test sequence was
corrected after the live reference confirmed that one Escape closes both menus;
the request, result-count, and reload assertions were retained.

Artifacts under `/tmp/codex-team-filter/`:

- `shots/ref-desktop-crop.png`: reference Team entry and open submenu.
- `shots/ref-phone-crop.png`: reference phone Team submenu and child breadcrumb.
- `shots/ref-phone-submenu-390.png`, `shots/ref-phone-submenu-375.png`: full phone reference submenu captures.
- `shots/flow-{1440,390,375}-{dark,light}-{en,zh-CN}.png`: 12 contact sheets containing the actual control crops for each reviewed state.
- `measurements.json`: stable Flow geometry and computed typography.
- `search-team.expect.json`, `search-team.locators.json`: independently measured reference row/input expectations and Flow locators.
- `search-team-main.expect.json`, `search-team-main.locators.json`: main search toolbar and overflow gate with a Team chip present. This does not establish reference chip parity.

The shared reference directory is read-only, so the gate reads this task's isolated
expectations with the area path `../codex-team-filter/search-team`. No existing
reference expectations or gate tolerances were changed.

Rebasing retained the new record property sheets' 13px rows and the search page's
fiscal-month prop. Generic embedded filter menus use their measured 15px phone rows;
the shared sheet rule takes typography variables from record property surfaces.
The e2e checks both the desktop and phone Team row font and height.

## Checks

All heavy commands ran through `~/.flow-local/heavy.sh`; Go commands used
`GOMAXPROCS=2` and tests used `-p 1`.

| Command / check | Exit | Result |
| --- | --- | --- |
| `npm ci` in this worktree's web directory | 0 | Installed local dependencies |
| `go test ./internal/store ./cmd/server -run 'Test(TeamFilterValidation\|TeamFilterQueries\|TeamMetadataFilterDialects\|SearchTeamPredicates\|MCPTeamFilterTree\|SearchRejectsUnknownFiltersConsistentlyAcrossResourceTypes)$' -count=1` | 0 | Predicate, compound, access, search endpoints, and MCP coverage |
| `go test ./internal/store -run 'Test(TeamFilterQueries\|TeamFilterValidation\|TeamMetadataFilterDialects)$' -count=1` | 0 | Includes real metadata search assertions |
| `go test -tags integration ./internal/store -run '^TestExternalTeamFilterQueries$' -count=1` with isolated MySQL 8.4 | 0 | Same issue and project-search cases |
| Same integration command with isolated PostgreSQL 16 | 0 | Same issue and project-search cases |
| `go vet ./...` | 0 | API vet |
| `node scripts/generate-openapi.mjs` and `--check` | 0 | REST documentation current |
| `node sdk/typescript/scripts/generate.mjs` and `--check` | 0 | SDK current |
| `npm run typecheck` | 0 | Also repeated inside full lint |
| `npm run lint` | 0 | Full lint and all repository UI checks |
| `npx vitest run --maxWorkers=2` for search-team-filter, workspace-search-page, search-date-filter, person-info, project-overview, and applied-filter-bar test files | 0 | 66 tests, six files |
| `npx playwright test -c team-filter.private.config.ts e2e/search-team-filter.spec.ts` | 0 | Desktop and phone: all operators, request AST, actual results, multi-selection, and reload |
| Real-browser state review script | 0 | 12 viewport/theme/language combinations; no page errors or horizontal overflow |
| Required phone gate, `team-menu` | 0 | Two element checks at 375 / 390 |
| Required phone gate, `team-values` | 0 | Four element checks at 375 / 390 |
| Required phone gate, `team-chip` screen | 0 | Toolbar geometry and overflow; reference chip itself remains unmeasured |

## Reviewer Steps

Open `/{workspace}/search`, enter a term matching issues in two teams, and choose
Add filter > Team. Search by name or key, select both teams, and dismiss once with
Escape. Open the chip's operator and value buttons, try each operator, reload the
URL, then remove the chip. Repeat at 390 / 375px and with zh-CN. Advanced filter
also offers Team and all four operators.

The remaining limit is reference chip parity: its dimensions and selected-menu
behavior were not measured without approval to select a temporary reference search
filter. Flow's chip behavior, states, serialization, and results were verified.

## Review Continuation

The MCP schema coverage now supplies a real Team filter and requires a nonzero
fixture count while retaining the assertion that filter trees bypass the counter
fast path. `go test ./cmd/server ./internal/store` and `go vet ./...` pass.

Phone surfaces retain border-box sizing; their command body follows the shared
viewport width, and Inbox filter/action searches and lists use the shared 6px
inset. Both the generic Team entry and Inbox content measure exactly 331px at
375 and 346px at 390. The reference dark desktop Team entry remains 206 x 32;
phone Team entries and choices remain 46px tall. The e2e now checks the entry
geometry after its opening animation, using the reference device scale.

All five requested phone gates pass, along with the Inbox actions gate (182
element checks total). Full web lint/typecheck, the targeted picker/Inbox unit
tests, and desktop/phone `search-team-filter.spec.ts` pass. The browser review
again covered 1440/375/390, both themes, English/zh-CN, resting, hover,
focus-visible, open, keyboard-active and selected states, short/long names and a
two-character CJK chip. Team controls have no disabled state or avatar.

Continuation artifacts are `/tmp/codex-team-filter/continuation-measurements.json`,
`continuation-go-tests.log`, and `shots/continuation-*.png` in the same directory.
The shared picker sheets show Inbox filters/actions, labels, assignee, status and
display options at both phone widths. The theme/language sheets show the Team
states, and `continuation-desktop-full.png` shows the entire open submenu with
short, long and CJK team names. Earlier failed e2e captures are not verification.

Review the width corrections at `/{workspace}/inbox` > Add filter and notification
actions; `/{workspace}/my-issues/assigned` > New issue > Labels, Assignee and Status;
and `/{workspace}/team/{teamKey}/all` > Display options. Review Team at
`/{workspace}/search` > Add filter > Team, then select a team and edit its chip.
