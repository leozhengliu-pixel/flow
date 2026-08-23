# Linear Issue and sub-issue display verification

Date: 2026-08-22

## Reference surfaces

- Linear team list: `https://linear.app/leozhengliu/team/LEO/all`
- Linear My issues: `https://linear.app/leozhengliu/my-issues/assigned`
- Linear issue detail: `https://linear.app/leozhengliu/issue/LEO-6`
- Flow shared lists: My issues, team/workspace issues, project issues, cycles and saved views
- Flow issue detail: `/cleantrack/issue/:identifier`

Measurements were collected from the signed-in Chrome DOM using element
rectangles and computed styles. A temporary three-level Flow issue hierarchy was
created in the isolated audit database and removed after verification.

## Data model and API audit

Flow already stores `parentId`, `subIssueIds` and `sortOrder`. This change adds:

- Runtime hierarchy projection: immediate parent, complete ancestor chain and
  direct child completion progress.
- Parent/sub-issue consistency repair when existing workspace data is loaded.
- Cycle prevention when updating `parentId` or creating `parent_of` /
  `sub_issue_of` relations.
- Regression tests proving that self/descendant parent cycles are rejected.

## Shared list measurements

Desktop content viewport: 1470 x 693.

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| List row width/height | 867 x 44 in split view | Shared surface width x 44 | height exact |
| Full-width list row | 1217 x 44 | 1217 x 44 | exact |
| Row radius | 8 | 8 | exact |
| Identifier x | 310.5 | 310.5 | exact |
| Title font | 13 px, 500 | 13 px, 500 | exact |
| Parent separator | `›`, 13 px/500 | `›`, 13 px/500 | exact |
| Checkbox | 14 x 14, radius 3 | 14 x 14, radius 3 | exact |
| Parent progress | 20 px ring + `done/total` | 20 px ring + `done/total` | exact structure |
| Context menu | 192 px, radius 12, z 600 | 192 px, radius 12, z 600 | exact |

List behavior:

- A child shows its immediate parent after the title when nesting is disabled.
- A parent shows direct-child completed/total progress.
- Nested mode hides the redundant breadcrumb and indents by actual hierarchy
  depth, not a single boolean level.
- The same shared row is used by My issues, team/workspace issues, project
  issues, cycles and saved views.

## Board measurements

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Card width | 322 | 322 | exact |
| Card radius | 8 | 8 | exact |
| Card shadow | 0.5 px outline plus 3/1 px layers | same tokenized layers | exact |
| Card transition | background 100 ms | background/shadow/opacity 100 ms | equivalent |
| Parent chain | immediate parent through root | immediate parent through root | exact behavior |
| Child progress | ring and completed/total | ring and completed/total | exact behavior |

Board cards remain draggable and keyboard-openable. Breadcrumb, progress,
property pickers, selection, drop indicators and empty-column behavior were
verified together.

## Issue detail measurements

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| Section x/width | 303.023 / 673.867 | 303.023 / 673.859 | -0.008 px width |
| Header height | 34 | 34 | exact |
| Collapse button | 24 high, pill | 24 high, pill | exact |
| Display/Create | 28 x 28, pill | 28 x 28, pill | exact |
| Child row | 673.867 x 36 | 673.859 x 36 | -0.008 px width |
| Row radius | 8 | 8 | exact |
| Checkbox | 14 x 14, radius 3 | 14 x 14, radius 3 | exact |
| Display Portal | 301 wide, radius 12, z 500 | 301 wide, radius 12, z 500 | exact |
| Portal motion | 190 ms cubic-bezier(.16,1,.3,1) | same | exact |

Detail behavior verified:

- Flattened completed/total count with optional recursive nesting.
- Collapse/expand by click and keyboard.
- Ordering and direction, completed visibility and nested toggle.
- Functional ID, priority, status, labels, project, cycle, due date, links and
  assignee display-property switches.
- Checkbox click/Space selection.
- Existing create sub-issue editor with disabled empty submit and Escape cancel.
- Recursive rows indent according to depth.

## Menus and nonfunctional controls

Issue property pickers and context menus use theme-aware Portals at z-index 600.
Historical placeholder context actions without handlers were removed. The menu
now exposes only working property updates plus copy and delete when their caller
provides those actions.

## Locale, theme and responsive matrix

| Combination | Result |
| --- | --- |
| English / Light / desktop | list, Board, context menu and detail verified |
| Chinese / Light / desktop | translated controls; entity titles unchanged |
| English / Dark / desktop | hierarchy rows and detail verified |
| Chinese / Dark / desktop | Display Portal and property pills verified |
| 390 x 844 mobile | list rows remain 44 px; detail rows remain 36 px; no document overflow |

Business issue titles, identifiers, labels, projects, cycles and user names use
untranslated entity content. Mobile hides excess metadata pills while retaining
title, status, selection and first label/assignee signals.

## Automated checks

- `go test ./...`
- `npm run build`
- `npm run lint`
- `git diff --check`
- Fresh browser console error check after final reload
