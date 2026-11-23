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
a field outside the catalog projection. MySQL uses `JSON_TABLE` for one shell
parse per selected row, while PostgreSQL materializes native JSON for the
selected shells. PostgreSQL and SQLite batch single-term trigram hits across
kinds and fall back to per-kind lookups if the batch exceeds 4,096 hits.
With the shared result cache enabled, PostgreSQL and SQLite release each
search snapshot before entering the next collection's cache fill.

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
resolution. The issue statements accounted for 1,232 seconds of summed MySQL
time. On the seed, `EXPLAIN ANALYZE` sorted 4,800 qualifying issues before
returning 120 for `search` in 25 ms with an idle database.

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
for 40 seconds. These runs used the rotating fixture terms and measured search
traffic on a fixed seed. The lead's longer default mix also included resource
writes and sync traffic. The raw driver reports, phase timings and SQL plans
are in `~/.flow-local/live/loadrig-search/reports/`.

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

### Load loop

The starting revision is `35ac477b8`. The MySQL report set under
`~/.flow-local/live/loadrig-loop/reports/` uses the default mix, scale 1,
real Redis, a 3 GiB MySQL container, a 3 GiB metadata target per replica
and `GOMEMLIMIT=1536MiB`. Each measurement starts from a fresh seed.
HTTP percentiles include failed requests. Throughput and success counts
are reported separately in the raw JSON.

