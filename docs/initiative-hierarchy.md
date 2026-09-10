# Flow initiative hierarchy and team scope

## Data model

An initiative remains one entity at both workspace and team scope. A nonempty
`leadTeamId` makes it a team initiative; the contributing teams array is not a
second discriminator.

Parent relationships form a directed acyclic graph. An initiative may have
multiple parents. Flow keeps its existing representation:

| Field or relation | Meaning |
| --- | --- |
| `Initiative.parentInitiativeIds` | Direct parent IDs exposed to clients |
| `InitiativeRelation` with `type: parent` | `initiativeId` is the child; `relatedInitiativeId` is the parent |
| `Initiative.projectIds` | Directly associated projects only |
| `Initiative.leadTeamId` | Optional leading team |
| `TeamSettings.parentTeamId` | The team's hierarchy |

The initiative PATCH API and relation create/update/delete APIs keep the parent
array and relation records synchronized. Self-links, missing parents, and cycles
are rejected. Changing a relation away from `parent` removes that parent link.
Deleting a parent unlinks its children; it does not delete the children or their
other parents. Historical relation-only records are projected into parent IDs.

## Derived values

Effective projects are the union of the initiative's direct projects and every
accessible descendant's projects, deduplicated by project ID. List counters,
overview project rows, roadmap contents, and detail sidebar project statistics
use this union. Inherited rows are identified in the overview. Adding/removing
direct associations still edits only the current initiative's `projectIds`.

These values are calculated while reading. No child project list is copied into
an ancestor's stored payload, so a child update does not fan out into ancestor
database writes. Status, owner and target date remain independent properties.

## Team scope

The team page includes the selected team and its accessible subteams. With
contributing initiatives enabled, related initiatives can come from:

- Their leading team and that team's ancestors.
- The leading teams of their initiative ancestors.
- Teams belonging to directly associated or inherited projects.
- Flow's existing explicit contributing-team assignments.

Disabling contributing initiatives limits the page to initiatives led by the
team or its subteams. The workspace's Show team initiatives setting checks for
a nonempty leading team. Creation from a team page preselects its team.

A hidden leading team cannot be bypassed by attaching a public project or
contributing team. Projection removes hidden parent IDs, relations and updates;
restricted API-key and MCP projections also redact hierarchy links. Existing
Flow workspace/team access policies continue to apply. Commercial plan gates
are not introduced.

## UI

- The list supports nested rows, expansion/collapse, and optional nonmatching
  parents as context. The display menu controls nested and parent rows.
- A shared initiative is shown once in the main list; all direct parents remain
  editable in its detail page.
- The overview exposes parent navigation, a searchable multi-parent picker,
  direct sub-initiatives, Add existing, New sub-initiative, and unlink actions.
- Unlink changes only the selected edge. It does not delete an initiative.
- New sub-initiatives reuse the existing create controls and default to the
  parent's leading team. Their parent association is included in the create
  mutation.
- The team page retains team routes when changing tabs and exposes a contributing
  initiatives toggle.
