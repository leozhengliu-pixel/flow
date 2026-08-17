# Cycles / Cycle Detail Replication

## Scope

Cycles is the next core planning module after Projects and Initiatives. It reuses
the Issue engine instead of maintaining a second issue representation.

## Module Breakdown

### Cycle domain and recurrence

- `Cycle`: number, display name, description, team, date range, status,
  capacity, favorite state, and timestamps.
- `CycleSettings`: enabled state, duration, cooldown, start weekday, future
  cycle count, and automatic issue assignment preferences.
- `Issue.cycleId` is the source of truth for membership.
- Starting or completing a cycle rolls unfinished `unstarted` and `started`
  issues forward. Backlog, canceled, and completed issues remain in place.
- Future cycles are generated from team settings and all mutations emit domain
  events through the SQLite store.

### All cycles

- Route: `/:workspace/team/:teamKey/cycles`.
- All, Current, and Upcoming sub-routes.
- Flow-style date rail, status chip, capacity dial, scope/success metrics,
  favorite control, and expanded current-cycle progress graph.
- Edit name/description/capacity, change dates, start today, complete cycle,
  and team cycle settings are persisted menus/dialogs.
- Disabled/no-cycle state matches Flow's centered empty state.

### Cycle detail

- Route: `/:workspace/team/:teamKey/cycle/:cycleId`.
- Shared List and Board issue presentations.
- Shared inline status, priority, assignee, labels, project, and due-date menus.
- Board drag and drop updates workflow status.
- Add/remove Issue picker persists `cycleId`.
- Issue click opens the existing full detail preview; mutations remain shared
  with the Issue engine.
- Progress sidebar contains scope, started, completed, capacity, success, date
  range, and the cycle graph. `Cmd/Ctrl+I` toggles it.

## Visual Measurements

- App panel inset: 8px with the existing 12px shell radius.
- Header: 44px; secondary toolbar: 39-40px.
- Timeline date rail: 150px desktop, collapsed responsively.
- Overview cycle header: 84px; current expanded graph: 300px.
- Detail graph panel: 43% with a 370px minimum on desktop.
- Menus use shared Flow dark surfaces, 30px rows, 5px row radius, and
  Radix outside-click/escape dismissal.

## Deferred

- Server-side cycle analytics history sampled per day.
- Estimate-based capacity when team estimation is enabled.
- Per-member capacity overrides and leave calendars.
- Automatic assignment rules based on workflow transition timestamps.

