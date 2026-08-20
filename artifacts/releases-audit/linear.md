# Linear Releases live DOM audit

Date: 2026-08-20
Workspace: `leozhengliu`
Reference surface: authenticated Linear in the user's Chrome
Desktop viewport: `1470 x 693`, DPR `2`
Mutation policy: read-only. No pipeline, release, issue, note, key, archive, or
delete operation was submitted.

This is an interaction and DOM audit, not a screenshot interpretation. Settings
Releases is included only because it configures the pipeline consumed by the
workspace Releases product.

## Executive behavior map

Linear models Releases as three related surfaces:

1. A workspace pipeline directory at `/leozhengliu/release-pipelines`.
2. A pipeline workspace at `/leozhengliu/pipeline/1/releases`, with a sibling
   Changelog tab and release detail routes.
3. A settings pipeline directory/editor at `/leozhengliu/settings/releases`.

The business surface is not a CRUD settings table. The normal path is pipeline
directory -> grouped releases -> release Issues/Release notes. Settings owns
pipeline construction, ordered stages, completion behavior, release-note
automation, and CI credentials.

Linear exposes deletion and a 30-day recently-deleted flow for pipelines. No
explicit `Archive pipeline` action exists in either observed pipeline menu.
Release archive is exposed as a separate workspace view, but the active release
menu did not expose an `Archive` item and the workspace had no archived release
from which to measure restore behavior.

## Routes verified

| Surface | Route |
| --- | --- |
| Workspace pipeline directory | `/leozhengliu/release-pipelines` |
| Pipeline Releases | `/leozhengliu/pipeline/1/releases` |
| Pipeline Changelog | `/leozhengliu/pipeline/1/changelog` |
| Release Issues | `/leozhengliu/pipeline/1/release/%E7%89%88%E6%9C%AC1-befd326a9e2c/issues` |
| Release notes editor | same release path ending `/release-notes` |
| Archived releases | `/leozhengliu/pipeline/1/releases/archived` |
| Settings pipeline directory | `/leozhengliu/settings/releases` |
| New pipeline | `/leozhengliu/settings/releases/pipelines/new` |
| Existing pipeline settings | `/leozhengliu/settings/releases/pipelines/1` |

## Global measured tokens

| Token/element | Light | Dark |
| --- | --- | --- |
| App sidebar width | `244px` | `244px` |
| Main frame | `x=244, y=8, 1218 x 649` | same |
| Main radius/border | `12px`, `0.5px` | `12px`, `0.5px solid lch(13.08 1.48 272)` |
| Main surface | `lch(97.94 0.5 282)` | `lch(5.52 0.4 272)` |
| Main text | near `lch(9.794 0 282)` | `lch(100 0 272)` |
| Main shadow | `0 3px 6px -2px / .02`, `0 1px 1px / .04` | `lch(0 0 0 / .3) 0 .5px 1px 1px` |
| Menu surface | `lch(100 0 282)` | `lch(12.72 .85 272)` |
| Menu border | `0.5px solid lch(91.9 0 282)` | `0.5px solid lch(25.68 1.93 272)` |
| Menu radius | `12px` | `12px` |
| Menu shadow | `0 6px 18px / .02`, `0 3px 9px / .04`, `0 1px 1px / .04` | `0 3px 8px / .125`, `0 2px 5px / .125`, `0 1px 1px / .125` |
| Menu z-index | `500` for selectors, `600` for action menus | same |
| Standard UI font | Inter Variable, normally `12-13px` | same |
| Standard state transition | `150ms`; row computed `color 0.15s` | same |

The attached Chrome remained in Light mode after the audit. Dark was selected
through Preferences, measured on the workspace list and a portaled action menu,
then restored to Light.

## Workspace pipeline directory

Route: `/leozhengliu/release-pipelines`

### Structure and measurements

