# Flow Releases frontend implementation plan

Audited 2026-08-20 from the current Flow source. This is a code and
architecture audit, not a screenshot-derived UI specification. Pixel values for
the final replica must come from the separate live Linear DOM audit before UI
implementation starts.

## Scope and constraints

- Scope: the workspace Releases experience, release editor, release row menus,
  routes, shared frontend state, I18n, theme, Portal layering, responsive
  behavior, and testability.
- Related but separate: Releases Settings manages `ReleasePipeline` records. It
  should share domain copy and selectors, but remains a settings surface.
- Out of scope for this pass: backend changes, final Releases UI implementation,
  speculative pixel styling, and fake controls.

## Current architecture

| Concern | Current owner | Finding |
| --- | --- | --- |
| Route parsing | `web/src/lib/app-routes.ts` | Only `/{workspace}/releases`; no pipeline, tab, or release route identity |
| App composition | `web/src/App.tsx` | Releases is grouped with Drafts, Asks, and Library through `WorkspaceOperationsPage` |
| Releases UI | `web/src/components/workspace-operations/workspace-operations-page.tsx` | List, changelog, editor, Asks, archive, and audit log share one file |
| Releases CSS | `web/src/components/workspace-operations/workspace-operations.css` | Release styles share a large multi-product stylesheet |
| Release API client | `web/src/lib/api.ts` | Real create/update/delete calls exist |
| Release types | `web/src/types/flow.ts` | `Release` and `ReleasePipeline` are separate; `Release` has no pipeline reference |
| Pipeline settings | `web/src/components/settings/feature-settings.tsx` | Real pipeline CRUD editor exists, but is private to Settings |
| Issue association | `App.tsx` plus `issue-options-menu.tsx` | Issues can be added to any release using the real update API |
| Sidebar entry | `web/src/components/layout/sidebar.tsx` | Feature-gated link exists in the More menu |
| Search deep link | `App.tsx` | Search opens `?release=<id>`, which triggers the edit dialog |
| Portals | shared Radix Dialog plus local Radix Dropdown | Menus and dialogs use inconsistent class and z-index contracts |
| Localization | legacy MutationObserver plus scattered translations | Releases does not call `useI18n`; entity text can be translated accidentally |

### Current behavior worth preserving

- Real create/update/delete and bootstrap refresh.
- Four release statuses: `planned`, `inProgress`, `released`, `canceled`.
- Issue/project scope editing and completion derived from associated issues.
- Releases and Changelog tabs.
- Empty, list, hover menu, create, edit, saving, and scope-expanded states.
- Search result deep links and recent-resource recording.
- Feature-gated sidebar discovery.
- Radix focus trapping, Escape handling, and focus restoration where shared
  primitives are used correctly.

### Current behavior that must not be carried forward

1. The pipeline directory is hard-coded to one `Production` row and does not
   render `data.releasePipelines`.
2. `Release` has no `pipelineId`; the frontend cannot truthfully group releases
   into multiple pipelines. This is a contract blocker, not a UI problem.
3. `releaseSurface` and `releaseTab` are component-local. Reload, Back/Forward,
   bookmarks, and links lose the selected surface.
4. `?release=<id>` means “open the edit dialog,” not a stable release detail
   resource. Closing the dialog does not normalize the URL.
5. Delete runs directly from the row menu without confirmation, busy state, or
   an error boundary.
6. The empty-state Documentation button has no action. It must be linked to a
   real destination or omitted/disabled.
7. Header option and display-option buttons have no action. They must not appear
   until their menus are implemented.
8. Release editor CSS forces `color-scheme: dark` and uses hard-coded dark LCH
   surfaces, so Light mode is not theme-correct.
9. Release text relies on DOM mutation translation. Dates use browser locale
   (`Intl(..., undefined)`) instead of Flow's selected locale.
10. Scope renders only the first 100 issues without search or disclosure, which
    silently makes later issues impossible to select.
