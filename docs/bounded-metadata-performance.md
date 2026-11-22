# Bounded metadata: performance design

Status: in progress. Operator settings stay in
[bounded-metadata.md](bounded-metadata.md); this note is for maintainers.

## Goal

`FLOW_BOUNDED_METADATA` exists to keep a replica's memory under a ceiling. The
ceiling is a constraint, not the objective: with the flag on, every route
class should be faster than with it off (p50, p95, p99 and throughput, at 1x
and 10x load, on MySQL, PostgreSQL and SQLite). RSS should stabilize below
the configured per-replica target. The bounded replica may retain more
memory than the flag-off replica within that target.

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
4. **Encoded bootstrap cache**: the process retains encoded project,
   document and initiative fields by collection generation and visibility
   scope. A collection that keeps its frame across writes keeps its ID.
   Each request resolves live authorization and checks that the finalized
   projection equals the value that was encoded. Fields that need file
   signing are encoded per request. The cache bounds retained encodings,
   their projection witnesses and one active builder to 1/16 of the memory
   target.

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
- **Budget accounting.** The image cache charges decoded containers, values,
  record memos and ordinal indexes. Its default allowance is 35% of
  `FLOW_METADATA_MEMORY_BYTES`. The process sizes work admission, policy
  and other caches from the same target. See the [operator settings](bounded-metadata.md#tuning).
- **The bootstrap still owns its projection.** Live authorization and
  decoration operate on a copy of the resident view. Cache hits skip JSON
  encoding and reuse bytes for the finalized field.
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

- **No larger spool block cache either.** A process-wide 128 MiB LRU of
  spool blocks behind each group's 96 KiB cache (SQLite, sequential edits)
  left document edits at p50 1491 ms (1370 ms without it) and project edits
  at 228 ms (278 ms), and raised RSS from 365 to 781 MiB. Profiles of the
  edits show why: 60% of the remaining `pread` time is large frame reads
  (record bodies read whole), not header walks; the writers spend their
  time on data volume, not on lookups.

### What document and project edits spend (flag on, SQLite, sequential)

A document title edit (p50 about 1.4 s against 0.4 s flag off) runs the
scoped image writer, all of it under the workspace lock. Its plan rewrites
exactly one row (the document; logged per field over 20 edits), yet the
edit streams that document's large body (content and revisions) through the
spool about six times:

| Share of CPU | Work |
| --- | --- |
| 28% | Legacy metadata persistence: rebasing the document's large row (a streamed comparison of before, stored and next) and writing it |
| 17% | Aggregate publication: encoding the document before and after for previous values and the realtime payload |
| 8% | Workspace-wide display refresh (`Transform`) over the spool |
| about 10% | Selections and forks finding records by ID |

So collection ID indexes, the first planned step, address about a tenth of
an edit. The larger wins: decode the edited record once (from the resident
image) and derive the comparison, the row and both payloads from that one
decoded pair, and do it before the lock, keeping under the lock only the
version check, the SQL write and the install.

### Document edits from the resident image (done)

`scale_p21_resident_document_write.go`. The edit pins the installed
generation and its shared image, runs the callback on inputs taken from
the image and derives everything else from the before and after values of
the records it changes: the rows (rebased onto the stored rows as the
scoped writer rebases them), the previous values, the realtime payload,
the next generation (only the changed records are new frames) and that
generation's decoded image, seeded with the edit's own values so nothing
is decoded again. All of it happens before the lock. Under the lock the
writer checks that the installed generation and the stored stamp are
still the pinned ones and that the content rows it read are unchanged,
then writes the rows, the viewer (when another principal edits), the
event, commits and installs.

What else the outputs depend on, and how it stays exact:

- The display refresh rewrites people embedded in members, projects,
  initiatives, updates, comments, activities and notifications with the
  user directory's values. The resident write runs only when that refresh
  changes nothing (checked once per image, and per edit for the loaded
  content).
- Actor substitution stores the actor as the viewer and as its user
  record. The resident write requires the stored user record to equal the
  actor (no request attribution) and stores the viewer itself when it
  differs.
- Lookups the callback may read (former members, collaboration states),
  favorites, issues, content changes and a document stored twice send the
  edit to the scoped writer.

A concurrent write invalidates a prepared edit; it is prepared again on
the new generation (three attempts). With coordinated servers a
generation behind the stored stamp is reloaded first, as the scoped
writer reloads it; a single server leaves it to the scoped writer, which
merges its installed generation into the stored rows.

In tests every resident edit also runs through the scoped writer up to
its commit: the callback input, previous values, realtime payload, SQL
state (records, search documents, blobs, content records, root, account)
and next generation must be identical, and the seeded image must equal a
fresh decode.

### Collection copies

A generation that changes one record of a collection still writes the
whole collection frame. Record by record that was one read and about one
write per record (2,321 projects, 36 MB, per project edit). Two steps:

- Consecutive records kept verbatim are copied as one range in 1 MiB
  reads (`metadataLogicalCopyRun`), each keeping the offset, origin and
  child index a single copy gives it. CPU per project edit fell from about
  117 to 46 ms, but an execution trace showed the edits blocked in `pwrite`
  for up to two seconds: 36 MB of page-cache writes per edit stall on a
  busy machine, which a CPU profile does not show.
- So a large range of a settled storage is now borrowed, not copied
  (`scale_p21_logical_borrowed_range.go`): it takes its place in the new
  storage's offsets (a hole in the file) and its reads are served by the
  storage that holds the bytes, which stays open until its last borrower
  is discarded. Ranges resolve to the holder, so a read is redirected
  once; settled bytes never change and a patch of a borrowed range is
  refused. A storage borrows from at most 48 storages and copies
  otherwise, so a long run of edits does not keep every earlier
  generation's files alive.

## Increments

Each increment is one commit with tests, measured on the load rig (MySQL
first, then PostgreSQL and SQLite) against flag-off.

1. Bootstrap from the resident decoded generation (done): `GET
   /api/issue-records/bootstrap` takes the shared image of the generation it
   pinned instead of the body projection, runs the unchanged paged projection
   on it and encodes the result with the flag-off encoder.
2. Resident spool bytes through memory mappings: measured and rejected (see
   Decisions).
3. Size the image cache, admission and concurrency from a replica memory
   target. Evict whole workspaces and charge decoded allocations.
4. Cache encoded project, document and initiative fields for the bootstrap.
5. Batch single-term MySQL metadata search candidates and extract each
   selected row's result columns with one JSON parse.
6. Prepare the scoped project callback from the image before taking the
   lock. The locked path revalidates its inputs and persists the selected
   changes. Peer reloads and verified pages also prepare before locking.
7. Share lazy ID indexes across unchanged collections. Project, document and
   initiative detail reads can use matching fields from a cached image.
   Issue detail still uses the canonical record reader.

## Correctness

The P21 differential tests compare flag-on and flag-off responses and
persistence on SQLite, MySQL and PostgreSQL. SW480 checks the large peer
document merge at 768 MiB of work admission and the atomic refusal at
64 MiB. Focused tests cover eviction, budget accounting, concurrent
readers, stale write preparation and peer publication races.

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

Document and project edits (resident document write, borrowed collection
ranges), same rig and seed procedure, flag off → on:

| class | req/s | p50 ms | p95 ms | p99 ms | 5xx |
| --- | --- | --- | --- | --- | --- |
| overall | 11.92 → 18.08 | 54 → 32 | 2600 → 1004 | 5059 → 1993 | 60 → 37 |
| hot-read | 7.36 → 11.13 | 25 → 16 | 1539 → 239 | 3005 → 839 | 18 → 0 |
| hot-write | 1.59 → 2.38 | 223 → 170 | 2732 → 861 | 5483 → 1296 | 2 → 0 |
| cold-write | 0.43 → 0.72 | 1576 → 487 | 5946 → 2006 | 8060 → 3255 | 7 → 3 |
| search | 0.59 → 0.91 | 1079 → 1018 | 12919 → 15004 | 15609 → 15119 | 8 → 9 |

project.update: 19 → 45 requests, p50 1008 → 517 ms, p95 5946 → 2006 ms;
its two flag-on 500s were MySQL deadlocks (error 1213), which mutation
attempts now retry like a lost race (labels and loop runs hit the same
before). document.update succeeded only 6 and 3 times (the rig's VUs mostly
lack permission; 403 in both modes): p50 1497 → 106 ms. Peak RSS per
replica 897/1008 MiB off, 739/758 MiB on. Realtime lag p95 4597 → 957 ms.

SQLite, one request at a time (load rig seed, 20 rounds of five project
and five document title edits):

| edit | off p50 / p95 | on p50 / p95 |
| --- | --- | --- |
| project | 174 / 268 ms | 21 / 53 ms |
| document | 353 / 1071 ms | 138 / 373 ms |

(Before this step flag on was 315 / 1369 ms and 1398 / 2772 ms.) End RSS
503 MiB on, 683 MiB off in that run; the off value varies with GC timing
(245-683 MiB across runs).

### Replica memory target and resident fast paths

The MySQL measurements below use two replicas, MySQL 8.4, real Redis,
fresh database copies for each flag state and a 3 GiB target per replica.
The plan allows 1,075 MiB of decoded images, 768 MiB of admitted work,
192 MiB of policy, 96 MiB each for rows and source descriptors, and
192 MiB for bootstrap encodings and their builder. It allows three COLD
slots, three legacy-view lanes and six bootstrap readers.

| Report | Workload | req/s off to on | p50 / p95 / p99 ms off to on | 5xx + transport off to on | Peak RSS MiB per replica off to on |
| --- | --- | --- | --- | --- | --- |
| `01-memory-mysql2-1x` | Mixed, 10 VUs | 16.19 to 23.21 | 32 / 1521 / 2525 to 14 / 583 / 957 | 5 to 10 | 839 / 766 to 673 / 543 |
| `03-bootstrap-mysql2-1x` | Bootstrap, 10 VUs | 10.06 to 9.47 | 343 / 2460 / 3972 to 390 / 2548 / 4207 | 0 to 0 | 600 / 671 to 520 / 567 |
| `04-search-mysql2-1x` | Search-heavy, 10 VUs | 2.50 to 5.87 | 3238 / 14810 / 15068 to 815 / 9586 / 15007 | 26 to 9 | 589 / 447 to 612 / 658 |
| `05-bootstrap-search-mysql2-1x` | Bootstrap and search, 10 VUs | 6.61 to 10.42 | 475 / 1944 / 15014 to 287 / 1510 / 11670 | 16 to 6 | 587 / 491 to 706 / 606 |
| `06-mixed-mysql2-1x` | Mixed, 10 VUs for 100s | 12.59 to 23.18 | 57 / 2370 / 3663 to 15 / 395 / 1846 | 19 to 15 | 783 / 835 to 884 / 860 |

In `01`, search p95 rose from 7.74 to 9.10 s. The first bootstrap cache
in `03` reduced RSS and CPU, with higher latency. `04` profiles the
initial batched search plan. Its SQL statement digest puts the largest
remaining cost in shell extraction for projects and documents. The
single-parse `JSON_TABLE` extraction in `05` cut search p95 from 15.01 to
7.80 s. Bootstrap throughput in that mix rose from 3.81 to 6.25 req/s
with p95 at 774 and 785 ms and 257 encoding hits.

The mixed run `06` includes resident project preparation, out-of-lock
incremental peer reloads, collection generations and image-based detail
reads. Every measured class improved at p95 and p99. Agent p50 rose from
72 to 231 ms across 12 and 13 samples. The measurements combine the
changes, so they give the resulting latency for each operation.

| Operation | p95 ms off to on |
| --- | --- |
| Project update | 5246 to 1064 |
| Document update | 6007 to 825 |
| Project detail | 1155 to 261 |
| Document detail | 2125 to 169 |
| Workspace bootstrap | 2550 to 927 |
| Search | 15005 to 8282 |
| Realtime lag | 2925 to 1133 |

### Sustained MySQL load

`18-mixed-mysql2-10x` ran the scale-1 seed with 100 VUs and 50 streams for
600 seconds per flag state, using a 3 GiB per-replica metadata target and
a 3 GiB MySQL container. Both database and Redis stayed up throughout.
This pair predates the larger COLD projection gate and the later changes
to project preparation and peer reloads. The earlier `07` 100-VU pair
lost MySQL and is excluded.

| Class | req/s off to on | p50 / p95 / p99 ms off to on | 5xx + transport off to on |
| --- | --- | --- | --- |
| Overall | 36.17 to 55.35 | 2676 / 6246 / 11657 to 386 / 5378 / 14221 | 7770 to 5154 |
| HOT read | 24.29 to 37.36 | 1704 / 3140 / 3493 to 179 / 3025 / 3162 | 4388 to 2175 |
| HOT write | 4.05 to 5.96 | 3117 / 12012 / 18724 to 3019 / 10098 / 13741 | 952 to 481 |
| Search | 1.83 to 2.70 | 3022 / 15030 / 16952 to 963 / 14930 / 15496 | 422 to 252 |
| Sync | 1.58 to 1.94 | 3035 / 7346 / 10036 to 2310 / 6127 / 10926 | 833 to 1053 |
| Bootstrap | 0.22 to 0.38 | 2556 / 4352 / 5930 to 1192 / 3468 / 7355 | 52 to 22 |
| API key | 1.26 to 2.18 | 1916 / 3206 / 3394 to 212 / 3041 / 3193 | 254 to 108 |
| COLD read | 1.96 to 3.26 | 3006 / 4966 / 6866 to 1409 / 15023 / 23913 | 391 to 321 |
| COLD write | 0.93 to 1.52 | 6005 / 10635 / 15118 to 5651 / 21479 / 30001 | 452 to 722 |
| Agent | 0.06 to 0.06 | 3049 / 15626 / 29724 to 3006 / 8245 / 23608 | 26 to 20 |

Successful responses rose from 23.2 to 46.4 per second. Successful sync
responses fell from 110 to 101 over the run. The rig includes HTTP
refusals and transport failures in its latency percentiles. Its
`projection-gate` label groups multiple busy responses, including lock
contention and deadlines. Replica logs recorded 45 resource projection
timeouts and eight COLD facade slot timeouts with the flag on.

Flag-on RSS peaked at 1556 and 1616 MiB by `getrusage`. Four-second samples
averaged 1100 and 1114 MiB in minutes 6-8, then 1070 and 1209 MiB in
minutes 8-10. Both replicas stayed below the 3072 MiB target throughout
the run. Fixed data scale and sample duration limit the scope of this
measurement.

## Search

The lead's cache-off MySQL run `base-mysql2-1x-cache-off-a` used the
default mix with 10 virtual users. Search p50/p95/p99 were 244/6419/15113 ms.
Four of 197 searches returned 503. Their request IDs match `context deadline
exceeded` in the replica logs. The rig labels this response `projection-gate`
because it shares the gate's error text.

The repeat `base-mysql2-1x-cache-off-b` measured 274/8525/15006 ms and
2/170 search 503s. Its two most expensive metadata page statements spent
149.41 and 33.94 seconds in MySQL, averaging 1698 and 185 ms per statement.

`FLOW_LOADRIG_SEARCH_PRINCIPALS=mixed` rotates the search workload across
the seeded workspace administrator, members, guests and team-restricted
personal keys. With `FLOW_LOADRIG_SEARCH_TIMINGS=on`, the API records candidate
lookup, page reads by kind, visibility, history, issue search and encoding
in microseconds. Timing logs contain no search terms or credentials.
The timing line shares the HTTP log's request ID so the two durations can be
compared. The issue phase reports label lookup, candidate reads and reference
resolution. `FLOW_LOADRIG_SEARCH_TYPES` selects resource types while retaining
the rotating terms and mixed principals.

SDK document search now intersects the capped index hits with the authorized
directory before loading headers. It uses the existing ranking and connection
pagination. The HTTP differential covers forward and backward search cursors,
team restrictions, and a match past the document shell's text prefix.

MCP and SDK document scoring now share a single lowercase body between keyword
scoring and snippet selection. Snippets still quote the original text with the
same Unicode character offsets and section headings. The snippet benchmark's
median dropped from 459 to 116 microseconds and from 1033 to 6 allocations.
An MCP tool call also builds its authorized document lookup once for all its
keyword sets, after applying the team, project and archived filters.
In a SQLite benchmark with 2000 authorized documents and five keyword sets,
the ranking call used about 1.7 MB of allocations per operation, down from
9.7 MB. This benchmark starts with the document projection already loaded.

Semantic HTTP and MCP issue search now expand the query once before scoring
the candidates. They normalize each candidate's title once. In
`BenchmarkSemanticSearchTextScore`, median scoring time fell from 35.3 to
18.3 microseconds and allocations fell from 23 to 5 per candidate.

Bounded MySQL projects apply the viewer's team scope before sorting. The SQL
predicate uses the same bytewise team ID comparison as Go. It keeps at most 200
eligible projects per page so the common search limit needs just one sort.
Bounded search uses the existing catalog summaries for
project and document candidates when the collection is complete, has no
omissions and the journal covers the workspace in the same read snapshot.
The query applies the original JSON sort and filter expressions to those
summaries. After the Go visibility checks, one batch reads the canonical
2,048-character text prefix for the accepted keys in each collection. The
canonical path also covers explicit `teamId` queries because those can read
a field outside the catalog projection. MySQL and PostgreSQL materialize
native JSON for the selected shells to share parsing across output fields.
PostgreSQL and SQLite batch single-term trigram hits across kinds and fall back
to per-kind lookups if the batch exceeds 4,096 hits. With the shared result
cache enabled, PostgreSQL and SQLite release each search snapshot before
entering the next collection's cache fill.

The store differential exercises result fields for all seven metadata kinds.
It compares both flags for six principal and key scopes, including title and
timestamp ties across multiple pages, archived and deleted records, and
private document grants. An HTTP differential compares workspace and semantic
search results, including semantic counts and cursors, for members, guests,
administrators and team-restricted keys.

The MySQL search-only probe used 5,000 issues, 2,341 projects and 90 documents
with cache disabled. `EXPLAIN ANALYZE` returned 2,341 project hits for `load`
in about 9 ms. The original project page took 858 ms and a key-and-sort-only
probe took 1,039 ms. The project team's candidate filter and sort took 664 ms
with separate JSON expressions and 457 ms when `JSON_TABLE` shared their
parse. On the final seed, the corresponding catalog selection took 35 ms.
Project summaries averaged 817 bytes and document summaries averaged 539
bytes. The collections were complete with zero omissions.

| MySQL search-only run, 10 virtual users | Search p50 / p95 / p99 (ms) | Metadata SQL mean (ms/search) | 5xx / searches |
| --- | ---: | ---: | ---: |
| Branch base, flag on | 445 / 5,390 / 15,025 | 942 | 6 / 216 |
| Branch base, flag off | 1,477 / 4,239 / 15,009 | 1,804 | 4 / 128 |
| Project team predicate and larger pages | 482 / 3,935 / 4,623 | 556 | 0 / 274 |
| Shared JSON parse for project candidates | 421 / 1,906 / 2,276 | 325 | 0 / 350 |
| Covered catalog, flag on | 182 / 320 / 464 | 52 | 0 / 696 |
| Same build, flag off | 1,299 / 12,490 / 15,119 | 2,075 | 6 / 126 |

The metadata SQL column sums the elapsed candidate, page and text reads per
handler, including connection wait and row scanning. It includes the catalog
coverage check. Issue search and search history have their own phases.
The timing log includes the four-second warmup. The driver latency
percentiles use its measurement window. The catalog comparison used MySQL
8.4.13 with a 2,304 MiB container limit and a 128 MiB temporary table memory
budget. The earlier probes used a 1,536 MiB container limit.

The catalog build at 100 virtual users measured 2,860 / 4,140 / 5,147 ms
with 236 ms of metadata SQL per search. All 1,230 measured requests returned
200 with no timeouts. The run missed the 1,500 ms p95 target. Issue candidate
reads accounted for about 1.29 seconds per handler, including reference
resolution. The issue statements accounted for 1,232 seconds of summed MySQL time. On the seed,
`EXPLAIN ANALYZE` sorted 4,800 qualifying issues before returning 120 for
`search` in 25 ms with an idle database.

A MySQL probe matched issues inside a recent index window, plus an exact
identifier lookup. It retried with the original query when the window
contained fewer visible hits than the requested limit. The differential
passed on MySQL 8.4.13. At 100 users it measured 2,535 / 4,801 / 5,920 ms
with 215 ms of metadata SQL, 1,237 ms in the issue phase and no errors or
timeouts in 1,280 requests. The query is excluded from the release changes
because it increased tail latency.

At 100 users, an ordered join returned 1,239 successful searches at
2,794 / 4,139 / 5,295 ms. A second probe split `MATCH` into its own query.
That query accounted for 1,184 seconds of summed MySQL time, even though it
returned keys alone. The follow-up query failed on 1,308 of 1,347 requests.
Both probes were discarded.

The resumed all-resource, issue-only and metadata-only comparison could not
start because it needed both an available rig lock and host load below 20.
The last measured all-resource p95 remains 4,140 ms.

| PostgreSQL 17.12 search-only run, 10 virtual users | Search p50 / p95 / p99 (ms) | Metadata SQL mean (ms/search) | 5xx / searches |
| --- | ---: | ---: | ---: |
| Branch base, flag on | 614 / 7,234 / 15,039 | 1,550 | 5 / 145 |
| Covered catalog, flag on | 130 / 1,123 / 2,522 | 145 | 0 / 622 |
| Covered catalog with batched trigram hits, flag on | 101 / 445 / 913 | 53 | 0 / 789 |
| Covered catalog build, flag off | 632 / 7,806 / 15,022 | 1,134 | 4 / 180 |

On the same seed as the batched run, the catalog build with per-kind lookups
measured 303 / 2,175 / 3,774 ms, 312 ms of metadata SQL and 2/421 search 5xx.
Batching reduced repeated trigram scans and eliminated pages for collections
with no hits.

| SQLite search-only run, 10 virtual users | Search p50 / p95 / p99 (ms) | Metadata SQL mean (ms/search) | 5xx / searches |
| --- | ---: | ---: | ---: |
| Branch base, flag on | 807 / 5,043 / 5,690 | 652 | 0 / 209 |
| Covered catalog and batched trigram hits, flag on | 44 / 288 / 433 | 19 | 0 / 926 |
| Same build, flag off | 947 / 3,496 / 3,861 | 535 | 0 / 217 |

Each 10-user sample ran for 35 seconds after warmup. The 100-user sample ran
for 40 seconds. These runs used the rotating fixture terms and measured
search traffic on a fixed seed. The lead's longer default mix also included
resource writes and sync traffic. The raw driver reports, phase timings and
SQL plans are in `~/.flow-local/live/loadrig-search/reports/`.

The store and HTTP search differentials passed on MySQL, PostgreSQL and
SQLite. Both vet runs, the store guards, and the targeted store search tests
exited 0. The server guards exited 1 on SW480's webhook `nextAttemptAt`
mismatch, which reproduced on the branch base. SW530 and SW559 passed on
the final run. An earlier SW530 generated release slug mismatch also
reproduced on the branch base.

The resumed targeted server search suite exited 1 when
`TestSharedCacheDifferential` compared different sync heads after a team
revocation. The focused rerun of that test and `TestSharedCacheSearchWithoutRedis`
exited 0. Command outcomes are in `reports/verification-resume.txt`.