| Bottleneck | Change | Evidence |
| --- | --- | --- |
| One-off searches paid for an SQL version stamp, Redis probes by collection and fills. | Wait for three recent searches from the same principal and credential before looking in Redis. | `base-mysql2-1x-cache-on-a` hit 6 of 1,028 search lookups at 10 VUs over 180 s. The differential checks that two initial misses run no version SQL or Redis lookups and that reuse and index repair still produce the same results. |
| Viewer cache hits and sync caching showed little benefit in the focused tests. | Make viewer memberships and eligible sync pages opt-in. | With the original tier on and off, `base-mysql2-1x-cache-{on,off}-a` served 20.20 and 23.02 req/s, with p95 at 707 and 502 ms. Viewer hit 3,591 of 4,545 and sync had no eligible pages in the cache-on run. |
| Live realtime checks read source versions and authorization epochs separately. | Read both in one live SQL statement so the two values come from the same snapshot. | The baseline executed 74,483 source-version reads and 41,416 standalone epoch reads across all callers. The delivery differential checks two statements down to one and verifies the epoch changes on revocation. |
| The member directory parser repeatedly fetched chunks of the same installation. | Read installations up to 16 KiB in the initial size query. Stream larger rows within the existing memory budget. | The starting 100-VU MySQL run spent 96.65 s on 191,618 chunk reads. The differential compares directory projections and verifies one SQL read for small installations, including malformed rows. |
| A peer reload that raced a write could repeat preparation while holding the writer lock. | Retry preparation once outside the lock. Revalidate the installed image, SQL metadata stamp and journal head before installing. | One starting 100-VU MySQL reload held the writer lock for 18.7 s. The race tests cover a concurrent local write and verify both preparations run unlocked when the journal head advances between preparation and install. |
| Simple issue updates fetched the locked target a second time. | Decode an independent callback copy from the locked row. Keep the related and family reads for edits that need them. | The starting 100-VU issue updates held writer locks for 387 s across both replicas. The write budget test verifies one fewer SQL statement for each single-issue edit. |
| Full-text issue search fetched a full issue body alongside each list projection. | On MySQL select the list projection first. Missing or invalid projections repeat the entire scoped query with the body so visibility and order come from a single statement. | The default-mix shared-issue query took 442.43 s across 1,049 calls. Across 40 paired 120-row query replays on MySQL 8.4, p50 fell from 5.43 to 5.06 ms and p95 from 8.54 to 6.34 ms. The differential covers restricted and shared issues in both flag states. |
| The application-access path still fetched small installations in many chunks. | Share the member-directory reader under the existing read snapshot and scratch budget. It fetches installations up to 16 KiB in one statement and streams larger ones. | `final-mysql2-10x-on` spent 75.24 s on 290,537 installation chunk queries after the member-directory optimization. The application-access differential went from 62 statements to one and checks the full output against the legacy installation read. |
| Sync changes lost the source-version proof while projecting full pages under concurrent writes. | After two verified source races, project one complete journal transaction on the final attempt; if that transaction also races, return an empty reset page at the current journal head. Keep the cursor, byte budget, and trailing authorization check. | The MySQL 100-VU logs recorded 837 exhausted third attempts across both replicas, including warmup. The differential covers the short retry, the final reset page, and an authorization change that still stops delivery immediately. |
| A source-cache flight could report a stale sentinel after the live authority had already settled back to the request's source version, turning a safe sync rebuild into a 503. | Treat that sentinel as reset-eligible only while the request is live, its credential still verifies, and a fresh authority read confirms the authorization epoch is unchanged. Return an empty page at the authenticated journal head; keep bare authorization failures as 503s. | `TestScaleP12SyncRaceProofAndCoverageReset` covers the settled stale-source reset, a journal coverage gap, and an authorization change that remains denied. |
| Sync 503s still combined safe source races with authorization changes under the same stale sentinel. | Classify final sync failures with a live source-version and authorization-epoch read, without broadening reset eligibility. A reset remains valid only for a source-only race; an epoch change stays a 503. | `post-91075-3g-100v-180s` recorded 35 sync 503s (19 denied/stale=false and 16 stale=true). `logSyncChangesFailureClass` now records `source_changed` and `authz_changed`; `TestScaleP12SyncRaceProofAndCoverageReset` proves a membership epoch change cannot cross the reset path. |
| Realtime cache misses held the cache mutex during SQL and projection work. | Build outside the mutex. Each cache permits two distinct builds and shares one build per key. Waiting requests can cancel. | The MySQL 100-VU trace attributed 182 s of cumulative wait to this cache over 15 s. The concurrency test completes a warm hit with both builders blocked, admits only two keys, and verifies same-key sharing. |
| Sequencing a journal entry could invalidate a prepared peer reload whose metadata was still current. | Check the SQL metadata stamp, journal generation and head together before installing. Reuse the prepared metadata after a head advance and retain its captured cursor. | The controlled head race takes one preparation instead of two and produces the same image as a fresh SQL reload. A journal reset declines the prepared install. The existing race test verifies that a concurrent local metadata write survives. |
| MySQL sorted wide comment and activity result sets across all resources even though hydration consumes a map. | Sort workspace-wide comment and activity reads by resource first, preserving `(created_at,id)` within each resource. | The 100-VU MySQL digest spent 275 s and sorted 7.3 million rows. Across 24 queries per order on the same seed, MySQL used the existing resource-order index, reduced mean SQL time from 129.66 to 64.61 ms, and eliminated 391,392 sorted rows. The differential covers accented resource keys, order ties, and malformed comment handling in webhook listing. |
| Base-aware legacy hydration still sorted each workspace-wide content collection by `created_at,id` after the map-order optimization. | Use the same resource-first order in `readContentRecordsWithBase`, preserving per-resource order while using `content_resource_order_idx`. | `final-mysql2-10x-on` recorded 1,164 calls and 275.22 s for this digest, with two errors. On the active MySQL seed, a 24-query replay fell from 3,426 ms to 2,364 ms, and `EXPLAIN` changed from `Using filesort` to the resource-order index. `TestContentRecordGroupedOrderDifferential` and its race run pass. |
| Realtime cache builders could wait behind other work and then project records for a source revision that could no longer populate the requested key. | Check the expected source revision immediately after admission, then retry through the existing delivery path. Keep final delivery validation. | The 100-VU MySQL trace at `463323b79` recorded 324 s of cumulative cache admission wait in 15 s. In the SQLite differential, a stale build went from six SQL statements to one with bounded metadata on, and from eight to one with it off. The fresh retry matches the legacy projection in both modes. |
| Incremental peer reloads fetched the same metadata stamp twice inside one repeatable SQL snapshot. | Pass the preflight stamp into state capture and retain the locked stamp and journal checks before publishing. | Removes one SQL round trip per stale incremental reload. The reload differential checks three snapshot stamp reads plus one locked publication check and compares the installed image with a fresh SQL reload. |
| Callers waiting for the same stale realtime source repeated its live version check after each rejection. | Return a live version mismatch to callers waiting on that build, then discard the completed build. | The concurrency test gives nine callers one stale build. A legacy snapshot mismatch or canceled leader lets waiters complete a fresh build. |
| Workflow `createIssue` runs on MySQL and PostgreSQL always entered the full-image writer, extending the workspace lock for the complete metadata image. | Use the existing dialect-aware SQL `MAX(number)` fold in the point writer. Keep full-image fallback for overflow, stored-ID collisions and unsupported shapes. | The load-loop logs showed `workflow_run.executed` holding the lock for about 43 s on the legacy path. The MySQL and PostgreSQL SW500/SW580 differentials pass with zero full-image attempts for ordinary create-issue runs. SW580 keeps SQL work flat at 99 statements for a notify run and 196 for a retry issue run when 400 unrelated issues are added. |
| Freshness waiters could lose a race with a completed peer refresh at their three-second request deadline. | Probe the authoritative peer state for 50 ms after a deadline and accept only an installed `Current` state. Cancellations and stale state still fail closed. | The deadline-probe differential accepts a completed current install and preserves `context.DeadlineExceeded` for a stale replica. A full rig measurement is required to quantify the narrow race reduction. |
| An error during logical resident normalization returned a nil cleanup function after allocating its stage, work lease and builder. A background freshness reload then panicked the replica while handling that error. | Keep error cleanup in a separate local closure, and recover panics in the shared reload worker so the waiting request receives an error and the worker state is released. | `TestScaleP21SW30LogicalNormalizerErrorCleansUp` covers the malformed logical index and verifies the spool and descriptor budgets return to their prior values. `TestWorkspaceReloadPanicBecomesRequestError` and both tests' `-race` runs pass. The only panic found in the stored rig replica logs was `head-939e-disciplined-a` replica 0 at `scale_p21_logical_normalize.go:76`. |
| Legacy Redis freshness reloaded a bounded replica even when its installed logical generation already matched the SQL head. | When Redis is ahead of the local counter, probe `WorkspacePeerState` and accept only an exact, non-`behind` logical source. Record the Redis version after the SQL check; stale or failed probes keep the existing reload path. | `TestScaleP21LegacyFreshnessChecksSQLBeforeReload` covers current and stale installed copies. `TestScaleP21PeerStateAcceptsExactLogicalSourceWithoutPolicyRebuild` covers a HOT bounded generation whose policy stamp was not rebuilt. |
| Releasing the COLD facade before retrying a busy workspace lock made the retry path slower under the mixed 100-VU load and increased admission churn. | Remove the COLD lock-retry and slot-release behavior introduced by `50129f8bf`; keep the original single bounded attempt until a route-scoped writer owns the work. | The two-arm 180-second MySQL2 comparison recovered from a median 54.95 req/s, 7.51 s p95 and 11.84% errors at the fixed HEAD to 66.8 req/s, 6.50 s p95 and 4.47% errors after the revert. Both replicas remained alive. |
| API-key and loop-settings COLD handlers hydrated the whole workspace before their record-backed work, so 17 of the 18 MySQL COLD timeout samples paid for unrelated collections. | Read only the required metadata records and check live membership with `WorkspaceRole`; use the scoped writer for API-key and loop-settings mutations. | SW450, SW191, SW18 and SW490 differential/race tests plus the store and server activation guards pass. A 60-second MySQL2 100-VU run on the real seed recorded 77.5 req/s, 3.65 s p95 and 7.47% 5xx/transport. The route-specific failures remained projection/freshness admission failures, so COLD admission is the next bottleneck. |
| The route-scoped handlers still shared the load rig's one-and-a-half GiB metadata ceiling, which reduced the default COLD facade plan to one slot per replica. | Repeat the route-scoped measurement with the configured 3 GiB metadata budget so COLD admission is compared at its intended three-slot plan. | `scoped-api-100v-180s` at the runtime-derived 1,536 MiB ceiling served 108.4 req/s at 3.005 s p95 with 3.99% 5xx/transport. `scoped-3g-100v-180s` with `FLOW_METADATA_MEMORY_BYTES=3221225472` served 99.8 req/s at 3.810 s p95 with 5.49% 5xx/transport. COLD slot refusals fell from 120 to 15 and mean waits from about 14 s to 8.5 s on replica 0, but freshness 503s rose and the mixed run remains above target. |
| COLD writes that met admission could still lose the workspace lock to another replica and return `projection-gate` 503s after holding a decoded facade. | Retry the complete COLD legacy write up to three times after `ErrWorkspaceLockBusy`, releasing the facade and slot between attempts and honoring request cancellation. | The scoped 3 GiB diagnostic recorded 352 projection-gate failures among 488 COLD writes. Replica 0 logged 76 slot waits averaging 8.47 s and 15 lock-busy refusals. The new differential covers one busy attempt, one retry, and zero leaked COLD slots. |
| COLD writes waited on the shared projection gate after their COLD authorization path had already bounded the legacy view or facade, producing duplicate admission and `projection-gate` failures. | Skip shared projection admission for COLD writes and registered COLD reads with a retained facade. Keep the gate for unregistered COLD reads and HOT routes. | The pre-change MySQL 100-VU run recorded 676 `projection-gate` 503s and 33 transport failures among 1,064 COLD writes, with 23.7 s p95 and 30.0 s p99. `TestProjectionGateReleasesColdWriteAfterAuthorization` and `TestProjectionGateRegisteredColdRouteUsesFacadeAdmission` cover the bypass and slot ownership. In the post-change `post-91075-3g-100v-180s` run, projection labels fell to 231 (208 COLD writes) and throughput reached 63.8 req/s, but p95/p99 remained 7.14/18.24 s with 384 HTTP 5xx and 61 transport failures. Replica logs recorded 42 direct COLD-slot timeout markers, so queue refusal and slot waits remain the next COLD bottleneck. |
| Unregistered COLD paged authorization decoded or admitted a separate legacy view before the handler's COLD work, so one request could consume two legacy-view lanes and lose admission under concurrency. | Route every unregistered COLD authorization path, including paged authorization, through `ColdRequestView` and retain the request's decoded legacy image for later work. | `TestScaleP21SW215UnregisteredColdPagedAuthorizationReusesRequestView` compares the bounded and legacy projections and proves the retained view is reused while another lane is held. `TestScaleP21SW215UnregisteredColdPagedAuthorizationViewRace` covers concurrent access and scope cleanup. A load-rig rerun is pending. |
| COLD facades and retained authorization views allocated fresh large collection backing arrays for every resident-image copy, leaving transient decoded graphs for the allocator to retain during sustained concurrency. | Recycle large copied collection storage through a bounded request-lifetime pool, returning it only after the facade or COLD scope drops its graph. | `TestColdImageCopyReusePreservesIsolation` and the COLD decode differential verify that pooled storage stays isolated from the resident image and later copies. A sustained rig rerun is pending. |
| The shared large-slice pool discarded every idle type when one COLD image field had no suitable buffer, so successive fields repeatedly allocated and discarded large arrays. | Evict only unusable buffers of the requested slice type while retaining other bounded types for later fields. | `TestScaleP15SnapshotBufferCacheIsBoundedAndCleared` now covers retention across slice types and preserves the existing oversized-buffer eviction rule. A sustained rig rerun is pending. |