11. Archived releases are filtered out with no archive surface or not-found
    treatment for archived deep links.
12. Pipeline release counts currently use all releases rather than releases
    assigned to that pipeline.

## Required capability decision before implementation

The final frontend needs one explicit answer from the data/API contract:

| Capability | Required contract |
| --- | --- |
| Assign release to pipeline | `Release.pipelineId` or an equivalent authoritative relation |
| Pipeline-specific stages | Stable stage IDs, or an explicit mapping between pipeline stage and the four release statuses |
| Pipeline release count | Derived from authoritative pipeline relation, never `data.releases.length` |
| Archive directory | A query/list contract that returns archived releases or guarantees bootstrap contains them |
| Changelog/release notes | Confirm whether `description` is the release note or add a distinct field |
| Permissions | Explicit create/edit/archive/delete capability or a documented role rule |

Until these exist, the frontend may render a truthful single unassigned/default
pipeline state. It must not infer association from team IDs, production flags,
names, dates, or array order.

## Proposed route model

Final path spelling must follow the live Linear route audit. The Flow route
model should nevertheless represent these states as first-class variants:

```ts
type ReleasesRoute =
  | { kind: 'release-pipelines'; workspaceSlug: string }
  | { kind: 'release-pipeline'; workspaceSlug: string; pipelineId: string; tab: 'releases'|'changelog' }

type ReleaseOverlay =
  | { mode: 'create'; pipelineId: string }
  | { mode: 'edit'; releaseId: string }
  | undefined
```

Recommended provisional URLs, subject to Linear DOM/URL verification:

- `/{workspace}/releases`: pipeline directory.
- `/{workspace}/releases/{pipelineId}`: pipeline Releases tab.
- `/{workspace}/releases/{pipelineId}/changelog`: Changelog tab.
- `?new=1`: create overlay on the current pipeline.
- `?release={releaseId}`: compatibility edit overlay until a measured canonical
  release-detail URL is known.

Route requirements:

- Parse and build paths in `app-routes.ts`; components receive typed route state
  and never parse `location.search` themselves.
- Keep a compatibility redirect for the existing bare `/releases` and search
  links. Do not silently open a guessed pipeline when multiple pipelines exist.
- Back/Forward must close/open overlays and switch tabs without local-state
  desynchronization.
- Unknown, archived, or unauthorized pipeline/release IDs render explicit
  not-found/unavailable states, not a blank page.
- Closing a URL-driven dialog removes only its overlay query key and preserves
  the pipeline/tab URL.

## File and ownership boundaries

Create a dedicated Releases feature directory; keep Drafts, Asks, Archive, and
Audit Log in `workspace-operations` during this pass.

```text
web/src/components/releases/
  releases-page.tsx              route-to-screen composition only
  releases-page.css              measured layout and responsive rules
  release-pipelines-view.tsx     pipeline directory and its states
  release-pipeline-view.tsx      breadcrumb, tabs, grouped release list
  release-changelog-view.tsx     release-note timeline/list
  release-row.tsx                row rendering and semantic actions
  release-editor-dialog.tsx      create/edit form and dirty/save behavior
  release-scope-picker.tsx       searchable project/issue association UI
  release-actions-menu.tsx       keyboard menu; no mutation ownership
  release-delete-dialog.tsx      destructive confirmation
  release-empty-state.tsx        reusable measured empty states
  release-view-model.ts          pure grouping/progress/sort/filter helpers
  release-copy.ts                status/copy keys, not entity values
  release-test-harness.tsx       deterministic fixture surface for browser QA
```

Targeted existing-file edits for the eventual implementation:

