# Local-first sync: plan

Status: proposal (no code yet). Owner: web client. Builds on the repeat-visit snapshot paint and the app-shell
service worker (`web/src/lib/local-snapshot.ts`, `web/public/sw.js`).

## Goal

Open the app from local state: the first frame of every repeat visit renders from data on the device, the main
thread makes no data request before that frame, and the server is asked only for what changed since the device
last synced. Large collections (issue history, comments, archived and old items) are loaded lazily and partially,
so a large workspace does not need a large download or a large heap.

Today (after the snapshot paint landed) a repeat visit paints from one per-workspace JSON blob, then downloads the
whole bootstrap again and replaces the blob. That removes the blank screen, but the bytes on the wire, the parse
cost and the server's projection cost are the same as a cold start, and the blob caps out at 8 MB.

Non-goals: offline editing for long periods, peer-to-peer sync, changing the server's permission model.

## Where we start from

| Piece | Today | Reused as |
| --- | --- | --- |
| Change log | `domain_events` with a gap-free per-workspace `seq` (`api/internal/store/change_log.go`), kept at least 7 days / 10,000 rows | the delta source |
| Change cursor | bootstrap sends `X-Flow-Change-Cursor`; `GET /api/realtime/changes?since=` replays, answers `resyncRequired` when the cursor is gone | the resume protocol |
| Sync v2 | manifest (`schema`, `viewerId`, `authzEpoch`, `head`, `collections`), `GET /api/sync/records`, `POST /api/sync/batch`, `GET /api/sync/changes` (behind `VITE_SYNC_V2`) | per-entity record transport |
| Realtime | SSE stream, one leader tab per origin/workspace/viewer rebroadcasting (`shared-realtime.ts`) | live deltas |
| Bounded metadata (P21) | server never materialises a whole workspace per request; projections behind a read gate that answers 503 + `Retry-After` | the reason partial bootstrap must exist |
| Snapshot paint | one JSON blob per viewer+workspace in IndexedDB, keyed by schema and build, cleared through `session-cache-registry` | replaced by the store below in phase 3 |

## Storage layout

One IndexedDB database per viewer and workspace: `flow-ls:<viewerId>:<workspaceId>`. Separate databases make
"clear this viewer" and "clear this workspace" a `deleteDatabase` call and keep one workspace's upgrade from
blocking another.

Object stores:

- **One store per entity type**, keyed by `id`: `issue`, `team`, `user`, `workflowState`, `label`, `labelGroup`,
  `project`, `projectMilestone`, `initiative`, `cycle`, `document` (metadata only), `customer`, `savedView`,
  `favorite`, `notification`, `comment`, `activity`, `attachment`, ... Each value is the wire record plus
  `_v` (server row version) and `_seq` (change seq that last wrote it). Indexes only where a first frame needs a
  lookup without a scan: `issue.teamId`, `issue.stateId`, `issue.assigneeId`, `issue.projectId`, `issue.parentId`,
  `comment.issueId`, `activity.issueId`, `notification.createdAt`.
- **Partial-index stores** (`_partial_<type>`), one row per loaded slice of a lazily loaded collection:
  `{ key: "comment:issue:<issueId>", complete: boolean, cursor, loadedAt, seq }`. A slice says which subset is
  present (all comments of one issue, the first page of archived issues of a team, the history of one issue).
  Readers treat a missing or incomplete slice as "ask the server", never as "empty".
- **`_meta`**: one row per workspace: `schema` (client storage schema), `build`, `viewerId`, `workspaceId`,
  `authzEpoch`, `head` (last applied change seq), `bootstrapAt`, `fullCollections` (which entity types are complete),
  `bytes` (running estimate), `lastAccessAt`.
- **`_outbox`**: writes not yet acknowledged (see conflicts), so a reload never loses an edit that was accepted
  in the UI.

Text bodies (issue descriptions, document content) are not part of the store; they keep their own loaders and
collaboration channel. The store holds the summary record and `bodyVersion` so a stale cached body is never shown.

## Bootstrap protocol

Three requests, chosen by what `_meta` says:

