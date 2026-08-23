# Linear Releases vs Flow verification (2026-08-22)

Viewport for desktop measurements: `1470 x 749`. Mobile verification: `390 x 844`.
Linear source was the authenticated Chrome workspace `leozhengliu`; Flow used an
isolated copy of the local SQLite data on `8081` with the latest Vite client on
`5175`.

## Data and API gaps closed

| Capability | Linear behavior | Flow result |
| --- | --- | --- |
| Pipeline directory | `/release-pipelines` | Only `/release-pipelines`; no `/releases` alias |
| Stable pipeline URL | `/pipeline/:slug/releases|changelog` | Added persisted `ReleasePipeline.slugId` and matching routes |
| Release archive URL | `/pipeline/:slug/releases/archived` | Exact canonical route; no `/archive` alias |
| Stable release URL | `/pipeline/:pipeline/release/:release/issues|release-notes` | Added persisted `Release.slugId` and matching routes |
| Scheduled completion | Move open issues to next release toggle | Added `moveOpenIssuesToNextRelease` to model/API/settings |
| Release resources | Add document / Add link | Added validated `ReleaseResource[]` PATCH persistence and UI |
| Continuous pipeline | No manual release creation | UI omits create actions; API returns `409` for manual creation |
| Pipeline deletion | Available even when releases exist | Transaction moves pipeline and all releases to recently deleted |

Legacy local data is normalized on load: missing slugs and resource arrays are
filled without dropping existing entities.

No legacy query-string or database-ID URL parsing remains. Pipeline archival was
removed from the domain contract; pipelines use delete/recently-deleted/restore.

## DOM measurement comparison

| Element | Linear measured | Flow measured / result |
| --- | --- | --- |
| Directory title | `13px/500`, `x=262.5 y=22.5` | `13px/500`, same top-bar origin |
| Directory options | `28 x 28`, pill radius | `28 x 28`, pill radius |
| Directory create | `113.29 x 28`, padding `0 10 0 8` | `28px` high; desktop text and compact responsive state |
| Directory row | `1217 x 44`, radius `8` | `44px`, radius `8` |
| Directory display portal | `301 x 197`, radius `12` | `301 x 197`, radius `12` |
| Compact display select | `122 x 24`, radius `8` | `122 x 24`, radius `8` |
| Display property pill | `24px`, pill radius | `24px`, pill radius |
| Pipeline heading | `13px/500`, `y=22.5` | `13px/500`, `y=22.5` |
| Header icon actions | `28 x 28`, pill radius | `28 x 28`, pill radius |
| Pipeline tabs | `28px`, `12px/500`, padding `0 10` | `28px`, `12px/500`, padding `0 10` |
| Release row | `1217 x 48`, radius `8` | `48px`, radius `8` |
| Pipeline options portal | `247 x 229`, z `600` | `247px` wide, z `600`; conditional continuous actions |
| Composer | `500 x 298.5`, radius `22`, z `300` | `500 x 298.5`, radius `22`, z `300` |
| Composer motion | `300ms cubic-bezier(.43,.07,.59,.94)` outer padding; `150ms` controls | matching cubic-bezier and `150ms` controls |
| Release tabs | `28px`, Issues `57.94px` in English | `28px`, content-width pill |
| Details positioning wrapper | `400px`, `x=1061.5 y=96` | `400px` positioning scope |
| Details card | `388px`, padding `12`, radius `10`, border `.5` | `388px`, padding `12`, radius `10`, border `.5` |
| Details card shadow | `0 3px 6px -2px /.02`, `0 1px 1px /.04` | matching shadow |
| Release menu portal | command surface, z `600` | `247px` command surface, z `600` |
| Settings editor column | `640px` centered | `640px` centered |
| Stage row | `48px` | `48px` |
| Team selector portal | compact searchable list, radius `12` | searchable list, radius `12`, keyboard navigation |

## Interaction acceptance

- Directory options: search autofocus, filtering, Escape, Go to settings.
- Directory and pipeline display menus: grouping, ordering, direction, property
  toggles, nested select Escape behavior.