| File | Change |
| --- | --- |
| `app-routes.ts` | Add typed pipeline/tab parsing and builders |
| `App.tsx` | Render `ReleasesPage` directly; remove Releases from `WorkspaceOperationsPage` branch |
| `workspace-operations-page.tsx` | Remove Releases components and release-only state/imports |
| `workspace-operations.css` | Move release-only CSS; leave shared operation styles intact |
| `sidebar.tsx` | Keep feature-gated entry; use the measured canonical index path |
| `command-menu.tsx` and search result handler | Build typed release/pipeline links |
| `issue-options-menu.tsx` | Reuse shared release display/status helpers; preserve mutation API |
| `feature-settings.tsx` | Reuse shared pipeline copy/selectors only; keep settings editor ownership |
| `translations.ts` | Add explicit release UI strings missing from `useI18n` calls |

Do not create a generic “operations framework.” Releases has enough route and
interaction complexity to own its components directly.

## Reuse plan

### Reuse as-is

- `Dialog`, `DialogContent`, and `DialogTitle` from `components/ui/dialog`.
- Shared `DropdownMenu*` wrappers from `components/ui/dropdown-menu`; replace
  the local raw Radix row menu unless measured behavior requires an extension.
- Lucide icons and `CalendarIcon`.
- Existing API functions and `Release`/`ReleasePipeline` types until the contract
  owner extends them.
- Global theme tokens and `useI18n` formatting helpers.
- App-level `onReload` pattern initially; optimistic updates can be a separate
  measured performance improvement.

### Extract for cross-surface reuse

- `releaseStatusOptions(t)`: internal status IDs to translated labels/icons.
- `releaseProgress(release, issues)`: pure, unit-testable calculation.
- `releasePipelineLabel` and stage copy shared by Settings and workspace pages.
- A small entity-safe label wrapper or disciplined `data-i18n-ignore` usage for
  names, versions, descriptions, teams, projects, and issue titles.

### Do not reuse

- `FeatureDialog` from Settings: its 480px settings form is a different surface.
- Native `<select>` controls if live DOM measurement shows a portaled command
  menu; keyboard and geometry must match the measured target.
- Legacy `FeaturePage` or generic `WorkspaceOperationsPage` abstractions.

## State ownership

| State | Owner | URL-backed? |
| --- | --- | --- |
| Selected pipeline | Router | Yes |
| Releases/Changelog tab | Router | Yes |
| Create/edit overlay | Router adapter | Yes when opened from a link/search; may use navigation state for ephemeral create only after URL behavior is measured |
| Row group collapsed state | `ReleasePipelineView` | No; optionally persisted later |
| Display/filter/sort menu | `ReleasePipelineView` | Query string only if Linear exposes shareable state |
| Editor draft/dirty/saving/error | `ReleaseEditorDialog` | No |
| Scope search and selected scope | `ReleaseScopePicker` plus editor draft | No |
| Delete confirmation/busy/error | `ReleaseDeleteDialog` | No |
| Server entities | `BootstrapData` through App | Existing ownership |

Avoid mirroring router state in component state. Derived lists, grouping, counts,
completion, and empty-state selection belong in pure view-model functions.

## State matrix

### Pipeline directory

- Loading/skeleton during initial bootstrap.
- No pipelines with a functional create/settings path only if authorized.
- Active pipelines.
- Archived pipelines.
- Search/filter no-results.
- Hover, keyboard focus, selected/open, and disabled rows.
- Long pipeline/team/entity names and more than three teams.
- Permission-denied and feature-disabled states.

### Pipeline Releases tab

- No releases.
- Planned, in-progress, released, and canceled groups.
- Mixed groups; zero-issue and fully completed progress.
- Collapsed/expanded groups if Linear exposes collapse behavior.
- Missing/invalid dates and long names/descriptions/versions.
- Row hover/focus/menu-open.
- Archived release excluded, separately visible, or unavailable according to
  measured Linear behavior.
- Display/filter menu closed/open/submenu/keyboard-selected/disabled.

### Changelog tab

- No released items.
- One and many release notes.
- Missing version, note, or target date.
- Long formatted content and links after the data contract is confirmed.
- Locale-correct date ordering and labels.

### Editor and destructive flows

