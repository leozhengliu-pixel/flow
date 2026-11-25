# Bounded metadata activation

SQLite, MySQL, and PostgreSQL enable bounded metadata, record hydration, the
record journal, and Sync V2 by default. Startup repairs journal coverage and
completes the metadata catalog. Rehearse the upgrade and rollback on a restored
copy. Record route status and latency, realtime lag and resyncs, database
pool waits, background work, and RSS per replica. Keep a database backup
from before the upgrade.

## Prerequisites

- Let startup finish the metadata catalog (25) and record journal (29)
  schema migrations. The capture triggers record changes from replicas
  that are still running the previous release during an upgrade.
- For multiple replicas, use one writable MySQL or PostgreSQL database and
  shared Redis coordination. Check subscription delivery and distributed
  locks on every replica. Use the same `FLOW_REDIS_PREFIX` for the fleet and
  an environment-specific prefix. SQLite is for single-replica deployments.
  See [Redis coordination](configuration.md#redis-coordination-and-cluster-mode).
- The bundled MySQL configurations allow the migration role to create the
  journal triggers. On external MySQL 8.4 with binary logging enabled, have
  the DBA verify `TRIGGER` privileges and `log_bin_trust_function_creators`
  before the change. Keep the existing
  collation and binlog durability settings. Check `max_allowed_packet`
  against the largest metadata statements in the rehearsal. Allow room for
  redo, binlogs and temporary data.
- On PostgreSQL, grant the migration role the DDL permissions for the
  application schema and journal trigger functions. Install `pg_trgm`
  ahead of startup where the role cannot create it. Keep production
  synchronous commit settings. On either engine, budget connections for
  the sum of `FLOW_DATABASE_MAX_OPEN_CONNS` across replicas plus maintenance
  connections. Test with the production pool settings.
- The automatic memory target uses available RAM and honors a lower
  `GOMEMLIMIT`. Provision temporary space for the installed metadata
  sources. Rehearse startup on the copy with the same memory and disk
  limits. [Memory tuning](bounded-metadata.md#tuning)

## Upgrade

Remove old overrides for the settings you want to inherit. Explicit `on`
and `off` values take precedence on every database.

Startup uses the database's existing startup lock to serialize journal
installation and catalog preparation across replicas.
Repair and backfill persist checkpoints as they progress. Later starts
reuse completed checkpoints. If preparation or admission fails, automatic
startup uses legacy reads and checks again on restart. The optional
`server metadata-prepare` command reports remaining gaps and resumes repair.
Run it before starting bounded replicas, or while the fleet is on legacy
reads, because the command also revisits completed collections.

On external MySQL, a default startup that lacks permission to install
journal triggers logs `MySQL record journal trigger permission denied. Using legacy metadata and sync`
once. The replica serves legacy metadata and realtime, and Sync V2 returns
404 so clients can fall back. This covers a missing `TRIGGER` grant and
MySQL error 1419 when binary logging requires additional authority.
Startup retries trigger installation and completes journal and catalog
repair after the DBA authorizes trigger creation. An explicit
`FLOW_RECORD_JOURNAL=on` still requires trigger installation to succeed.

During a rolling upgrade, leave remaining replicas running until their
replacements are ready. The journal triggers capture their writes, and
bounded replicas repair pending journal and catalog entries when reloading.
Starting an older binary with its journal disabled can remove the shared
capture triggers. Use the rollback steps below before reintroducing an older
binary.

`FLOW_RECORD_PEER_SYNC` defaults to `off` and keeps the existing Redis event
envelopes during rolling upgrades. Enable compact peer sync only in a
separate fleet-wide switch. Stop catalog shadow workers before starting
bounded replicas because a shadow revisit resets completed checkpoints.
The sync manifest and indexed reads remain opt-in, as in the accepted load
profile. The browser local-store allowlist stays opt-in for the staged
rollout. The shared Redis result tier remains opt-in because it was slower
in mixed-load measurements. Shadow modes require explicit settings.

## Verify

Look for `bounded metadata memory` and the aggregate `bounded metadata:
catalog` log with `complete=true` and `incompleteCollections=0`.
Treat `Peer sync disabled`, `Peer apply`, `Redis event subscriber`,
`bounded metadata coverage is incomplete`, and
`record journal deferred to bounded repair` as investigation signals.
Use structured log counts by message and status. Exclude workspace and
record identifiers, keys and bodies from exported reports.

Before and after load, check that each SQL count is zero. A bounded replica
also checks catalog rows and record versions when it admits a workspace.

```sql
SELECT COUNT(*) FROM (
  SELECT r.workspace_key, r.field
  FROM (
    SELECT workspace_key, field FROM workspace_metadata_records
    UNION
    SELECT workspace_key, field FROM metadata_catalog_collections
  ) r
  LEFT JOIN metadata_catalog_collections p
    ON p.workspace_key = r.workspace_key AND p.field = r.field
  WHERE p.field IS NULL OR p.phase <> 2
) incomplete;

SELECT COUNT(*) FROM record_journal_coverage;
SELECT COUNT(*) FROM record_journal_dirty;
```

Use the same credentials against both replica addresses. Write a project or
document on the first, read it and its new value on the second, then reverse
the roles. Check both an authorized reader and a reader outside the resource's
scope. Subscribe to `/api/realtime/events` and check that peer state is visible
when the event arrives. Reconnect and verify catch-up or
`workspace.resync_required` followed by a fresh snapshot. Compare
`/api/sync/changes` pages for the same cursor and viewer across replicas,
including a Pulse change. Keep realtime and journal cursors separate.

Run the same workload against fresh copies of one prepared seed for each
flag set. Compare 5xx and transport errors separately, plus p95/p99,
realtime and sync success, and peak and late-window RSS per replica. Watch
`bounded metadata: cold path` (`slotRefusals`, `viewRefusals`, `slotWaiting`)
and `slow workspace mutation`. The COLD summary runs every 60 seconds by
default and uses `FLOW_BOUNDED_METADATA_STATS_INTERVAL` for its period. Track
HTTP 503s by cause and count `workspace.resync_required` events per connection.
Check that enabled workflows, loops, Pulse scheduling and deliveries
advance their expected work without duplicates.
Watch `Delivery scheduler` and `Loop run reconcile` errors. Hold the rollout
for new 5xx, persistent lag, mismatched responses or visibility, catalog
gaps, growing RSS, or stalled background work.

For the load rig, start the baseline with
`EXTRA_ENV="FLOW_RECORD_PEER_SYNC=off FLOW_METADATA_CATALOG=off"` and the
candidate with `EXTRA_ENV="FLOW_RECORD_PEER_SYNC=on FLOW_METADATA_CATALOG=off"`.
Use `start <topology> on` for both, the same memory setting for both, and real
Redis. Both variants start with the catalog completed by the seed step.
Catalog readiness is already required for the baseline to serve bounded
metadata. See
[Load rig options](bounded-metadata.md#pre-production-check-the-load-rig).

Use `LOADRIG_UPGRADE_SEED=1` when seeding to exercise unprepared data, and
`start <topology> default` to inherit the server defaults. Run
`bash scripts/ci/go-test-bounded-activation.sh` through the local heavy
command lock. It uses SQLite by default. Set `FLOW_TEST_DATABASE_DRIVER` and
`FLOW_TEST_DATABASE_URL` to a disposable MySQL or PostgreSQL database to
exercise that dialect. The profile completes catalog gaps in route
differential fixtures and starts a separate pair of bounded stores to
verify compact peer replay with production admission.

## Opt Out Or Roll Back

Drain before changing a fleet-wide setting and verify startup and cursor
recovery on the restarted replica before restoring traffic.

| Setting | Rollback |
| --- | --- |
| `FLOW_RECORD_PEER_SYNC` | Switch every replica to `off` together. Keep the journal and prepared catalog. Reconnect realtime clients and verify that writes reach peers through the legacy reload path. |
| `FLOW_BOUNDED_METADATA` | Set `off` on every replica and restart. Leave canonical records, catalog and journal tables in place. Unset the flag on any database to return to automatic preparation. |
| `FLOW_METADATA_CATALOG` | Set `off` to skip automatic preparation and keep existing catalog rows. `shadow` runs background preparation when bounded metadata is off. |
| `FLOW_SYNC_V2` | Switch to `off` with the journal still on. Verify the legacy change feed and have clients refresh their snapshot before resuming with legacy cursors. |
| `FLOW_SYNC_MANIFEST`, `FLOW_INDEXED_READS` | Set both to `off` to disable those endpoints and use compatibility reads. |
| `FLOW_RECORD_JOURNAL` | Switch bounded metadata, peer sync and sync v2 off first. Disable the catalog worker. Journal `off` removes the capture triggers. Re-enabling it requires another coverage repair. |
| `FLOW_RECORD_HYDRATION` | Switch bounded metadata off first, then revert hydration and verify legacy reads and writes. |

## Rehearsal evidence

The activation profile passed on SQLite (385 seconds), MySQL 8.4.13
(876 seconds) and PostgreSQL 17.12 (772 seconds). The SW530 sweep exercised
891 requests on each and retained the existing trash-window result of 501
on the unbounded path and 200 on the bounded path. The peer activation test
verified that an installed read sees the edit when the peer publishes it.
Legacy realtime fixtures with a fixed SQLite setup keep using SQLite in
the external database runs.

The rig used two replicas, Redis 7.4.12, a 3 GiB metadata budget per
replica, rotating reads, and the fake model. Each run used a fresh seed
from the same 5,000-issue fixture recipe. The MySQL 10-VU baseline came
from the earlier rehearsal. Current flags include bounded metadata and
the prepared catalog with peer sync `off`. Full flags switch peer sync
to `on`. Each 10-VU window ran for 180 seconds and each 100-VU window ran
for 600 seconds.

| Topology | VUs | Flags | Requests | Req/s | HTTP p95/p99 (ms) | HTTP 5xx | Timeouts | Realtime drops or failures / resyncs | RSS peak (end) per replica, MiB |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| mysql2 | 10 | Current | 2,916 | 16.20 | 1,671/3,165 | 17 | 0 | 0/81 | 788/795 (764/537) |
| mysql2 | 10 | Full | 4,049 | 22.50 | 530/1,490 | 22 | 0 | 0/364 | 842/761 (791/595) |
| mysql2 | 100 | Current | 31,771 | 52.97 | 5,520/15,025 | 4,392 | 47 | 2,439/3,564 | 1,661/1,458 (1,303/1,068) |
| mysql2 | 100 | Full | 33,666 | 56.15 | 7,325/17,131 | 4,163 | 40 | 3,144/4,680 | 1,625/1,222 (457/1,061) |
| pg2 | 10 | Current | 2,454 | 13.63 | 2,005/4,663 | 27 | 0 | 0/62 | 726/708 (502/88) |
| pg2 | 10 | Full | 2,515 | 13.97 | 1,891/3,095 | 45 | 0 | 0/431 | 765/692 (180/590) |
| pg2 | 100 | Current | 21,678 | 36.14 | 8,445/15,348 | 5,647 | 12 | 1,801/1,958 | 1,231/1,202 (928/626) |
| pg2 | 100 | Full | 26,543 | 44.25 | 8,965/17,262 | 4,989 | 48 | 1,896/5,197 | 1,282/1,232 (912/1,082) |

Transport errors were zero in all eight runs. The PostgreSQL 100-VU full
run included three HTTP 500s, two from issue comments and one from an
issue edit. All other 5xx responses across the matrix were retryable
503s from workspace freshness, the projection gate, or sync changes.
Full flags increased realtime resyncs in every comparison. At 100 VUs,
full flags yielded p95 realtime lag of 17.9 seconds on MySQL and 26.6
seconds on PostgreSQL, compared with 17.4 and 26.3 seconds on current
flags.

After load, both replicas matched on 18 sampled record reads, two replay
pages and both heads. Each observed a write from the other replica. The
seven new runs also passed the private document visibility check and
verified that both writes were visible when their realtime events arrived.
The catalog, journal coverage and dirty counts stayed at zero in every
run. The first MySQL full-flag Pulse probe expected an upsert. The corrected
probe checks for the `projectUpdates` invalidation and a matching Pulse read.
It passed in five runs and failed in the PostgreSQL 100-VU full run. The
specific failed step remains unresolved.

Workflow runs and webhook attempts advanced throughout the matrix. Each
10-VU run added five completed loops. At 100 VUs, MySQL completed zero
loops with current flags and one with full flags. PostgreSQL completed
three and two respectively. In each 100-VU run, the remaining starts out
of 15 returned 503. The PostgreSQL 100-VU full run logged two delivery
scheduler errors while webhook attempts continued.

The PostgreSQL diagnostic run reproduced a comment POST and an issue PATCH
returning 500 with `metadata process work budget exceeded`. The first
PostgreSQL recheck caught the same refusal on an issue create. Bounded
create, edit and discussion writers now wait for room after releasing
their transaction and write lock. They can repeat admission before the
callback. Sustained contention returns 503 with `Retry-After`.
`TestScaleP21ActivationIssueWriteBudget` checks that a refusal commits
no event and that a retry after the budget frees commits exactly one.

The diagnostic run passed Pulse writes and reads in both directions. The
activation differential now verifies that both replicas return the same
feed item, the corresponding legacy event and the `projectUpdates`
invalidation for that event's journal transaction.

Peer replay uses SQL to fill ordering gaps and advance past silent
credential events. It waits up to two poll intervals for Redis client
correlation. A missing hint after that wait signals recovery. The durable
change feed retains the event and later correlated events can still reach
live streams.
`TestScaleP21ActivationRealtimeDifferential` checks delivery order,
deduplication, client correlation and durable replay after a lost hint.
The issue-write, Pulse and realtime differentials passed on SQLite,
MySQL 8.4.13 and PostgreSQL 17.12 in off, current and full profiles.

| Topology | VUs | Flags | Requests | HTTP p95/p99 (ms) | HTTP 500 / all 5xx | Timeouts | Stream failures / resyncs | Peak RSS per replica, MiB |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| mysql2 | 10 | Current | 4,027 | 584/1,393 | 0/36 | 0 | 0/68 | 902/791 |
| mysql2 | 10 | Full | 4,474 | 373/947 | 0/19 | 0 | 0/47 | 836/832 |
| pg2 | 100 | Current | 24,571 | 7,028/14,775 | 0/5,333 | 6 | 1,773/1,946 | 1,229/1,404 |
| pg2 | 100 | Full, before create retry | 28,415 | 9,175/17,986 | 1/4,200 | 93 | 1,823/136 | 1,273/1,304 |
| pg2 | 100 | Full, after create retry | 30,452 | 7,988/15,804 | 0/4,702 | 40 | 1,644/141 | 1,387/1,343 |

Both replicas matched all 18 sampled state reads in each recheck. The
catalog had zero incomplete collections and the journal had zero coverage
gaps and dirty records. The catalog had one completed checkpoint with
summary omissions in each run. Pulse passed in both directions in all five
rechecks. The PostgreSQL probes also matched both legacy events and both
`projectUpdates` invalidations. The original Pulse probe retained only
aggregate counts, so its failing step remains unknown.

The final PostgreSQL run had zero HTTP 500s. Peak replica RSS stayed
below 1.4 GiB per replica. Workflow runs rose from 2 to 10 and webhook
attempts rose from 204 to 7,994. The delivery scheduler logged five errors.
The current-flags run logged 970 `late-sequence` resync causes. Both
full-flags rechecks recorded zero `late-sequence` causes. Subscriber
overflow remained in both modes.

**Hold activation.** On PostgreSQL at 100 VUs, the final full-flags run had
40 request timeouts and 46.2 seconds p95 realtime lag. The current-flags run
had six timeouts and 23.4 seconds p95 realtime lag. The workload also still
hits workspace freshness, projection admission and loop-start 503s.

## Limits

Peer SQL polling runs every 350 ms. Oversized, unsupported or missing
journal pages can require a workspace reload. A bounded replica also
reloads when its installed logical generation is behind the journal page.
The reload can use the existing incremental update path. Redis pub/sub has
no durable history. Lost client correlation can lead to a resync even when the
database state converges. Bounded COLD admission and freshness waits can
still return retryable 503s under load. The memory target controls accounted
work and caches, and measured RSS also includes runtime overhead. The rig
uses MySQL without binlogs and PostgreSQL with `synchronous_commit=off`.
Rehearse with production durability settings to verify throughput and
latency before cutover.