- Scheduled pipeline: composer, searchable stage picker, date picker, disabled
  create state, archive route, restore/delete row menu.
- Continuous pipeline: no create button/menu item; integration empty state.
- Release: stable route, Issues/Release notes tabs, back/forward and refresh,
  filter focus, display property toggle, details open/close, stage submenu,
  edit, add issues, add link, add document, resource removal, favorite, copy URL,
  delete confirmation.
- Details aggregates: Assignees, Labels, Priority, Projects contain real scoped
  issue aggregates and support arrow-key tab changes.
- Settings: active/recently-deleted states, new/edit routes, scheduled vs
  continuous sections, completion toggle, release-note template, CI links,
  access-key generation, path-filter disabled/enabled states, duplicate, delete.

## Portal/theme/I18n

| Portal | Light | Dark | z-index |
| --- | --- | --- | --- |
| Command menus | white surface / LCH neutral text | `lch(12.72 .85 272)` / `lch(91.178 1.425 272)` | `600` |
| Select submenus | themed surface/border | themed surface/border | `650` |
| Release composer | themed surface + overlay | themed surface + overlay | `300` |
| Calendar | themed surface | themed surface | `701` |
| Pipeline delete dialog | themed surface + overlay | themed surface + overlay | `700` |

English and Simplified Chinese were switched through the visible workspace
language menu. Light and Dark were switched through Preferences. Pipeline,
release, team, issue, document, and resource names remain literal and carry
`data-i18n-ignore` where rendered inside translated surfaces.

## Responsive acceptance

At `390 x 844`, directory controls, pipeline rows, composer, release tabs, the
floating details card, nested menus, and settings editor remained inside the
viewport. Long business names truncate rather than overlap; details becomes a
bounded overlay and remains scrollable when aggregate tabs add height.

## Issue to release association

Linear reference: authenticated issue `LEO-6`. Flow acceptance issue: `CLE-25`
on the isolated audit database. The relationship remains release-owned through
`Release.issueIds`; Flow adds the atomic issue-centric mutation
`PUT /api/issues/:id/releases` with `{ releaseIds: string[] }`.

| Element / behavior | Linear measured | Flow measured / result |
| --- | --- | --- |
| Feature gate | Property appears after Releases is enabled | Property omitted when disabled; issue association API also returns `403` |
| Add button | `24 x 24`, padding `0 2`, radius `100%` | `24 x 24`, padding `0 2`, radius `100%` |
| Selected pill | `28px`, padding `0 10px 0 6px`, pill radius | `28px`, padding `0 10px 0 6px`, pill radius |
| Release link | Independent `Open release` control | Independent `Open release` control using canonical slug route |
| Picker surface | `228 x 201.5`, content-sized, radius `12px`, z `600` | `228 x 179` with two rows, content-sized, radius `12px`, z `600` |
| Light shadow | `0 6px 18px /.02`, `0 3px 9px /.04`, `0 1px 1px /.04` | Exact match |
| Search row | Search icon, autofocus, `Add to release…`, `⌥ R` | Same structure, autofocus and shortcut hint |
| Selection | Checkbox multi-select | Checkbox multi-select; optimistic atomic save with rollback |
| Pipeline navigation | `All pipelines…` then pipeline releases | Same three-view hierarchy and searchable lists |
| Keyboard | Arrow navigation; Escape steps back then closes | Arrow navigation; stacked Escape verified in Chrome |
| Frozen stage | New association disabled | Disabled option plus API conflict enforcement |
| Activity | `added to release TEST3 in TEST` | Add/remove activity persisted; localized action, literal release name |

Portal checks were repeated in Chinese and English, Light and Dark. The issue
picker used `lch(100 0 282)` in Light and `lch(12.72 .85 272)` in Dark, with
themed border/text and z-index `600`. Pipeline and release names remained
literal in both locales. At `390 x 844`, the `300px` responsive picker stayed
inside the viewport and the document had no horizontal overflow.

The add, remove, add-back, refresh persistence, canonical release link,
`All pipelines…` navigation, pipeline release view, search autofocus, checkbox
semantics, and two-level Escape behavior were exercised in the user's Chrome.