- Header: `Releases`, `Releases options`, `New pipeline`.
- Count line: `1 release pipeline`.
- Columns: `Release pipeline`, `Active releases`, `Teams`, `Latest release`.
- Measured row: pipeline `1`, active release `版本1`, team `LEO`.
- Header title: `13px/500`, `x=262.5`, `y=22.5`.
- New pipeline button: `113.29 x 28`, padding `0 10px 0 8px`, pill radius.
- Display options trigger: `28 x 28`.
- Table header begins at `y=100`.
- Pipeline row: `x=244.5`, `y=128`, `1217 x 44`, radius `8px`.
- Resting row background is transparent; the row's computed transition is
  `color 0.15s`.

### Menus

`Releases options` contains only `Go to settings`.

- Portal: `175 x 45`, `x=326`, `y=48`, z-index `600`.
- Radius `12px`, border `0.5px`; option height `32px`.
- Dark-mode portal: background `lch(12.72 .85 272)`, text
  `lch(91.178 1.425 272)`, border `lch(25.68 1.93 272)`.

Directory `Display options`:

- Portal: `301 x 197`, `x=1152.5`, `y=92.5`, fixed parent z-index `500`.
- Grouping values: `No grouping`, `Team`.
- Ordering values: `Release pipeline`, `Type`, `Latest release`.
- Separate direction icon button.
- Display properties: `Active releases`, `Teams`, `Latest release`.
- Compact selector: `122 x 24`, radius `8px`.
- Property chips: `24px` high with pill radius.
- Grouping submenu: `122 x 56.5`, z-index `500`, radius `12px`; two
  `121 x 24` options.
- `ArrowDown` moved active highlight to `Team`; `Escape` closed the submenu and
  parent panel without applying a selection.

## Pipeline Releases list

Route: `/leozhengliu/pipeline/1/releases`

### Header and list

- Header: pipeline name `1`, favorite switch, pipeline options, create release.
- Tabs: Releases `72.63 x 28`; Changelog `83.24 x 28`; pill radius with
  `0 10px`, `12px/500` text.
- Columns: `Release`, `Release notes`, `Release date`, `Completion`.
- Group: `In Progress`, with collapse button.
- Release row: `版本1`, completion `0%`, `1217 x 48`, inner padding `10px 0`,
  radius `8px`.

The Releases-tab `Display options` exposes:

- Grouping: current `Stage`.
- Ordering: current `Release date` plus a direction button.
- Display properties: `Description`, `Version`, `Release date`, `Completion`,
  `Release notes`.

### Pipeline options menu

- Create new release, shortcut sequence `N` then `R`.
- Favorite, `Option+F`.
- Copy URL, `Cmd+Shift+,`.
- Pipeline settings.
- Open archive.
- View recently deleted releases.
- Surface: `247 x 229`, `x=312`, `y=48`, z-index `600`.
- Items: `246 x 32`, padding `0 18px 0 14px`; separators are `12px` high.

### Create release composer

The create experience is a composer-style modal, not the standard settings
dialog.

- Fields: release name (autofocus), version, description, stage, target date.
- Actions: Cancel and Create release.
- Create remains disabled until the required data is valid.
- Surface: `500 x 298.49`, `x=485`, `y=90`, radius `22px`, border `0.5px`.
- Shadow: `0 9px 48px / .08`, `0 6px 24px / .1`, `0 1px 1px / .04`.
- Fixed overlay z-index `300`.
- Surface motion uses `cubic-bezier(.43,.07,.59,.94)`.

Stage picker:

- Searchable portal with Planned, In Progress, Released, Canceled.
- `175 x 177.5`, radius `12px`, z-index `600`.
- Search row `36px`; options `32px`.

Target date picker:

- Quick menu: Custom, Tomorrow, In 1 week, In 2 weeks, In 1 month.
- Custom opens a stacked modal at z-index `701`.
- Calendar surface: `562 x 486.5`, `x=454`, `y=68.84`, radius `12px`.
- Two month grids were visible: August and September 2026; each grid `246px`.
- Previous/Next month, Cancel, and Save target date were present.
- The modal was closed without a selection or release creation.

## Release detail

Verified route entity: `版本1`.

### Issues tab