Both original-tier MySQL comparisons ran for 180 seconds at 10 VUs with
bounded metadata on. The second comparison reversed the run order.

| Trial | Result tier | All / successful req/s | p95 / p99 ms | HTTP 5xx |
| --- | --- | --- | --- | --- |
| A | On | 20.20 / 19.86 | 707 / 2,061 | 30 |
| A | Off | 23.02 / 22.71 | 502 / 1,655 | 18 |
| B | Off | 21.44 / 21.05 | 621 / 1,789 | 27 |
| B | On | 17.75 / 17.30 | 1,040 / 3,012 | 47 |

The original result tier reduced overall throughput by 12% and 17% in
the two pairs. All four runs reported zero transport timeouts. Searches
hit Redis in 0.6% and 3.0% of lookups. Viewer hit rates were 79% and 84%,
and neither run had an eligible sync page.

At `e3f45e23a`, the revised result tier bypassed Redis for 88 of 89
search requests in the 180-second MySQL run. The one admitted search made
six collection lookups, all misses. Cache on served 10.52 req/s with
3,016 ms p95 and 84 HTTP 5xx. Cache off served 18.13 req/s with 951 ms
p95 and 18 HTTP 5xx. The runs used separate fresh seeds. The cache
counters isolate the lookup volume, and the pair leaves the source of
the throughput gap unresolved.

