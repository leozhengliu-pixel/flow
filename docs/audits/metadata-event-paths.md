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
| `migration.previewed` | Metadata | New job, source bundle and preview audit; bundle is still stored by `writeImportInputs`, not discarded with omitted discussions | Bookkeeping |
| `migration.mappings_updated` | Metadata | Job/status validation and mapping edits; snapshot comparison still suppresses identical edits | Bookkeeping |
| `migration.users_invited` | Metadata | Current job mapping patches/timestamp; invitations and email happen separately before this callback | Bookkeeping |
| `integration.token_updated` | Metadata | Connection config/scopes/status/timestamps; probe and encrypted credential storage already finished | Bookkeeping |
| `integration.token_rotated` | Metadata | Connection rotation metadata/scopes/status; remote rotation and credential storage happen separately | Bookkeeping |
| `integration.token_rotation_failed` | Metadata | Connection config failure reason/timestamp; unchanged config still returns `ErrNoMutation` | Bookkeeping |
| `integration.token_rotation_updated` | Metadata | Connection rotation enable/schedule config/timestamp; unchanged config still returns `ErrNoMutation` | Bookkeeping |
| `oauth_sync_group.approved` | Metadata | Matching application's sync request, status/team/reviewer/time; no issue/team-membership cascade | Bookkeeping |
| `oauth_sync_group.denied` | Metadata | Same request lookup and decision fields; invalid decision and missing request errors unchanged | Bookkeeping |

No dedicated narrow transaction is needed for these callbacks: they use the
existing metadata record delta writer, including its cross-replica rebase and
retry behavior. Domain events, previous values, realtime enrichment, cache
invalidation and publishing remain on the same writer. In particular, nil
issue/content fields leave the corresponding issue/discussion/delivery rows
untouched.
The exception is intentional source-input storage: a preview still writes its
new `migration_input` record through `writeImportInputs`. The performance table's
issue/content column counts issue, comment, activity, notification and delivery
rows, not source-input records; all input writes remain in total row operations.

## Wider Inventory

The server scan covers 445 `MutateWorkspace`/`MutateWorkspaceWithAggregate`
call sites: 375 distinct literal event names and 24 dynamic calls. Dynamic
branches and helper callers were followed too, including the four token events
passed into the connection-saving helper and the two validated sync decisions.
Those six omissions were not in the original audit table. The remaining
literal/dynamic callbacks already select metadata, issue-record, discussion or
bounded mutation scopes, or need the collections listed below.

| Remaining Events / Callbacks | Decision | Reason |
| --- | --- | --- |
| `workspace_invitation.accepted`, `workspace_invite_link.joined` | Full | Callback returns `IssueCount: len(workspace.Issues)`; allowlisting would silently return zero. A future change needs a durable count query, not empty issues. |
| `coding.approval_requested` | Full | Deduplicates existing inbox notifications and creates deliveries; metadata omits both collections. |
| `notification.desktop_acknowledged`, `notification.delivery_retried` | Full | Reads/updates delivery content records, including recipient/status checks; needs a dedicated delivery scope. |
| `notification.welcome_sent`, `notification.welcome_test` | Full | Welcome notification deduplication reads existing notification content. |
| `import.committed`, `migration.executed`, `migration.rolled_back` | Full | Imports/rollbacks read or cascade through issues, comments and other related entities, not just job status. |
| Three remote-migration bookkeeping callbacks in `api/cmd/server/migration_import.go` | Full, deferred | These are additional metadata candidates (job snapshot/status, preserved bundle, completion audit), not issue-dependent callbacks. Left unchanged to respect the restriction on their existing event identifiers. |
| Issue/discussion, workflow/cycle/label cascades, document content/notification writes, review activity/sync/import, release CI and trash callbacks | Existing bounded paths, with existing full fallback for broad scopes | Issue validation, progress/automation, cascades, activities and notification ownership must retain their declared record scopes. |

`TestAuditedMetadataCallbackReads` parses the production call sites and guards
the 15 direct callbacks against reads of omitted issue/content fields. It does
not pretend to prove helper behavior: ownership, ACL/inherited-team scope,
template validation, audit and remote-result helpers were read and are exercised
by the production-handler comparisons. The six dynamic events have individual
dispatcher/full-path cases as well. No generic event prefix was allowlisted.

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
| Hot / Before Full | 6 | 7,328 | 60 | 0 | 59,710.646 | 59,714.142 | 576.82 |
| Hot / After Metadata | 6 | 165 | 42 | 0 | 101.674 | 104.015 | 3.40 |
| Records / Before Full | 6 | 7,329 | 63 | 0 | 62,502.599 | 62,507.082 | 576.89 |
| Records / After Metadata | 6 | 169 | 48 | 0 | 84.779 | 88.829 | 3.48 |
| Bookkeeping / Before Full | 9 | 10,995 | 92 | 0 | 113,287.871 | 113,292.257 | 577.34 |
| Bookkeeping / After Metadata | 9 | 248 | 66 | 0 | 119.467 | 122.612 | 3.59 |

