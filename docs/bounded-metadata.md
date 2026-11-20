# Bounded metadata

`FLOW_BOUNDED_METADATA` keeps a bounded amount of workspace metadata in
memory on each API replica instead of loading a whole workspace per request.
It is off by default. This page covers what it needs, how to turn it on
safely and how to turn it off again.

Anything marked **to be confirmed** is not determined by the code and needs a
decision or a test by the operator or the maintainers before you rely on it.

## What it does

With the flag on, each replica:

- keeps workspace policy data (teams, team settings, workspace settings) of
  every workspace in a resident, paged form with a 192 MiB policy budget
  (`FLOW_METADATA_POLICY_BYTES`), plus a 64 MiB evictable body cache, and
  admits internal metadata work against a 256 MiB budget;
- installs one logical source per workspace, charged 128 KiB each against a
  64 MiB budget (`FLOW_METADATA_SOURCE_BYTES`; every later generation of a
  workspace's source shares that charge) and spooled to temporary files
  within 1 GiB (`FLOW_METADATA_SOURCE_DISK_BYTES`). Every workspace is
  resident from startup on, so these budgets grow with the number of
  workspaces (see [Tuning](#tuning));
- reads records through the record tables and journal rather than copying the
  whole workspace;
- sorts every HTTP route into **HOT** (proven to touch only bounded data) or
  **COLD** (still needs a whole-workspace view). HOT routes run normally.
  COLD routes run in a COLD scope and share one COLD slot per replica.

The route table is generated into
`api/cmd/server/scale_p21_route_temperature.go`. A route missing from the
table runs COLD.

## Related settings

| Variable | Needed for the flag | Notes |
| --- | --- | --- |
| `FLOW_BOUNDED_METADATA` | - | `1`, `true` or `on` enables it. Anything else is off. |
| `FLOW_RECORD_HYDRATION` | must be `1`, `true` or `on` | Bounded record scopes that keep unloaded values. |
| `FLOW_RECORD_JOURNAL` | must be `on` | `off` (default), `shadow` and `on`. `shadow` is not enough. |
| `FLOW_RECORD_PEER_SYNC` | must **not** be `shadow` | `off` (default) and `on` are accepted. |
| `FLOW_METADATA_CATALOG` | `shadow` on the replicas that build the catalog | Starts a background worker that repairs the record journal and builds the metadata catalog. Not needed once the catalog is complete and journal maintenance keeps it current. `server metadata-prepare` does the same work once, in the foreground. |
| `FLOW_SYNC_V2` | recommended, not checked at startup | `on`, `1` or `true`. Only takes effect with `FLOW_RECORD_JOURNAL=on`. The load rig runs both states with it on. |
| `FLOW_RECORD_SPOOL_MAX_BYTES` | optional | Process-wide byte budget for temporary files used by record hydration. Default `268435456` (256 MiB). `0` streams directly. A negative or invalid value falls back to the default. The load rig sets the default explicitly. |

`FLOW_METADATA_DIR` appears only in tests; it is not an operator setting.

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

Enabling `FLOW_RECORD_JOURNAL=shadow` or `on` on a database that had it `off`
marks every existing workspace as lacking journal writer coverage. A bounded
start refuses such workspaces (`record journal writer coverage is
incomplete`) until the journal repair has walked them. The repair runs in
bounded, resumable steps in the catalog worker (`FLOW_METADATA_CATALOG=shadow`)
and in `server metadata-prepare`; nothing else runs it. Each repaired
workspace publishes one workspace invalidation to its clients.

### Preparing a database: `server metadata-prepare`

`server metadata-prepare` runs the journal repair over every workspace that
needs it, then the catalog over every collection, once and at full speed, and
exits. Give it the same configuration as the server (environment or
`--config`); it needs `FLOW_RECORD_JOURNAL=on`. It always opens the database
flag-off, whatever `FLOW_BOUNDED_METADATA` says, and starts no catalog worker.
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
| `complete-snapshot peer shadow requires bounded metadata off` | `FLOW_RECORD_PEER_SYNC=shadow`. | Set it to `off` or `on`. |

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

A workspace that fails admission at startup stops the replica starting; one
that fails later (for example the first record of a new collection written
before its checkpoint exists) is handled by the journal, which opens the
checkpoint itself when every other record of the collection is already
cataloged. **To be confirmed:** whether every admission failure aborts
startup or only fails requests for that workspace.

## Recommended rollout

Mixed flag-on and flag-off replicas are **not supported**. Replicas share one
database and one change stream; the flag changes how a replica holds and
publishes workspace state. Roll the flag out to all replicas, and roll back
all replicas, within one maintenance window.

1. Back up the database (see [Backup boundary](operations.md#backup-boundary)).
2. On all replicas, set `FLOW_RECORD_HYDRATION=on` and
   `FLOW_RECORD_JOURNAL=on`, and leave `FLOW_RECORD_PEER_SYNC` at `off` or
   `on`. Restart them one at a time.
3. Run `server metadata-prepare` with the replicas' configuration until it
   exits 0, or add `FLOW_METADATA_CATALOG=shadow` and restart, and wait until
   the SQL checks above return no rows for every workspace. Fix the records
   named in `catalog.skipped` (or the `collection skipped` log lines). Run
   `scripts/loadrig/loadrig.sh` against a copy of production-sized data first
   (see [Pre-production check](#pre-production-check-the-load-rig)).
4. Size the budgets for the number of workspaces (see [Tuning](#tuning)).
   Starting one replica flag-on against a copy of the database is the
   simplest check: a start that does not fit names the setting and the size.
5. Stop all replicas and start **one** with `FLOW_BOUNDED_METADATA=on`, so
   that no flag-off replica runs next to it. Check that it starts and watch the logs under
   [Monitoring](#monitoring). **To be confirmed:** the observation period
   before moving on; the code does not set one.
6. Set the flag on, and restart, every remaining replica. Remove
   `FLOW_METADATA_CATALOG=shadow` when you no longer need the worker.
   **To be confirmed:** whether the worker should stay on after activation.

A single-replica deployment follows steps 1 to 5 only.

## What changes for users

- **HOT routes** behave as before and do not wait for any shared slot.
- **COLD routes** take the replica's single COLD slot for each whole-workspace
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

| Variable | Default | Description |
| --- | --- | --- |
| `FLOW_COLD_QUEUE_LIMIT` | `64` | Waiters allowed for the COLD slot, for the legacy-view lane and for the COLD projection gate. Positive integer; anything else uses the default. |
| `FLOW_COLD_QUEUE_TIMEOUT` | `15s` | How long a COLD waiter waits before it is refused. Go duration such as `30s`. Positive; anything else uses the default. |
| `FLOW_COLD_PROJECTION_CONCURRENCY` | `2` | Concurrent COLD projections per replica. Positive integer. |
| `FLOW_RECORD_SPOOL_MAX_BYTES` | `268435456` | See [Related settings](#related-settings). |
| `FLOW_SLOW_MUTATION_MS` | `500` | Threshold for the `slow workspace mutation` log line. `0` disables it. |
| `FLOW_METADATA_POLICY_BYTES` | `201326592` (192 MiB) | Resident policy of all workspaces together, in bytes. One workspace's policy may take at most 192 MiB whatever the setting. |
| `FLOW_METADATA_SOURCE_BYTES` | `67108864` (64 MiB) | Memory for installed workspace sources, in bytes: 128 KiB per workspace (its read cache and write buffer, shared by every generation of its source) plus 16 MiB that a start keeps free for request work and new workspaces. The default fits 384 workspaces at a start; workspaces created afterwards take from what the start left free, at least those 16 MiB. |
| `FLOW_METADATA_SOURCE_DISK_BYTES` | `1073741824` (1 GiB) | Temporary-file space for workspace sources, in bytes, in the process's metadata spool directory. |

The three budget settings take a positive whole number of bytes; anything
else uses the default. They are upper limits that the replica accounts
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

Every workspace stays resident: the budgets bound memory per replica, they
do not evict workspaces. Installing workspaces on first use with eviction is
not supported in this build.

The 64 MiB cache and 256 MiB work budgets are constants in this build, not
settings. The retry delay (5 seconds) is also fixed.

Raise `FLOW_COLD_QUEUE_LIMIT` or the timeout only if 503s appear under normal
load and the slot is not the bottleneck. **To be confirmed:** sizing guidance
per workspace size; use the load rig to measure it.

## Monitoring

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

1. Set `FLOW_BOUNDED_METADATA` to `off` (or remove it) on **all** replicas and
   restart them.
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

Turning the flag back **on** after a flag-off period needs the catalog to
cover what was written meanwhile. Writes into collections the catalog never
covered (for example the first loop, document or workflow written flag-off)
make the next flag-on start refuse with "bounded metadata coverage is
incomplete" until the catalog backfill has run again
(`FLOW_METADATA_CATALOG=shadow` on the flag-off replicas).

A flag-on start that finds any workspace without complete coverage stops the
whole process (`recover resident workspace "<key>": ...`); no workspace is
served. After startup, a workspace whose coverage check fails only fails its
own requests.

## Pre-production check: the load rig

`scripts/loadrig/loadrig.sh` seeds a realistic workspace and drives a mixed
workload against the same data with the flag off and on, then writes a
comparison report.

```bash
scripts/loadrig/loadrig.sh build
SCALE=10 scripts/loadrig/loadrig.sh seed pg2     # or sqlite
scripts/loadrig/loadrig.sh pair pg2              # off, drive, stop, on, drive, stop, compare
```

- Topologies: `sqlite` is one replica; `pg2` is two replicas on PostgreSQL
  with Redis coordination.
- `seed` runs the seeding test with hydration, journal and `FLOW_SYNC_V2` on,
  and keeps the result as the seed copy. Each `start` uses a fresh copy.
- Both flag states run with `FLOW_RECORD_HYDRATION=on`,
  `FLOW_RECORD_JOURNAL=on` and `FLOW_SYNC_V2=on`; `on` adds
  `FLOW_BOUNDED_METADATA=on`. Use `EXTRA_ENV="FLOW_METADATA_CATALOG=shadow"` to
  add the catalog worker to the flag-off replicas, as a real rollout would.
- The rig completes the metadata catalog over every collection of every
  workspace before the flag-on start (`CompleteLoadRigMetadataCatalog`), so a
  flag-on start tests the same state a finished catalog step gives.
- Settings include `SCALE`, `VUS`, `DURATION`, `WARMUP`, `THINK`, `MIX`,
  `MODEL`, `GOMAXPROCS_REPLICA` and `GOMEMLIMIT_REPLICA`; the script header
  lists them all. Reports go to `$LOADRIG_HOME/reports`.

Compare latency, error rate, 503 count and memory between the two runs on
data of your size before you enable the flag in production. **To be
confirmed:** acceptance thresholds; the rig reports numbers but defines no
pass/fail limits.

## To be confirmed

- Observation period and exit criteria between the single-replica and fleet-wide steps.
- Whether the catalog worker stays on after activation.
- A flag-on to flag-off rollback rehearsed on a restored production copy.
- Sizing guidance for `FLOW_COLD_QUEUE_LIMIT`, `FLOW_COLD_QUEUE_TIMEOUT` and `FLOW_COLD_PROJECTION_CONCURRENCY`.
- Load-rig pass/fail thresholds.