#### Starting-revision MySQL saturation

`base-mysql2-10x-bounded-{off,on}` drove the default mix for 600 seconds
at 100 VUs per arm. Both used the original Redis result tier.

| Bounded | All / successful req/s | p50 / p95 / p99 ms | HTTP 5xx | Timeouts | Freshness 503s |
| --- | --- | --- | --- | --- | --- |
| Off | 36.59 / 24.16 | 2,552 / 6,296 / 11,685 | 7,437 (33.88%) | 0 | 6,412 |
| On | 45.19 / 35.36 | 552 / 7,712 / 15,888 | 5,731 (21.14%) | 17 | 4,397 |

The starting 10-VU arms lasted 180 seconds each. Bounded mode served
19.86 successful req/s versus 8.75 with the flag off and moved p95 from
3,022 to 707 ms. These runs used separate seeds.

With bounded metadata on, hot writes accounted for 32.5% of summed
request time, hot reads 30.9%, COLD writes 11.2%, sync 8.0%, search 7.3%
and COLD reads 7.3%. All 17 timed-out requests were COLD writes.

The 15-second trace attributed 398 s of goroutine wait to workspace
reloads, 176 s to mutexes, and 152 s to shared realtime source waits.
These are cumulative stacks and can overlap. The two replicas spent
387 s holding the writer lock for issue updates and 333 s for issue
discussion mutations during the full run. They recorded 341 COLD slot
waits, 31 refusals, 156 incremental reloads and zero full reloads.
The SQL digest report attributes 442 s to the main recursive issue query
and 97 s to 191,618 application installation chunk reads.

Sampled RSS peaks were 1,234 and 1,194 MiB. Third to fourth quarter
averages moved from 559 to 557 MiB and from 814 to 742 MiB. Redis used
70.3 MiB at peak, averaged 62.6 and 64.4 MiB in those quarters, and
ended at 54.3 MiB. The shared host's one-minute load ranged from 7 to
58 during the run. With the flag off, RSS peaked at 1,498 and 1,383 MiB
and third-to-fourth-quarter averages rose from 1,175 to 1,243 MiB and
from 1,070 to 1,174 MiB. Redis peaked at 131.9 MiB, its quarter averages
rose from 66.4 to 81.7 MiB, and it ended at 46.9 MiB.

#### Same-seed comparisons

The matrix runs `d77706265` and restores the same seed for each arm within
a topology. The two-replica runs reset their dedicated Redis instance
between arms. Requests use the default weighted mix and generated test
workspaces. The driver picks operations independently in each arm.

| Topology | VUs / seconds | Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| mysql2 | 10 / 180 | Off | 11.95 / 11.76 | 59 / 2,290 / 4,537 | 1,716 / 2,205 / 5,057 | 19 (0.88%) | 0 |
| mysql2 | 10 / 180 | On | 22.66 / 22.25 | 19 / 629 / 2,050 | 371 / 590 / 3,001 | 33 (0.81%) | 0 |
| mysql2 | 100 / 600 | Off | 37.26 / 24.92 | 2,464 / 6,103 / 10,854 | 3,101 / 10,194 / 11,284 | 7,379 (33.01%) | 0 |
| mysql2 | 100 / 600 | On | 60.34 / 52.29 | 250 / 5,212 / 14,590 | 3,007 / 8,017 / 23,715 | 4,517 (12.48%) | 48 |

MySQL bounded mode served 1.89x the successful throughput at 10 VUs
and 2.10x at 100 VUs. Its p95 was 3.64x faster at 10 VUs and 1.17x at
100 VUs. The 100-VU run reached 2.35x the 10-VU successful throughput.
The absolute latency and 5xx goals remain open. The 10-VU run had zero
timeouts. The 100-VU run timed out on 33 COLD writes, 13 COLD reads,
and two page loads.

Between the starting and same-seed bounded MySQL 100-VU runs, successful
throughput rose from 35.36 to 52.29 req/s. Search p95 fell from 15.06 to
3.44 s and hot-write p95 from 15.94 to 8.02 s. COLD-write p95 rose from
21.44 to 23.72 s. These runs had separate seeds and different host load.
In the revised run, hot reads accounted for 28.5% of summed request time,
hot writes 24.2%, COLD writes 19.8%, and COLD reads 13.8%. Freshness
accounted for 2,734 HTTP 503s, sync changes for 1,016, and projection
gates for 767. The 15-second trace recorded 309 s of cumulative wait
in workspace reloads and 323 s in mutex acquisition. Stack totals can
overlap. COLD facades recorded 764 slot waits averaging 6.8 s and 82
refusals. Issue edits, discussions and creates held local writer locks
for 321, 309 and 211 s, respectively, summed across the two replicas.

