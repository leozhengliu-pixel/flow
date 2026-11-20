# Bounded metadata: performance design

Status: in progress. Operator settings stay in
[bounded-metadata.md](bounded-metadata.md); this note is for maintainers.

## Goal

`FLOW_BOUNDED_METADATA` exists to keep a replica's memory under a ceiling. The
ceiling is a constraint, not the objective: with the flag on, every route
class should be faster than with it off (p50, p95, p99 and throughput, at 1x
and 10x load, on MySQL, PostgreSQL and SQLite), while peak RSS stays bounded
and no higher than flag-off. Memory below the ceiling is there to be spent on
speed.

## Where the time goes today

Flag-off keeps every workspace decoded in memory and deep-copies it for each
request. Flag-on keeps each workspace's installed generation encoded in a
per-process spool file (typed frames, `scale_p21_logical_*.go`), read with
`pread` through a 96 KiB block cache per workspace, and decodes what a request
needs on every request.

Measured on the load-rig workspace (5,000 issues, 2,321 projects, 90
documents, 40 members; spool about 127 MB):

| Route class | Off | On | Where flag-on spends it |
| --- | --- | --- | --- |
| Issue bootstrap, one request at a time, SQLite | admin 0.36-0.48 s, member 0.15 s | 6-10 s | 74% of CPU in `pread` (frame headers of large collections miss the 96 KiB cache one by one), then reflect decode into a body projection, re-encoding the projected value into a scratch spool (`bind`) and streaming JSON from that spool. About 1 GB allocated per admin request. |
| Project edit (`PATCH /api/projects/{id}`), MySQL, 2 replicas | p50 2234 ms | p50 4206 ms | Three passes over the stored projects collection per edit (find the record, find the realtime entity, rebuild the collection frame for the new generation), 40-45 ms each when idle, all under the write lock and the cross-replica lock (held about 590 ms). |
| Same, SQLite | p50 69 ms | p50 952 ms | As above; nothing hides the passes behind database latency. |
| Document write | | lock held about 650 ms | Same pattern; peer replicas reload under the lock on every event. |
| HOT point reads, SQLite under writes | document.detail p95 59 ms | 1444 ms | Header scans of large collections through the block cache and per-request decode, queued behind writers. |
| HOT reads, 2 replicas | p95 1.3-2.9 s | p95 0.9-1.0 s | Flag-off loses here: every peer event makes it reload the whole workspace. |

What already worked: the decoded-image cache for COLD facades
(`scale_p21_cold_decode_cache.go`): spending up to 256 MiB on decoded images
cut MySQL COLD writes from about 1.1 s to 0.3 s p50.

The root causes are the same everywhere:

1. Installed frames are read from a file through a cache sized for minimum
   memory, so a walk over a large collection is one system call per record.
2. Decoded values are never shared: every request decodes its own copy.
3. Writers find records by scanning and produce the next generation by
   rewriting whole collection frames.
4. Most of that work happens while the workspace write lock (and, with
   several replicas, the cross-replica lock) is held.

## Target architecture

Four tiers per workspace, each optional and each falling back to the one
below it, so the memory ceiling is enforced by dropping upper tiers, never by
refusing work:

1. **SQL rows and the record journal** stay the truth.
2. **Installed generation (spool)**, as today: immutable typed frames,
   structurally shared between generations (`Fork` writes only changed
   frames), pinned by readers with `Retain`. It remains the fallback for any
   workspace whose upper tiers are not resident.
   - Its reads stay as they are (block cache and `pread`): a memory mapping
     of the spool was measured and rejected (see Decisions). The spool is
     the fallback tier; the fast paths stop walking it.
3. **Resident decoded generation**: for the installed generation, one decoded
   image (`domain.Bootstrap`) shared by every reader of that generation.
   - Immutable: readers take a reference, never a deep copy; nobody writes
     into it. A reader that must change values works on its own copy (as
     flag-off copies its resident workspace).
   - Built lazily from the installed generation, incrementally: a field or
     record whose frame has the same origin as in the previous image reuses
     its decoded value (structural sharing), so after a write only the
     changed records are decoded again. Concurrent readers of a new
     generation build it once (per-workspace build lock).
   - Exact by construction: an image is used only when every frame it was
     decoded from is the frame of the generation the reader pinned
     (`coldImageCurrent`), so a reader holding a generation never sees a
     later write, and an image never outlives a patch of its frames.
   - Bounded: least recently used workspaces are dropped beyond the byte
     budget, idle images expire, and a workspace whose image does not fit is
     served from tier 2 as today. A dropped image stays alive only while a
     reader that took it runs.
   - Indexes (planned): per collection, record ID to position, built on first
     use and carried to the next generation when the collection's frame is
     unchanged; rebuilt for a changed collection in one pass over decoded
     values (no I/O). Writers, realtime entity lookup and aggregate lookup use
     them instead of scans.
4. **Encoded caches** (planned): the JSON of large viewer-independent records
   (projects, documents, initiatives) memoized per record by frame origin, so
   a bootstrap concatenates cached bytes for the records the viewer may see
   and re-encodes only records changed since; per-request parts (signed URLs,
   viewer flags) are applied on top as today.

Writes (planned, after the read tiers):

- Inputs come from the resident image (index lookups) instead of spool
  scans; the callback's validation, the record's JSON and the realtime
  payload are prepared before the lock where the result does not depend on
  state the lock protects, and checked against the stamp under it.