- Create clean, create dirty, edit clean, edit dirty.
- Valid/invalid name and date; no silent coercion.
- Scope closed/open/searching/no results/selected/large result set.
- Save disabled, saving, API failure, success, and stale entity.
- Escape, overlay click, close button, and navigation while dirty.
- Delete confirmation closed/open/confirming/failure/success.
- Archived or unauthorized entity opened by deep link.

## I18n contract

- Every UI label must be produced with `useI18n().t`; do not depend on
  `LegacyUiTranslator` for Releases.
- Use `formatDate` and `formatNumber` from `useI18n`; never use locale
  `undefined` for UI output.
- Translate status labels by stable internal IDs. Never translate API values in
  place.
- Add `data-i18n-ignore` to user/workspace business content: release and
  pipeline names, version, description/release notes, team/project names, issue
  identifier/title, and user names.
- Portal owners call `useI18n` before constructing menu/dialog content. The
  MutationObserver is only a fallback and not part of acceptance.
- Test long Chinese labels in buttons, breadcrumb, columns, tabs, menu items,
  dialog footer, scope rows, and empty states at 390px.

## Theme and Portal contract

- Remove `color-scheme: dark` and all release-specific hard-coded dark surfaces.
  Use `--theme-surface-*`, `--theme-border-*`, `--theme-text-*`, and measured
  semantic status colors with Light/Dark pairs.
- Portals attach to `document.body` and inherit root `data-theme`,
  `data-theme-variant`, and `data-locale`; no page-local theme class.
- Use one portal stack contract. Current values conflict (`operations-menu:80`,
  shared menu:1000, dialog overlay:900, dialog:901). Proposed starting contract,
  to be reconciled globally before implementation:
  - page/content: normal stacking context
  - dropdown/popover: `1000`
  - dialog overlay: `1100`
  - dialog content: `1101`
  - picker opened from dialog: `1110`
  - toast: above dialog stack
- A row menu must close before a delete dialog opens; focus moves to the dialog
  title/first safe control, and cancel returns focus to the originating row.
- Escape closes only the topmost surface. Nested picker Escape must not close
  the editor.
- Measure and record portal background, border, radius, shadow, position,
  animation duration/easing, z-index, focus restoration, and reduced-motion
  behavior in both themes.

## Mobile and responsive contract

Required test viewport: `390 x 844`, plus the desktop measurement viewport used
by the Linear audit.

- Main surface remains within document width; no body horizontal scrolling.
- Pipeline table becomes a deliberate compact row layout, not a squeezed
  desktop grid. Hidden columns must remain available in row detail/menu.
- Release rows keep name, status, completion, and action access; date/note
  columns may collapse only according to measured priority.
- Header breadcrumb uses `min-width:0` and ellipsis without hiding the create or
  sidebar actions.
- Tabs and filter controls may horizontally scroll inside their own strip, not
  the document.
- Editor uses `max-width:calc(100vw - 24px)`, safe-area-aware vertical insets,
  stable footer, and a one-column scope list. The on-screen keyboard must not
  hide Save/Cancel.
- Long Chinese words/entity names wrap or ellipsize within defined tracks.
- Hover-only menus become persistently discoverable on touch/coarse pointer.
- Test portrait resize while dialog and menus are open.

## Testability plan

The web package currently has no component/unit/browser test runner. Add only
independent frontend test infrastructure during the implementation pass:

1. A deterministic `release-test-harness.tsx` route enabled only in development
   or explicit QA mode. It receives fixture `BootstrapData` and mutation stubs;
   it must never ship a production navigation entry.
2. Fixture sets: empty, all statuses, long English, long Chinese, multiple
   pipelines, archived, unauthorized, 150+ issues, mutation failure, and slow
   mutation.
3. Pure view-model tests once a repository-approved runner exists: grouping,
   progress, sort, route parsing/building, invalid IDs, and locale-independent
   status mapping.
4. Browser acceptance via Chrome/Playwright against the real app and harness.
   Use DOM computed styles and bounding boxes, not screenshot estimation.