- Tabs: Issues `57.94 x 28`; Release notes `101.31 x 28`.
- Groups: Todo `2`, In Review `2`.
- Rows: `LEO-14` through `LEO-17`.
- Metadata shown in rows includes assignee, priority, project, status, created
  date.
- Empty groups available through hidden columns: Backlog, In Progress, Done,
  Canceled, Duplicate.
- Toolbar icon buttons: Add filter, Display options, Close details; each
  `28 x 28` with accessible names.

Details aside:

- `x=1061.5`, `y=96`, `400 x 560.5`.
- Entity name `版本1`, favorite control, overflow menu.
- Stage `In Progress`; target date empty; add document/link actions.
- Aside tabs: Assignees, Labels, Priority, Projects.
- Selected Assignees tab `98.45 x 28`; other tabs roughly `76-86 x 28`.

Release menu:

- Edit; Stage submenu; Add issues to release (`Option+R`); Add document; Add
  link (`Ctrl+L`); Favorite (`Option+F`); Copy URL (`Cmd+Shift+,`); Delete.
- Stage submenu: Planned, In Progress checked, Released, Canceled.
- Stackable menu/submenu focus behavior was verified.
- No explicit Archive release action appeared.

Edit release reuses the `500 x 298.49`, 22px-radius composer. Name autofocuses;
version, description, and target date were empty; stage was In Progress. Save
was enabled (`49.09 x 28`). Closing through the discard path made no mutation.

### Release notes tab

- Editable entity title `版本1` and stage `In Progress` remain visible.
- `Write with Agent` is available but carries an accessibility hint that issues
  must be added.
- Rich Release notes editor occupies the content pane.
- Details aside remains open while editing notes.

## Changelog and archive states

Pipeline Changelog showed a missing-release-notes empty state:

- Illustration `144.26 x 110`.
- Heading `Missing release notes`.
- Business entity `版本1` remains literal.
- Supporting copy links to pipeline settings.
- Create action `59.2 x 28`, primary purple pill.

No notes were generated.

Archived releases route showed:

- Breadcrumb `1 > Archived releases`, count `0`.
- Heading `No archived releases`, `15px/600`, width `340`, centered around
  `x=683`.
- Copy: `Archived releases will appear here. You can restore them at any time.`
  at `13px/18.2`, width `340`.

Because the workspace had no archived row, its row menu and restore confirmation
could not be measured without first mutating production data.

## Settings Releases directory

Route: `/leozhengliu/settings/releases`

Visible structure:

- Title and copy: `Releases`, `Track which issues ship in each release.`
- Docs link, pipeline-name search, status selector, New pipeline.
- Columns: Pipeline name, Teams, Type, Releases, Latest release.
- Row: pipeline `1`, Production, team `LEO`, Scheduled, one release.

Status selector options are **Active** and **Recently deleted pipelines**. There
is no Archived value.

- Selector portal: `209.52 x 72.5`, `x=604.5`, `y=139.5`, z-index `500`.
- Light background `lch(100 0 282)`, border
  `0.5px solid lch(86.5 0 282)`, radius `12px`.
- Options: `208.52 x 32`, padding `0 35px 0 12px`.

Pipeline row menu:

- `View releases`, `Duplicate...`, separator, `Delete`.
- Surface: `175 x 121`, `x=1258.5`, `y=271`, z-index `600`.
- Options: `174 x 32`, padding `0 18px 0 14px`, Inter Variable
  `13px/19.5px`.
- There is no Archive action.

Delete is a real guarded workflow, not an immediate row mutation:

- Confirmation title: `Delete the pipeline "1"?`.
- Copy states that the pipeline remains in Recently deleted pipelines for 30
  days, then it and all releases are permanently deleted.
- User must type the exact pipeline name `1`.
- Delete starts disabled and became enabled only after exact-name input.
- Surface: `480 x 301`, `x=613`, `y=130.66`, radius `12px`, border `0.5px`.
- Shadow: `0 9px 48px / .08`, `0 6px 24px / .1`, `0 1px 1px / .04`.
- Content padding `32px`; input `415 x 32`, padding `6px 12px`, radius `8px`.
- Focus outline: `1px lch(53 52.26 286.91)`.
- Cancel: `68.15 x 32`; Delete: `64.86 x 32`; both pill radius.
- The test typed the name, observed enabled state, then clicked Cancel. Delete
  was never submitted.

