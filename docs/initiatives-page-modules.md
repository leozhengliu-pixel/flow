# Initiatives / Initiative Detail replication modules

Reference audited against the authenticated Flow workspace on 2026-08-15.

## I1. Workspace routes and navigation

- `/initiatives/active`, `/initiatives/planned`, `/initiatives`
- `/initiative/:slug/overview`, `/activity`, `/projects`
- Active workspace sidebar state, canonical routes, not-found state

## I2. Initiative list shell

- 44 px title header and inline `New initiative` action
- Active / Planned / All initiatives pill tabs
- Filter, display and side-panel toolbar controls
- Persistent owner/health insight sidebar with Planned and zero-result empty states
- Configurable columns: priority, owner, target, projects, health, active projects
- Empty, loading and filtered-empty states

## I3. Initiative row

- Hover checkbox and bulk selection
- Editable icon/color, priority, owner and target date
- Project completion count and project association menu
- Initiative update health and active-project count
- Row action menu and detail navigation

## I4. Inline creation

- Inline expanded row, not a modal
- Name, summary, icon/color, status, priority, owner, target and projects
- Keyboard-aware shared property menus
- Cancel, disabled Create and persisted Create states

## I5. Initiative Overview

- Breadcrumb header, favorite, actions, copy, notifications and add-project controls
- Autosaving name, summary and description
- Reusable property controls and Flow date precision picker
- Create, edit and delete resources; sortable project table with real display-property toggles
- Properties/activity details sidebar

## I6. Initiative Activity

- Comment / Update segmented composer
- On track / At risk / Off track health selector
- Persistent comments with edit/delete/reactions and persistent initiative updates
- Update deletion and created-initiative timeline event
- Functional activity visibility menu for updates, comments and system activity

## I7. Initiative Projects roadmap

- Month/year scale, fortnight tick grid, today marker and zoom controls
- Pixel-aligned 28 px project visual, update, status, priority and lead controls
- Reused project property command menus with keyboard navigation and persisted changes
- Functional project-update preview, project health, owner and timeline bars
- Project navigation and horizontal overflow behavior

## I8. Go aggregate and domain events

- `Initiative`, `InitiativeResource`, `InitiativeUpdate`
- Stable `projectIds` association with reverse synchronization into projects
- Create, update, delete, resource, comment and update events
- SQLite snapshot normalization for existing databases

## I9. Custom initiative views

- `/initiative/:slug/view/new` and `/initiative/:slug/view/:viewId` canonical routes
- Icon/color, placeholder title, optional description and Cancel/Save controls
- Filter, display-property and zoom configuration applied to the live roadmap preview
- Workspace-local saved view projection with immediate saved-tab navigation
- Right-click Copy link, Favorite, Edit, Duplicate and Delete actions
- Edit and delete confirmation dialogs; active-view deletion returns to Projects

## Deferred external integrations

- Slack notification delivery
- Agent-authored updates
- Remote document provider previews
- Server-shared custom initiative views and multi-user collaboration
