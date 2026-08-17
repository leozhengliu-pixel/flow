# Pulse page modules

## Route and projection

- `/cleantrack/pulse/following` renders updates for projects where the viewer is lead, member, or subscribed, plus owned or subscribed initiatives.
- `/cleantrack/pulse/popular` ranks updates by reactions and comments and hides updates without engagement.
- `/cleantrack/pulse/all` renders the complete workspace update stream in reverse chronological order.
- `/cleantrack/pulse` canonicalizes to the `following` route.
- The feed is a projection over the existing `projectUpdates` and `initiativeUpdates` bootstrap collections. It does not duplicate update records.

## Page shell

- `pulse-page.tsx` owns the 44 px page header, view tabs, empty state, populated feed, saved Pulse views, and composer lifecycle.
- `pulse.css` contains the measured dark-surface, border, spacing, type, dialog, menu, empty-state, and responsive rules.
- The header subscription button persists the selected `Daily`, `Weekly`, or `Never` summary cadence.

## Saved Pulse views

- The add-view button switches the tab strip into the inline Flow editor.
- Icon and color use the shared `ViewIconPicker`.
- Source filters support all updates, project updates, or initiative updates.
- Saved views are workspace-scoped and persisted in local storage until a server-side Pulse-view entity is introduced.

## Update composer

- `pulse-composer.tsx` shares one dialog for project and initiative updates.
- Source, health, body, attachment selection, keyboard submit, outside click, and Escape are functional.
- The change summary is derived from the selected project or initiative.
- Attachments are retained in the local draft UI only because update attachments do not yet exist in the Go domain model.

## Feed cards

- `pulse-update-card.tsx` renders source, health, author, timestamp, body, comments, reactions, and author actions.
- Project and initiative update cards use identical edit, delete, comment, reaction, copy-link, and copy-markdown interactions.
- Initiative update comments and reactions have dedicated Go endpoints and domain events so both update types persist consistently.

## Menus and dismissal

- `pulse-menus.tsx` owns subscription cadence, source filter, and inline new-view controls.
- Dialogs and menus use Radix primitives, so outside click, Escape, focus return, and keyboard menu navigation are shared with the rest of the application.
