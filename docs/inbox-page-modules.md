# Flow Inbox page module map

Reference: signed-in `https://flow.app/cleantrack/inbox`, captured on 2026-08-13. The inspection used visible DOM, computed styles, screenshots, and production SVG paths. It did not inspect cookies, Local Storage, authentication headers, or credentials.

## Module order

| # | Module | Smallest independently verifiable surfaces | Status |
|---|---|---|---|
| 1 | Surface and header | page panel, list/detail split, 44px header, title, overflow/filter/display buttons | Implemented |
| 2 | Header menus | notification actions, filter search/list, display ordering/toggles, focus/escape | Implemented |
| 3 | Notification row | avatar/event glyph, issue identifier/title, activity excerpt, timestamp, progress/read indicator | Implemented |
| 4 | Row states/actions | unread/read, hover, keyboard-active, selected, snooze, mark read/unread, delete | Implemented |
| 5 | Row context menu | contextual actions, shortcuts, nested snooze picker, copy/favorite | Implemented |
| 6 | Detail preview | issue source/header controls, activity/comment body, properties slot, narrow replacement/back | Implemented |
| 7 | Bulk mode | selection affordance, selected count, read/snooze/delete controls, escape | Not present in captured Flow version; intentionally not implemented |
| 8 | Data state | controlled notification rows, optimistic persistence, rollback/retry, incremental pagination | Implemented UI/controller contract |
| 9 | Empty/loading/error | no inbox items, no selected notification, skeleton rows, retry and optimistic rollback | Implemented presentational states; rollback belongs to integration |
| 10 | Responsive | desktop split, narrow list, detail navigation, off-canvas sidebar | Implemented for shell/header/rows |

## Observed desktop geometry

Viewport: `1470 x 754`.

| Surface | Rect or value |
|---|---|
| Main panel | `x=244, y=8, width=1218, height=710` |
| Main panel decoration | `0.5px` border, `12px` radius, `0 0.5px 1px 1px rgb(0 0 0 / 30%)` |
| Notification/list pane | `400px` wide with no selection (`x=244..644`); `384px` with detail open (`x=244..628`) |
| Header | `44px` high, lower `0.5px` divider |
| Heading | `x=262.5, y=22.5, 34.22 x 15.5`, `13px/500` |
| Notification actions | `x=304.72, y=16.25, 28 x 28` |
| Add filter | `x=574, y=16.25, 28 x 28` |
| Display options | `x=608, y=16.25, 28 x 28` |
| Notification row | `399.5 x 55`, `8px` row radius |

Current Flow does **not** render persistent `Unread / All` tabs in this Inbox. Read visibility and ordering live under Display options. The implementation should not add tabs unless another verified workspace/version exposes them.

## Observed header menu contracts

### Notification actions

- Popover: `277/278 x 109`, positioned at `x=305, y=48` for the desktop reference.
- Visual: LCH `12.72 0.85 272` background, `0.5px` LCH `25.68 1.93 272` border, `12px` radius, three-layer shadow.
- Virtual list top/bottom padding: `6px`; each option is exactly `32px`.
- Options in source order:
  - `Delete all`
  - `Delete all read`, shortcut `Shift Backspace` / `⇧⌫`
  - `Delete all read for completed issues`
- Trigger is `aria-haspopup=menu`, toggles `aria-expanded`, and is `28 x 28`.

### Filter menu

- Popover: `206/207 x 209.5`, desktop `x=574, y=48`.
- Search field: `Add Filter…`, autofocus, visible `F` key hint.
- Options are `32px` rows with a trailing submenu disclosure:
  - Notification type
  - From
  - Project
  - Issue priority
  - Issue status type
- Text filtering occurs in the first surface; selecting an option must hand off to the corresponding value picker in Module 8.

### Display options

- Popover: `300/301 x 161.5`, desktop observed at `x=335, y=48` because it avoids the list/detail boundary.
- Controls:
  - Ordering combobox, default `Newest`, alternate `Oldest`
  - Show snoozed, default off
  - Show read, captured default on
  - Show unread first, captured default off
- Toggle rows are `32px`; menu stays open while toggles are changed.

## Narrow behavior

Viewport: `768 x 754`.

| Surface | Rect or behavior |
|---|---|
| Main | `x=0.43, y=0, width=767.57, height=718`; no border/radius |
| Header | `43.5px` high; list grows to full viewport width |
| Heading | `x=50.43, y=14`; sidebar trigger precedes it |
| Row | `767.57 x 55` |
| Detail preview | replaced by list; selecting a row navigates to the detail surface |

