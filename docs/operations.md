# Operations

## Health and readiness

`GET /api/health` returns `200` when the API and configured Redis coordinator
are healthy. The container image invokes the same endpoint through
`/app/flow-api healthcheck`.

## Backup boundary

A complete backup contains both the SQL database and attachment storage. Keep
them at the same logical point in time.

- SQLite: stop the API or use `sqlite3 flow.db ".backup backup.db"`.
- PostgreSQL: use `pg_dump --format=custom` and restore with `pg_restore`.
- MySQL: use a transaction-consistent `mysqldump --single-transaction`.
- Local attachments: archive `FLOW_STORAGE_LOCAL_PATH` while writes are paused.
- S3: enable bucket versioning and lifecycle retention in the object store.

Never rely on the `domain_events` table as the only backup; workspace state is
stored in `workspace_states`.

## Restore drill

1. Restore SQL and attachments into an isolated environment.
2. Start the exact Flow image version used when the backup was taken.
3. Confirm `/api/health`, sign-in, workspace bootstrap, attachment download, and
   a create/update round trip.
4. Upgrade to the target version and verify `schema_migrations` advanced once.
5. Record recovery time and the newest restored timestamp.

Run a restore drill before every production upgrade and at least quarterly.

## Upgrade procedure

1. Read `CHANGELOG.md` and back up SQL plus attachments.
2. Pull an immutable version tag rather than `latest`.
3. Run one new instance and wait for its health check.
4. Verify migrations and a representative workspace before replacing remaining
   instances.
5. Retain the old image and backup until the validation window ends.
