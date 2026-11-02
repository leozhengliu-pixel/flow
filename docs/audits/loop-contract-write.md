# Loop Contract Write Verification

## Cause And Change

`storeLoopContract` used `MutateWorkspace` with `loop.output_contract`, which
was absent from the metadata allowlist. The resulting full path held the
workspace writer lock while cloning metadata, loading all issue and discussion
records, running the callback, comparing records, and persisting its delta.
This does not imply that every issue was rewritten or that binlog volume grew.

Simply adding the event to the allowlist would include Loops and LoopVersions
in metadata persistence, but would still reload and clone unrelated metadata.
The new dispatch path instead reads the current Loop and its related versions
inside one transaction. MySQL and PostgreSQL use `FOR UPDATE`; SQLite uses its
transaction and existing busy-write retries. Only changed records are upserted.
An identical save returns before any write, event, cache revision, or publish.

The callback checks the instruction hash against the freshly locked Loop. A
stale extraction returns without updating either the current Loop or versions.
Person contracts remain authoritative; stale person contracts get merged
suggestions, including chosen targets. Model contracts update matching versions
except person-owned versions. Fallback contracts update the current Loop only.

The path retains the existing Redis workspace lock when configured, plus the
process-local `writeMu`; it does not hold the snapshot reader mutex during SQL.
Only Loop and LoopVersion metadata rows are selected for update. The versions
query filters `loopId` within that workspace's version collection, not issues,
comments, activities, or notifications. No new timeout or resource limit is
part of the fix.

## Reproduction

From this worktree:

```sh
scripts/perf/loop-contract-write.sh
```

The script uses the machine-wide heavy-command lock, creates uniquely named
throwaway MySQL 8.4, PostgreSQL 16, and Redis 7 containers on dynamic localhost
ports, and removes only those containers and their volumes on exit. Databases,
test binaries, and filtered reports stay under its printed `/tmp` directory.
All model responses are scripted; no paid or local-proxy model calls are made.

## Measurements

The MySQL workspace has 75,000 issues, 75,000 comments, 75,000 activities, one
Loop, three versions, and two drafts. Each measurement runs in a separate test
process, without competing API processes. The baseline calls the original
`mutateFull` path with the original delta persistence, not the test switch that
forces legacy full-table persistence. Changed saves update the same four
metadata records in both paths.