5. Capture console errors, page errors, request counts, and mutation payloads.

Do not add a large test framework solely for this page without repository
agreement. The harness plus the existing Chrome DOM tooling is the lowest-risk
first step.

## DOM measurement worksheet

For each Linear screen and state, capture both target and Flow values:

| Element | Required measurements |
| --- | --- |
| Main surface | x/y/w/h, border, radius, background, shadow |
| Header/breadcrumb | height, padding, gap, icon boxes, font metrics |
| Tabs/toolbar | tracks, control dimensions, active/hover/focus colors |
| Pipeline table | column tracks, header/row height, padding, truncation |
| Status groups | header/row geometry, status icon SVG, collapse animation |
| Release row | every column box, typography, hover/focus/menu-open state |
| Empty states | bounds, icon SVG, copy width, button metrics |
| Changelog | grid tracks, spacing, typography, date behavior |
| Editor | overlay/content bounds, padding, radius, shadow, focus ring |
| Menus/pickers | portal bounds, collision placement, z-index, animation/easing |
| Mobile | document width, scroll containers, touch target size, overlap checks |

SVG validation must record the actual icon markup/viewBox/stroke properties, not
only the nominal Lucide component name.

## Implementation order

1. Finish the live Linear workflow/DOM/URL audit and freeze measured states.
2. Resolve the pipeline relation, stage, notes, archive, and permission contract
   gaps with the backend owner.
3. Add typed routes/builders and compatibility handling; test Back/Forward.
4. Add the fixture harness and pure release view-model helpers.
5. Extract Releases from `WorkspaceOperationsPage` without visual changes.
6. Implement the pipeline directory and pipeline tabs against real data.
7. Rebuild row menus, filters, editor, scope picker, and delete confirmation with
   complete interactions and no placeholder actions.
8. Replace legacy translation dependency and hard-coded theme values.
9. Apply measured DOM geometry/styles, then verify portals independently.
10. Run the full locale/theme/viewport/state matrix and publish the final
    Linear-versus-Flow measurement table.

## Acceptance checklist

- [ ] Every visible control has a real command, real link, or truthful disabled state.
- [ ] No hard-coded Production pipeline or global release count remains.
- [ ] Pipeline/tab/overlay URL state survives reload and Back/Forward.
- [ ] Search and issue-menu links open the intended release and normalize on close.
- [ ] Create, edit, archive/restore, delete, scope, and error states are covered.
- [ ] Destructive actions require confirmation and expose busy/failure states.
- [ ] All UI copy uses explicit I18n; all business entities remain unchanged.
- [ ] Light/Dark portals inherit root tokens; no forced dark color scheme remains.
- [ ] Portal z-index, focus trap/restoration, Escape order, and keyboard navigation pass.
- [ ] Desktop and `390 x 844` mobile have no document overflow or overlap.
- [ ] Reduced motion, focus-visible, disabled, hover, coarse-pointer, and long-text states pass.
- [ ] `npm run build`, scoped `oxlint`, console checks, and request/payload checks pass.
- [ ] Final audit contains measured Linear and Flow values for every critical element.

## Highest-risk items

1. **Data integrity:** no release-to-pipeline relation exists in the current
   frontend contract. Building multi-pipeline UI first would create misleading
   grouping and counts.
2. **Routing regression:** current search links depend on `?release=` and local
   effects. Route extraction must retain compatibility and browser history.
3. **Theme regression:** the editor is explicitly dark; Light mode cannot pass
   until hard-coded colors are removed.
4. **Localization corruption:** the legacy DOM translator can alter business
   names and portaled content. Explicit I18n is required before final QA.
5. **Large-workspace usability:** the 100-issue slice makes scope incomplete.
   Search/pagination/virtualization behavior must be real, not decorative.
6. **Shared-file collision:** `workspace-operations-page.tsx` and CSS contain
   unrelated products. Extraction should be mechanical before redesign to keep
   concurrent work reviewable.