## Interaction matrix

| Control | Pointer | Keyboard/focus | State change |
|---|---|---|---|
| Notification actions | click opens anchored menu | Enter/Space opens; arrows move; Escape closes and returns focus | destructive callbacks only after option selection |
| Add filter | click opens searchable picker | search autofocus; type filters; arrows/Enter select; Escape returns focus | emits filter kind; value picker is next module |
| Display options | click opens controls | Enter/Space opens; checkbox rows toggle; Escape returns focus | controlled `InboxDisplayOptions`; menu remains open for toggles |
| Narrow sidebar | click | Enter/Space | emits `onOpenSidebar`; absent on desktop |

## Notification row evidence

The current workspace row is a single focusable `a`-equivalent surface with a contextual menu wrapper. At desktop reference width, the list pane becomes `384px` (`383.5px` row accounting for the border) after the detail panel opens; each row remains exactly `55px` high. `InboxPageShell` exposes this measured transition via `data-detail-open`, derived from whether the controlled `detail` prop was supplied.

| Element | Measured geometry/style |
|---|---|
| Row content inset | wrapper `367.5 x 55`, horizontal padding `8px` |
| Row inner layout | `351.5 x 55`, gap `12px` |
| Actor column | `32 x 44`, top padding `12px` |
| Avatar | `32 x 32`, circular |
| Event/type glyph | `16 x 16`, bottom-right overlay on avatar with dark circular backing |
| Text column | top/bottom padding `10px`, gap `2px` |
| Headline | `17px` high; identifier `13px/450`; title `13px/500` |
| Summary | `16px` high, gap `6px`; body/time `12px/450` |
| Read indicator | `14 x 14` SVG in a stable `16px` end slot |
| Hover/active fill | approximately `rgba(255,255,255,0.075)` / dark `#252527` |
| Read typography | headline and body use muted LCH `61.803 1.2 272`, mostly `0.7` opacity |
| Unread/active typography | headline LCH `91.269 1.425 272`; body LCH `65.078 1.425 272`, opacity `1` |

### Row action model

- There are **not** separate always-visible or hover-only snooze and ellipsis buttons in this captured Flow version.
- The only line-end control is the `14px` read state circle/check, semantically reproduced as `Mark as read` / `Mark as unread`.
- Clicking the row sets `data-active=true` and populates the detail preview. It does not set `data-selected=true`.
- `ArrowDown` transferred focus and active state from the first row to the second in the live page.
- The independent row contract emits focus direction instead of owning the parent collection, so virtualization/list selection can remain centralized.

### Context menu

Right-clicking a read notification produced a `191/192 x 185` menu, `12px` radius, with `32px` options:

- `Mark as unread`, shortcut `U` (becomes `Mark as read` for unread data)
- `Delete notification`, shortcut `Backspace` / `⌫`
- `Snooze`, shortcut `H`, submenu
- separator
- `Favorite`, shortcut `Option F` / `⌥F`
- `Copy`, submenu

The Snooze submenu contains a natural-language input placeholder and these exact presets:

- An hour from now
- Tomorrow
- Next week
- A month from now
- Custom…

The component emits preset identifiers and does not invent timestamps; the integration/domain layer must calculate and persist the locale-aware date.

## List state contract

`InboxListBoundary` accepts `loading`, `error`, `empty`, and `retry`, selecting exactly one presentational state. `InboxListLoading`, `InboxListEmpty`, and `InboxListError` are also individually exported. Because the live workspace was populated, loading/error artwork and copy cannot honestly be claimed as captured pixel-exact; their dimensions and tonal system are scoped to the measured list surface, and this limitation is explicit.

## Detail preview evidence

Desktop, selected CLE-26 at `1470 x 754`:

| Surface | Geometry/behavior |
|---|---|
| Detail pane | `x=628.5, y=8.5, width=833, height=709` |
| Header | `833 x 44`, padding `0 8px`, gap `6px` |
| Issue source | starts `x=642.5`, `20px` high including `2px 4px` hit-area padding; `13px/500` |
| Favorite | `28 x 28`, icon `14 x 14` |
| Header actions | Issue options, subscription bell, Snooze notification, Delete notification; every control `28 x 28` |
| Body | begins `y=52.5`, height `665`; primary content plus measured `~284px` properties column and `~17px` gap |
| Primary inset | content starts near `x=653.6` (`25px` from pane edge) |
| Properties inset | starts near `x=1161`, top padding `21.75px` |