| MySQL at 100 VUs | Peak RSS per replica, MiB | RSS Q3 to Q4 mean, MiB | Peak Redis, MiB | Redis Q3 to Q4 mean, MiB |
| --- | --- | --- | --- | --- |
| Off | 1,520 / 1,394 | 1,235 to 792 / 1,099 to 845 | 126.9 | 76.2 to 65.5 |
| On | 1,345 / 1,468 | 868 to 819 / 891 to 1,120 | 105.6 | 86.0 to 76.1 |

Bounded MySQL stayed below the 3 GiB target per replica over the measured
interval. Last-half RSS slopes were +12.6 and +103.5 MiB/min, so the run
does not establish an RSS plateau. Redis used 99.4 MiB at the end and
its last-half slope was -0.5 MiB/min. This is total Redis memory,
including coordination, distinct from the result-tier admission budget.
The revised enabled run recorded 1,631 search bypasses and 76 cache
misses with no hits at 100 VUs.

#### Revised result tier

Both MySQL pairs ran 10 VUs for 180 seconds per arm with bounded metadata
on, using the same seed and reversing cache arm order in trial B.

| Trial | Result tier | All / successful req/s | p95 / p99 ms | HTTP 5xx | Search bypass / miss / hit |
| --- | --- | --- | --- | --- | --- |
| A | On | 22.66 / 22.25 | 629 / 2,050 | 33 | 169 / 0 / 0 |
| A | Off | 23.67 / 23.24 | 581 / 1,663 | 37 | 0 / 0 / 0 |
| B | Off | 21.58 / 21.16 | 749 / 2,019 | 33 | 0 / 0 / 0 |
| B | On | 19.35 / 18.93 | 1,074 / 2,585 | 44 | 176 / 6 / 0 |

The enabled arms served 4.3% and 10.3% fewer requests. Trial A performed
no result-cache SQL stamp reads, Redis lookups or fills. In trial B,
one admitted search missed all six collection keys. The repeated
throughput gap leaves default-mix neutrality unproven. The counters also
rule out result-cache network calls as the source of trial A's gap.

`BenchmarkSharedCacheSearchAdmission` isolates the local admission check
from SQL and Redis. Across three runs on the rig host, the reuse gate took
105 to 130 ns per search with zero allocations. The disabled check took
4.0 to 4.7 ns. This cost is too small to account for the mixed-run gap at
approximately one search per second.

A same-seed MySQL search-only control at `d77706265` repeated one query
across non-issue metadata types, using 10 VUs, 15 seconds of warmup and
60 seconds of measurement. Cache off served 5.07 req/s at 2,222 ms p95.
Cache on served 61.64 req/s at 18 ms p95. Both had zero 5xx and timeouts.
The enabled arm counted 22,194 collection hits and zero misses during the
measurement window. The result applies to stable search terms and source
versions, which were rare in the mixed runs.

At `13289ff28`, two more MySQL trials used 10 VUs for 180 seconds per arm
with bounded metadata on. All arms used the same seed. Trial B reversed
the cache order.

| Trial | Result tier | All / successful req/s | p95 / p99 ms | HTTP 5xx | Search bypass / miss / hit |
| --- | --- | --- | --- | --- | --- |
| A | On | 22.87 / 22.54 | 648 / 2,328 | 27 (0.66%) | 196 / 24 / 0 |
| A | Off | 23.29 / 23.00 | 634 / 2,139 | 24 (0.57%) | 0 / 0 / 0 |
| B | Off | 22.11 / 21.87 | 767 / 1,738 | 19 (0.48%) | 0 / 0 / 0 |
| B | On | 19.96 / 19.66 | 1,078 / 2,614 | 18 (0.50%) | 192 / 6 / 0 |

The enabled arms served 1.8% and 9.7% fewer requests. All four arms had
zero timeouts. Median host load was 6.02 and 6.41 in trial A and 7.49 and
6.25 in trial B, in cache on/off order. The small number of result-tier
operations and variation in host load limit attribution of the throughput
gap. The mixed workload showed no search hits and consistently lower
throughput with the tier enabled. `FLOW_REDIS_CACHE` now defaults to
`off`. Explicitly enable it for workloads with stable search reuse.

The original PostgreSQL tier ran the same 180-second, 10-VU mixed
comparison twice using separate fresh seeds. Cache on and off served
11.13 and 15.76 req/s in trial A, and 19.51 and 17.87 req/s in trial B.
Overall p95 was 2,875 and 1,609 ms in trial A and 990 and 1,192 ms in
trial B, in cache on/off order. The cache-on arm had higher median host
load in A (11.3 versus 6.0) and lower in B (4.4 versus 6.3). Search hit
rates were 0.9% and 2.2%, viewer hit rates were 81% in both, and sync
had zero eligible pages. The PostgreSQL throughput effect varied across
the two trials.

#### PostgreSQL matrix

The `final-pg2-*` runs at `d77706265` used the same PostgreSQL seed and
the default mix. The 10-VU arms lasted 180 seconds each. The 100-VU arms
lasted 600 seconds each.

