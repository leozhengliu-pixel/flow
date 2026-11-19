# Bounded metadata

`FLOW_BOUNDED_METADATA` keeps a bounded amount of workspace metadata in
memory on each API replica instead of loading a whole workspace per request.
It is off by default. This page covers what it needs, how to turn it on
safely and how to turn it off again.

Anything marked **to be confirmed** is not determined by the code and needs a
decision or a test by the operator or the maintainers before you rely on it.

## What it does

With the flag on, each replica:

- keeps workspace policy data (teams, team settings, workspace settings) in a
  resident, paged form with a 192 MiB policy budget, plus a 64 MiB evictable
  body cache, and admits internal metadata work against a 256 MiB budget;
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
| `FLOW_METADATA_CATALOG` | `shadow` on the replicas that build the catalog | Starts a background worker that builds the metadata catalog. Not needed once the catalog is complete and journal maintenance keeps it current. |
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
repairs existing workspaces in bounded steps before consumers trust the
journal. Do this, and let it finish, before the catalog step.

### Completing the metadata catalog

On the flag-off replicas, set `FLOW_METADATA_CATALOG=shadow` (with the
journal on) and leave them running. Each replica starts one background worker
that scans every collection of every workspace and records a checkpoint per
collection. After a full pass it waits 30 seconds and scans again, to catch
changes made by older writers. It stops with the warning
`metadata catalog shadow worker stopped` and an error if the scan fails.

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

The `GROUP BY` form above is valid on PostgreSQL, MySQL and SQLite. There is
no log line or metric that says "catalog complete"; the SQL check is the
signal (**to be confirmed**: whether a dedicated indicator should be added).
A flag-on replica applies the full check itself, per workspace, and refuses a
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
| `record journal authority: ...` | The journal's writer coverage for the workspace is incomplete, for example after old replicas wrote to it. |
| `canonical records, catalog or old-writer repair is incomplete for "<workspace>"` | The catalog is not complete or repair is pending. Run the catalog step again. |
| `canonical field "<name>" is outside the collection manifest` | The workspace holds a collection this build does not know. |
| `unsplit field "<name>"` | A large legacy field has not been split into records. |

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
   `on`. Restart them one at a time and wait for the journal repair to finish.
3. Add `FLOW_METADATA_CATALOG=shadow` and restart. Wait until the SQL checks
   above return no rows for every workspace. Run
   `scripts/loadrig/loadrig.sh` against a copy of production-sized data first
   (see [Pre-production check](#pre-production-check-the-load-rig)).
4. Stop all replicas and start **one** with `FLOW_BOUNDED_METADATA=on`, so
   that no flag-off replica runs next to it. Check that it starts and watch the logs under
   [Monitoring](#monitoring). **To be confirmed:** the observation period
   before moving on; the code does not set one.
5. Set the flag on, and restart, every remaining replica. Remove
   `FLOW_METADATA_CATALOG=shadow` when you no longer need the worker.
   **To be confirmed:** whether the worker should stay on after activation.

A single-replica deployment follows steps 1 to 4 only.

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

The 192 MiB policy, 64 MiB cache and 256 MiB work budgets are constants in
this build, not settings. The retry delay (5 seconds) is also fixed.

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
| `metadata catalog shadow worker stopped` | The catalog worker failed. |
| `Peer sync disabled: FLOW_RECORD_PEER_SYNC=... requires an enabled, ready record journal` | Peer sync fell back to off. |

Repeated COLD slot or legacy-view refusals on a route mean that route is
doing whole-workspace work often. The route name in `cold_scope_route` is the
pattern from the route table. A route that runs COLD is listed there as
`cold: <first legacy consumer>`.

Counters for COLD slot waits and refusals are collected only for the load rig
(`FLOW_LOADRIG_METRICS_ADDR`). **To be confirmed:** whether to expose
them to operators.

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
The load rig starts flag-off replicas on a copy of the same seed. The code
does not contain an explicit "rollback" test of a database written flag-on and
then served flag-off (**to be confirmed**; test it on a restored copy first).

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
- Whether any admission failure aborts startup or only fails requests for that workspace.
- A tested flag-on to flag-off rollback on a restored production copy.
- Sizing guidance for `FLOW_COLD_QUEUE_LIMIT`, `FLOW_COLD_QUEUE_TIMEOUT` and `FLOW_COLD_PROJECTION_CONCURRENCY`.
- An operator-visible "catalog complete" indicator and public counters for COLD waits.
- Load-rig pass/fail thresholds.
