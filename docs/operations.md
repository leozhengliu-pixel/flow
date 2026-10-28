# Operations

## Health and readiness

`GET /api/health` returns `200` with `{"status":"ok"}` when the API and
configured Redis coordinator are healthy, and `503` with `"status":"degraded"`
when the coordinator does not answer. The container image invokes the same
endpoint through `/app/flow-api healthcheck`.

The endpoint is anonymous, so it reports nothing else: no build version,
commit or Redis mode, and no error detail. The startup log line names the
version, commit, database, storage and Redis mode, and a coordinator outage is
logged once at error level (`health check degraded`, with the Redis mode and
the error) and once more when it recovers (`health check recovered`).

## Handler panics

A panic in a request handler does not take the server down. Flow logs it at
error level as `http handler panic` with `request_id`, `method`, `path`
(without the query string), `panic` (the value, cut to 512 characters) and
`stack`; headers and bodies are never logged. The client gets a `500` with
`{"error":"Internal server error","code":"INTERNAL_ERROR","type":"INTERNAL_ERROR","requestId":"..."}`,
and `requestId` matches the `X-Request-ID` response header, so a user's
report leads to the log line. If the response had already started (an event
stream or a download), the connection is cut instead, and a WebSocket is left
to its handler. Search the logs for `http handler panic` after an incident.

## Internal errors

Every other `500` gets the same generic body: database and driver errors
(table, column and SQL state names) never reach the client. Flow logs the
real error at error level as `internal error response` with `request_id`,
`method` and `path`. SCIM and OAuth answer in their own error formats with
the request id in the message. When an MCP client or an agent's tool call
fails on a database error, the tool result reads "The change could not be
saved because of an internal server error (request id …)" and the log line
is `flow tool internal error` with `request_id` and `tool`. Validation,
conflict and permission errors (4xx) keep their specific messages.

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

## Proxies in front of Flow

Check what Flow makes of the proxy chain from outside:

```bash
curl -sS https://flow.example.com/.well-known/oauth-authorization-server | grep -o '"issuer":"[^"]*"'
```

The issuer should be the `https` address users open, and after signing in
the browser shows the session cookie as `Secure`. An `http` issuer means `FLOW_APP_URL` does not match the host
users open and the proxy is not trusted: fix `FLOW_APP_URL`, or set
`FLOW_TRUST_PROXY_HEADERS=true` when only the proxy can reach Flow.

If every client shares one rate limit budget (429s for everyone after a few
sign-in attempts), Flow sees the proxy's address as the client: trust the
proxy. If clients get their own budget but it follows an address they can
choose, the chain has more hops than Flow trusts: list the extra proxies (a
CDN or load balancer in front of the ingress) in `FLOW_TRUSTED_PROXIES`.

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

## Issue group counts on PostgreSQL

Issue lists count their groups (by status, priority, ...) on every open and
refresh. Counts with nested sub-issues read the sub-issues and their parents
from the `issue_records_tree_idx` index without touching the table rows: an
index-only scan. PostgreSQL can skip the table only for pages its visibility
map marks all-visible, and it marks them when it vacuums. Right after a bulk
import or a large migration most pages are unmarked, and a workspace-wide count
reads table pages as well, and with stale statistics the planner can fall back
to a sequential scan of the table. On 75,000 issues the workspace-wide read
took 20 ms once vacuumed, 30 ms with a fifth of the rows on unmarked pages,
and 70 ms as a sequential scan.

- Keep autovacuum on. Since PostgreSQL 13 it also vacuums tables that only
  receive inserts, so the visibility map catches up by itself.
- After a large import (an importer run, a restore, a migration that rewrote
  `issue_records`), vacuum and analyze the issue table once:

  ```sql
  VACUUM (ANALYZE) issue_records;
  ```

  Flow analyzes the issue tables after its own imports (statistics), but it
  does not vacuum.
- On SSD storage, set `random_page_cost` to about `1.1` (the default `4` is
  meant for spinning disks); it makes the planner prefer the index scans these
  counts are built for.

To check, run `EXPLAIN (ANALYZE, BUFFERS)` on a count. The scans on
`issue_records_tree_idx` should be `Index Only Scan` with `Heap Fetches` close
to zero.

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

### Inherited document scope

