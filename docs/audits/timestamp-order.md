# Timestamp ordering audit

Go's `RFC3339Nano` drops trailing zeros. At the same second, `.3716Z`
compares before `.371Z` as text. Whole-second values have the same problem.
`formatStoredTime` writes nine fractional digits in UTC so timestamp columns,
cutoffs, and keysets can use normal indexed text comparisons.

## SQL timestamp columns

File names in this section are relative to `api/internal/store`. The audit
followed SQL `ORDER BY`, comparisons, `MIN`/`MAX`, range bounds, and generated
sort clauses back to their schema, write paths, and bound parameters.

| Area | Ordered or compared reads |
| --- | --- |
| Team memberships | `auth.go` (`ListTeamMembers`), both membership projections in `team_member_request.go` (`teamMemberOrder`), and inherited `MIN(joined_at)` in `team_hierarchy.go`. Existing user and team ID tie-breakers apply after equal timestamps. |
| Workspace memberships | `auth.go` (earliest active owner/admin for seed selection), `application_agents.go` (first owner for application installation), `scim.go` (first owner for a linked-account invitation), and `oauth_workspace_reads.go` (accessible workspaces by join time). |
| External identities | `auth.go` (last login and identity selection in the member list), `workspace_member_directory.go`, `document_read.go`, and `visibility_keys.go` (SCIM preference, then identity creation time). |
| Sessions | `auth.go` (unexpired sessions by last seen); `session_touch.go` parses the prior touch for throttling. Session authentication, token expiry, and MFA readers parse both widths. |
| Invitations and SCIM | `auth.go` orders invitations by creation time. `scim.go` compares open invitation expiry, orders tokens by creation time, optionally orders users by creation or modification time, lists group members by update time, and selects a role by the newest membership/group update. |
| History and connections | `search.go` orders `last_used_at` and `last_viewed_at`. `code_host_accounts.go` orders `updated_at`; both connection stores parse textual expiry and away-window timestamps. `oauth.go` orders client registration times. |
| Workspace startup and rename | `sqlite.go` loads roots by `workspace_states.updated_at`. `workspace_keys.go` and `workspace_rename_migration.go` choose the newest event for each prior key, including the comparison inside the staging database. `workspace_rename.go` reads rename rows in key order and parses the timestamp. |
| Agent sessions and uploads | `agent_tasks.go` orders tasks by creation time and compares `next_attempt` and `lease_until`. `issue_attributes.go` compares `stale_at`. `agent_session_updates.go` writes the parked-session `next_attempt` sentinel. Activity replay in `agent_tasks.go` uses IDs; `file_uploads.go` compares and orders expiry for quotas, transitions, and cleanup. |
| Collaboration | `collaboration.go` orders activity by `(created_at, update_id)` for the list, walker, and hydration lookup. `issue_editor_lookups.go` uses the same order when reconstructing the editor state. `collaboration_pages.go` uses the tuple for its upper boundary, resume predicate, byte-budget window, and returned order. Both inbound cursor timestamps are normalized before use. |
| Event history and retention | `sqlite.go` lists events by `(created_at, id)`. `event_reads.go` orders and pages by time and ID, with existing compatibility handling for mixed widths from the same second. `event_retention.go` applies its indexed time cutoff and orders prune candidates. `change_log.go` normalizes legacy times when assigning missing sequence numbers. Replay in `change_log.go`, `chat_domain_events.go`, and `replay.go` uses sequence numbers. `agent_web_access_migration.go` and `web_history.go` parse event times before comparing them. |
| Exports and email | `personal_data.go` orders pending, claimed, live, and expired exports, checks the export limit window, and handles exported sign-in times. `account_email_events.go` scans first-login follow-ups between two cutoffs and orders those candidates. The email ledger is still created on demand. |
| Erasure and deletion | `workspace_purge.go` lists receipts by `started_at` and normalizes event timestamps to select the latest deletion request. `workspace_departures.go` records the original join instant and parses join/departure times; the former-member sort uses Go times. |
| Record journal | `record_journal.go` uses `MIN(created_at)` to find the workspace due for retention, then parses timestamps in sequence order when pruning. `journal_repair.go` orders unnumbered entries by `(created_at, id)`. |

## Other comparison paths