## New and existing pipeline settings

New pipeline is a full settings page, not a dialog.

### New pipeline

- Sections: General (Name, Teams, Production, Type), Stages, footer actions.
- Type cards: Scheduled and Continuous.
- Default stages: Planned, Started/In Progress, Released, Canceled.
- Main content starts at `x=549`.
- Name input `300 x 32`, padding `6px 12px`, radius `8px`, border `0.5px`.
- Focus outline `1px lch(53 52.26 286.91)`.
- Type cards each `300 x 129.59`, padding `12px`, gap `16px`, radius `8px`.
- Cancel/Create buttons are `32px` high pills.
- The create button appeared enabled with an empty name; it was not clicked.

Teams picker contained `Leozhengliu`:

- Menu `149 x 56.5`; option `141 x 32`, padding `2px 12px 2px 6px`.
- `ArrowDown` applied active highlight; `Escape` closed without selecting.

Stage inline editor:

- Add-stage button becomes disabled while editing.
- Name receives autofocus.
- Input `408.46 x 34`, padding `6px 12px`, radius `8px`.
- Cancel `68.15 x 32`; Create `66.39 x 32`.
- Create is disabled for empty input and enabled after locally entering
  `QA stage`.
- The editor was canceled, so no stage was added.

### Existing pipeline

The existing editor autosaves; it has no Save button.

- General: Name, Teams, Production.
- Stages.
- Completion: `Move open issues to the next release`.
- Release notes: auto-generate toggle and rich template editor.
- CI setup: GitHub Action and Linear Release CLI.
- Access key generation.
- Path filters disabled until an access key exists.
- Overflow: `Duplicate...`, separator, `Delete`.
- Existing built-in `In Progress` stage menu is disabled.
- Generate access key was initially loading-disabled, then enabled; no key was
  generated.

## Keyboard and state contract

Observed keyboard behavior that Flow must preserve:

- `ArrowDown` changes active option in selectors without immediately mutating
  the underlying value.
- `Escape` closes the topmost submenu/panel/modal and restores the previous
  interaction layer.
- Name inputs autofocus in create/edit and inline-stage editors.
- Disabled actions remain present and semantically disabled until validation.
- Shortcut hints are visible in action menus and must map to the same action as
  clicking the row/menu item.
- Nested stage/date menus layer above composer modals (`600`/`701` versus
  composer overlay `300`).

Exact focus-visible styling was measured for form inputs. A reliable
focus-visible style for the workspace pipeline row could not be extracted: the
role locator repeatedly exceeded Linear's selector dispatch deadline, while
direct DOM inspection confirmed its resting geometry and transition. This is a
known measurement gap and must not be invented in Flow.

## I18n findings

- Linear's visible interface remained English throughout the audit.
- `document.documentElement.lang` was `zh-CN`, so the HTML language attribute is
  not evidence that this Linear account exposes a Chinese UI.
- Account Preferences contained no language/locale selector. It exposed theme,
  font size, sidebar, pointer, link, date-week, and comment-submit preferences.
- A Chinese Linear UI was therefore not reachable in this authenticated
  account without changing browser/account conditions outside the page.
- Business entity values such as pipeline `1`, release `版本1`, team `LEO`, and
  issue keys/titles remained literal. Flow must never pass these values through
  UI-copy translation.

Flow acceptance should still test its own supported `zh-CN` and English
locales. Only chrome copy, labels, empty-state copy, status display names, date
formatting, and accessibility labels should translate. Entity names, versions,
descriptions, notes, team names, project names, issue keys, and issue titles must
not.

## Mobile limitation