| Save Path | SQL Commands | Row Operations | Issue/Content Rows Written | Lock Held (ms) | Elapsed (ms) | Peak Heap (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Before: full | 1,226 | 13 | 0 | 15,359.182 | 15,360.303 | 564.50 |
| After: narrow | 26 | 10 | 0 | 13.698 | 14.578 | 2.13 |
| After: repeated | 4 | 0 | 0 | 4.420 | 5.418 | 1.79 |
| After: stale | 4 | 0 | 0 | 8.124 | 10.582 | 1.71 |

Observed on 2026-11-01 in `/tmp/codex-loop-contract-write.u3j3Mw`.
The row-operation totals include event sequencing and cache bookkeeping, not
only entity rows. Both changed paths left every issue/content row unwritten.

Query counts come from the throwaway MySQL general log for the application
account, including transaction commands and executed prepared statements but
not prepare/close commands or the separate observer connection. Row operations
are observed by INSERT/UPDATE/DELETE triggers on every application table,
excluding the instrumentation table itself. Peak heap is Go `HeapAlloc`,
sampled every millisecond, not an OS RSS or database-memory measurement.
Lock-held time is the store's existing mutation trace, inside the Redis lock.

## Two Processes

`TestLoopContractTwoReplicaGate` launches two independent API processes sharing
the database and Redis with the unchanged five-second lock-wait setting. Twelve
rounds each start four concurrent HTTP requests: forced contract extraction and
draft update on each replica. It checks all 48 responses, reopens the database,
checks both final draft titles and bodies, and checks current/version contract
hashes. SHA-256 comparisons cover every seeded issue, comment, and activity
record before and after. Child processes are stopped by PID.

| Database | Processes | Successful Requests | Busy Errors | Lost Updates | Run Time |
| --- | ---: | ---: | ---: | ---: | ---: |
| MySQL | 2 | 48 | 0 | 0 | 3.875 s |
| PostgreSQL | 2 | 48 | 0 | 0 | 1.930 s |
| SQLite | 2 | 48 | 0 | 0 | 0.778 s |

The MySQL run uses the 75k workspace; PostgreSQL and SQLite runs use small
independently created workspaces to check the shared path, not to claim
equivalent scale benchmarks. The MySQL before/after content digest was
`b01b565f5ac82d7ab05beaa053b0e24d2d3e16a0fa7f80ac77744d679af627f2`.
The gate exited 0 and removed all three of its containers and their volumes.

## Draft Lock And Other Events

Issue draft saves (`draft.created`, `draft.updated`, `draft.deleted`, and
`drafts.deleted`) are already metadata-only. They still run through the generic
snapshot writer, taking the same process-local `writeMu` and, with Redis
configured, the same workspace-scoped distributed lock as contract saves.
Resource validation for a draft can separately query its referenced issue.
Loop draft edits use `loop.updated`, also already metadata-only. These paths
were not changed; shortening the contract transaction removes its long hold
of their shared lock.

All other direct Loop mutation events found are already allowlisted:
`loop.created`, `loop.updated`, `loop.deleted`, `loop.duplicated`,
`loop.described`, `loop.run_started`, `loop.run_progress`, `loop.run_finished`,
`loop.scheduled`, `loop.version_restored`, `loop.run_feedback`, and
`loop.attachment_uploaded`. Workspace Loop settings use the already allowlisted
`workspace_preferences.updated`. The fixed event uses explicit dispatch rather
than expanding the generic allowlist.

Confirmed metadata-only omissions found in other callbacks, left unchanged:

| Events | Metadata Touched |
| --- | --- |
| `agent.approval_resolved`, `agent.message_changes` | Agent session message state after separately executed actions |
| `agent.message_feedback`, `agent.session_feedback` | Agent session feedback |
| `document.attachment_created`, `document.attachment_saved`, `document.attachment_deleted` | Document attachments and metadata authorization |
| `email_intake.updated` | Intake address and metadata validation |
| `oauth_application.secret_rotated` | Application credentials; separate audit call follows |
| `release_pipeline.access_key_revoked`, `release_pipeline.access_key_used` | Pipeline credential state and usage time |
| `review.written_back` | Review synchronization results |
| `migration.previewed`, `migration.mappings_updated`, `migration.users_invited` | Migration job bookkeeping, plus preview audit entry; invitations are made separately |

This is a callback audit, not a proposal to blindly allowlist names. Other
missing names can legitimately need issue validation, cascades, notifications,
or already use a mutation scope. None of those are changed here.

## Regression Coverage And Limits

`TestLoopContractSaveRegression` reads persisted rows and covers model saves,
multiple matching versions, nonmatching versions, person-owned versions,
fallback downgrade, repeated saves with zero observed writes, stale hashes
against a deliberately stale replica snapshot, person suggestions, chosen
target merging, and already-reviewed person contracts.
`TestLoopContractMutationUsesFreshNarrowRecords` checks scoped callback inputs,
fresh transactional data, two changed rows, zero repeat rows/events, and the
explicit `loop_contract` dispatch trace instead of `mutateFull`.

No REST shape, schema migration, web code, production database, or live model
configuration changed. The synthetic local benchmark is not a production
latency prediction. Very large Loop version collections can still increase
the version-filter query's work; the query does not scan issue collections.

Verification commands (all exit 0, from the worktree or `api/` as appropriate):

```sh
scripts/perf/loop-contract-write.sh
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./internal/store ./cmd/server -run 'TestLoop.*Contract|TestNormalizeLoopContract' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet -p 1 ./...
bash -n scripts/perf/loop-contract-write.sh
git diff --check
```
