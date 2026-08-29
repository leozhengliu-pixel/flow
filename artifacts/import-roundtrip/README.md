# Linear / Flow issue import round-trip

Test date: 2026-08-29

## Linear -> Flow

Source issue: `LEO-5` (`test`). A Linear-compatible CSV row was imported into
the local Flow `test` workspace.

Preserved before compatibility fixes:

- title and description
- status and priority

Lost or remapped before compatibility fixes:

- assignee fell back to the Flow viewer
- missing label and project were skipped
- source identifier, estimate, and lifecycle timestamps were not mapped

The importer now additionally maps Linear's canonical headers, preserves source
IDs and lifecycle timestamps, imports estimates and archived state, creates
missing statuses and labels, resolves parent/sub-issue links, and rejects a
duplicate source ID on re-import.

## Flow -> Linear

The old Flow export used lowercase headers and crashed Linear's official
`LinearCsvImporter` while reading the missing `Title` field. Flow now exports
Linear's current 29-column issue schema.

The updated export was parsed successfully by `@linear/import@3.2.9`, then
imported into the `LEO` team as temporary issue `LEO-18`.

Preserved by the actual import:

- title and description
- priority and workflow status
- original creation timestamp

Changed or excluded by Linear's CLI importer:

- the issue identifier changed from `TES-1` to `LEO-18`
- `updatedAt` and `startedAt` became import-time values
- assignee was set to the importing user by `--self-assign`
- Linear team automation added the issue to the active cycle
- project, cycle, initiatives, milestone, SLA status, relations, subscribers,
  attachments, comments, releases, and customer requests are not restored by
  the Linear CSV importer

The temporary Linear issue was deleted and the temporary API key was revoked;
the revoked key returns HTTP 401.

## Full migration assistant

Flow now also exports a versioned workspace migration bundle and persists a
source-to-target manifest. The assistant supports interactive user/team/project
mapping, invitations, dependency-ordered Flow or Linear execution, resumable
failed jobs, and rollback. Planning entities, comments, attachments, relations,
subscriptions, releases, customers, customer requests, and SLA records are
processed after their dependencies. Linear API limitations for human-readable
identifiers, `updatedAt`, `startedAt`, and internal SLA timestamps are retained
in the manifest and an import metadata comment instead of being reported as
native fields.

## Evidence

- `linear-leo5.csv`
- `flow-result-linear-leo5.json`
- `flow-imported-linear-leo5.json`
- `flow-imported-linear-after-fix.json`
- `schema-diff.json`
- `official-linear-parser-results.jsonl`
- `official-linear-parser-flow-export-failure.txt`
- `flow-export-linear-compatible.csv`
- `official-linear-parser-flow-compatible.json`
- `linear-imported-flow-result.json`