- The next generation replaces single records (`Fork` of the changed record
  frames plus a collection frame rebuilt from memory).
- Peer replicas apply the journal delta to a candidate generation outside the
  lock and swap it in; the lock covers only the stamp check and the install.

## Decisions and deviations

- **The decoded generation is derived from the installed spool generation,
  not maintained by writers.** Writers keep producing the spool generation
  exactly as now (their output is what the differential tests pin); the
  decoded image follows by incremental decode keyed on frame origin. This
  gives structural sharing and exactness without a second write path. If
  measurement shows the lazy rebuild after a write is significant, writers
  can hand their decoded records to the next image later.
- **The image cache of the COLD path is the resident generation.** It
  already holds the right image under a budget; HOT readers now take it by
  reference while COLD facades keep taking copies (legacy code mutates its
  graph). One cache, one budget, one eviction policy.
- **Budget accounting.** The image budget is still counted in spool bytes
  (`FLOW_COLD_DECODE_CACHE_BYTES`, 256 MiB). Decoded images are larger than
  their spool; the budget moves to measured decoded bytes before the image
  serves more routes, so the ceiling is a real heap ceiling.
- **Per-request copies stay for now.** The paged bootstrap projection still
  copies the view it projects (the same copy flag-off makes). Removing it
  needs every in-place mutation on that path audited; that comes with the
  encoded caches, route by route, each with an immutability test.
- **No memory mapping of the spool.** Mapping every settled spool file of at
  least 256 KiB (read-only, shared; a seqlock against patches; unmapped by
  the storage's cleanup) removed the `pread`s, but on the rig (mysql2,
  default mix) it raised peak RSS from 752/618 MiB to 1281/1521 MiB per
  replica, above flag-off's 877/787 MiB: every generation's storages and the
  COLD and reload rewrites stay mapped until collected, and their touched
  pages count as resident. Throughput and p95 did not improve over the
  resident image alone (12.5 against 15.5 req/s, p95 2120 against 1470 ms,
  within the noise of a shared machine). The attempt is not merged. Making
  the spool cheaper is the wrong lever: hot paths should stop walking it
  (resident image and indexes), leaving it for cold workspaces.

## Increments

Each increment is one commit with tests, measured on the load rig (MySQL
first, then PostgreSQL and SQLite) against flag-off.

1. Bootstrap from the resident decoded generation (done): `GET
   /api/issue-records/bootstrap` takes the shared image of the generation it
   pinned instead of the body projection, runs the unchanged paged projection
   on it and encodes the result with the flag-off encoder.
2. Resident spool bytes through memory mappings: measured and rejected (see
   Decisions).
3. Collection indexes in the resident image; writers and realtime entity
   lookup use them.
4. Single-record generations and shorter lock holds for project and document
   writes; peer delta application outside the lock.
5. Encoded per-record caches for the bootstrap and list routes.
6. Budget in decoded bytes; HOT point reads (project, document, issue
   detail) from the image.

## Correctness

Every increment keeps flag-on responses identical to flag-off: the P21
differential tests of the touched routes run on SQLite, MySQL and
PostgreSQL. New tests cover snapshot isolation (a reader holding a generation
sees no later write), concurrent readers and writers under `-race`, eviction
and budget accounting, index exactness after every write kind, peer delta
application and the memory budget.

## Measurements

Load rig, mysql2 (two replicas, MySQL 8.4, Redis), scale 1, 10 VUs, default
mix, 180 s per run, one seed for all runs, on a shared 10-core machine (load
average 10-20 during the runs; treat differences under about 20% as noise).

Increment 1 (resident bootstrap), flag off → on:

| class | req/s | p50 ms | p95 ms | p99 ms | 5xx |
| --- | --- | --- | --- | --- | --- |
| overall | 9.09 → 15.51 | 117 → 38 | 3044 → 1470 | 6218 → 3611 | 133 → 28 |
| hot-read | 5.61 → 9.95 | 47 → 21 | 3006 → 569 | 3040 → 1628 | 63 → 1 |
| hot-write | 1.19 → 1.90 | 386 → 253 | 5158 → 1595 | 11014 → 2382 | 13 → 0 |
| page-load (bootstrap) | 0.07 → 0.09 | 365 → 438 | 3022 → 1391 | 3022 → 1391 | 2 → 0 |
| cold-write | 0.35 → 0.58 | 2663 → 1068 | 6218 → 4826 | 12587 → 6048 | 15 → 2 |
| search | 0.39 → 0.74 | 1334 → 1233 | 13077 → 15007 | 17607 → 15024 | 6 → 8 |

Peak RSS per replica 877/787 MiB off, 752/618 MiB on. The flag-on bootstrap
before this increment (same rig, earlier build): p50 4351 ms, p95 7326 ms.
SQLite, one request at a time: admin bootstrap 6-10 s → 0.36-0.85 s
(flag-off 0.36-0.48 s), member 5.5-10 s → 0.12-0.48 s (flag-off 0.15 s),
same bytes.

Still slower or not yet faster with the flag on: search (p95), project
edits (p50 1734 ms on against 1999 ms off, p95 4841 against 6218: better,
but far from flag-off SQLite's 69 ms), the bootstrap's p50 (it still copies
the view once and encodes 57 MB per admin request, as flag-off does).