This audit used the user's real authenticated Chrome at `1470 x 693`. The
attached Chrome control surface did not expose device emulation or a viewport
resize contract for the existing tab, and resizing the user's OS window was not
performed. Consequently, mobile geometry and responsive navigation are **not
measured** in this artifact. They must be audited in a separately controlled
mobile-sized Chrome session before mobile pixel parity can be claimed.

This is an explicit acceptance gap, not permission to infer mobile behavior
from desktop DOM or screenshots.

## Linear vs Flow capability comparison

This table combines the live Linear audit with the Flow model/API audit in
`artifacts/releases-audit/model-api.md` and the frontend audit in
`artifacts/releases-audit/flow-frontend-plan.md`.

| Behavior | Linear observed | Flow status / required disposition |
| --- | --- | --- |
| Pipeline directory | Real multi-column directory and display controls | Backend can list pipelines; current frontend hard-codes one row and must be replaced |
| Pipeline routes | Stable pipeline, tab, archive, and release-detail routes | Typed routes are still required in `app-routes.ts` |
| Pipeline types/stages | Scheduled/Continuous, ordered stages | Model/API supports type, stages, ordering, and validation |
| Release association | Every release belongs to a pipeline | `pipelineId` and stage validation were added to the model/API |
| Create/edit release | Composer with stage/date subportals | API supports lifecycle; measured composer UI remains to implement |
| Release issues/progress | Grouped issue rows and completion | Existing issue association exists; frontend grouping/progress must be extracted and tested |
| Release notes | Rich editor plus optional generation | Notes/template/config are expressible; generation worker is not implemented, so no functional generate control may ship |
| Changelog | Dedicated tab, missing-notes state | Changelog assembly/publishing is deferred; do not show a fake tab/action |
| Pipeline access key | Generate key plus gated path filters | Secure rotation endpoint exists; UI must reveal plaintext once and never expose hash |
| CI ingestion | GitHub Action / CLI setup shown | Authenticated CI ingestion is deferred; setup must not claim successful automation yet |
| Delete pipeline | Typed-name confirmation, 30-day recently deleted view | Delete/restore APIs exist; UI must implement guarded confirmation and trash view |
| Archive pipeline | Not observed | Do not add an invented Archive pipeline action |
| Archive release | Separate empty view; no active-row action observed | Archive filter is expressible; action and restore UI require another live entity audit |
| Theme | Light/Dark have distinct LCH surfaces and Portal shadows | Current release editor hard-codes dark colors; replace with shared theme tokens |
| I18n | English UI only in this account; entity names literal | Use `useI18n`; remove mutation-based translation and mark entity values translation-safe |
| Mobile | Not measurable in attached session | Separate real mobile-size audit is required before parity sign-off |

## Implementation blockers and non-negotiable acceptance rules

1. Do not implement release-note generation, CI ingestion, completion automation,
   or Changelog publishing as live controls until their deferred runtime
   capabilities exist.
2. Do not invent Archive actions absent from the measured menus.
3. Route identity must survive reload, Back/Forward, bookmarks, and deep links.
4. Every menu/submenu/modal must use the measured layering order and be tested in
   both Light and Dark. Portaled content must receive the same theme and locale
   context as its trigger.
5. UI copy may translate; entity values must remain byte-for-byte literal.
6. Delete must require exact-name confirmation and must route the pipeline to a
   30-day recently-deleted state.
7. Desktop parity may use the measurements above. Mobile parity cannot be signed
   off until a real mobile-sized run supplies equivalent DOM measurements.
8. No pixel-perfect completion claim is valid while the explicitly documented
   focus-visible, archived-row restore, and mobile gaps remain unmeasured.

## Audit gaps left intentionally unmutated

- Archived release row menu and restore confirmation.
- Recently deleted pipeline row and permanent-delete/restore menus.
- Successful release/pipeline creation, autosave, delete, note generation, and
  access-key generation.
- Mobile layout and responsive navigation.
- Chinese Linear UI, because no locale control was reachable in the account.
- Exact focus-visible token for the workspace pipeline row.

These states require prepared disposable fixtures or a dedicated test workspace,
not mutation of the user's current workspace.
