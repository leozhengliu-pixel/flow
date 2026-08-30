# Pulse page modules

## Route and projection

- `/acme/pulse/following` renders updates for projects where the viewer is lead, member, or subscribed, plus owned or subscribed initiatives.
- `/acme/pulse/popular` prioritizes recent updates with reactions and comments while retaining a useful recent fallback when engagement is sparse.
- `/acme/pulse/all` renders the complete workspace update stream in reverse chronological order.
- `/acme/pulse` canonicalizes to the `following` route.
- The feed is a projection over the existing `projectUpdates` and `initiativeUpdates` bootstrap collections. It does not duplicate update records.

## Page shell

- `pulse-page.tsx` owns the 44 px page header, view tabs, empty state, populated feed, saved Pulse views, and composer lifecycle.
- `pulse.css` contains the measured dark-surface, border, spacing, type, dialog, menu, empty-state, and responsive rules.
- The header subscription button persists the selected `Daily`, `Weekly`, or `Never` summary cadence.

## Saved Pulse views

- The add-view button switches the tab strip into the inline Flow editor.
- Icon and color use the shared `ViewIconPicker`.
- Source filters support all updates, project updates, or initiative updates.
- Saved Pulse views reuse the persisted `SavedView` aggregate with `resource=pulse`, forced personal scope, viewer ownership, and an independent `/pulse/view/:id` route.
- Personal views are removed from other users' bootstrap projections and cannot be promoted to team/workspace scope through the Pulse creation API.
- Filters support Author, Team, Created date, Update type, Update health, Initiative, Project, Project members, Project status, and Project labels. Advanced mode controls all/any matching and positive/negative operators.

## Update composer

- `pulse-composer.tsx` shares one dialog for project and initiative updates.
- Source, health, rich-text body, attachment upload, keyboard submit, outside click, and Escape are functional.
- The change summary is derived from the selected project or initiative.
- Update attachments persist through object storage, remain authorization-checked under `/uploads`, render on Pulse cards, and are deleted with their update.

## Feed cards

- `pulse-update-card.tsx` renders source, health, author, timestamp, body, comments, reactions, and author actions.
- Project and initiative update cards use identical edit, delete, comment, reaction, copy-link, and copy-markdown interactions.
- Cards lead with a one-sentence takeaway when an update contains additional detail, and render persisted rich-text bodies and attachments.
- Initiative update comments and reactions have dedicated Go endpoints and domain events so both update types persist consistently.

## Menus and dismissal

- `pulse-menus.tsx` owns subscription cadence, source filter, and inline new-view controls.
- Dialogs and menus use Radix primitives, so outside click, Escape, focus return, and keyboard menu navigation are shared with the rest of the application.
- Personal Pulse summary cadence is persisted in `UserSettings` rather than browser storage.
