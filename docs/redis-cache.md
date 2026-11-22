# Shared Redis cache tier

API replicas behind a load balancer can share results through the Redis
instance they already use for coordination. SQL remains authoritative for
data versions and authorization.

## Goal

When a replica computes a result from a particular SQL version and access
scope, another replica can reuse it. This reduces repeated database reads
and projection work across replicas. The resident metadata and write-path
optimizations have their own performance notes.
[Bounded metadata performance](bounded-metadata-performance.md)

## What Redis does today

Existing Redis uses in `api/internal/coordination` share the
`FLOW_REDIS_PREFIX` namespace.

| Use | Keys | Notes |
| --- | --- | --- |
| Workspace write lock | `lock:{slot}` | `SET NX` lease with refresh; held for the length of a write. |
| Peer change signals | pub/sub channel | Wakes other replicas; SQL stays authoritative. |
| Workspace version (flag off) | `c:{slot}:statever` | `INCR` per committed change; a replica behind it reloads. |
| Presence | `presence:*` | Sorted set and hash with TTL. |
| Rate limits | `rate:*`, `bucket:*` | Fixed window and token bucket scripts. |
| Issue records | `c:{slot}:issue:<id>` | Served only while the row's version still matches (one SQL check). |
| Issue list pages and group counts | `c:{slot}:iq:*`, `c:{slot}:ig:*` | Keyed by the issue cache revision and a generation counter. |
| Project directory pages | `c:{slot}:pq:*` | Keyed by the metadata cache revision. |
| Metadata records (flag off) | `c:{slot}:mr:*`, `mfill`, `mgen` | Hashes per collection, filled on reload. |

Measured on the load rig (mysql2, flag on, default mix, 120 s): about 6,000
Redis commands per run, 22 MiB in use, hit ratio 70% on GETs. Flag off writes
140,000 `HSET`s per run (the metadata record cache refilled on every reload).

## Where cross-replica repetition costs time

Method: the load rig (`scripts/loadrig/loadrig.sh`, topology mysql2: two
replicas, MySQL 8.4, `redis:7-alpine`) driven with the default mix (10 VUs,
300 ms think time) for 120 s, with a 20 s runtime trace of replica 0. The
trace's network-blocking profile (`go tool trace -pprof=net`) attributes every
wait on a database or Redis round trip to its call stack, so it shows which
request paths spend the shared database's time.

Baseline, flag on, 20 s window, replica 0 (network wait summed over request
goroutines; 40.1 s in total):

| Path | Wait | Share | Depends on |
| --- | ---: | ---: | --- |
| Metadata search candidates (`SearchMetadata`) | 31.8 s | 79% | Collection state, query text and filters |
| Realtime event projection (`eventSourceForAuthority`, `readRealtimeIssueSource`) | 1.5 s | 4% | journal head, authorization epoch, credential scope |
| Request authorization (`authorizeWorkspaceRequest`, `AuthenticateSession`, `IssueQueryAccess`) | 2.0 s | 5% | session row, authorization epoch, principal |
| Sync change pages (`collectSyncChanges`) | 0.5 s | 1% | journal head, authorization epoch, credential scope |
| Issue list pages (`QueryIssueRecords`) | 0.4 s | 1% | already cached in Redis |