The preview is an Issue detail projection rather than a notification-only card: its body includes issue title/description, Activity, the targeted comment, reply composer, and Properties. This branch exports structural slots (`children`, `properties`) instead of duplicating the Issue engine that the parent task is already implementing.

### Header interaction contract

- Issue source button emits `onOpenIssue`; the app router decides whether that becomes full-screen Issue navigation.
- Favorite is a pressed toggle and emits the new state.
- Subscription control is rendered only when `onSubscribeChange` exists; its controlled value is `subscribed`.
- Snooze is an anchored menu with the same five verified presets as the row context menu.
- Delete is a direct callback. Persistence/undo belongs to the parent data layer.
- Read/unread appears in Issue options and emits through the same `onReadChange` callback as the row. This keeps list and preview synchronized from one notification object.

### Narrow detail behavior

At `768 x 754`, selected detail replaces the list in the same main surface; it is not a floating drawer. The measured header begins with `Inbox ›`, then the source link at approximately `x=82`, while actions remain aligned right (`Issue options x=614`, subscription `x=664`, snooze `x=698`, delete `x=732`). `InboxPageShell` now hides the list pane and exposes its controlled detail slot when `data-detail-open=true`; `Back to Inbox` emits `onBack`.

`InboxDetailLoading` and `InboxDetailError` are exported for fetch transitions. Like the list error/loading states, their exact artwork cannot be claimed as captured because the signed-in reference resolved synchronously.

## Cross-module list behavior

### No bulk or date grouping in the captured version

The populated desktop DOM contained 14 notification row nodes and no selection checkbox, `Select…` control, bulk toolbar, date heading, or sticky date boundary. Every row exposed `data-selected=false`. Although generic list metadata includes `data-first-in-group`, the rendered children are continuous `55px` rows with no visible group heading. Therefore this branch intentionally adds neither bulk controls nor synthetic `Today / Earlier` grouping.

### Scroll and navigation evidence

- With detail selected, the notification scroller is `x=244.5, y=52.5, width=383.5, clientHeight=665, scrollHeight=1090`.
- The detail content has a separate `833 x 665` scroller. Selecting a notification does not merge these scroll contexts.
- `InboxNotificationList` owns one stable overflow element. Changing `selectedId` only changes row props, so React does not replace the element and native `scrollTop` is retained.
- `ArrowUp / ArrowDown` resolves the adjacent notification by id and focuses its row. Enter/Space delegates to `onOpen`, which makes the parent-controlled `selectedId` the single source for both row active state and detail presence.
- Incremental load requests at `165px` from the end, exactly three row heights, and is guarded while `loadingMore` is true.

### Persistence and rollback contract

`useInboxController` accepts controlled `notifications`, controlled `selectedId`, and an `InboxPersistenceAdapter`. It performs optimistic mutations while preserving a snapshot:

| Mutation | Optimistic effect | Failure rollback |
|---|---|---|
| Open unread | row/detail share `read=true` | restore prior read state and selection |
| Mark read/unread | update the notification object in place | restore full list snapshot |
| Favorite | update the notification object in place | restore full list snapshot |
| Delete | remove row and close matching detail | restore row ordering and selected detail |
| Snooze | remove row and close matching detail | restore row ordering and selected detail |

The hook exposes per-id `pending`, structured `error`, `retry`, and `dismissError`. Rollback is id-scoped: field mutations restore only the failed notification field, while delete/snooze reinsert only the removed item at its prior index. Concurrent mutation success on another notification is therefore preserved. The persistence adapter remains domain-neutral; the Go/API layer decides endpoint names, actual snooze timestamps, idempotency, and server event shape. A retry reapplies the same id-scoped optimistic mutation and persistence operation.

## Visual fixture matrix

These are the same-viewport target fixtures recorded from live Flow; no generated screenshot binary is checked in until the parent integrates the route.

| Fixture | Viewport | Expected geometry |
|---|---:|---|
| Desktop/no selection | `1470 x 754` | main `(244,8,1218x710)`, list `400px`, centered empty preview |
| Desktop/selected | `1470 x 754` | list `384px`, detail begins `x=628.5`, header `44px`, independent scrollers |
| Desktop/row states | `1470 x 754` | rows `55px`; read muted, unread bright, active fill; line-end indicator `14px` |
| Desktop/context menu | `1470 x 754` | row menu `192x185`, snooze submenu, no bulk toolbar |
| Narrow/list | `768 x 754` | main full width/no radius, rows full width `55px`, detail hidden |
| Narrow/selected | `768 x 754` | list hidden, detail replaces main, `Inbox ›` back/source header |

