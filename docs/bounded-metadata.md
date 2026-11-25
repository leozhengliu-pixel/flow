# Bounded metadata

`FLOW_BOUNDED_METADATA` keeps a bounded amount of workspace metadata in
memory on each API replica instead of loading a whole workspace per request.
It defaults to on. This page covers admission, sizing and operator controls.

Anything marked **to be confirmed** is not determined by the code and needs a
decision or a test by the operator or the maintainers before you rely on it.

## What it does

With the flag on, each replica:

- keeps workspace policy data (teams, team settings, workspace settings) of
  every workspace in a resident, paged form. Its evictable row and decoded
  image caches and concurrent work each use a share of `FLOW_METADATA_MEMORY_BYTES`;
- installs one logical source per workspace, charged 128 KiB each against a
  descriptor budget (`FLOW_METADATA_SOURCE_BYTES`). All generations of a
  workspace share the charge. Sources are spooled to temporary files
  under `FLOW_METADATA_SOURCE_DISK_BYTES`. Every workspace is
  resident from startup on, so these budgets grow with the number of
  workspaces (see [Tuning](#tuning));
- reads records through the record tables and journal rather than copying the
  whole workspace;
- sorts every HTTP route into **HOT** (proven to touch only bounded data) or
  **COLD** (still needs a whole-workspace view). HOT routes run normally.
  COLD routes run in a COLD scope with configurable concurrency per replica.

The route table is generated into
`api/cmd/server/scale_p21_route_temperature.go`. A route missing from the
table runs COLD.

## Related settings

| Variable | Needed for the flag | Notes |
| --- | --- | --- |
| `FLOW_BOUNDED_METADATA` | - | Defaults to `on` on SQLite, MySQL, and PostgreSQL when hydration and journaling are on and shadow modes are off. |
| `FLOW_RECORD_HYDRATION` | must be on | Defaults to `on` on SQLite, MySQL, and PostgreSQL. Bounded record scopes keep unloaded values. |
| `FLOW_RECORD_JOURNAL` | must be on | Defaults to `on` on SQLite, MySQL, and PostgreSQL. The explicit modes are `off`, `shadow` and `on`. |
| `FLOW_RECORD_PEER_SYNC` | must **not** be `shadow` | `off` (default) and `on` are accepted. |
| `FLOW_METADATA_CATALOG` | complete catalog | Unset repairs the record journal and completes missing catalog checkpoints before bounded admission. |
| `FLOW_SYNC_V2` | optional | Defaults to `on` on SQLite, MySQL, and PostgreSQL. It needs a ready record journal. |
| `FLOW_RECORD_SPOOL_MAX_BYTES` | optional | Process-wide byte budget for temporary files used by record hydration. Default `268435456` (256 MiB). `0` streams directly. A negative or invalid value falls back to the default. The load rig sets the default explicitly. |

`FLOW_METADATA_DIR` appears only in tests; it is not an operator setting.

## Activation

| Setting | State | Effect |
| --- | --- | --- |
| `FLOW_RECORD_PEER_SYNC` | `off` (default) | Uses Redis workspace versions and event envelopes for legacy reload and publication. Unrecognized values also select `off`. |
| | `shadow` | Uses the SQL journal for freshness and peer replay while still sending legacy Redis envelopes. For eligible pages it compares a candidate journal apply with the compatibility snapshot and keeps the compatibility result. `Peer journal shadow differs` reports a mismatch. Pages that need a reload fall back without a comparison. |
| | `on` | Installs a covered SQL journal page before publishing replayed events. Reloads on gaps or unsupported pages. Sends a compact Redis head hint when a durable event has a journal cursor. |
| `FLOW_METADATA_CATALOG` | unset (default) | Completes missing checkpoints at startup when bounded metadata is enabled. |
| | `on` | Runs the startup preparation even with bounded reads off. |
| | `off` | Skips automatic preparation. Catalog reads and journal writes still use the persisted rows and checkpoints. |
| | `shadow` | Repairs journal coverage, builds bounded summary batches and revisits completed collections for changes. Tests compare catalog summaries, ordering and filters to canonical reads. The worker itself checks backfill progress and reports skips. |

Peer sync needs a locally ready journal for either active mode and falls back
to `off` when the journal is unavailable. Coordination must be configured to
start the poller. Bounded startup requires journal mode `on` and rejects
peer `shadow` because the comparison needs full workspace snapshots.
`TestScaleP11*` covers mode guards, journal apply and fallback, missed or
reordered Redis events, envelope compatibility and permission changes.

Catalog **complete** is persisted coverage. Startup reuses completed
checkpoints. Bounded admission checks canonical layout, phase-2
checkpoints and journal writer coverage. The shadow worker can reset a
completed checkpoint while revisiting it, so stop it before bounded startup.
`TestScaleP05*`, `TestBoundedRollout*`, `TestMetadataPrepareCommandReadiesBoundedStart`,
`TestScaleP21ActivationGuard*` and `TestScaleP21CatalogFirstRecordThenColdWrite`
cover the catalog, preparation and activation guards.

See the [activation runbook](bounded-metadata-activation.md) for the full
fleet cutover and rollback.

## Prerequisites

1. Record hydration and the record journal are on:
   `FLOW_RECORD_HYDRATION=on` and `FLOW_RECORD_JOURNAL=on`.
2. `FLOW_RECORD_PEER_SYNC` is `off` or `on`, never `shadow`. With
   `FLOW_RECORD_PEER_SYNC=on` the journal must also be ready on that replica,
   or peer sync logs `Peer sync disabled: FLOW_RECORD_PEER_SYNC=on requires an
   enabled, ready record journal` and stays off.
3. The metadata catalog is complete for every workspace (see below).
4. Every consumer of whole-workspace data is either migrated to HOT or
   reviewed as COLD. This is a property of the build, not of your setup; a
   build that still has unreviewed consumers refuses the flag (see
   [Startup refusals](#startup-refusals)).

Enabling the record journal on a database that had it `off` marks each
existing workspace as needing journal repair. Startup, the catalog shadow
worker, and `server metadata-prepare` run that repair in bounded, resumable
steps. Bounded admission checks the resulting coverage. Each repaired
workspace publishes one workspace invalidation to its clients.

### Preparing a database: `server metadata-prepare`

`server metadata-prepare` runs the journal repair over every workspace that
needs it, then the catalog over every collection, once and at full speed, and
exits. Give it the same configuration as the server (environment or
`--config`). The command needs the journal in `on` or `shadow` mode. All
supported databases can use the unset journal default. The command opens the database with bounded metadata
and the catalog worker disabled.
It uses the same bounded, checkpointed transactions as the worker, so it is
safe to run while flag-off replicas serve the database, it can be interrupted
(`Ctrl-C`, `SIGTERM`) and it can be run again; a second run repeats the
catalog scan and finds nothing to repair.

It writes progress to stderr and a JSON report to stdout:

| Field | Meaning |
| --- | --- |
| `journal.workspaces`, `journal.repaired` | Workspaces that needed the journal repair, and those finished. |
| `journal.incomplete` | Workspaces not finished, with the error. |
| `journal.discarded` | Workspace keys with no workspace behind them whose leftover journal keys were removed (see below). |
| `catalog.collections`, `catalog.completed` | Collections scanned, and those whose catalog is complete. |
| `catalog.normalized` | Canonical rows stored as SQLite text, rewritten as blobs (see below). |
| `catalog.skipped` | Collections that could not be cataloged: `workspace`, `field` and the `error`, which names the record. |
| `journalSeconds`, `catalogSeconds` | Wall time of each step. |
| `uncoveredWorkspaces`, `incompleteCollections`, `ready` | What a bounded start would still refuse, from the SQL checks below. `ready` is true when both counts are 0. |
| `uncovered` | The first 20 uncovered workspace keys: `workspace`, `workspaceExists`, `activationWalk` (the walk after the journal was enabled is unfinished) and `pending`, the keys old writers changed by `field` (`@authz` for memberships and permissions, `@core` for the workspace root), `source` table and count. Also printed on stderr. |

Exit status 0 means ready, 1 that something is still incomplete (see
`journal.incomplete`, `uncovered` and `catalog.skipped`), 2 an error (the
message is on stderr; the progress made so far is kept).

Writers outside the app (an older binary, a bulk copy of tables with the
journal triggers installed) can leave journal keys under a workspace key no
workspace owns: a workspace an older binary deleted, or the empty key, under
which memberships copied in before their `workspace_states` rows are
journaled. Nothing can publish those keys, so the repair removes them and
counts the key in `journal.discarded`; a workspace whose root is copied in
later journals its own root and is repaired as a whole. Copy
`workspace_states` before the membership tables to avoid them altogether.

The command needs no particular collation. On MySQL the canonical
`workspace_metadata_records` key columns use the database's default
collation (`utf8mb4_0900_ai_ci` on 8.4, which sorts `teams` before
`teamSettings`) and the catalog and journal tables byte order; the catalog
pass pages each table in its own order and never compares keys across them.
Do not change the collation of these columns to make prepare finish.

For scale: on a SQLite database with 2520 small workspaces (25,200
collections), on a busy development machine, the journal repair took 30
seconds and the catalog 44 seconds (49 seconds for a second run, which
rescans everything). The worker's previous pace, 25 ms per step, managed
about 9 collections per second there, some 45 minutes per pass.

### Completing the metadata catalog

Run `server metadata-prepare` (above), or, on the flag-off replicas, set
`FLOW_METADATA_CATALOG=shadow` (with the journal on) and leave them running.
Each replica then starts one background worker. Each pass of the worker
first runs the journal repair, then scans every collection of every workspace
and records a checkpoint per collection. Collections that still need the
catalog are scanned back to back, pausing a quarter of each step's time
between steps; collections already complete are rescanned at a gentle pace
(25 ms between steps) to catch changes made by older writers. After a pass
the worker logs `metadata catalog pass finished` (with `collections`,
`complete`, `skipped`, `normalized` and `duration`), waits 30 seconds and
starts the next pass.

A collection the worker cannot catalog does not stop it. It logs
`metadata catalog: collection skipped` with the workspace, the field and the
error (which names the record, for example `metadata agentSessions/<id>:
invalid canonical JSON`), leaves that collection incomplete and goes on with
the next one; every later pass tries it again. Fix or remove the record, and
the next pass completes the collection. A bounded start refuses the
workspace while the collection is incomplete.

Older SQLite writers stored some canonical rows as text instead of blobs.
The worker and `server metadata-prepare` rewrite those rows as blobs with the
same bytes (the record and its version do not change; clients see one
update of it) and log `metadata catalog: canonical rows stored as text
rewritten as blobs`. PostgreSQL and MySQL store bytes only and need nothing.

The catalog is complete for a workspace when every collection that holds
records has a finished checkpoint and no rows are waiting for repair. To
check it in SQL, these must all return no rows (taken from the check a bounded
store runs when it admits a workspace):

```sql
-- collections whose checkpoint is missing or unfinished
SELECT r.workspace_key, r.field
FROM workspace_metadata_records r
LEFT JOIN metadata_catalog_collections p
  ON p.workspace_key = r.workspace_key AND p.field = r.field
WHERE p.field IS NULL OR p.phase <> 2
GROUP BY r.workspace_key, r.field;

-- keys the journal still marks for repair
SELECT DISTINCT workspace_key FROM record_journal_dirty;
```

The `GROUP BY` form above is valid on PostgreSQL, MySQL and SQLite. The
same checks, with the journal coverage (`record_journal_coverage` and
`record_journal_dirty` must be empty too), give the `ready` field of
`server metadata-prepare`, and a worker pass that ends with `skipped=0`
after the repair pass found nothing left is the signal in the logs. A
flag-on replica applies the full check itself, per workspace, and refuses a
workspace that fails it.

## Startup refusals

`OpenDatabase` checks the flag before opening the database. A failure aborts
startup with `bounded metadata coverage is incomplete` followed by one of:

| Message after the prefix | Meaning | Fix |
| --- | --- | --- |
| `N registered full-workspace consumers (owners); keep FLOW_BOUNDED_METADATA off` | This build still has HOT consumers that are not migrated. | Not an operator fix. Keep the flag off or use a build that has them migrated. |
| a coverage review error | The consumer inventory in this build is inconsistent. | Report it; keep the flag off. |
| a collaboration-engine runtime error | The pinned collaboration engine cannot be admitted in this process. | Report it with the error text; keep the flag off. |
| `record hydration and the record journal must both be on` | `FLOW_RECORD_HYDRATION` is not on, or `FLOW_RECORD_JOURNAL` is not `on`. | Set both and let the journal repair finish. |
| `peer sync and catalog shadow modes require bounded metadata off` | A shadow mode conflicts with explicitly enabled bounded metadata. | Disable shadow mode or set `FLOW_BOUNDED_METADATA=off`. |

Once the database is open, a workspace is admitted only if its data is
covered. These errors use the same `bounded metadata coverage is incomplete`
prefix:

| Message | Meaning |
| --- | --- |
| `record journal is not authoritative` | The journal is not ready on this replica, or the mode is not `on`. |
| `record journal authority: ...` (`record journal writer coverage is incomplete`) | The journal's writer coverage for the workspace is incomplete: the journal was enabled on existing data, or old replicas wrote to it. Run `server metadata-prepare` or the catalog worker, which run the journal repair. |
| `canonical records, catalog or old-writer repair is incomplete for "<workspace>"` | The catalog is not complete or repair is pending. Run `server metadata-prepare` (or the catalog step) again and fix the records it reports as skipped. |
| `canonical field "<name>" is outside the collection manifest` | The workspace holds a collection this build does not know. |
| `unsplit field "<name>"` | A large legacy field has not been split into records. |

A start also refuses when every workspace cannot be resident within the
budgets. These messages start with `metadata process work budget exceeded`:

| Message | Fix |
| --- | --- |
| `N workspaces need X MiB of installed workspace sources (128 KiB each plus 16 MiB for reloads), over FLOW_METADATA_SOURCE_BYTES (Y MiB); set FLOW_METADATA_SOURCE_BYTES to at least Z` | Checked before any workspace is read. Set the variable to at least the bytes named. |
| `the resident policy of K of N workspaces takes X MiB, over FLOW_METADATA_POLICY_BYTES (Y MiB); all N need about Z MiB, so set FLOW_METADATA_POLICY_BYTES to at least B` | The estimate extrapolates from the workspaces read so far and adds a quarter. Set the variable as suggested and start again. |
| `recover logical workspace "<key>": metadata process work budget exceeded: installed workspace sources use all of FLOW_METADATA_SOURCE_BYTES ...` | Sources outgrew the budget during recovery; raise `FLOW_METADATA_SOURCE_BYTES`. |

Automatic startup falls back to legacy reads for the replica if preparation
or bounded admission fails. Restart to retry admission. An explicit
`FLOW_BOUNDED_METADATA=on` with `FLOW_METADATA_CATALOG=off` requires a prepared
database and reports incomplete admission as a startup error. The journal
opens checkpoints for new collections when their first records are written.

## Recommended rollout

1. Back up the database (see [Backup boundary](operations.md#backup-boundary)).
2. Rehearse the upgrade against a restored copy with the deployment's
   memory and disk limits. Check [Tuning](#tuning) for budget overrides.
3. Keep `FLOW_RECORD_PEER_SYNC=off` and stop any catalog shadow workers.
   Use the same journal setting across replicas because capture triggers
   are shared. Coordinate upgrades of installations that explicitly
   disabled the journal to avoid old writers removing those triggers.
4. Start the new release with the default flags. Check the aggregate
   preparation and admission logs before sending traffic to each replica.
   See the [activation runbook](bounded-metadata-activation.md).

## What changes for users

- **HOT routes** behave as before and do not wait for any shared slot.
- **COLD routes** take one of the replica's COLD slots for each whole-workspace
  operation. Waiters queue in arrival order. COLD requests also use their own
  projection gate, so they cannot hold a slot a HOT request needs.
- When the queue is full, or a wait runs longer than the timeout, the API
  answers **503** with `Retry-After: 5` and the message "The workspace is busy
  loading for other people; retry in a moment". The same answer replaces a
  403 or 404 that a refused legacy-view wait would have caused. Clients that
  honour `Retry-After` (the web app and the SDKs) retry.
- Background work that needs a whole workspace (for example some workers) runs
  under the same COLD slot.
- Response bodies and URLs do not change.

## Tuning

Set `FLOW_METADATA_MEMORY_BYTES` to the per-replica memory target in bytes.
The automatic target is 70% of the smaller of the process's Linux cgroup
limit and detected physical RAM. It has a 1 GiB floor where possible and
uses at most 80% of detected memory when the floor cannot fit. A lower
`GOMEMLIMIT` reduces the automatic target. With bounded metadata enabled,
the runtime uses the lower of its existing soft memory limit and the target.

The target controls accounted caches and work. Process RSS also includes
live requests, runtime overhead, and native memory. Leave room above the
target in the deployment limit and verify RSS with your workload. Tier
overrides can spend additional headroom. Sizing is per replica.

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_COLD_QUEUE_LIMIT` | `64` | Waiters allowed for the COLD slot, for the legacy-view lane and for the COLD projection gate. Positive integer; anything else uses the default. |
| `FLOW_COLD_QUEUE_TIMEOUT` | `15s` | How long a COLD waiter waits before it is refused. Go duration such as `30s`. Positive; anything else uses the default. |
| `FLOW_COLD_PROJECTION_CONCURRENCY` | One per 512 MiB, from 2 to 8 | Concurrent COLD projections per replica. Positive integer. |
| `FLOW_COLD_SLOTS` | One per GiB, from 1 to 4 | Concurrent COLD facades. Each owns a private copy of the decoded workspace. COLD writes still serialize. Positive integer. |
| `FLOW_COLD_VIEW_LANES` | One per GiB, from 1 to 4 | Concurrent legacy views. Read-only projections clone their output from the shared image. Other views use private copies. Positive integer. |
| `FLOW_METADATA_READ_CONCURRENCY` | One per 512 MiB, from 4 to 16 | Concurrent issue bootstrap requests on a bounded replica. Readers of one generation share its immutable image and reserve their own projection work. Flag-off defaults to 4. Positive integer. |
| `FLOW_COLD_DECODE_CACHE_BYTES` | 35% of the target | Budget for decoded workspace images, including record memos and indexes. The cache evicts entire workspaces in least-recently-used order. An image too large for this budget falls back to request-local reads. `0` turns the cache off. |
| `FLOW_COLD_DECODE_CACHE_IDLE` | `5m` | A cached image unused this long is dropped. Go duration. |
| Encoded bootstrap cache | 1/16 of the target | Memory shared by retained JSON and the active cache producer. The issue bootstrap reuses encoded project, initiative and document fields for the same generation and visibility scope after computing live authorization and decorations. Fields that depend on file URL signing are encoded per request. |
| `FLOW_RECORD_SPOOL_MAX_BYTES` | `268435456` | See [Related settings](#related-settings). |
| `FLOW_SLOW_MUTATION_MS` | `500` | Threshold for the `slow workspace mutation` log line. `0` disables it. |
| `FLOW_METADATA_WORK_BYTES` | 25% of the target | Aggregate admission across concurrent metadata operations. The existing per-operation limits still apply. |
| `FLOW_METADATA_POLICY_BYTES` | 1/16 of the target | Resident policy of all workspaces together, in bytes. One workspace's policy may take at most 192 MiB. |
| `FLOW_METADATA_SOURCE_BYTES` | 1/32 of the target | Memory budget for installed workspace sources. Each workspace uses a 128 KiB descriptor charge. At startup, 16 MiB must remain free for request work and new workspaces. |
| `FLOW_METADATA_SOURCE_DISK_BYTES` | Same byte count as the target | Separate temporary-file space budget for workspace sources in the metadata spool directory. Check available disk space for your replica. |

Budget settings take a positive whole number of bytes, aside from the
documented cache-off value. Invalid values use the default. They are upper limits that the replica accounts
against, not allocations: an idle workspace's source uses well under its
128 KiB. Raise them with the number of workspaces a replica holds. For
example, 2520 workspaces need `FLOW_METADATA_SOURCE_BYTES` of at least
`347078656` (331 MiB); the resident policy of small workspaces takes about
60 KiB each, about 150 MiB for 2520, and more for workspaces with many teams
or large settings. A start that does not fit says what it needs (see
[Startup refusals](#startup-refusals)). A bounded start reads every
workspace once: with 2520 small workspaces on SQLite it took 83 seconds and
about 160 MiB of heap (150 MiB of accounted policy, 315 MiB of source
budget in use, 42 MiB of spool). Large workspaces take longer.

Every installed workspace keeps its policy and logical source. Decoded
images and the row cache (1/32 of the target) are evictable. The retry delay
is five seconds.

Raise `FLOW_COLD_QUEUE_LIMIT` or the timeout only if 503s appear under normal
load and the slot is not the bottleneck. **To be confirmed:** sizing guidance
per workspace size; use the load rig to measure it.

## Monitoring

`bounded metadata memory` logs the per-replica target and its source, the
cache and work budgets, and the effective Go runtime limit at startup.

| Log line (warning) | Meaning |
| --- | --- |
| `bounded metadata: COLD slot wait timed out` | A COLD request waited the full timeout. Attributes: `timeout`, `waiting`, `cold_scope_route` (the route), `cold_work_or_scope`. Users see 503. |
| `bounded metadata: legacy view wait refused` | A whole-workspace read was refused (queue full or timeout). Attributes: `accessor`, `reason`, `timeout`, `waiting`. Users see 503 with `Retry-After`. |
| `slow workspace mutation` | A write or its workspace lock hold exceeded `FLOW_SLOW_MUTATION_MS`. Includes per-stage timings. |
| `record journal deferred to bounded repair` | A very large transaction skipped journal sidecars; the catalog is unavailable for those keys until repair runs. |
| `metadata catalog: collection skipped` | The catalog worker (or `server metadata-prepare`) could not catalog a collection; the error names the record. The collection stays incomplete; later passes retry it. |
| `metadata catalog pass finished` | `INFO` after a worker pass, `WARN` when `skipped` is not 0. Attributes: `collections`, `complete`, `skipped`, `normalized`, `duration`. |
| `metadata catalog pass failed` | A pass could not list collections; the worker tries again 30 seconds later. |
| `record journal repair: workspace skipped` | The journal repair of a workspace failed or did not finish in this pass; the next pass continues it. |
| `record journal repair pass finished` | `INFO` after a worker pass that repaired workspaces: `workspaces`, `repaired`, `incomplete`, `duration`. |
| `metadata catalog: canonical rows stored as text rewritten as blobs` | SQLite rows from older writers were normalised. |
| `metadata catalog shadow worker stopped` | The catalog worker ended without being cancelled. |
| `Peer sync disabled: FLOW_RECORD_PEER_SYNC=... requires an enabled, ready record journal` | Peer sync fell back to off. |

Repeated COLD slot or legacy-view refusals on a route mean that route is
doing whole-workspace work often. The route name in `cold_scope_route` is the
pattern from the route table. A route that runs COLD is listed there as
`cold: <first legacy consumer>`.

### Summary lines

With the flag on the server also writes two structured lines. There is no
metrics endpoint.

**`bounded metadata: catalog`**, once per workspace at startup: `workspace`,
`complete`, `collections`, `incompleteCollections`. It is `INFO` when the
catalog is complete and `WARN` (with a `hint`) when collections still need the
catalog backfill. It is a SQL count and decodes no workspace.

**`bounded metadata: cold path`** (`INFO`), once per interval and only when
something changed since the previous line:

| Field | Meaning |
| --- | --- |
| `interval` | The reporting period. |
| `slotWaits`, `slotWaitAvg` | COLD slot waits in the period and their average. |
| `slotWaitMax` | Longest slot wait since start. |
| `slotRefusals` | COLD slot refusals in the period. |
| `slotsHeld`, `slotWaiting` | COLD slots held and callers waiting now. |
| `viewRefusals` | Legacy-view lane refusals in the period. |
| `facadeDecodes`, `facadeDecodeAvg`, `facadeDecodeMax` | COLD facade decodes: count and average in the period, maximum since start. |
| `viewDecodes`, `viewDecodeAvg`, `viewDecodeMax`, `viewFailures` | Legacy-view decodes, the same way, plus failures in the period. |
| `lockWaits`, `lockWaitAvg`, `lockWaitMax` | Writers that waited on the store write lock. |
| `coldLockWaits`, `coldLockWaitAvg`, `coldLockWaitMax` | The same for COLD facade writers. |
| `coldWriteLockHolds` | COLD legacy writes that held the shared lock in the period. |

`FLOW_BOUNDED_METADATA_STATS_INTERVAL` sets the period (a Go duration such as
`30s`, or whole seconds). The default is 60 s; `0` turns the summary off.

## Rollback

1. Set `FLOW_BOUNDED_METADATA=off` on every replica and restart them.
2. Keep `FLOW_RECORD_HYDRATION=on` and `FLOW_RECORD_JOURNAL=on` unless you
   also mean to roll those back. Setting the journal to `off` removes the
   canonical row triggers; turning it back on needs the bounded repair again.

The flag changes how a replica holds and routes workspace data. It does not
change the table layout: both modes read and write the same canonical record
tables (`workspace_metadata_records`), record versions and journal. The
catalog rows and checkpoints the flag-on mode maintains stay in place and are
ignored by a flag-off replica (flag-off never runs the checkpoint opener).
`TestScaleP21RollbackRoundTrip` writes a broad set of changes flag-on, reopens
the database flag-off and compares every table and API response with a run
that stayed flag-off; it also checks the opposite direction.

Re-enabling bounded metadata repairs journal coverage and fills unfinished
catalog checkpoints before admitting bounded reads. Startup serves
the legacy path if preparation or admission fails. A restart retries
preparation. With `FLOW_BOUNDED_METADATA=on` and automatic preparation
explicitly disabled, startup requires the catalog and journal to be ready.
After bounded admission, a workspace coverage failure affects only that
workspace's requests.

## Pre-production check: the load rig

`scripts/loadrig/loadrig.sh` seeds a realistic workspace and drives a mixed
workload against the same data with the flag off and on, then writes a
comparison report.

```bash
scripts/loadrig/loadrig.sh build
SCALE=10 scripts/loadrig/loadrig.sh seed pg2     # or sqlite
scripts/loadrig/loadrig.sh pair pg2              # off, drive, stop, on, drive, stop, compare
```

- Topologies include `sqlite` (one replica), `pg2` (two replicas on
  PostgreSQL) and `mysql2` (two replicas on MySQL 8.4). Both two-replica
  topologies use Redis coordination.
- `seed` normally runs with hydration, journal and `FLOW_SYNC_V2` enabled and
  completes the catalog. `LOADRIG_UPGRADE_SEED=1` creates a seed with the
  previous defaults and leaves its journal and catalog unprepared.
- Each `start` uses a fresh copy. `start <topology> default` inherits the code
  defaults from the server. Clear feature overrides before starting it.
- The explicit `on` and `off` profiles use `FLOW_RECORD_HYDRATION=on`,
  `FLOW_RECORD_JOURNAL=on` and `FLOW_SYNC_V2=on`; `on` adds
  `FLOW_BOUNDED_METADATA=on`. Use `EXTRA_ENV="FLOW_METADATA_CATALOG=shadow"` to
  add the catalog worker to the flag-off replicas, as a real rollout would.
- Settings include `SCALE`, `VUS`, `DURATION`, `WARMUP`, `THINK`, `MIX`,
  `MODEL`, `GOMAXPROCS_REPLICA` and `GOMEMLIMIT_REPLICA`; the script header
  lists them all. Reports go to `$LOADRIG_HOME/reports`.
- `LOADRIG_MYSQL_MEMORY` sets the MySQL container limit for `mysql2`
  (default `1536m`). Use the same limit for both flag states and verify the
  database stays up throughout the measurement.
- Set `LOADRIG_CONTAINER_PREFIX` alongside `LOADRIG_HOME` when the rig needs
  dedicated container names and a separate reports directory.

Compare latency, error rate, 503 count and memory between the two runs on
data of your size before you enable the flag in production. **To be
confirmed:** acceptance thresholds; the rig reports numbers but defines no
pass/fail limits.

## To be confirmed

- Observation period and exit criteria between the single-replica and fleet-wide steps.
- A flag-on to flag-off rollback rehearsed on a restored production copy.
- Sizing guidance for `FLOW_COLD_QUEUE_LIMIT`, `FLOW_COLD_QUEUE_TIMEOUT` and `FLOW_COLD_PROJECTION_CONCURRENCY`.
- Load-rig pass/fail thresholds.
