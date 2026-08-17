# Workspace replication modules

## Reference observations

- The sidebar workspace trigger opens a command-style menu, not a native select.
- `Switch workspace` opens a second menu grouped by account. It shows the current workspace checkmark, per-workspace issue count, and account actions.
- Keyboard sequences are `G` then `S` for settings and `O` then `W` for workspace switching.
- Workspace creation lives at `/join`. Name auto-generates the URL key until the URL is edited manually.
- Creation requires a name and URL key, offers United States and European Union regions, and keeps the submit action disabled while incomplete.

## Account and routing

- `GET /api/account/bootstrap` returns the viewer, memberships, counts, and last active workspace.
- `/` restores the last workspace, or redirects to `/join` when the account has no workspace.
- Every existing mutation sends `X-Workspace-Key`; Issues, Projects, Views, Cycles, Inbox, Pulse, and preferences are isolated by workspace.
- Existing `cleantrack` SQLite data migrates from `workspace_state` into `workspace_states` without being reseeded.

## Workspace lifecycle

- Create an empty workspace with canonical issue and project states plus its initial team.
- Switch between workspaces from the nested menu or `O` then `W`.
- Rename, change URL, and delete from workspace settings.
- Deleting the final workspace returns the account to `/join`.
- Workspace create, update, and delete operations write domain events.

## Team lifecycle and scope

- The sidebar renders every team and independently expands or collapses its navigation.
- Team Issues, Projects, Cycles, and Views preserve the workspace and team route scope.
- Team settings support create, rename/key edit, and delete while preventing deletion of the final team.
- Team create, update, and delete operations write domain events in the owning workspace.

## Empty states

- A new workspace begins with no issues, projects, saved views, cycles, initiatives, notifications, or updates.
- Workspace and team Issues, Projects, and Views reuse their existing empty states and creation actions.
- The workspace shell remains fully navigable while each resource collection is empty.

## Persistence layout

- `workspace_states` stores one serialized `domain.Bootstrap` aggregate per URL key.
- `account_state` stores the viewer and last active workspace key.
- The store exposes workspace-specific bootstrap and mutation operations while retaining legacy single-workspace methods for compatibility.