For a repeatable visual test, the parent route should render deterministic notification fixtures at these viewports and compare screenshots after fonts settle. This module does not alter App routing, so it cannot truthfully generate an integrated local screenshot itself.

## Page composition and adapter

`InboxPage` is the integration boundary for the standalone branch. It composes `InboxPageShell`, `InboxNotificationList`, `useInboxController`, `InboxDetailPreview`, and all list/detail states.

Integration update (`2026-08-13`): `InboxAppPage` is mounted in the shared navigation, projects real comments/activity into notification rows, opens the shared Issue engine, persists subscriber changes through Issue updates, and connects its narrow header control to the shared off-canvas Sidebar. Read, favorite, snooze, and delete remain session scoped until a first-class Inbox notification entity exists in Go.

Required parent contracts:

- controlled `notifications` and `selectedId`
- `InboxPageAdapter` for row mutations plus the three verified header cleanup actions
- controlled Display options and filter-start callback
- `renderDetail(notification)`, returning shared Issue content and optional Properties content
- Issue navigation callback and optional copy/subscription callbacks
- load/retry and incremental pagination signals

The page uses the same notification object in both list and detail. It does not cache a second preview model. A selected id absent from the current list yields the no-selection preview, which prevents a stale detail after deletion/filtering.

## Application integration contract

`InboxAppPage` is the concrete adapter from the current `BootstrapData` payload to the standalone Inbox composition. It lives entirely under the Inbox module and requires no invented API shape:

```tsx
<InboxAppPage
  data={data}
  onOpenIssue={openIssue}
  onSubscriberChange={optionalSubscriberMutation}
  onCopyIssueLink={optionalCopyLink}
/>
```

The adapter projects one stable notification per real `Comment` and relevant `ActivityEvent`, keyed as `comment:<id>` or `activity:<id>`. Comment-created activities are de-duplicated when their referenced comment is present. The selected notification keeps a private `issueId/sourceId` mapping, so the detail surface renders the real Issue title, description, status, priority, assignee, due date, labels, project, actor, comment, and activity copy. The Issue source button returns the real `Issue` through `onOpenIssue`.

### Current persistence boundary

The inspected Go/bootstrap domain has no `Notification` entity and no `readAt`, `snoozedUntil`, `favorite`, or Inbox deletion endpoint. Consequently:

- Issue, Comment, Activity, subscriber, and property data comes from the real persisted bootstrap model.
- Read/unread, favorite, snooze, and notification deletion are explicitly session scoped in `InboxAppPage`.
- The session adapter still exercises the same optimistic pending, id-scoped rollback, retry, display, and selection paths as a future server adapter.
- No request is sent to a nonexistent endpoint and no UI implies cross-session persistence.

A persistent Inbox requires a Go `Notification` aggregate plus list/mutation endpoints or an equivalent event-backed projection. That backend work remains a separate domain milestone rather than an undocumented client contract.

### Root mount changes

The owner of shared navigation needs only these edits:

- add `'inbox'` to `PageId`
- wire the existing Inbox sidebar row to `onNavigate('inbox')`
- import `InboxAppPage` from `@/components/inbox`
- render `<InboxAppPage data={data} onOpenIssue={openIssue} />` for the Inbox page

`onSubscriberChange` and `onCopyIssueLink` are optional; omitting them hides the subscription button and copy-URL item rather than presenting controls without a valid behavior.

## Browser QA results

Chrome fixture verification after the application adapter and Radix trigger fixes:

| Check | Result |
|---|---|
| Desktop shell | `1470px` browser width: main inset `8px`, list `400px`, header `44px`, rows `55px` |
| Selected detail | list changes to `384px`; detail receives the remaining width; row and detail share active/read state |
| Display menu | opens through pointer/keyboard; measured `300 x 158.5`; check rows remain interactive |
| Filter menu | opens through pointer/keyboard; measured `206 x 213.5`; search receives focus |
| Issue options | opens through the detail icon button; measured `190 x 109` with read/copy actions |
| Row context menu | opens on right click; measured `191 x 178.5`; read/delete/snooze/favorite/copy branches present |
| Accessibility snapshot | named main/list/detail regions, link rows, icon-button labels, menu semantics, no alert on initial render |

The Chrome window available to this task reported a `1470 x 698` content viewport despite the `1470 x 754` outer target, so vertical whole-window coordinates were not claimed from that fixture run. Component-local widths and fixed heights matched the live Flow evidence. Narrow CSS behavior remains covered by the measured `768 x 754` live reference and the module's `max-width: 800px` replacement rules; final same-viewport narrow screenshot belongs to the integrated root route.

