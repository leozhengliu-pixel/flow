# Linear Project Templates verification

Date: 2026-08-23

## Routes

- Linear list: `/settings/project-templates`
- Linear create: `/settings/templates/project/new`
- Linear edit: `/settings/templates/project/{id}/edit`
- Flow implements the equivalent three deep links.

The signed-in Chrome session was used for all Linear measurements. Temporary
Linear and Flow templates and milestone drafts were removed after verification.

## Model/API mapping

| Linear behavior | Flow representation | Result |
| --- | --- | --- |
| Template and project names | `name`, `projectName` | Create/update/bootstrap |
| Icon and color | `icon`, `color` | Persists |
| Summary and rich description | `summary`, `description` | Persists and instantiates |
| Status/priority/lead | `statusId`, `priority`, `leadId` | Validated references |
| Members/teams/initiatives/labels | ID arrays | Multi-select Portals |
| Dependencies/issues | ID arrays | Multi-select Portals |
| Milestones | `TemplateMilestone[]` | Create/remove/sort/persist/instantiate |
| Visibility | `visibilityTeamId`, separate from default `teamIds` | Workspace/team scope |
| Duplicate | New route initialized from source | No premature mutation |
| Copy URL | `?create=1&template={id}` | Project composer opens with template |

## Measurements

Desktop viewport: 1470 x 693.

| Element | Linear | Flow | Difference |
| --- | ---: | ---: | ---: |
| List title x/y/size | 549 / 72.5 / 608 x 32 | 549 / 72.5 / 608 x 32 | exact |
| Empty card x/y/width | 533 / 184.5 / 640 | 533 / 184.5 / 640 | exact |
| Editor inner width/x | 750 / 478 | 750 / 478 | exact |
| Template name input x/y/size | 478 / 168.5 / 750 x 32 | 478 / 168 / 750 x 32 | 0.5 px |
| Project card x/y/size | 478 / 225 / 750 x 434 | 478 / 224 / 750 x 434 | 1 px |
| Property height/radius | 24 / 9999 | 24 / 9999 | exact |
| Empty milestone header | 750 x 45 | 750 x 45 | exact |
| List menu | 312.5 x 165 | 312.5 x 165 | exact |
| Edit menu | 312.5 x 109 | 312.5 x 109 | exact |
| Delete dialog x/y/size | 613 / 174.66 / 480 x 169 | 613 / 174.5 / 480 x 169 | < 0.2 px |

## State edges

- Empty/populated list, row hover, strict four-item menu and menu close.
- New/edit/duplicate routes, dirty discard, save failure and delete confirmation.
- Nine project property controls and independent visibility control.
- Empty, composing, one-item and multi-item milestone states.
- Multiple milestones expose a six-dot handle on every row; mouse and
  `Alt+Arrow` sorting persist through refresh.
- Copy URL opens the real project composer with template defaults.
- Portal levels: menus 600, dialog overlay/content 700/701.

## Matrix

- English/Chinese x Light/Dark x desktop/390x844 mobile.
- Desktop card is 750 px. Mobile card and inputs are 353 px at x=18.5.
- No horizontal overflow or text overlap was detected.
- Template, project, team and user entity names are not translated.

## Evidence

- `linear-list-target.png`, `flow-list-target.png`, `overlay-list-target.png`, `diff-list-target.png`
- `linear-editor-target.png`, `flow-editor-target.png`, `overlay-editor-target.png`, `diff-editor-target.png`
- `model-api.json`, `state-edges.json`, `visual-diff.json`

**Pixel gate:** not passed. The latest RGB-threshold-3 target ratios are 7.68%
for the empty list and 8.99% for the editor. They remain above the requested
1%, so this audit must not be reported as pixel-complete despite the DOM
geometry and interaction coverage.
