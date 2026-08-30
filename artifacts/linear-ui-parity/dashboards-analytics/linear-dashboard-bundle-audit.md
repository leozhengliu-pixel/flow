# Linear Dashboard production bundle audit

Inspected 2026-08-30 from the current `static.linear.app/client/assets`
production build. Asset hashes are deployment-specific.

## Route architecture

Linear does use separate route modules, but they share the same Views header and
tabs, which makes Dashboards behave like a resource tab on the Views screen.

Observed routes in `router.DY8IibY8.js`:

- Workspace directory: `/:orgKey/dashboards`
- Team directory: `/:orgKey/team/:teamKey/dashboards`
- Dashboard detail: `/:orgKey/dashboard/:dashboardId`
- Dashboard editor: `/:orgKey/dashboard/:dashboardId/edit`
- Widget editor: `/:orgKey/dashboard/:dashboardId/widget/:widgetId`
- New workspace dashboard: `/:orgKey/dashboards/new`
- New team dashboard: `/:orgKey/team/:teamKey/dashboards/new`

Route chunks:

- `DashboardsPage.DayovAnU.js`
- `DashboardsTeamPage.B-dgErM4.js`
- `DashboardPage.B146zZFy.js`
- `DashboardWidgetPage.CdKZB3o0.js`

## Shared Views chrome

`CustomViewsHeader.7YjcczrL.js` creates the Issues, Projects, Initiatives and
Dashboards tabs. It includes Dashboards only when
`organization.dashboardsAvailable` is true and the viewer is eligible. The
current application bundle can render `Dashboards` as its content-header title,
while the official Dashboards documentation image shows the directory chrome as
`Views` followed by `Issues`, `Projects`, and `Dashboards` in one row. Flow uses
the documented directory presentation requested for parity.

Both `DashboardsPage` and `DashboardsTeamPage` render `CustomViewsHeader` with
`viewType: dashboards`, an inline `Find a dashboard…` search, the Dashboard
display menu, and the New dashboard action.

## Directory implementation

`DashboardsHeaderSide.Deb1BnrJ.js` defines the dashboard list provider.

- Workspace groups, in order: Personal, Workspace, Cross-team.
- Group header height: 36px.
- Dashboard row height: 60px.
- Default columns: Name and Owner.
- Optional columns: Created (75px) and Updated (75px).
- Owner column: 120–140px.
- Ordering: Name, Owner, Updated, Created with direction control.
- Personal dashboard header copy: `Only visible to you`.
- Empty-state copy: `Group Insights charts together into dashboards to see
  trends and metrics across your organization.`
- Team dashboard pages use the same row renderer without workspace grouping.

## Flow correction

Flow follows the same route split and shared visual chrome: Views exposes a
Dashboards tab, `/dashboards` renders the same Views resource header, team Views
link to `/team/:teamKey/dashboards`, and dashboard details use
`/dashboard/:dashboardId`. The directory now implements Linear's search,
grouping, row heights, ordering and optional display columns.

The shared chrome is implemented by `ViewsDirectoryHeader`; both the Views and
Dashboards directories render that component. Dashboards no longer mounts its
old 48 px standalone header or 66 px bespoke tab row, and both routes use the
same `main-panel` container. The 44 px directory header contains `Views`, the
three resource tabs, optional directory controls, and the create action in one
row, matching the official documentation image's hierarchy.

Local DOM comparison at a 1280 x 720 viewport:

| Element | Views | Dashboards | Difference |
| --- | ---: | ---: | ---: |
| Panel | x 244, width 1028 | x 244, width 1028 | 0 px |
| Header | 1027 x 44 | 1027 x 44 | 0 px |
| Header padding | 0 11 0 18 | 0 11 0 18 | 0 px |
| Header title | `Views` | `Views` | identical |
| Resource tabs | Issues, Projects, Dashboards | Issues, Projects, Dashboards | identical |
| Tab height | 28 px | 28 px | 0 px |

The browser run also verified tab navigation, inline search, display-menu open
and Escape close, New dashboard dialog open and Escape close, and zero console
warnings or errors.