| Representation | Audited reads |
| --- | --- |
| Timestamp strings inside canonical JSON and catalog summaries | `search_metadata.go` uses normalized comparison keys for `createdAt`/`updatedAt` order, filters, and keyset pagination. `discussion_rows.go` and `discussion_mutation.go` compare notification `updatedAt` against the active window. `api_key_authentication.go` compares the API key and grant `lastUsedAt` against the write throttle. SQL normalizes these keys at query time and retains all nine fractional digits. `release_keys.go` already parses expiry into `time.Time`. |
| Go values in server query windows | `initiatives.go` and `project_initiative_query.go` compare `time.Time` directly for creation and update order. Stable ties and the project sort's existing direction handling apply. Issue sorts and cursor comparisons in `issues_query.go` use `time.Time`, and its legacy range filters parse date strings. `scim_filter.go` and `scim_versioning.go` parse date strings in the SCIM filter and sort fallback using the wire precision. Server activity, presence, release, and notification sorts already compare parsed times. |
| Issue and content columns already using fixed-width UTC | `issue_query.go` (filters, sorts, and keysets), `issue_search_index.go`, `scoped_mutation.go`, `public_api_reads.go`, `project_progress_records.go` (including `MIN(created_at)`), `project_templates.go`, `issue_history.go`, `legacy_issue_history.go`, `legacy_editor_lookups.go`, `issue_scope_mutation.go`, `issue_record_mutation.go`, `issue_child_content_rows.go`, `issue_update_content_rows.go`, `issue_insight_history.go`, `bootstrap_content_walk.go`, `discussion_rows.go`, `discussion_mutation.go`, `source_records.go`, `cycle_graph_history.go`, `logical_external.go`, `content_records.go`, `notification_query.go`, `notification_visibility.go`, `notification_read.go`, `notification_state.go`, and `notification_delivery_records.go`. `release_issue_filter.go` and the catalog date columns use the same fixed-width layout. |
| Other fixed-width text | `loop_run_records.go` orders and pages by `started_at` and compares lease expiry. `loop_document_appends.go` compares creation time. `audit_events.go` applies time windows and legacy cursor bounds using its fixed-width format and uses sequence order for current cursors. |
| Integer timestamps | The comparable times in `agent_runs.go`, `coding_sessions.go`, `coding_usage.go`, `coding_environments.go`, `coding_pull_requests.go`, `coding_sandboxes.go`, `coding_artifacts.go`, `review_imports.go`, `webhook_deliveries.go`, `webhook_disable_write.go`, `chat_integrations.go`, `chat_reply_jobs.go`, `chat_threads.go`, `chat_domain_events.go`, `chat_intake_requests.go`, and `chat_mutation_receipts.go` are epoch numbers. The same applies to connection refresh leases and `personal_connections.sync_next_at`. |

## Migration and rollout

Version 36 runs under the schema startup lock, after the target tables exist and
before workspace loading. The column inventory lives in `stored_time_migration.go`.
Each transaction seeks past the previous primary key, reads only keys and
timestamp columns, and updates the rows with changed timestamps. It holds up to
128 rows or 32 for tables carrying large payloads. SQLite and PostgreSQL use a
values table and `UPDATE FROM`. MySQL uses a derived table and a primary-key
join. Composite scans use row-value ranges on SQLite and PostgreSQL and expanded
key ranges on MySQL.

The migration records completion after the last committed batch. A restart
after interruption starts the scan again and skips canonical rows. It preserves
nulls, empty sentinels, and unparseable values. `auth_users.deleted_at` is a
presence/authorization field and retains its historical text to avoid firing
the authorization epoch trigger. Historical migration bookkeeping timestamps
are informational and outside the compared column inventory.

Each target table has one primary-key pass. A million rows mean about 7,813
128-row batches or 31,250 32-row batches. The query returns narrow scalar
columns, and the migration's application memory is bounded by a batch.
Representative composite-key plans on all three engines used a primary-key
range for the next batch and primary-key lookups for the update. Database
engines can still write whole row versions or pages and maintain affected
indexes. Allow startup time for the scans and transactions, and disk space for
the WAL or binlog and old row versions on large databases.

Stop older API processes and other writers before the first upgraded start.
The lock serializes startups. It does not fence writes from already-running
older processes, which can create variable-width rows and use old-width cutoff
parameters. A rollback that restores older writers should use the pre-upgrade
backup or a build with compatible timestamp formatting.

## API and integrity

- Readers still parse `RFC3339Nano`, which accepts both widths. Public time
  fields serialize from `time.Time`. JSON payloads and the public pagination
  cursor encodings keep their established representations. If a historical SQL
  timestamp has an explicit UTC offset, migration converts it to the same
  instant in UTC, so a read of that scalar can emit `Z` in place of the offset.
- The migration changes timestamp scalars. Journal payloads, sequence numbers,
  event IDs, workspace revisions, and authorization epochs retain their
  values. Root JSON and its metadata spool content keep their original bytes.
- Audit hashes, event payloads, document revision ID inputs, the loop match
  cache timestamp, and webhook deduplication times keep their existing inputs.
  The shared metadata search result cache uses a new version so a fill from
  the prior comparator cannot supply an old order.
- The SW318 source pins were recalculated for the edited files. The guard's
  source and carrier inventory stays in `borrowed_source_origin_guard_test.go`.
