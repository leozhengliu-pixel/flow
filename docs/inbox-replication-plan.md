# Inbox Replication Plan

## Scope

Inbox is a notification workflow, not a second issue table. Each notification
keeps its own lifecycle while projecting its source Issue into the detail pane.
The implementation uses the signed-in Flow Inbox at `1470 x 754` as the
desktop reference and preserves the existing shared Issue engine as the source
of truth for Issue content and properties.

## M12.1 Notification Aggregate and Projection

- `Notification` fields: id, recipient, source type/id, issue id, actor,
  read timestamp, snooze timestamp, favorite timestamp, archived timestamp,
  created/updated timestamps.
- Bootstrap projection joins notification data to Issue, Comment, Activity,
  and User without duplicating the Issue document.
- Seed an initial notification exactly once from eligible comments/activity.
- Persist read/unread, favorite, snooze, archive/delete mutations and emit a
  domain event for every state change.
- Expose typed Go handlers and React API/client types. Optimistic UI updates
  must roll back only the failed notification.

## M12.2 Inbox Shell and Toolbar

- Main desktop surface: `x=244, y=8`, rounded workspace panel; list width is
  `400px` without a selection and approximately `384px` once a preview opens.
- Header title, Notification actions, Add filter, Display options, and their
  exact `28px` hit areas, spacing, menu shadows, focus behavior, and shortcuts.
- Notification actions: clear all, clear read, clear read/completed, with
  confirmation only where the original requires it.
- Display menu: oldest/newest, read visibility, unread-first ordering, snoozed
  visibility. Values persist with the Inbox preference state.
- Narrow layout removes the floating-panel treatment and replaces the list with
  detail instead of drawing an overlay.

## M12.3 Filter Builder

- Filter trigger and count badge.
- Property chooser and keyboard-searchable value menu.
- Supported notification-specific filters: unread/read, assigned, mentioned,
  comment, project, team, and snoozed state.
- Filter chips support update, removal, clear-all, URL-safe serialization, and
  use the same query semantics as workspace Issue views where applicable.

## M12.4 Notification Collection

- One stable virtualizable scroller with continuous `55px` rows, no invented
  date headers or bulk-selection toolbar.
- Row anatomy: `32px` actor avatar, `16px` event badge, issue identifier/title,
  actor/event body, timestamp, and `14px` read indicator in a fixed end slot.
- Read, unread, hover, focused, active-detail, pending, error, and favorite
  states have independent visual and accessibility contracts.
- Arrow Up/Down retains scroller state and moves active notification; Enter or
  Space opens the shared detail projection and opens unread notifications as
  read.
- Near-end incremental load threshold is three row heights (`165px`), guarded
  while a page is loading.

## M12.5 Row Context and Snooze Menus

- Context menu: Mark read/unread (`U`), Delete (`Backspace`), Snooze (`H`),
  Favorite (`Option F`), and Copy sub-menu.
- Snooze presets: An hour from now, Tomorrow, Next week, A month from now,
  Custom. The application computes concrete timestamps and persists them.
- Copy exposes issue identifier and issue URL and uses the shared clipboard
  feedback convention.
- Delete/archive and snooze remove the notification from the visible list,
  close its detail if open, and offer id-scoped rollback after a failed API call.

## M12.6 Detail Projection

- Desktop preview has a `44px` header: source Issue link, favorite, Issue
  options, subscription, snooze, and delete controls.
- Body is a real Issue projection: title, description, activity/comment source,
  reply composer, and approximately `284px` Properties column.
- Detail and notification list keep independent scroll containers.
- Every mutation in the header delegates to the same notification controller as
  its row equivalent; it may not create divergent read/favorite/snooze state.

## M12.7 Delivery Gates

1. Persisted aggregate and typed client contract, including domain events.
2. Shell/toolbars and filter/display behavior at desktop and narrow widths.
3. Notification rows, keyboard movement, menu action state machine.
4. Shared Issue detail projection and all mutation paths.
5. Pixel QA against the reference viewport plus loading, empty, error, and
   API rollback flows.