| VUs | Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- | --- |
| 10 | Off | 15.61 / 15.46 | 42 / 1,601 / 2,741 | 1,402 / 1,797 / 3,058 | 4 (0.14%) | 0 |
| 10 | On | 21.93 / 21.69 | 16 / 680 / 1,941 | 261 / 650 / 1,871 | 12 (0.30%) | 0 |
| 100 | Off | 34.38 / 23.47 | 2,667 / 6,697 / 11,816 | 3,239 / 11,843 / 10,586 | 6,517 (31.60%) | 0 |
| 100 | On | 43.80 / 34.22 | 848 / 7,140 / 15,026 | 3,071 / 14,139 / 18,487 | 5,600 (21.31%) | 6 |

At 100 VUs bounded mode served 1.46x the successful throughput of the
off arm and 1.58x its own 10-VU throughput. Hot reads and hot writes
accounted for 34.2% and 27.8% of summed request time. Freshness caused
4,259 of the 5,600 HTTP 5xx responses, followed by sync changes (774)
and projection gates (566). In the 15-second trace, workspace reloads
accounted for 300 s of cumulative goroutine wait, SQL connection
acquisition accounted for 149 s and the store writer lock for 128 s.
The three largest metadata search digests consumed 2,870 s of aggregate
SQL execution time. Host median one-minute load was 41.17 with bounded
metadata on and 26.39 with it off.

| PostgreSQL at 100 VUs | Peak RSS per replica, MiB | RSS last-half slope, MiB/min | Peak Redis, MiB | Redis last-half slope, MiB/min |
| --- | --- | --- | --- | --- |
| Off | 1,533 / 1,418 | -23.6 / +6.7 | 104.7 | -0.8 |
| On | 1,451 / 1,499 | +45.1 / -1.6 | 73.1 | +0.9 |

The RSS samples stayed below the per-replica target. The positive slopes
leave a plateau unverified on one replica in each arm.

#### SQLite matrix

The `final-sqlite1-*` runs at `d77706265` restored the same seed and ran
for 180 seconds each with 10 VUs and one replica.

| Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts | Peak RSS MiB |
| --- | --- | --- | --- | --- | --- | --- |
| Off | 24.73 / 24.46 | 16 / 504 / 1,486 | 374 / 827 / 995 | 6 (0.13%) | 0 | 1,014 |
| On | 23.97 / 23.61 | 9 / 552 / 1,368 | 411 / 774 / 2,331 | 16 (0.37%) | 0 | 1,303 |

SQLite used no Redis. Bounded mode reduced p50 and p99, with lower
successful throughput and higher p95. Both RSS series fell over the
last half of their 180-second measurement.

#### MySQL after the comment and activity query change

The `loop-mysql2-10x-*` runs at `463323b79` used the same seed and ran for
600 seconds with 100 VUs each.

| Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- |
| Off | 37.52 / 24.95 | 2,421 / 5,912 / 10,815 | 3,102 / 11,569 / 9,570 | 7,512 (33.37%) | 0 |
| On | 58.96 / 49.91 | 278 / 5,269 / 13,754 | 3,019 / 12,378 / 19,423 | 5,186 (14.66%) | 20 |

Relative to the earlier bounded MySQL arm, COLD read p95 fell from
15.09 to 10.05 s and COLD write p95 from 23.72 to 19.42 s. Hot write p95
rose from 8.02 to 12.38 s. The comparisons used different seeds and host
loads. Hot writes and hot reads accounted for 31.2% and 29.9% of summed
request time. Freshness caused 3,784 of 5,186 HTTP 503s. The 15-second
trace recorded 324 s of cumulative wait in realtime source cache admission
and 105 s in the writer mutex. These stack totals can overlap.

| Bounded | Peak RSS per replica, MiB | RSS last-half slope, MiB/min | Peak Redis, MiB | Redis last-half slope, MiB/min |
| --- | --- | --- | --- | --- |
| Off | 1,485 / 1,427 | +25.5 / +43.7 | 127.9 | +4.0 |
| On | 1,341 / 1,443 | -161.0 / -177.5 | 104.0 | -3.1 |

#### Closeout at `9d0d0eecb`

The `closeout-*` reports use the default mix and a fresh copy of the
topology's seed in each arm. MySQL and PostgreSQL use two replicas and
real Redis with the result tier disabled. Record hydration and journaling
are enabled. The first MySQL attempt ended before measurement after
replica 1 reported a startup deadlock. The completed retry restored the
seed before starting its replicas.

| MySQL, 10 VUs for 180 s | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- |
| Bounded off | 13.75 / 13.55 | 45 / 1,965 / 3,278 | 1,483 / 2,623 / 4,230 | 20 (0.81%) | 0 |
| Bounded on | 20.12 / 19.87 | 21 / 1,074 / 2,892 | 497 / 1,396 / 4,259 | 27 (0.75%) | 0 |

At 10 VUs, bounded mode delivered 1.47 times the successful throughput
and improved p95 by a factor of 1.83. It missed the latency and error
targets. Median host load was 9.62 with bounded off and 5.46 with it on.
The bounded arm's peak RSS was 738 / 840 MiB and Redis peaked at
33.4 MiB. Last-half trends were -200 / -161 MiB/min for RSS and
-3.7 MiB/min for Redis.