### Pending and failure states

- Mutating rows receive both `aria-busy=true` and a fixed `14px` spinner in the existing read-indicator slot.
- The pending row is non-interactive until persistence resolves, preventing conflicting writes.
- A mutation failure restores the previous list/selection and shows a `role=alert` notification with Retry and Dismiss.
- Page cleanup actions snapshot the list and selected id before their optimistic change and restore both on rejection.
- Load failures remain inside the list or detail state rather than covering the whole application shell.

### Demo fixture

`InboxPageStory` is a deterministic, API-free story component in `inbox-page.story.tsx`. It includes six mixed read/unread notification fixtures, comment/assignment glyphs, selected-detail content, Activity, and Properties. The in-memory adapter resolves after `180ms` so pending UI can be captured. The parent can temporarily mount:

```tsx
<InboxPageStory />
<InboxPageStory initialSelectedId="inbox-2" />
```

at a story route for the six fixture screenshots above. It intentionally contains no router, global CSS import, shared API, or side effects outside its local state.

## Accessibility audit

- Page main, list pane, preview pane, list/listitem, row link, activity, and alert regions have explicit semantics.
- Row keyboard behavior: arrows navigate, Enter/Space opens, `U` toggles read, `H` snoozes, Delete/Backspace deletes.
- Radix DropdownMenu/ContextMenu manage roving focus, arrow navigation, submenus, Escape, and focus restoration.
- All icon controls have stable accessible labels; pressed favorite/subscription controls expose `aria-pressed`.
- Pending rows expose `aria-busy`; disabled rows are removed from tab order.
- Menus retain `32px` fixed rows and query input autofocus. Checkboxes keep their menu open for repeated changes.
- Narrow detail exposes an explicit Back to Inbox control, rather than relying only on browser history.

## Implemented component contract

`web/src/components/inbox/inbox-page-shell.tsx` is deliberately API/domain agnostic:

- `children`: notification rows supplied by the integrating page.
- `detail`: selected-notification preview; omitted value renders exact no-selection copy.
- `displayOptions` and `onDisplayOptionsChange`: controlled ordering/visibility settings.
- `onAddFilter(kind)`: starts the verified filter branch.
- three destructive callbacks: action menu does not invent persistence or confirmation policy.
- optional `onOpenSidebar`: enables the measured narrow header button.

All styling is locally prefixed `flow-inbox*` in `components/inbox/inbox.css`; no global token or shared layout file is modified.

## Acceptance checklist for Modules 1–2

- [x] Desktop two-pane surface and exact 400px list width.
- [x] 44px header, 13px/500 title, 28px round controls.
- [x] Production SVG paths used for overflow, filter, and display controls.
- [x] All three action entries and exact filter branch names.
- [x] Search input autofocus and query filtering.
- [x] Display ordering and all three controlled checkboxes.
- [x] Escape/focus behavior delegated to Radix menu primitives.
- [x] Narrow shell removes border/radius, hides preview, and exposes sidebar trigger.
- [x] Same-viewport desktop/narrow browser comparison after route integration.
- [ ] Notification value pickers and persistence.
- [x] Row geometry, read/unread/active/hover styling and keyboard contract.
- [x] Read toggle plus exact right-click menu labels/shortcuts/Snooze presets.
- [x] Actor avatar fallback and comment/assignment/mention/generic glyph contract.
- [x] Loading/empty/error presentational boundary.
- [x] Detail preview header, source navigation, actions, activity wrapper and properties slot.
- [x] Shared callback contract for row/detail read, favorite, snooze and delete synchronization.
- [x] Narrow list-to-detail replacement and back callback.
- [x] Verified absence of bulk UI/date headings; no invented controls.
- [x] Stable scroll container, incremental loading threshold and adjacent-row focus.
- [x] Controlled row/detail selection contract.
- [x] Optimistic read/favorite/delete/snooze with snapshot rollback and retry.
- [x] Desktop/narrow visual fixture matrix recorded for route integration.
- [x] Standalone `InboxPage` composition and `InboxPageAdapter`.
- [x] Deterministic story fixture for selected/unselected screenshot capture.
- [x] Pending spinner, retry/dismiss alert, page-action rollback and stale-selection guard.
- [x] Dropdown/context/a11y/responsive composition audit.
- [ ] Go/API persistence for first-class Inbox notification state.