The first start of a version with this migration (schema migration 1006,
"inherited document scope") records, for every document attached to an issue
or project, the teams it inherits from them. Until then a document on a
private team's issue or project stayed readable across the workspace until it
was attached or detached again. Live documents and documents in "Recently
deleted" are covered; grants, the document's own teams, its version and its
update time do not change. Issue teams are read from the issue records 500 at
a time and documents are written 500 per transaction, under the startup lock.
Only the issues documents are attached to are read, so the work grows with
the number of documents, not with the number of issues. The log line
`derived inherited document scope documents=… deleted_documents=…` reports
how many documents changed. The migration only fills in what attaching the
document today would record, so running it again changes nothing.

### Large event and identity values (MySQL)

Schema migration 16 ("large event and identity values") changes three MySQL
columns that were `TEXT` (64 KiB) and can hold more:

| Column | New type | Holds |
| --- | --- | --- |
| `domain_events.previous_values` | `LONGTEXT` | The fields an edit changed, as they were before it (a document's whole pre-edit body) |
| `auth_identities.claims_json` | `LONGTEXT` | Identity provider claims, or a SCIM user's profile |
| `code_host_accounts.scopes` | `MEDIUMTEXT` | The scopes a code host granted |

Before it, editing a document of more than about 64 KiB failed with HTTP 500
(`Error 1406 … Data too long for column 'previous_values'`) in workspaces with
an enabled webhook, an event-triggered loop or a connected Feishu/Lark app,
and nothing was saved. PostgreSQL and SQLite `TEXT` has no such limit and
does not change.

The migration keeps every row. MySQL cannot change a column type in place, so
each table is rebuilt (`ALGORITHM=COPY, LOCK=SHARED`): the table stays
readable, but **writes to it wait until the copy finishes**. Every change in
Flow writes a `domain_events` row, so on a large `domain_events` table this
migration can take a while (minutes for tens of GB), and servers still running
the old version cannot save changes until it is done. A local test rebuilt
about 200 MB in 2 seconds. Disk speed and table size decide the time on your
server. Check the size first with the [database size report](#database-size).
On a large database:

- run the upgrade in a maintenance window, as described in steps 3 and 4
  above (one new instance first), or
- change the columns beforehand with an online schema change tool such as
  `gh-ost` or `pt-online-schema-change` (for example
  `ALTER TABLE domain_events MODIFY COLUMN previous_values LONGTEXT NULL`).
  The migration reads each column's type and leaves alone any column that is
  already large enough. It also does nothing on a second run.

Event retention (`FLOW_DOMAIN_EVENT_RETENTION_DAYS`, see
[Database growth](configuration.md#database-growth)) keeps `domain_events`
bounded.

### Wider issue attribute fields (MySQL, PostgreSQL)

Schema migration 17 ("wider issue attribute fields") widens
`issue_attribute_records.field` from `VARCHAR(32)` to `VARCHAR(191)`. The
column names the filterable property each row indexes, and Triage
Intelligence writes one row per suggested label, assignee, project, team or
issue (`suggestedLabel:<label id>`), which is longer than 32 characters.
Before it, generating or refreshing triage suggestions failed with HTTP 500
(`Data too long for column 'field'` on MySQL, `value too long for type
character varying(32)` on PostgreSQL) and the issue's suggestions were not
saved. SQLite does not enforce the width and does not change.

The migration keeps every row, the primary key and the
`issue_attributes_value_idx` index. Both indexes stay under MySQL's
3,072-byte InnoDB key limit in `utf8mb4`. How it locks:

- **MySQL** rebuilds the table (`ALGORITHM=COPY, LOCK=SHARED`): the length
  prefix of a `VARCHAR` over 255 bytes grows from one byte to two, which
  InnoDB cannot change in place. The table stays readable, but **writes to it
  wait until the copy finishes**. Every issue change writes this table, so
  issue edits from servers still running the old version wait too. The table
  holds a few short rows per issue, so it is much smaller than
  `issue_records`. Check its size first with the
  [database size report](#database-size), or widen the column beforehand with
  an online schema change tool (`ALTER TABLE issue_attribute_records MODIFY
  COLUMN field VARCHAR(191) NOT NULL`).
- **PostgreSQL** changes only the catalog (`ALTER COLUMN field TYPE
  VARCHAR(191)`): no rewrite and no index rebuild. It takes an `ACCESS
  EXCLUSIVE` lock for that moment, so it first waits for transactions already
  using the table, and queries on the table wait behind it.

The migration reads the column's width and leaves alone a column that is
already as wide or wider (a fresh install, a second run, or an earlier manual
`ALTER`). Fields longer than 191 characters (a suggestion for an imported
label with a very long id) are stored as a prefix plus a SHA-256 digest, so
they never fail the write and filters on them still match.

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

## Rolling updates and web assets

Each build of the web app names its scripts and styles by content hash
(`/assets/index-<hash>.js`). During a rolling update or a rollback, replicas
of two versions serve side by side, and a page from one version asks for files
only that version built. If such a request reaches a replica of the other
version and fails, the page stays on the loading screen, or a route of a page
opened before the deploy fails to load. A replica that does not have a hashed
file looks for it in four layers:

1. **Each image carries earlier releases.** The image build copies the
   assets of the image passed as `PREVIOUS_RELEASE_IMAGE` (that image's own
   release and the releases it carried, `CARRY_RELEASES`, default 2) into
   `/app/web-previous`. The build output's `asset-manifest.json` decides
   which files belong to which release. The server reads them from
   `FLOW_ASSET_PREVIOUS_PATH`. A new replica therefore serves an old page's
   chunks, also after every replica is new. The published images pass the
   previous version tag (tag builds) or the last published `main` image. For
   your own builds, pass the image you are replacing:

   ```bash
   docker build --build-arg PREVIOUS_RELEASE_IMAGE=registry.example.com/flow:1.4.2 -t registry.example.com/flow:1.5.0 .
   ```

2. **Replicas ask each other.** When a file is missing, a replica asks every
   replica behind `FLOW_ASSET_PEERS` (the Helm chart's headless
   `<release>-peers` Service, `assets.peerFallback.enabled`, on by default).
   This works in both directions while versions are mixed, during a rollout
   and during a rollback, without shared storage. Requests between replicas
   carry `X-Flow-Asset-Peer` and are answered from disk only, so they never
   bounce on.
3. **Shared directory (optional).** With `FLOW_ASSET_SHARED_PATH` (Helm
   `assets.shared`, a ReadWriteMany volume), every replica publishes its
   release's assets there at startup and reads missing ones from there. The
   newest `FLOW_ASSET_SHARED_KEEP_RELEASES` releases (default 5) are kept.
   This is the layer that covers a page of a *later* release after a rollback
   finished: an older image cannot carry files that were built after it.
4. **Published origins (optional).** `FLOW_ASSET_FALLBACK_URL` (Helm
   `assets.fallbackUrls`) lists base URLs where releases' assets are
   published, such as an object storage bucket, a CDN, or the Service of a
   blue/green twin. Flow fetches `<url>/assets/<name>`. To publish a release,
   copy `/app/web/assets/` out of the image and upload it without deleting
   older files:

   ```bash
   id=$(docker create registry.example.com/flow:1.5.0)
   docker cp "$id:/app/web/assets" ./assets && docker rm "$id"
   aws s3 sync ./assets s3://flow-web-assets/assets --cache-control "public, max-age=31536000, immutable"
   ```

Files from peers and origins are kept in a 64 MiB memory cache. A file no
layer has is remembered for 10 seconds. A fallback answer carries
`X-Flow-Asset-Source: previous|shared|peer|url`. An answer that is an HTML
page is never used, because an older server, or a misconfigured origin, may
answer a missing file with the app's index page.

What the browser sees:

- `index.html` (any app route) is served with `Cache-Control: no-cache`.
  Hashed files are served with `public, max-age=31536000, immutable`.
- A hashed file that no layer has is a `404` with `Cache-Control: no-store`.
  It is never the app's HTML.
- As a last resort, the page reloads once: when a lazy route chunk fails
  (`vite:preloadError`, or a failed dynamic import), or when an entry script
  or stylesheet fails before the app starts. The reload fetches current HTML.
  A `sessionStorage` guard allows one reload per 30 seconds, so a file that is
  really gone shows an error instead of a reload loop.

What stays uncovered:

- The first rollout *to* a version with this fallback. The old replicas do
  not have it yet, so a new page's files can still fail on them until the
  rollout finishes. The client reload covers that window.
- After a rollback finishes, a page of the later release that is still open
  needs layer 3 or 4 to open routes it has not loaded yet. Without them it
  reloads once onto the rolled-back version.
- Peers are found through DNS. A replica that is terminating has already
  left the headless Service, so its files come from the other layers.

To check a rollout, request a file of the new release from an old Pod
directly, for example `kubectl port-forward pod/<old-pod> 8080` and then
`curl -sI localhost:8080/assets/<new-file>`. The answer should be `200` with
`X-Flow-Asset-Source: peer`.