| MySQL, 100 VUs for 600 s | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- |
| Bounded off | 43.05 / 33.00 | 1,779 / 5,340 / 9,730 | 3,065 / 9,751 / 9,939 | 5,994 (23.21%) | 0 |
| Bounded on | 73.93 / 68.51 | 159 / 4,486 / 15,009 | 2,303 / 5,898 / 20,877 | 2,842 (6.41%) | 18 |

At 100 VUs, bounded mode delivered 2.08 times the successful throughput
and improved p95 by a factor of 1.19. Bounded throughput scaled 3.45 times
from 10 VUs. The 18 COLD-write timeouts comprised seven loop settings
updates, six API key creates, four API key revocations and one issue
deletion. Hot-read and hot-write p95 improved, and COLD-read p95 rose
from 4.45 to 16.15 s.

| MySQL, 100 VUs | Peak RSS per replica, MiB | RSS last-half slope, MiB/min | Peak Redis, MiB | Redis last-half slope, MiB/min |
| --- | --- | --- | --- | --- |
| Bounded off | 1,503 / 1,480 | -32.8 / -61.9 | 120.8 | +1.3 |
| Bounded on | 1,562 / 1,402 | +114.7 / +85.1 | 125.8 | +4.2 |

Both bounded replicas were under the 3 GiB metadata target and Redis was
under the 256 MiB container limit. The positive last-half slopes leave
the plateau target open.

Freshness accounted for 1,205 bounded 503s, projection gates for 985
and sync changes for 651. Hot reads, hot writes, COLD reads and COLD
writes accounted for 23.7%, 21.6%, 16.9% and 26.8% of summed request time.
The projection gate accounted for 897 of the COLD write 503s. Issue
updates alone took 5,565 s of summed request time at 5.40 s p95.
The bounded arm logged 1,115 COLD slot waits averaging 6.9 s, with 45
refusals. On the two replicas, the local writer lock averaged 2.22 and
1.05 s per wait. Issue updates and discussions held the locks for 311
and 276 s across the two replicas. A 15-second trace captured 216 s of
cumulative workspace reload wait and 131 s of local writer lock wait.
The top MySQL statement digests were metadata search shell reads (452 s),
search candidate sorts (380 s) and shared-issue queries (367 s).
The CPU sample spent 4.70 of 22.20 CPU seconds in `pread`, including
4.60 in metadata spool reads. Average COLD facade decoding took 104 and
59 ms on the two replicas. Those averages were much smaller than the
slot waits. The profile does not isolate connection-pool saturation or
the share of the response time spent in JSON encoding and GC.

The earlier 100-VU bounded arm at `463323b79` delivered 49.91 successful
req/s with 5,269 ms p95 and 14.66% 5xx, including 3,784 freshness 503s.
The closeout improved those figures while p99 rose from 13,754 to
15,009 ms. Different seeds and median host loads (30.46 earlier, 17.21
at closeout) limit attribution to any one change.

The same-seed `FLOW_RECORD_PEER_SYNC=on` control at 100 VUs served
64.48 successful req/s with 5,243 / 17,960 ms p95 / p99, 5.88% 5xx
and 128 timeouts. Freshness 503s fell from 1,205 to 1,022 and sync 503s
from 651 to 399. COLD-write p95 rose from 20.88 to 30.00 s. Median host
load was 24.28 in the peer-sync arm and 17.21 with peer sync off.
The 10-VU peer-sync control at `13289ff28` returned 13 freshness 503s
in both modes and raised p95 from 648 to 951 ms. Keep peer sync at the
existing default for this rig.

#### PostgreSQL closeout

The `closeout-pg2-*` arms restored one seed, with real Redis coordination
and the result tier off.

| VUs / seconds | Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts |
| --- | --- | --- | --- | --- | --- | --- |
| 10 / 180 | Off | 16.01 / 15.80 | 39 / 1,714 / 2,633 | 1,275 / 1,948 / 3,476 | 12 (0.42%) | 0 |
| 10 / 180 | On | 22.87 / 22.56 | 16 / 647 / 1,804 | 125 / 551 / 2,205 | 9 (0.22%) | 0 |
| 100 / 600 | Off | 35.73 / 25.67 | 2,479 / 6,408 / 10,905 | 3,165 / 10,881 / 10,654 | 6,000 (27.99%) | 0 |
| 100 / 600 | On | 49.40 / 40.79 | 525 / 6,247 / 15,017 | 3,037 / 13,364 / 20,120 | 4,968 (16.76%) | 8 |

Bounded mode delivered 1.43 times the successful throughput at 10 VUs
and 1.59 times at 100 VUs. Its p95 improved by a factor of 2.65 and 1.03,
respectively. Bounded throughput scaled 1.81 times from 10 VUs. Freshness
503s fell from 5,199 to 3,667 at 100 VUs. All eight bounded timeouts were
COLD writes. Hot reads and hot writes together accounted for 61% of
summed bounded request time. The 15-second bounded trace captured 405 s
of cumulative workspace reload wait and 92 s of local writer lock wait.
Median host load during the two 100-VU arms was 22.48 off and 30.82 on,
with an 84.36 peak in the bounded arm.

| PostgreSQL, 100 VUs | Peak RSS per replica, MiB | RSS last-half slope, MiB/min | Peak Redis, MiB | Redis last-half slope, MiB/min |
| --- | --- | --- | --- | --- |
| Bounded off | 1,459 / 1,315 | +26.8 / -4.0 | 109.3 | 0.0 |
| Bounded on | 1,114 / 1,322 | -34.7 / -3.9 | 73.5 | +1.8 |

