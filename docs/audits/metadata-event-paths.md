# Metadata Event Write Paths

## Callback Decisions

The metadata path still uses the serialized workspace writer, but skips issue,
comment, activity, notification and delivery hydration and replacement. Those
collections are nil in its callback, not lazily loaded: reading an omitted
collection silently looks empty. `WithMetadataFields` additionally hides
unlisted metadata fields. None of these conversions uses that field restriction.
The decisions below follow callback/helper reads, not event prefixes.

| Event | Decision | Callback Reads And Changes | Group |
| --- | --- | --- | --- |
| `release_pipeline.access_key_used` | Metadata | Pipeline last-used timestamp only; existing pre-write minute throttle unchanged | Hot |
| `agent.message_feedback` | Metadata | Owned session and workspace usage-feedback policy; nested reply feedback; identical feedback returns `ErrNoMutation` | Hot |
| `agent.session_feedback` | Metadata | Owned session and feedback policy; appends conversation feedback without changing conversation order/read state | Hot |
| `agent.message_changes` | Metadata | Owned session message parts; target changes and their events already committed through separate operations | Hot |
| `agent.approval_resolved` | Metadata | Owned session question/approval decision and already-handled validation; held tools run separately afterwards | Hot |
| `release_pipeline.access_key_revoked` | Metadata | Pipeline and effective team administration metadata; credential/grace timestamps; unchanged errors and public response | Hot |
| `document.attachment_created` | Metadata | Document, ACL, viewer role/membership, teams/settings and persisted inherited-team scope; appends attachment/creator | Records |
| `document.attachment_saved` | Metadata | Same authorization; creates or updates a same-URL link attachment and timestamp | Records |
| `document.attachment_deleted` | Metadata | Same authorization and missing-attachment validation; removes attachment; object deletion remains outside callback | Records |
| `email_intake.updated` | Metadata | Address, teams and standard templates; forwarding-address uniqueness/domain verification; identical edits return `ErrNoMutation` | Records |
| `oauth_application.secret_rotated` | Metadata | Application credentials/authorizer/timestamp; credential audit remains a separate unchanged call | Records |
| `review.written_back` | Metadata | Review events, remote references, synchronization results and duplicate reconciliation; remote push already finished | Records |

No dedicated narrow transaction is needed for this group: callbacks use the
existing metadata record delta writer, including its cross-replica rebase and
retry behavior. Domain events, previous values, realtime enrichment, cache
invalidation and publishing remain on the same writer. In particular, nil
issue/content fields cause persistence to leave those tables untouched.

## Measurement

Rerun from the worktree root:

```sh
bash scripts/perf/metadata-event-paths.sh
```

The gate owns uniquely named throwaway MySQL, PostgreSQL and Redis containers,
uses the machine-wide heavy-command lock, and removes only those containers and
their volumes. All generated databases, binaries and reports are under its
printed `/tmp/codex-metadata-event-paths.*` directory. No provider calls occur.
The MySQL fixture contains 75,000 issues, 75,000 comments and 75,000 activities.
Each group measures one changed callback per event in a separate process for
each path. Baselines append a test-only event suffix to bypass the new allowlist
without enabling the forced-full switch's legacy full metadata persistence.
The suffix has no event-specific persistence behavior for these callbacks.

| Group / Path | Events | SQL Commands | Row Operations | Issue/Content Rows Written | Lock Held (ms) | Elapsed (ms) | Peak Heap (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Hot / Before Full | 6 | 7,320 | 60 | 0 | 56,684.197 | 56,687.361 | 574.25 |
| Hot / After Metadata | 6 | 166 | 42 | 0 | 76.160 | 78.509 | 3.38 |
| Records / Before Full | 6 | 7,328 | 63 | 0 | 54,825.575 | 54,830.220 | 582.95 |
| Records / After Metadata | 6 | 172 | 48 | 0 | 99.637 | 104.549 | 3.35 |

Observed in `/tmp/codex-metadata-event-paths.NmIpDY` on 2026-11-01. SQL command
counts include transaction and execute commands for the application account,
excluding prepare/close and the observer connection. MySQL triggers count
INSERT/UPDATE/DELETE row operations on every application table, including event
sequencing and cache bookkeeping. Peak heap is Go `HeapAlloc` sampled every
millisecond, not RSS. Lock time is the existing store trace inside the Redis
workspace lock. No timeout, threshold or resource limit was increased.
The records group was measured in `/tmp/codex-metadata-event-paths.vP4T05`.

## Regression Coverage

`TestAuditedMetadataEventsAvoidIssueHydration` has a subtest per converted event:
invalid issue-record JSON makes an accidental full hydration fail before the
callback, and the dispatch trace must be `metadata`. The companion full-path
comparison checks all metadata/search rows, stored domain event contents,
webhook previous values and realtime envelopes against
`ForceFullMutationsForTesting(true)`, ignoring only event IDs and creation times.
Callback errors and `ErrNoMutation` must not write entity or domain-event rows.

`TestMetadataEventHandlerFullPathParity` runs the production feedback, approval,
undo and key-rotation/revocation scenarios on both paths, preserving their HTTP,
authorization and validation assertions. No REST, schema or web change is needed.
It also runs document upload/link/update/delete and private-team ACL scenarios,
intake validation/conflict scenarios, credential authentication after rotation,
and member-attributed review writeback/failure/retry scenarios on both paths.
Repeated production feedback/intake handlers retain timestamps, write zero
observed entity/domain-event rows and publish zero realtime/webhook envelopes.

## Two Processes

Two independent test API processes share the database and Redis with the
unchanged five-second workspace-lock wait. Gate-only HTTP routes execute
deterministic callbacks for each converted event; draft requests use the normal
API route. These are dispatcher/concurrency tests, not substitutes for the
production-handler tests. Twelve rounds launch every event plus a draft save
on each process concurrently. Reopening the database verifies every changed
field, every appended feedback entry, and both final draft titles/bodies.
SHA-256 digests verify all seeded issue/comment/activity bytes are unchanged.
Children are stopped by PID and waited for on success or failure.

| Group | Database | Requests | Busy Errors | Lost Updates | Run Time |
| --- | --- | ---: | ---: | ---: | ---: |
| Hot | MySQL (75k) | 168 | 0 | 0 | 9.905 s |
| Hot | PostgreSQL (small) | 168 | 0 | 0 | 6.634 s |
| Hot | SQLite (small) | 168 | 0 | 0 | 4.513 s |
| Hot + Records | MySQL (75k) | 312 | 0 | 0 | 10.105 s |
| Hot + Records | PostgreSQL (small) | 312 | 0 | 0 | 8.247 s |
| Hot + Records | SQLite (small) | 312 | 0 | 0 | 9.699 s |

Verification commands, all exit 0:

```sh
bash scripts/perf/metadata-event-paths.sh
cd api
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./internal/store ./cmd/server -run '^(TestAuditedMetadataEvents.*|TestMetadataEventHandlerFullPathParity|TestMetadataEventReplicaProcess|TestMetadataEventTwoReplicaGate)$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet -p 1 ./...
```