Observed in `/tmp/codex-metadata-event-paths.7ZjD5z` on 2026-11-02. SQL command
counts include transaction and execute commands for the application account,
excluding prepare/close and the observer connection. MySQL triggers count
INSERT/UPDATE/DELETE row operations on every application table, including event
sequencing and cache bookkeeping. Peak heap is Go `HeapAlloc` sampled every
millisecond, not RSS. Lock time is the existing store trace inside the Redis
workspace lock. No timeout, threshold or resource limit was increased.
This final run remeasured all three groups with the same source-input fixture.
Earlier verified hot/records runs are retained under
`/tmp/codex-metadata-event-paths.NmIpDY` and
`/tmp/codex-metadata-event-paths.vP4T05` respectively.

## Regression Coverage

`TestAuditedMetadataEventsAvoidIssueHydration` has a subtest per converted event:
invalid issue-record JSON makes an accidental full hydration fail before the
callback, and the dispatch trace must be `metadata`. The companion full-path
comparison checks all metadata/search rows, stored domain event contents,
webhook previous values and realtime envelopes against
`ForceFullMutationsForTesting(true)`, ignoring only event IDs and creation times.
Callback errors and `ErrNoMutation` must not write entity or domain-event rows.
The comparison also checks stored migration source-input bytes. The direct
membership-count callbacks have explicit tests that require loaded issues;
silently treating them as metadata-only would fail instead of returning zero.

`TestMetadataEventHandlerFullPathParity` runs the production feedback, approval,
undo and key-rotation/revocation scenarios on both paths, preserving their HTTP,
authorization and validation assertions. No REST, schema or web change is needed.
It also runs document upload/link/update/delete and private-team ACL scenarios,
intake validation/conflict scenarios, credential authentication after rotation,
and member-attributed review writeback/failure/retry scenarios on both paths.
Repeated production feedback/intake handlers retain timestamps, write zero
observed entity/domain-event rows and publish zero realtime/webhook envelopes.
Migration handlers cover preview audit/source retention, mapping edits and
unchanged mappings, separate durable invitations and missing-job errors. Sync
decision handlers and a scripted token lifecycle cover the six dynamic events.

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
The final all-event run also checks all preview source bytes/audit entries and
all token/sync-request fields, including concurrent changes to the same records.

These are synthetic dispatcher measurements, not production endpoint latency
predictions. Generic metadata writes still clone metadata and share the local
and distributed workspace writer locks. Uploads, remote actions and handler
reads before the mutation remain unchanged. No new narrow transaction, REST
shape, schema, timeout, threshold or resource limit is introduced.

| Group | Database | Requests | Busy Errors | Lost Updates | Run Time |
| --- | --- | ---: | ---: | ---: | ---: |
| Hot | MySQL (75k) | 168 | 0 | 0 | 9.905 s |
| Hot | PostgreSQL (small) | 168 | 0 | 0 | 6.634 s |
| Hot | SQLite (small) | 168 | 0 | 0 | 4.513 s |
| Hot + Records | MySQL (75k) | 312 | 0 | 0 | 10.105 s |
| Hot + Records | PostgreSQL (small) | 312 | 0 | 0 | 8.247 s |
| Hot + Records | SQLite (small) | 312 | 0 | 0 | 9.699 s |
| All 21 | MySQL (75k) | 528 | 0 | 0 | 16.378 s |
| All 21 | PostgreSQL (small) | 528 | 0 | 0 | 14.233 s |
| All 21 | SQLite (small) | 528 | 0 | 0 | 13.907 s |

The final end-to-end script exited 0 and removed its three containers and their
volumes. Its MySQL issue/discussion digest before and after was
`0bd7f8266fef53df3c487e0401d44e4d03b3baa075ad764c2495d0e9739bab9c`.
PostgreSQL/SQLite seed a small issue/comment/activity fixture; they are shared-path
correctness checks, not 75k scale claims. All providers and callbacks are scripted.

Verification commands, all exit 0:

```sh
bash scripts/perf/metadata-event-paths.sh
cd api
~/.flow-local/heavy.sh env GOMAXPROCS=2 go test -p 1 ./internal/store ./cmd/server -run '^(TestAuditedMetadata.*|TestIssueCountCallbacksKeepLoadedIssues|TestMetadataEventHandlerFullPathParity)$' -count=1
~/.flow-local/heavy.sh env GOMAXPROCS=2 go vet -p 1 ./...
```
