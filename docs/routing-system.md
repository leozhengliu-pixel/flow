# Routing system

The browser URL is the canonical application state. `web/src/lib/app-routes.ts` owns all route parsing and URL generation; components must not concatenate workspace, Issue, or Project paths independently.

## Canonical routes

| Surface | Route |
| --- | --- |
| Workspace root | `/{workspace}` -> `/{workspace}/my-issues/assigned` |
| Inbox | `/{workspace}/inbox` |
| My Issues | `/{workspace}/my-issues/{assigned|created|subscribed|activity}` |
| Workspace Issues | `/{workspace}/issues/{active|backlog|all}` |
| Team Issues | `/{workspace}/team/{teamKey}/{active|backlog|all}` |
| Workspace Projects | `/{workspace}/projects/all` |
| Team Projects | `/{workspace}/team/{teamKey}/projects/all` |
| Issue detail | `/{workspace}/issue/{identifier}/{titleSlug}` |
| Project detail | `/{workspace}/project/{projectSlugId}/{overview|updates|issues}` |

## Behavior contract

- `BrowserRouter` owns navigation and browser history.
- `App` derives the current page, My Issues view, selected Issue, selected Project, and detail tab from the URL. There is no parallel page-selection state.
- Root and incomplete paths are canonicalized with history replacement.
- Issue title slugs are canonicalized from the current Issue title. The stable lookup key remains the Issue identifier.
- Project lookup uses the stable `slugId` returned by Go.
- Workspace and Team keys are validated against bootstrap data before rendering scoped content.
- Unknown entities and paths render explicit not-found states without leaking another workspace or team's data.
- Sidebar navigation, My Issues tabs, Team Issue tabs, Project tabs, Command Menu entries, Issue rows, and Project rows all use the same route builders.
- Entity rows expose real `href` attributes. Modified click and middle click preserve native new-tab behavior; ordinary clicks use SPA navigation.
- Row property controls prevent ancestor-link navigation while retaining their own popover interaction.

## Deep-link verification

Verified locally on 2026-08-13:

- Direct Issue URL load, canonical title replacement, and browser refresh.
- Direct Project overview load and browser refresh.
- My Issues Assigned -> Created and browser Back restoration.
- Team Issues Active/Backlog/All URL changes.
- Project Overview/Updates/Issues URL changes.
- Workspace and Team mismatch not-found states.
- Issue and Project row `href` values and normal click navigation.
- Issue row property picker opens without changing the URL.