Search was the clear outlier: a metadata search runs one full-text page query
per resource kind (projects, documents, initiatives, members, customers,
releases, views) against `metadata_search_documents` and
`workspace_metadata_records`, 1.1 s p50 and 15 s p95 (the handler's deadline)
for a workspace with 2,321 projects. Every replica repeats it for every
keystroke-level query, and at 30 VUs with 50 ms think time the concurrent
full-text queries pushed the MySQL container (1.5 GiB) out of memory.

Route classes for the same runs (flag off, then flag on):

| class | req/s | p50 ms | p95 ms | p99 ms |
| --- | --- | --- | --- | --- |
| overall | 10.4 / 16.4 | 91 / 43 | 3012 / 1205 | 4900 / 3350 |
| hot-read | 6.66 / 10.16 | 47 / 24 | 2715 / 492 | 3021 / 1049 |
| search | 0.47 / 0.79 | 1424 / 1119 | 15017 / 15006 | 15034 / 15015 |
| sync | 0.56 / 0.91 | 213 / 119 | 2257 / 1046 | 3322 / 1457 |

## Artifacts and versions

| Kind | Value | Key material |
| --- | --- | --- |
| `search-meta` | Ordered metadata search results for one collection after its visibility checks | Journal generation, workspace ID, collection version, index repair revision, authorization epoch, dialect and index cap, query and effective viewer policy, credential ID when present |
| `viewer` | An active workspace member's role and team memberships | Journal generation, workspace ID, authorization epoch and viewer ID |
| `sync-page` | Projected issue journal page | Workspace ID, journal head and page digest, source metadata and issue revisions, authorization epoch, principal, credential scope and permission fingerprint, cursor and limit |

**Metadata search.** Journal capture advances a record's version in the same
transaction as the canonical change and retains deletion versions. The sum
of these versions moves forward for each collection. Views and initiatives
also depend on the project version. Index-only repairs advance a separate
revision with the repaired rows. The first implementation used a single
workspace metadata revision and hit 7% of the time in a mixed run. Collection
versions avoid retiring search results when an unrelated collection changes.
Regular and semantic search use the same tier. Recent items, releases,
application keys, borrowed snapshots and stores without a ready journal use
live reads.

**Viewer access.** `IssueQueryAccess` reads the user profile and live epoch
together. It shares only workspace role and team memberships. The journal
triggers advance the epoch on membership and policy changes. Session and API
key authentication, expiry and revocation checks run through their existing
live paths.

**Sync.** The server checks the committed page, cursor floor and journal
coverage before looking in Redis. Cacheable pages contain only issue
upserts and any associated issue field history. Field history produces the
existing collection invalidation. Other invalidation fallbacks, including
transient projection budget failures, bypass storage. Metadata projections
that depend on an installed view and pages with deletions use live reads.
The server validates each caller's epoch, credential and source revisions
before delivering a cached page. This also covers callers that join a flight.

**Bootstrap.** The resident branch's `cdafdf64b` adds an in-process encoding
cache interface. Wiring a Redis tier awaits that interface on bounded
integration. The current image generation is process-local. A Redis key
needs canonical SQL version stamps for the field's live dependencies, plus
its field identity and visibility scope. Encodings with request-specific
signed URLs stay local to the response.

## Keys and admission

Each Redis key is a hash shard named `<prefix>:sc:{result-N}`, for `N` from
0 to 31. A field is `<kind>:<format and version>:<digest>`. Length-prefixed
inputs feed SHA-256 and the first 128 bits form the digest. Principal,
workspace and credential identifiers and query text stay out of key names.
Values contain scope-checked results or the viewer's own membership data.
Raw credentials and tokens stay in the authentication path.

Fills confirm their versions after computation. Batched MySQL fills confirm
the stamp in the same SQL snapshot as their candidates and rows. Committed
writes and revocations change key material as soon as a new request reads
the SQL authority. Old fields expire with their shard. Concurrent lookups
for one key share one flight per replica. A waiter can compute locally
when the leader's request ends or its result cannot be shared.

Each replica waits until it has seen three searches for the same workspace,
principal, credential and normalized terms within 15 seconds before reading
the SQL version stamp or looking in Redis. It keeps 1,024 hashed observations
with fixed storage. The Redis lookup still uses the full versioned key above.
The load rig counts searches that bypass this lookup as
`redisCacheSearchBypasses`.

| Setting | Default | Behavior |
| --- | --- | --- |
| `FLOW_REDIS_CACHE` | `off` | Set to `on` to enable the result tier when Redis coordination is configured |
| `FLOW_REDIS_CACHE_VIEWER` | `false` | Set to `true` to cache viewer memberships |
| `FLOW_REDIS_CACHE_SYNC_PAGE` | `false` | Set to `true` to cache eligible projected sync pages |
| `FLOW_REDIS_CACHE_TTL` | `5m` | Starts when a shard first fills. Later admissions keep the original expiration |
| `FLOW_REDIS_CACHE_TIMEOUT` | `25ms` | Deadline per Redis operation on a separate connection pool |
| `FLOW_REDIS_CACHE_MAX_ENTRY_BYTES` | `1048576` | Caps encoded entries. Values above 2 KiB use deflate and decoded values are capped at 16 MiB |
| `FLOW_REDIS_CACHE_MAX_BYTES` | `67108864` | Admission budget per namespace, divided evenly among the 32 shards |

A Lua script atomically accounts for field and encoded value bytes and a
128-byte entry allowance. When a shard fills, new entries in that shard
bypass storage until expiration or eviction. Eviction removes both entries
and accounting. Keep the settings consistent on all replicas. The budget
estimates cache storage and Redis also needs capacity for the allocator,
connections and coordination keys.

On a timeout, connection error or unreadable value, the replica computes
locally. Three consecutive Redis operation failures skip the result cache
for two seconds. A deployment without Redis or with the result tier disabled
uses the ordinary read paths. Traditional Redis coordination has its own
availability and eviction requirements.

Enable the result tier for deployments with measurable reuse of stable
metadata searches. The mixed workload mostly bypassed or missed the tier.
Viewer memberships and sync pages each require an additional opt-in.
Focused viewer and sync tests reached the client's pacing limit with live
reads, and the default mix rarely reuses an eligible sync page.

## Verification

The HTTP differential compares sessions and team-restricted API keys with
uncached references in both bounded flag states. It covers cold and warm
search and sync, peer writes, membership grants and revocations, key
revocation, and eviction. A store test checks that an index-only repair
retires a cached empty result. Coordination tests exercise compression,
expiration, admission across replicas, single-flight, unavailable or slow
Redis, and eviction on real Redis.

## Operations

Give `maxmemory` headroom for coordination. Eviction policies that remove
expiring keys can evict a coordination lock. Result shards are safe to evict.
The load rig reports aggregate and per-kind hits and misses, timeouts and
errors, breaker skips, single-flight waiters, size and budget rejections, and
bytes read and written per replica.

## Measurements

The load rig uses two replicas, real Redis, and a 1x fixture (5,000 issues).
Each configuration starts from a fresh copy of the same database seed.
The focused workload rotates reads for the same credential across replicas
and replays a fixed issue change cursor. The default mix includes writes,
realtime streams, and fake-model agent traffic. The cache-on and cache-off
runs use the same binary. Per-kind hit rates count Redis hits out of hits
plus misses, after warmup. Redis memory is `used_memory` at the end of each
run, and replica RSS is the sampled peak during the measured window.

### MySQL mixed workload

These tables describe the earlier tier with all three artifacts enabled.
See [Load loop](bounded-metadata-performance.md#load-loop) for the longer runs
and the revised defaults.

The 30-second runs used 10 VUs with 300 ms think time and five realtime
streams. Each cell reports req/s and p50/p95/p99 in ms. Legacy runs used
`FLOW_BOUNDED_METADATA=off` and `FLOW_REDIS_CACHE=off`. Both bounded columns
used `FLOW_BOUNDED_METADATA=on`.

| Class | Legacy | Bounded, cache off | Bounded, cache on |
| --- | ---: | ---: | ---: |
| Overall | 12.54, 43/2108/4217 | 21.37, 21/906/2073 | 19.51, 21/950/2282 |
| Hot read | 8.24, 25/1725/2145 | 13.17, 10/441/798 | 12.64, 14/401/800 |
| Search | 0.60, 995/15018/15018 | 0.97, 930/8859/15007 | 0.93, 949/9111/10129 |
| Sync | 0.63, 57/1442/1442 | 1.30, 97/614/1129 | 1.07, 72/1232/2025 |
| Hot write | 1.37, 238/3728/4259 | 2.97, 124/739/1110 | 2.13, 154/1160/2316 |
| Cold read | 0.67, 120/1214/1571 | 0.87, 61/635/664 | 0.93, 111/796/1492 |
| Cold write | 0.53, 1388/4331/4331 | 1.17, 415/2531/2537 | 0.87, 442/2282/2682 |
| API key | 0.43, 37/1754/1754 | 0.57, 17/474/474 | 0.73, 17/151/575 |
| Page load | N/A | 0.27, 173/980/980 | 0.13, 204/348/348 |
| Agent | 0.10, 971/2063/2063 | 0.13, 20/3480/3480 | 0.10, 1397/2405/2405 |

Redis used 69.03 / 13.93 / 15.31 MiB. Peak replica RSS was 707/720,
540/566, and 555/699 MiB, respectively. The runs had 3 / 5 / 1 HTTP 4xx,
1 / 6 / 0 HTTP 5xx, and zero transport errors. The cache-on run hit 511 of
722 viewer lookups (70.8%), missed all 163 search lookups, and saw no
eligible sync pages. Bounded with the cache on served 56% more requests per
second than legacy. Enabling the result tier within bounded mode coincided
with an 8.7% throughput decrease in this short mixed run.

### MySQL focused workloads

Each run used 10 VUs, 150 ms think time, 5 seconds of warmup, and a
12-second measurement window. Viewer runs used the issue groups route.
Search used one fixed query across six metadata collections. Sync replayed
one committed issue edit with field history. All six runs had zero HTTP
4xx, HTTP 5xx, and transport errors.

| Request | Cache off req/s, p50/p95/p99 ms | Cache on req/s, p50/p95/p99 ms | Result hits / lookups | Redis MiB off / on | Peak RSS MiB by replica, off / on |
| --- | ---: | ---: | ---: | ---: | ---: |
| Viewer via issue groups | 63.60, 5/7/9 | 64.70, 5/7/10 | 776/776 (100%) | 1.37 / 1.53 | 259/330 / 493/329 |
| Metadata search | 11.02, 763/876/993 | 64.29, 6/7/9 | 4,638/4,638 (100%) | 1.31 / 1.52 | 317/347 / 494/330 |
| Sync replay | 63.45, 6/9/11 | 63.62, 7/9/12 | 753/763 (98.7%) | 1.31 / 1.56 | 317/348 / 465/324 |

Repeated search served 5.8 times as many requests per second. Both viewer
and sync runs approached the pacing limit of 66.7 req/s even with the cache
off, and their tail latencies stayed within 1 ms. Replica 0 retained more
RSS during the cache-on runs. The reported RSS includes the entire API
process, and Redis memory includes coordination.

### PostgreSQL focused workloads

The PostgreSQL runs used the same focused inputs, pacing, warmup and
measurement window. All nine runs had zero HTTP 4xx, HTTP 5xx, and
transport errors. Latencies are p50/p95/p99 in ms.

| Request | Legacy req/s, ms | Bounded cache off req/s, ms | Bounded cache on req/s, ms | Result hits / lookups |
| --- | ---: | ---: | ---: | ---: |
| Viewer via issue groups | 58.94, 5/14/594 | 64.39, 5/7/16 | 63.45, 5/7/13 | 761/761 (100%) |
| Metadata search | 21.19, 253/1026/1791 | 20.96, 267/583/1513 | 64.03, 5/8/11 | 4,608/4,608 (100%) |
| Sync replay | 64.22, 6/10/30 | 62.97, 7/12/33 | 62.19, 7/13/23 | 726/746 (97.3%) |

Each memory cell shows Redis MiB followed by peak RSS MiB for replicas 0/1.

| Request | Legacy | Bounded, cache off | Bounded, cache on |
| --- | ---: | ---: | ---: |
| Viewer via issue groups | 60.81, 563/420 | 1.36, 323/243 | 1.56, 334/250 |
| Metadata search | 60.75, 548/442 | 1.31, 291/179 | 1.52, 339/267 |
| Sync replay | 60.77, 451/403 | 1.31, 300/238 | 1.54, 340/321 |

Repeated search served 3.1 times as many requests per second. Viewer and
sync throughput stayed close to the pacing limit with and without the tier.

### PostgreSQL mixed workload

Each configuration ran the default mix after the focused reads, with the
same 30-second window, VUs, pacing and stream count as the MySQL mixed
workload. Each cell reports req/s and p50/p95/p99 in ms.

| Class | Legacy | Bounded, cache off | Bounded, cache on |
| --- | ---: | ---: | ---: |
| Overall | 14.14, 47/1719/3097 | 22.21, 26/585/968 | 17.51, 31/1261/2075 |
| Hot read | 9.10, 28/1230/2351 | 14.21, 15/298/620 | 11.47, 20/316/742 |
| Search | 1.00, 600/8074/15005 | 1.03, 551/8813/9164 | 1.00, 1347/10693/11289 |
| Sync | 0.57, 36/2150/2150 | 1.13, 42/313/390 | 0.90, 93/782/784 |
| Hot write | 1.80, 167/1308/2053 | 2.77, 145/454/715 | 1.93, 208/1132/1422 |
| Cold read | 0.63, 126/1865/1865 | 1.03, 112/666/723 | 0.73, 126/622/1257 |
| Cold write | 0.47, 1374/3127/3127 | 1.03, 374/968/1249 | 0.67, 562/2139/2734 |
| API key | 0.33, 22/1719/1719 | 0.70, 18/32/189 | 0.67, 29/102/770 |
| Page load | 0.17, 251/2470/2470 | 0.20, 251/314/314 | 0.03, 612/612/612 |
| Agent | 0.10, 906/1912/1912 | 0.13, 20/2130/2130 | 0.13, 33/4121/4121 |

Redis used 70.05 / 16.17 / 14.18 MiB. Peak RSS was 709/690, 541/577, and
539/743 MiB. The runs had 8 / 7 / 3 HTTP 4xx, 1 / 2 / 0 HTTP 5xx, and zero
transport errors. The cache-on run hit 492 of 639 viewer lookups (77.0%)
and 6 of 180 search lookups (3.3%). It saw no eligible sync pages. Bounded
with the cache on served 24% more requests per second than legacy and
21% fewer than bounded with the cache off in this run.

The mixed runs on both databases show how much the search gain depends on
query reuse. Repeat-query runs reach the driver pacing limit. The mixed
runs saw few search hits and no sync hits, and throughput was lower with
the result tier enabled within bounded mode. The tables report individual
runs, including requests that returned errors.

### Earlier MySQL search build

The search-only build was tested for 30 seconds with 10 VUs, 300 ms think
time, five realtime streams, and queries from the fixture's word list.
With bounded metadata on, enabling Redis increased search throughput from
3.13 to 10.60 req/s. Search p50/p95/p99 moved from 2023/9244/15013 ms to
147/914/12975 ms, with a 74.1% cache hit rate. Redis used 1.43 / 1.72 MiB
and peak replica RSS was 309/404 / 304/439 MiB. The runs had 1 / 3 server
errors and zero transport errors.
