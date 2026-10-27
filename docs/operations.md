# Operations

## Health and readiness

`GET /api/health` returns `200` when the API and configured Redis coordinator
are healthy. The container image invokes the same endpoint through
`/app/flow-api healthcheck`.

## Security headers

Responses carry the security headers in
[Security response headers](configuration.md#security-response-headers). To
check a deployment, compare a page, an API response and a missing asset:

```bash
curl -sSI https://flow.example.com/ | grep -iE 'content-security|frame|permissions|referrer|nosniff|strict-transport'
curl -sSI https://flow.example.com/api/health | grep -iE 'content-security|frame|nosniff|strict-transport'
curl -sSI https://flow.example.com/assets/missing-00000000.js | grep -iE 'content-security|nosniff'
```

Each header should appear once. Two copies mean the gateway sets it too, so
list it in `FLOW_SECURITY_HEADERS_SKIP`. No `Strict-Transport-Security` behind
a TLS ingress means Flow does not trust the proxy's `X-Forwarded-Proto`. Set
`FLOW_TRUST_PROXY_HEADERS=true` if only the ingress can reach Flow.

If an enforced CSP breaks a page, set `FLOW_SECURITY_CSP=report-only` and
restart. The page works again and the browser console keeps naming the
blocked source. `FLOW_SECURITY_HEADERS=off` removes every header Flow adds.
HSTS is the exception: browsers keep it for its max-age, so serve
`FLOW_SECURITY_HSTS_MAX_AGE=0` over HTTPS before turning it off.

## Backup boundary

A complete backup contains both the SQL database and attachment storage. Keep
them at the same logical point in time.

- SQLite: stop the API or use `sqlite3 flow.db ".backup backup.db"`.
- PostgreSQL: use `pg_dump --format=custom` and restore with `pg_restore`.
- MySQL: use a transaction-consistent `mysqldump --single-transaction`.
- Local attachments: archive `FLOW_STORAGE_LOCAL_PATH` while writes are paused.
- S3: enable bucket versioning and lifecycle retention in the object store.
  With `FLOW_STORAGE_DRIVER=s3`, document version bodies and long agent
  transcript parts live under `FLOW_S3_CONTENT_PREFIX` (default `content`), so
  the database backup is incomplete without that prefix.

Never rely on the `domain_events` table as the only backup; workspace state is
stored in `workspace_states`. Events older than
`FLOW_DOMAIN_EVENT_RETENTION_DAYS` are pruned.

## Database size

`scripts/db-size-report/` has read-only reports for MySQL, PostgreSQL and
SQLite. Each lists tables by size (on MySQL including the full-text auxiliary
tablespaces of the ngram indexes), workspace metadata by collection, domain
events by type, and content stored outside metadata rows:

```bash
mysql --table -u flow -p flow < scripts/db-size-report/mysql.sql
```

```bash
psql "$FLOW_DATABASE_URL" -f scripts/db-size-report/postgres.sql
```

```bash
sqlite3 -box data/flow.db < scripts/db-size-report/sqlite.sql
```

See [Database growth](configuration.md#database-growth) for the retention and
offload settings.

## Restore drill

1. Restore SQL and attachments into an isolated environment.
2. Start the exact Flow image version used when the backup was taken.
3. Confirm `/api/health`, sign-in, workspace bootstrap, attachment download, and
   a create/update round trip.
4. Upgrade to the target version and verify `schema_migrations` advanced once.
5. Record recovery time and the newest restored timestamp.

Run a restore drill before every production upgrade and at least quarterly.

## Re-adding a lost document version

A document keeps its pre-edit body as its oldest version when it is first
edited. Documents edited before that existed may have lost the body they had
before their first edit. If a copy of that body survives (an export, a backup
file), a workspace admin can add it back to the document's history as a
restorable version. The document's current title, body, version and project,
team and issue links stay as they are. The request needs an admin session or an
API key with the `admin` scope.

```bash
DOC=document_1792043252194518676
# The document's createdAt (or the updatedAt it had before the edit) dates the version.
CREATED_AT=$(curl -fsS -H "Authorization: Bearer $FLOW_ADMIN_KEY" \
  "$FLOW_URL/api/documents/$DOC" | jq -r .createdAt)
jq -n --rawfile content backup.md --arg createdAt "$CREATED_AT" --arg title "Original title" \
  '{title: $title, content: $content, createdAt: $createdAt}' |
curl -fsS -X POST -H "Authorization: Bearer $FLOW_ADMIN_KEY" -H 'Content-Type: application/json' \
  --data @- "$FLOW_URL/api/workspace/documents/$DOC/revisions/import"
```

- `content` is the Markdown body; add `contentData` (the editor's JSON) when
  the backup has it. Without it the editor reads the Markdown when the version
  is restored.
- `title` defaults to the current title. `authorId` defaults to the document's
  creator.
- `createdAt` must be older than the document's current state, so the version
  sorts below the current one.
- The call answers 201 with `imported: true`. Sending the same title, body and
  time again answers 200 with `imported: false` and adds nothing.
- Each import is recorded in the audit log as `document.revision_imported`.

Then check `GET /api/documents/$DOC/history` (or the document's version
history) and restore the version from there if the body should come back.

## Upgrade procedure

1. Read `CHANGELOG.md` and back up SQL plus attachments.
2. Pull an immutable version tag rather than `latest`.
3. Run one new instance and wait for its health check.
4. Verify migrations and a representative workspace before replacing remaining
   instances.
5. Retain the old image and backup until the validation window ends.

### Several servers starting at once

Every server runs the migrations when it starts. There is no separate
migration job. Servers that start together on the same database (replicas of
a fresh install, a scale-up, a rolling update) take turns through a database
lock. The first one migrates and the others wait, then find the schema current.
The [Database](configuration.md#database) section describes the lock. A waiting
server logs this:

```text
another server holds the startup lock; waiting lock="database schema" holder="backend pid 433 (flow api-7d9c pid 1) from 10.0.3.17"
acquired startup lock lock="database schema" waited_ms=1758
```

- A waiting server answers no requests yet. The Helm chart's startup probe
  allows 10 minutes, the same as the default
  `FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT`. If you raise one, raise the other.
- After the timeout the server exits with
  `timed out waiting for the database startup lock` and the holder's identity.
  If no other server is starting, the holder is a stale session: end it with
  `SELECT pg_terminate_backend(<pid>)` on PostgreSQL or `KILL <id>` on MySQL.
  The lock goes away with the session.
- A long migration (an index build on a large MySQL database) holds the lock
  for its whole duration, so servers behind it start in turn after it. To keep
  serving traffic from the old version while the schema changes, run one
  instance of the new version first (step 3 above), for example as a one-off
  Pod or a Helm `pre-upgrade` hook Job with the same configuration. Then roll
  the remaining replicas, which find the schema current.
