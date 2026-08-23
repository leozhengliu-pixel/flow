# Linear Issue Templates verification

Date: 2026-08-23

## Scope

- Linear list: `/leozhengliu/settings/issue-templates`
- Linear standard create: `/leozhengliu/settings/templates/issue/new`
- Linear form create: `/leozhengliu/settings/templates/issue/new/form`
- Linear edit: `/leozhengliu/settings/templates/issue/{id}/edit`
- Flow list: `/cleantrack/settings/issue-templates`
- Flow standard/form/edit use the equivalent `/settings/templates/issue/...` paths.

The Linear states were traversed in the signed-in Chrome session. A temporary
standard template and custom-form drafts were created only for the audit and
removed before completion. Flow QA templates were also removed.

## Model and API

| Linear behavior | Flow representation | API / result |
| --- | --- | --- |
| Standard and Custom Form | `IssueTemplate.templateType` | Workspace create/update/delete |
| Template icon and color | `icon`, `color` | Persists through bootstrap |
| Default team, priority, assignee, project, labels | Existing default fields | Applied by issue creation |
| Template visibility | `visibilityTeamId`, separate from default `teamId` | Workspace/team visibility persists |
| Full custom field catalog | `TemplateFormFieldType` | Eleven measured field types validated |
| Template sub-issues | `TemplateSubIssue[]` | Parent and children instantiated together |
| Duplicate | New editor initialized from the source | No premature mutation |
| Copy creation URL | `?create=1&template={id}` | Opens create dialog with template applied |

## Desktop measurements

Viewport: 1470 x 693.

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| List title x/y/size | 549 / 72.5 / 608 x 32 | 549 / 72.5 / 608 x 32 | exact |
| Empty card x/y/width | 533 / 184.5 / 640 | 533 / 184.5 / 640 | exact |
| New button size | 130.59 x 32 | 130.12 x 32 | 0.47 px |
| Type dialog x/y/size | 572 / 120.34 / 562 x 332 | 572 / 120 / 562 x 332 | < 0.35 px |
| Type option width/height | 264 x 252 | 264 x 252 | exact |
| Standard heading x/y/width | 478 / 72.5 / 750 | 478 / 72.5 / 750 | exact |
| Issue preview x/y/size | 478 / 164.5 / 750 x 148 | 478 / 164.5 / 750 x 148 | exact |
| Defaults card x/y/size | 478 / 406.5 / 750 x 56 | 478 / 406.5 / 750 x 56 | exact |
| List menu size | 302 x 165 | 302 x 165 | exact |
| Delete dialog size | 480 x 169 | 480 x 169 | exact |

## Component and state edges

- Empty and populated list; singleton and multi-row layout.
- Row link, row hover, menu trigger hover/open/focus and menu close.
- Strict list menu order: Edit, Duplicate, Copy URL, Delete.
- Type Dialog open/close, outside click, Escape and focus return.
- Standard create/edit/duplicate, icon Portal, default-property Portals and sub-issue composer.
- Custom Form empty state, all field menu items, field editor, required state,
  options, type conversion, remove and multi-field sorting.
- Custom field sorting: no handle for one field; every field is sortable when
  count is greater than one; mouse and `Alt+Arrow` order persists.
- Editor actions: visibility submenu, conversion, copy URL and delete.
- Dirty editor back/Cancel produces Discard changes confirmation.
- Delete produces confirmation and removes the persisted entity.

## Portal matrix

| Combination | Result |
| --- | --- |
| English / Light / desktop | List, chooser, editors, menus and dialogs |
| Chinese / Light / desktop | UI translated; template/team/user names preserved |
| English / Dark / desktop | Portal background, border, shadow and z-index |
| Chinese / Dark / desktop | Same with translated controls |
| English / Light / 390 x 844 | No horizontal overflow |
| Chinese / Dark / 390 x 844 | No horizontal overflow |

Menu z-index is 600. Dialog overlay/content use 700/701. Mobile chooser is
366 px wide at x=12; the standard editor content is 353 px wide at x=18.5.

## Visual evidence

- `linear-list-target.png`, `flow-list-target.png`, `overlay-list-target.png`, `diff-list-target.png`
- `linear-chooser-target.png`, `flow-chooser-target.png`, `overlay-chooser-target.png`, `diff-chooser-target.png`

The target-area raw threshold report is stored in `visual-diff.json`. Cursor,
font rasterization and the signed-in Linear Agent chrome account for remaining
raw pixels; geometry comparisons above are DOM measurements.

**Pixel gate:** not passed. The latest RGB-threshold-3 target-area ratios are
3.38% for the empty list and 6.01% for the chooser, above the requested 1%.
The implementation and interaction tests are present, but under the requested
acceptance rule this audit remains incomplete until those ratios are reduced.

## Automated checks

- `go test ./...`
- `npm run build`
- `npm run lint`
- `git diff --check`
