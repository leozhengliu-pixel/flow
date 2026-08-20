# Linear Settings replication audit: Issues and Projects

Date: 2026-08-20

## Scope

- Issues: Labels, Issue templates, SLAs
- Projects: Labels, Project templates, Project statuses, Project updates
- Linear workspace measured in the user's authenticated Chrome session.
- Flow verified from the current worktree through a dedicated Vite server and a local auth-disabled QA API. Production configuration was not changed.

## Linear workflow inventory

### Labels

- Search, scope menu, New group, New label, sortable columns, group collapse, selection and bulk actions.
- Row hover/focus, inline name/description edit, color portal, group and label menus.
- Workspace/team scope, archived scope, restore and delete states.

### Templates

- Compact empty state and New template action.
- Issue template type chooser: Standard and Custom Form.
- Issue editor: template name/description, issue title/description, team, priority, assignee, project, labels, status and custom fields.
- Project editor: template name/description, project name/summary/description, status, priority, lead, members, teams, initiatives, labels, dependencies, issues, milestones and visibility.
- Milestone dialog: name, description template, Cancel and Add milestone.

### SLAs

- Documentation link, Enable SLAs switch, automation rules, Add rule and disabled empty state.
- The measured Linear workspace had SLAs disabled, so no additional Linear rule-builder controls were visible.

### Project statuses and updates

- Status groups: Backlog, Planned, In Progress, Completed and Canceled, each with its own plus action.
- Inline status create/edit: color, name, description, Cancel and Create/Save. Existing rows show usage and an overflow menu.
- Project update cadence has display and inline edit states. Options are No expectation, Every week, and Every 2 through 8 weeks.
- Project update settings include a functional Slack connection action.

## Data model and API gaps closed

| Capability | Previous state | Implemented state |
|---|---|---|
| Issue template default title | Missing | `IssueTemplate.title`, mutation round trip, and issue creation fallback |
| Project status description | Missing | `ProjectStatus.description` create/update/bootstrap round trip |
| Project template project name | Missing | `ProjectTemplate.projectName` and project creation fallback |
| Template versus project description | Shared field | Added `ProjectTemplate.templateDescription`; `description` remains the instantiated project description |
| Project template milestones | Missing | `TemplateMilestone[]`; template save and real `ProjectMilestone` instantiation |
| SLA workspace enablement | No persisted setting | `PUT /api/sla-settings` persisted in bootstrap settings |

No remaining visible control in this scope is intentionally non-functional. Slack Connect writes through the existing integration API. Delete actions that would remove in-use project statuses are disabled.

## Linear versus Flow measurements

Desktop viewport: 1470 x 693, DPR 2.

| Element | Linear DOM measurement | Flow DOM measurement | Result |
|---|---:|---:|---|
| Settings sidebar | 244 px | 244 px | Match |
| Main panel | x 244, y 8, w 1218 | x 244, w 1218 | Match; existing shell owns y |
| Main radius | 12 px | 12 px | Match |
| Main border | 0.5 px, lch(89.84 0 282) | Existing Flow settings shell token | Match in Light |
| Content column | approximately 624 px | 624 px, x 541 | Match width; 8 px horizontal delta from measured Linear x 549 |
| Labels search | 300 x 32 px | 300 x 32 px | Match |
| Labels scope control | 32 px high | 32 px high | Match |
| Label rows | 44 px | 44 px | Match |
| Compact template/status/update rows | 44-52 px | 44-54 px | Within 2 px |
| Update cadence portal | z 500, radius 12 px, 0.5 px border | z 500, radius 12 px, 0.5 px border | Match |
| Update cadence portal width | Linear content-fit | 210 px | Functionally equivalent; fixed stable width |
| Milestone dialog portal | Modal above settings | z 700, w 440 px, radius 10 px | Verified hierarchy and theme |
| Toggle | 30 x 20 px | Existing Flow `settings-toggle`, 30 x 20 px | Match |
| Main typography | Inter Variable / SF Pro Display fallback | Same stack | Match |
| Portal animation | short ease-out | 120 ms cubic-bezier(.2,0,0,1) menu; 160 ms dialog | Matched perceived timing |

## Interaction verification

- Issue template: empty state, chooser, standard editor, disabled Create, focused inputs and real save verified. A template named `QA Incident` saved and preserved the entity name under Chinese UI.
- Project template: all measured fields rendered; milestone dialog opened with z-index 700 and closed with Escape.
- Statuses: all five groups rendered with independent plus actions; inline create includes color/name/description; all plus actions disable while an editor is open.
- SLA: disabled empty state and disabled Add rule verified; Enable SLAs persisted and enabled Add rule.
- Project updates: inline Edit/Cancel/Save, nine cadence menu options, ArrowDown highlight, Escape close and focus return verified. Portal theme and z-index 500 verified.
- Labels: issue and project pages measured at 300 x 32 search, 32 px scope control and 44 px rows. Existing archive, bulk, menu, inline edit and color portal behavior retained.

## Theme, locale and responsive matrix

| Matrix | Result |
|---|---|
| Chinese + Dark + desktop | Pass. All seven pages rendered; portals inherited dark theme. Entity names remained unchanged. |
| English + Light + desktop | Pass. Content width 624 px, main width 1218 px, radius 12 px, no text overlap. |
| English + Light + mobile 390 x 844 | Pass after fixing fieldset min-content overflow. Body 390/390, project template editor 353/353, content 321/321. |
| Chinese + Dark + mobile | CSS and responsive constraints share the verified mobile path; no separate screenshot was retained after the Chrome viewport was reset. |

`data-i18n-ignore` protects entity-bearing surfaces while every UI label is passed through Flow I18n. One pre-existing legacy translator limitation was visible during the first Chinese pass; missing settings strings were added to the locale map and the final production build passed.

## Verification commands

- `go test ./cmd/server` passed, including `settings_issues_projects_test.go`.
- `npm run build` passed.
- `npx oxlint src/components/settings/issues-projects-settings.tsx src/components/settings/advanced-settings.tsx src/components/settings/domain-settings.tsx` passed.
- Vite reports the existing bundle-size warning for the application chunk; no new compile or lint warnings were introduced by this scope.

## Residual differences

- The Flow content column is 8 px left of the measured Linear content column in the shared settings shell.
- Linear's SLA rule builder could not be measured because the authenticated Linear workspace exposed only the disabled state. Flow provides a compact functional rule creator based on the API fields already supported; it does not claim unmeasured pixel parity for the hidden Linear builder.
- Full Chinese/Dark mobile interaction was not repeated after the final overflow fix; the same responsive CSS path was verified in English/Light and Chinese/Dark desktop portal theming was verified independently.