Bounded RSS fell on both replicas in the last half of the run. Redis
averaged 60.2 MiB in the third quarter and 63.9 MiB in the fourth,
leaving its plateau unverified.

#### SQLite closeout

The `closeout-sqlite1-*` arms restored one seed and ran for 180 seconds
each at 10 VUs on one replica without Redis.

| Bounded | All / successful req/s | p50 / p95 / p99 ms | Hot read / hot write / COLD write p95 ms | HTTP 5xx | Timeouts | Peak RSS MiB |
| --- | --- | --- | --- | --- | --- | --- |
| Off | 24.64 / 24.35 | 17 / 573 / 1,456 | 410 / 763 / 588 | 7 (0.16%) | 0 | 939 |
| On | 24.87 / 24.62 | 8 / 582 / 1,350 | 395 / 654 / 967 | 6 (0.13%) | 0 | 1,046 |

Successful throughput and overall p95 were close across arms. Bounded
mode improved hot-write p95 by 14%. COLD-write p95 rose by 64% and
realtime delivery lag p95 rose from 981 to 4,372 ms. The bounded arm's
last-half RSS slope was -59.5 MiB/min, versus +62.2 MiB/min with the
flag off. Both series are from short runs.

#### MySQL COLD slots

The `closeout-mysql2-10x-cold-slots-*` control ran 100 VUs for 600
seconds per arm on one restored seed with bounded metadata on. At
3 GiB per replica, the default is three COLD facade slots. The second
arm set `FLOW_COLD_SLOTS=4`. Both arms used six COLD projection slots.

| COLD facade slots | All / successful req/s | p50 / p95 / p99 ms | COLD read / write p95 ms | HTTP 5xx | Timeouts | Slot waits / mean wait / refusals |
| --- | --- | --- | --- | --- | --- | --- |
| 3 | 69.20 / 62.15 | 211 / 4,680 / 12,762 | 14,046 / 19,158 | 3,936 (9.48%) | 3 | 838 / 6.77 s / 16 |
| 4 | 72.96 / 64.85 | 227 / 4,483 / 8,235 | 4,507 / 13,709 | 4,552 (10.40%) | 0 | 570 / 2.84 s / 3 |

The fourth slot cut COLD wait time and improved overall p99 by 35%.
Successful throughput increased by 4.3%. Freshness 503s rose from
2,523 to 3,093. Host median load was 47.90 and 46.52 with three and four
slots, respectively, and peak load was 77.81 and 64.46. The higher 5xx
rate and variation across the shared host favor the three-slot default
for the mixed workload.

| COLD facade slots | Peak RSS per replica, MiB | RSS last-half slope, MiB/min | Peak Redis, MiB | Redis last-half slope, MiB/min |
| --- | --- | --- | --- | --- |
| 3 | 1,259 / 1,335 | +27.7 / +17.3 | 106.5 | +1.0 |
| 4 | 1,376 / 1,379 | -45.5 / -5.1 | 146.9 | +0.1 |

#### Remaining bottlenecks

MySQL reached 3.45x successful throughput scaling against the 5x
target. Sustained p95 and p99 were 4.49 and 15.01 s against 1 and 2 s,
and 5xx and timeouts exceeded the budgets. The peer sync and COLD slot
controls shifted errors and wait times across request classes. The
shorter COLD wait at four slots still left COLD writes at 13.71 s p95.

The next release decision is whether to fund record-backed authorization,
reads and writes for the remaining aggregate-based routes, starting with
API keys and loop settings, and reduce the work on the writer
and peer publication paths. Those changes need to preserve live permission
checks and journal ordering. The acceptance rerun needs a dedicated rig
to measure the 100-VU targets and memory plateau without shared host
load spikes.

#### Verification

The checks ran from `api/` through the host's `heavy.sh` wrapper.

| Check | Exit |
| --- | --- |
| `go vet ./...` | 0 |
| `go vet ./internal/store ./cmd/server ./internal/coordination` | 0 |
| `go test ./internal/store -run 'SW318\|SW559\|SW602\|Coverage\|Inventory\|ReviewedRoutes\|ActivationGuard' -count=1` | 0 |
| `go test ./cmd/server -run 'SW211\|SW275\|SW480\|SW530\|SW531\|SW550\|SW557\|SW558\|SW559' -count=1` | 0 |
| Redis configuration, SQLite differentials and focused concurrency tests with `-race` | 0 |
| `go test -tags=integration ./internal/store ./cmd/server -run <closeout selector> -count=1 -v` on MySQL 8.4 and PostgreSQL | 0 on each |
| `go test -tags=integration ./internal/coordination -run '^TestExternalRedisSharedCacheRoundTrip$' -count=1 -v` | 0 |

The reports directory contains command records and test outputs in
`closeout-*.log` and `closeout-*.summary.txt`. The SQLite checks cover
reload publication, realtime versions and revocation, comment and
activity ordering, and sync retries with complete journal transactions.
The final external selector ran seven tests per database, covering
realtime source admission, authorization and credential scopes, document
events and sync pages raced by a write. The prior external runs cover
search ordering and visibility, issue conflict retries, application
access, comment and activity ordering, and peer reload publication.
