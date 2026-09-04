# Settings replication matrix

Flow settings is a full-page application surface. It is not a dialog and it
does not reuse the application sidebar. All pages share the same 244px settings
navigation, 8px content inset, 12px content radius, 640px form column, and the
same row, select, toggle, menu, confirmation, empty, and save-state primitives.

## Personal

| Page | Route | Main controls | Persistence |
| --- | --- | --- | --- |
| Preferences | `/settings/account/preferences` | General, theme, desktop, workflow selects and toggles | Settings document |
| Profile | `/settings/account/profile` | Avatar, name, title, username, leave workspace | Settings document / workspace membership |
| Notifications | `/settings/account/notifications` | Channel matrix, digest, sound, subscription preferences | Settings document |
| Code & reviews | `/settings/account/code-and-reviews` | Git branch format, review automation | Settings document |
| Security & access | `/settings/account/security` | Sessions, passkeys, sign-out actions | Settings document |
| Connected accounts | `/settings/account/connections` | Account connection states | Settings document; external OAuth is simulated |
| Agent personalization | `/settings/account/agents` | Instruction editor and enablement | Settings document |

## Issues and projects

| Domain | Pages | Behaviour |
| --- | --- | --- |
| Issues | Labels, templates, SLAs | Search, create, edit, delete, empty states, enablement |
| Projects | Labels, templates, statuses, updates | Search, create, edit, reorder/delete, update cadence |

## Features

AI & Agents, Initiatives, Documents, Customer requests, Releases, Pulse, Asks,
Emojis, and Integrations use a shared feature setting surface. Controls persist
locally. Third-party authorization and integration side effects
remain represented by accurate connected/disconnected states.

## Administration

| Page | Behaviour |
| --- | --- |
| Workspace | Logo/name/URL, fiscal month, region, danger confirmation |
| Teams | Team list and create-team navigation |
| Members | Search, role/status filtering, export and invite surface |
| Security | Domain restrictions, guest policy, authentication toggles |
| API | Personal API key CRUD with one-time key reveal |
| Applications | OAuth application CRUD empty state |
| Import & export | JSON export and local import picker |
| Team | Team identity and defaults |

## Shared interaction contract

- Dropdowns close on outside pointer and Escape and restore trigger focus.
- Arrow keys move options; Enter selects; Escape closes.
- Destructive actions require an in-product confirmation dialog.
- Inputs save on blur; toggles and selects save immediately.
- Search filters both navigation and list pages.
- State survives reload and is scoped by workspace.
- Narrow viewports replace the fixed sidebar with an off-canvas navigation.