1. **Full bootstrap** (no store, schema or build mismatch, `authzEpoch` changed, cursor older than retention):
   `GET /api/sync/manifest` then paged `GET /api/sync/records?kind=` for the *eager* types (workspace directory:
   users, teams, states, labels, projects, cycles, views, favorites; active issues of the viewer's teams), each
   page bounded by `maxBytes`. Lazy types are not fetched. The manifest's `head` becomes `_meta.head` only after
   every eager page is stored, in the same transaction as the last page.
2. **Partial bootstrap** (store valid, cursor within retention): `GET /api/sync/changes?after=<head>` until
   `hasMore` is false. No snapshot download at all.
3. **Lazy load** (a surface needs a slice): `GET /api/sync/records?kind=comment&filter=issue:<id>` (or `/batch`
   for up to 50 keys), stored with a `_partial` row carrying the `seq` the server answered at. Deltas after that
   seq update the slice; a slice is complete only if the server says so.

Versioning:

- `_meta.schema` is the client storage schema. A bump runs an `onupgradeneeded` migration when it is additive
  (new store, new index) and drops the database when it is not (shape change of an existing store). Dropping is
  always safe: the fallback is a full bootstrap.
- The wire schema is the manifest's `schema`; an unknown value means full bootstrap with the legacy endpoints.
- `build` is kept for diagnostics only once the store is versioned; the blob snapshot's "any new build discards it"
  rule goes away in phase 3.

## Delta sync

- The change log is the single ordered source. Every applied change advances `_meta.head` in the same IndexedDB
  transaction as the records it wrote, so the store and its cursor can never disagree (the rule the snapshot
  paint gets by never patching its blob).
- Realtime (SSE) carries the same seqs. The leader tab applies an event only if `seq == head + 1`; a gap triggers
  `GET /api/sync/changes?after=head` before continuing. Duplicates (`seq <= head`) are ignored, so replay after a
  reconnect is idempotent.
- `resyncRequired` (cursor older than retention, server restore) discards the store for that workspace and runs a
  full bootstrap; the UI keeps rendering the old store until the new one commits, then swaps (same rule as today:
  stale data is shown only while its replacement is in flight, never after a refusal).
- Events that are not in the log (presence, agent streaming, coding sessions, collaboration) keep their own
  channels and never touch `head`.

## Conflicts and writes

- Writes go through `_outbox` with the record's `_v` as the base version and are sent by the sync owner in order.
  The UI applies them optimistically on top of the store (an overlay, not a store write) so a failure rolls back
  by dropping the overlay entry.
- Server answers: success returns the new record and seq (applied like any delta); `409` with `current` replaces
  the base and surfaces the existing conflict UI (field-level merge where the server already does it, otherwise
  "someone else changed this"); `403/404` drops the overlay and the record.
- Writes made before the first sync of a session completes wait (the rule the snapshot paint introduced in
  `snapshot-write-gate.ts`); with a versioned store the wait shrinks to "until `changes?after=head` caught up",
  usually one round trip.
- Text bodies stay on the collaboration channel (CRDT), outside this model.

## Permission revocation

- `_meta.authzEpoch` mirrors the manifest. Membership, team access, sharing and role events in the log carry the
  new epoch; a changed epoch makes the client re-read the manifest and run a scoped reset: every store loses the
  records of the scopes the viewer lost (team, project, shared issue), partial slices of those scopes are deleted,
  and anything else is kept.
- `401` anywhere: the session is gone; all databases of that viewer are deleted before the sign-in page renders
  (today's `clearNavigationCache` path, extended to `indexedDB.deleteDatabase`).
- `403/404` for the workspace: delete that workspace's database, today's no-access screen.
- Logout, account switch and "remembered viewer differs from the signed-in viewer" delete every database of the
  other viewer, as the snapshot paint does now. A localStorage index of readable databases keeps deletion
  effective synchronously.

## Eviction

- Budget per origin: 200 MB by default, lowered when `navigator.storage.estimate()` reports less than 1 GB free;
  `navigator.storage.persist()` is requested once the user has opened a workspace on three different days.
- Order: drop `_partial` slices by `lastAccessAt` (comments and history first, then archived pages), then whole
  workspaces not opened for 30 days, then the oldest workspace. Eager stores of the current workspace are never
  evicted while it is open.
- Quota errors during a write abort that transaction (head does not advance), run eviction and retry once; a second
  failure switches the session to network-only mode for that workspace.

## Multi-tab coordination

- One sync owner per viewer+workspace, elected with Web Locks (`navigator.locks.request('flow-sync:<v>:<w>')`).
  The owner holds the SSE connection (replacing today's leader election in `shared-realtime.ts`), runs catch-up and
  the outbox, and writes IndexedDB.
- Other tabs read IndexedDB directly and learn about commits through a `BroadcastChannel('flow-sync:<v>:<w>')`
  message `{ head, stores }` sent after each committed transaction; they re-read only the affected keys.
- Tabs send writes to the owner over the channel; when the owner dies the lock passes to the next tab, which
  resumes from `_meta.head` and the persisted outbox.
- Without Web Locks (old browsers) the client falls back to today's per-tab behaviour (no shared store).
- SharedWorker is not required; it can host the owner later if main-thread cost of applying deltas shows up in
  profiles.

## Interaction with bounded metadata (P21)

- The server stops building whole-workspace projections on the hot path: partial bootstrap is a change-log read,
  and full bootstrap is paged and byte-bounded per record kind, so it fits the same read gate (`503 + Retry-After`
  is retried with jitter, as `requestRetryingBusy` does now).
- Server-side per-record versions and the record journal (`FLOW_RECORD_JOURNAL`) become required for the
  partial path; the manifest advertises them in `capabilities`, and the client keeps the legacy bootstrap when a
  capability is missing.
- Counts that the server computes over whole collections (sidebar badges, view counts) stay server-provided until
  the client has the complete collection (`fullCollections`), so partial data never produces a wrong count.

## Rollout

Behind `VITE_LOCAL_STORE` (client) and the existing sync-v2 server flags, per workspace with a server-side
allowlist. Each phase ships only when its gate holds for a week on the allowlist.

| Phase | Scope | Gate |
| --- | --- | --- |
| 0 (done) | Snapshot paint + app-shell worker | repeat-visit time to content below bootstrap time; no parity diffs |
| 1 | Store + `_meta` written in the background from today's bootstrap and deltas; nothing reads it | store and in-memory data identical in a checksum compared on idle (mismatch rate < 0.1%); write cost < 8 ms p95 per delta batch |
| 2 | Partial bootstrap: start from the store, `changes?after=head` instead of `/api/bootstrap` | bootstrap bytes per repeat visit down > 90%; resync rate < 2%; zero permission-revocation test failures |
| 3 | Store becomes the source of rendering; snapshot blob removed | time to content p75 < 300 ms on repeat visits; heap no larger than today at 50k issues |
| 4 | Lazy collections: comments, history, archived issues from `_partial` slices | first issue-detail open < 150 ms from the store; eviction keeps origin usage under budget |
| 5 | Web Locks sync owner, BroadcastChannel fan-out, persisted outbox | one SSE per viewer+workspace across tabs; no lost writes across reloads in the e2e matrix |

Kill switch at every phase: the flag off deletes the databases on next start and returns to the current path.

## Risks

- **Divergence**: a delta applied twice or skipped leaves the store wrong without anyone noticing. Mitigation:
  seq-ordered application in the cursor's transaction, the phase 1 checksum, and a periodic (daily) manifest
  `head` + per-collection count comparison.
- **Permission leaks**: data of a revoked scope surviving locally. Mitigation: epoch-scoped resets, delete on
  401/403/404, the security e2e suite extended per phase; nothing is shown from the store for a scope whose epoch
  is older than the manifest's.
- **Storage limits and eviction by the browser**: Safari evicts after inactivity; private modes have no IndexedDB.
  Mitigation: every read path tolerates an empty store (network fallback), persistence request, budgets.
- **Migrations**: a bad migration bricks startup. Mitigation: migrations are additive or drop the database; a
  start that fails twice in a row deletes the store and starts from the network.
- **Main-thread cost** of applying large delta bursts (bulk edits). Mitigation: batched transactions, applying in
  idle slices, moving the owner to a worker if profiles demand it.
- **Server cost of retention**: partial bootstrap depends on the change log covering the gap; long-idle devices
  fall back to full bootstrap. Mitigation: keep the 7-day/10k-row floor; measure the resync rate in phase 2.
- **Two models during migration** (in-memory `BootstrapData` and the store). Mitigation: phases 1–2 keep
  `BootstrapData` as the rendering source and derive it from the store, so screens and pixel parity do not change
  until phase 3.
