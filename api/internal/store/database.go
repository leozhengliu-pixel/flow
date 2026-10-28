package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"flow/api/internal/objectstore"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type DatabaseConfig struct {
	Driver              string
	URL                 string
	Path                string
	FixtureProfile      string
	FixturePassword     string
	MaxOpenConns        int
	MaxIdleConns        int
	ConnMaxLifetime     time.Duration
	MaxStateBytes       int
	MaxTransactionBytes int
	// ContentStore receives offloaded bodies (document revision content,
	// large agent transcript parts) when FLOW_STORAGE_DRIVER=s3; without it
	// they are kept gzip-compressed in the metadata_blobs table.
	ContentStore objectstore.Store
	// AgentOffloadBytes is the size above which an agent transcript part is
	// offloaded (default 8 KiB).
	AgentOffloadBytes int
	// ContentCacheBytes bounds the in-process cache of offloaded bodies that
	// workspace reloads read instead of the database or object store.
	ContentCacheBytes int
	// SearchIndexMaxChars caps the text indexed for full-text search per
	// issue and per document (default 20,000; 0 indexes everything).
	SearchIndexMaxChars int
	// MigrationLockTimeout bounds how long startup waits for another server
	// that is running the schema work on the same database (default
	// DefaultStartupLockTimeout).
	MigrationLockTimeout time.Duration
}

type sqlDatabase struct {
	*sql.DB
	dialect             string
	maxTransactionBytes int
	// notified receives the notifications a committed transaction created
	// (or brought back as unread news), for the notification.created
	// realtime signal.
	notified atomic.Pointer[func([]notificationSignal)]
	// content offloads large metadata bodies (content_offload.go).
	content *contentOffload
	// searchMaxChars caps the text indexed per issue and per document; 0
	// indexes everything.
	searchMaxChars int
	// commitHooks lets tests pause a transaction around its commit
	// (change_cursor_test.go); nil in production.
	commitHooks atomic.Pointer[commitHooks]
}

// commitHooks run inside sqlTx.Commit: beforeCommit once the transaction's
// events have their sequencing rows (before the rows every writer shares),
// afterCommit once it committed (before its events are numbered),
// afterSequence when Commit is about to return, which is before the writer
// drops any cached copy of what it changed.
type commitHooks struct {
	beforeCommit, afterCommit, afterSequence func(tx *sqlTx)
}

type sqlTx struct {
	*sql.Tx
	dialect             string
	maxTransactionBytes int
	writtenBytes        int
	writeErr            error
	database            *sqlDatabase
	signals             []notificationSignal
	// issueCounts: the issue counter deltas and revisions this transaction
	// applies when it commits (issue_stats.go).
	issueCounts *pendingIssueCounts
	// rebasedMetadata: the metadata records this transaction locked and
	// compared with the writer's copy (metadata_record_merge.go).
	rebasedMetadata map[metadataRecordKey]bool
	// metadataWritten: a metadata record or the workspace document changed,
	// so the shared metadata cache must be dropped after commit.
	metadataWritten bool
	// sequencing: the change-log events this transaction inserted without a
	// cursor; they are queued at commit and numbered after it
	// (event_sequencer.go).
	sequencing []pendingEventSequence
	// cacheRevisions: the workspace cache revisions this transaction
	// advances as its last statements (cache_revisions.go).
	cacheRevisions map[cacheRevisionKey]bool
	ctx            context.Context
	// numbering: this transaction assigns change-log cursors
	// (sequenceWorkspaceBatch); commit hooks skip it.
	numbering bool
}

// notificationSignal names a notification that is news to its recipient.
type notificationSignal struct {
	workspace, id, recipient string
}

var ErrTransactionWriteBudget = errors.New("transaction write budget exceeded")

// Commit writes what the transaction queued for its end, commits, and numbers
// the change-log events it inserted. Statement order matters: the events'
// sequencing rows come after every read and write the events describe
// (event_sequencer.go), and the rows every writer of a workspace shares (issue
// counters, cache revisions) come last, so a writer holds them only while it
// commits.
func (tx *sqlTx) Commit() error {
	if tx.writeErr != nil {
		_ = tx.Tx.Rollback()
		return tx.writeErr
	}
	if err := tx.queueEventSequencing(); err != nil {
		_ = tx.Tx.Rollback()
		return err
	}
	hooks := tx.hooks()
	if hooks != nil && hooks.beforeCommit != nil {
		hooks.beforeCommit(tx)
	}
	if err := tx.flushIssueCounts(); err != nil {
		_ = tx.Tx.Rollback()
		return err
	}
	if err := tx.flushCacheRevisions(); err != nil {
		_ = tx.Tx.Rollback()
		return err
	}
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if hooks != nil && hooks.afterCommit != nil {
		hooks.afterCommit(tx)
	}
	tx.sequenceCommittedEvents()
	if hooks != nil && hooks.afterSequence != nil {
		hooks.afterSequence(tx)
	}
	if len(tx.signals) > 0 && tx.database != nil {
		if sink := tx.database.notified.Load(); sink != nil {
			(*sink)(tx.signals)
		}
	}
	tx.signals = nil
	return nil
}

func (tx *sqlTx) hooks() *commitHooks {
	if tx.database == nil || tx.numbering {
		return nil
	}
	return tx.database.commitHooks.Load()
}

// context is the context of the transaction's first statement, for the
// statements Commit writes on its behalf.
func (tx *sqlTx) context() context.Context {
	if tx.ctx == nil {
		return context.Background()
	}
	return tx.ctx
}

// noteNotificationRow records a written notification row that is news for
// its recipient: a new unread notification, or an unread one whose
// occurrence count grew (new activity grouped into it). Read-state edits
// (read, archive, snooze, mark unread) are not news; they have their own
// notification.* events.
func (tx *sqlTx) noteNotificationRow(row contentRecordRow, previous []byte, existed bool) {
	if row.kind != "notification" || row.status != "unread" || row.nextAttempt != "" || row.owner == "" {
		return
	}
	if existed {
		var before, after struct {
			OccurrenceCount int `json:"occurrenceCount"`
		}
		if json.Unmarshal(previous, &before) != nil || json.Unmarshal(row.raw, &after) != nil || after.OccurrenceCount <= before.OccurrenceCount {
			return
		}
	}
	tx.signals = append(tx.signals, notificationSignal{workspace: row.workspace, id: row.id, recipient: row.owner})
}

func OpenDatabase(config DatabaseConfig) (*SQLiteStore, error) {
	driver := strings.ToLower(strings.TrimSpace(config.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver == "postgresql" {
		driver = "postgres"
	}
	var sqlDriver, dsn, sqlitePath string
	switch driver {
	case "sqlite":
		path := strings.TrimSpace(config.Path)
		if path == "" {
			path = "data/flow.db"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		sqlDriver, dsn, sqlitePath = "sqlite", sqliteDSN(path), path
	case "postgres":
		if strings.TrimSpace(config.URL) == "" {
			return nil, fmt.Errorf("database URL is required for postgres")
		}
		sqlDriver, dsn = "pgx", config.URL
	case "mysql":
		if strings.TrimSpace(config.URL) == "" {
			return nil, fmt.Errorf("database URL is required for mysql")
		}
		var err error
		dsn, err = mysqlDSN(config.URL)
		if err != nil {
			return nil, err
		}
		sqlDriver = "mysql"
	default:
		return nil, fmt.Errorf("unsupported database driver %q (expected sqlite, postgres, or mysql)", driver)
	}
	// Startup creates or rewrites schema and seed rows, so servers starting
	// on the same database at once (several replicas of a fresh install, a
	// rolling upgrade) take turns. A server that waited reads the
	// schema_migrations rows the first one wrote, and each idempotent stage
	// finds its tables, indexes and backfills in place. SQLite takes the lock
	// before even switching the journal mode, which itself needs the file.
	openStartupLock := func(ctx context.Context) (startupLockBackend, error) {
		return openStartupLockBackend(ctx, driver, sqlDriver, dsn, sqlitePath)
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 30*time.Second)
	lockBackend, err := openStartupLock(lockCtx)
	cancelLock()
	if err != nil {
		return nil, fmt.Errorf("connect to %s database: %w", driver, err)
	}
	var startup *startupLock
	if lockBackend != nil {
		if startup, err = acquireStartupLock(context.Background(), lockBackend, startupLockOptions{name: "database schema", timeout: config.MigrationLockTimeout}); err != nil {
			return nil, err
		}
		defer startup.release()
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, err
	}
	maxOpen := config.MaxOpenConns
	if maxOpen <= 0 {
		if driver == "sqlite" {
			maxOpen = 1
		} else {
			maxOpen = 20
		}
	}
	maxIdle := config.MaxIdleConns
	if maxIdle < 0 {
		maxIdle = 0
	} else if maxIdle == 0 {
		// Keep one idle SQLite connection so connection-scoped PRAGMAs
		// (busy_timeout, synchronous, temp_store) remain in effect between
		// requests. A zero idle pool would reopen a fresh connection for each
		// statement and silently lose those settings.
		if driver == "sqlite" {
			maxIdle = 1
		} else {
			maxIdle = 5
		}
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(min(maxIdle, maxOpen))
	if config.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(config.ConnMaxLifetime)
	}
	// SQLite defaults to a rollback journal and a zero busy timeout. WAL keeps
	// readers moving while a write commits, and NORMAL sync avoids an fsync for
	// every full workspace snapshot without disabling durability. Configure it
	// after pool sizing so the first connection is retained in the pool.
	if driver == "sqlite" {
		if err := configureSQLite(db, dsn); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s database: %w", driver, err)
	}
	maxStateBytes := config.MaxStateBytes
	if maxStateBytes <= 0 {
		maxStateBytes = 64 << 20
	}
	maxTransactionBytes := config.MaxTransactionBytes
	if maxTransactionBytes <= 0 {
		maxTransactionBytes = 32 << 20
	}
	s := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: driver, maxTransactionBytes: maxTransactionBytes, content: newContentOffload(config), searchMaxChars: max(config.SearchIndexMaxChars, 0)}, dialect: driver, fixtureProfile: strings.TrimSpace(config.FixtureProfile), fixturePassword: config.FixturePassword, maxStateBytes: maxStateBytes}
	s.changeLogMinRows.Store(DefaultChangeLogMinRows)
	notify := s.publishNotificationSignals
	s.db.notified.Store(&notify)
	s.lifecycle, s.stopLifecycle = context.WithCancel(context.Background())
	// Startup runs schema checks, loads workspace metadata and applies
	// idempotent data migrations. Stage timings are logged for slow opens.
	stages := []struct {
		name string
		run  func(context.Context) error
	}{
		{"migrate", s.migrate},
		{"ensureIssueRecords", s.ensureIssueRecords},
		{"ensureAttachmentIndex", s.ensureAttachmentIndex},
		{"ensureIssueListProjection", s.ensureIssueListProjection},
		{"ensureIssueSearchIndex", s.ensureIssueSearchIndex},
		{"ensureContentRecords", s.ensureContentRecords},
		{"ensureIssueStats", s.ensureIssueStats},
		{"ensureSCIMSchema", s.ensureSCIMSchema},
		{"ensureWorkspaceMetadataRecords", s.ensureWorkspaceMetadataRecords},
		{"ensureApplicationAgents", s.ensureApplicationAgents},
		{"ensureAuditStreamSecrets", s.ensureAuditStreamSecrets},
		{"ensureWebhookDeliveries", s.ensureWebhookDeliveries},
		{"applySearchIndexCap", s.applySearchIndexCap},
		{"ensureMetadataSearchIndex", s.ensureMetadataSearchIndex},
		{"ensureAPIKeyLookup", s.ensureAPIKeyLookup},
		{"ensureCustomerFilterIndex", s.ensureCustomerFilterIndex},
		{"ensureReleaseIssueIndex", s.ensureReleaseIssueIndex},
		{"ensureTeamDefaultFavorites", s.ensureTeamDefaultFavorites},
		{"ensureLoopRunRecords", s.ensureLoopRunRecords},
		{"ensureLoopDocumentAppends", s.ensureLoopDocumentAppends},
		{"ensureAgentRuns", s.ensureAgentRuns},
		{"ensureCodingSessions", s.ensureCodingSessions},
		{"ensureFileUploads", s.ensureFileUploads},
		{"ensureOffloadedContent", s.ensureOffloadedContent},
		{"loadOrSeed", s.loadOrSeed},
		{"seedWorkspaceOwners", func(ctx context.Context) error {
			if strings.EqualFold(strings.TrimSpace(config.FixtureProfile), "test") {
				return s.ensureAuthTestFixture(ctx)
			}
			return s.ensureSeedWorkspaceOwners(ctx)
		}},
		{"migrateIssueCollections", s.migrateIssueCollections},
		{"migrateWorkspaceMetadataRecords", s.migrateWorkspaceMetadataRecords},
		{"migrateContentIndexes", s.migrateContentIndexes},
		{"migrateIssueStats", s.migrateIssueStats},
		{"migrateIssueSequences", s.migrateIssueSequences},
		{"migrateIssueAttributes", s.migrateIssueAttributes},
		{"migrateRecurrenceInstances", s.migrateRecurrenceInstances},
		{"migrateAttachmentIndex", s.migrateAttachmentIndex},
		{"migrateIssueListProjection", s.migrateIssueListProjection},
		{"migrateIssueSearchIndex", s.migrateIssueSearchIndex},
		{"migrateMetadataSearchIndex", s.migrateMetadataSearchIndex},
		{"migrateAPIKeyLookup", s.migrateAPIKeyLookup},
		{"migrateCustomerFilterIndex", s.migrateCustomerFilterIndex},
		{"migrateReleaseIssueIndex", s.migrateReleaseIssueIndex},
		{"migrateLoopRunRecords", s.migrateLoopRunRecords},
		{"dataMigrations", s.runDataMigrations},
		{"backfillAllProjectProgress", s.backfillAllProjectProgress},
	}
	s.startupLock, s.startupLockTimeout = openStartupLock, config.MigrationLockTimeout
	opened := time.Now()
	timings := make([]string, 0, len(stages))
	for _, stage := range stages {
		began := time.Now()
		if err := stage.run(context.Background()); err != nil {
			s.stopLifecycle()
			db.Close()
			return nil, err
		}
		if elapsed := time.Since(began); elapsed >= 50*time.Millisecond {
			timings = append(timings, fmt.Sprintf("%s=%dms", stage.name, elapsed.Milliseconds()))
		}
	}
	if total := time.Since(opened); total >= time.Second {
		slog.Info("workspace store opened", "driver", driver, "total_ms", total.Milliseconds(), "stages", strings.Join(timings, " "))
	}
	return s, nil
}

// WithStartupLock runs action while no other server on this database runs
// startup schema or seed work. It is for one-off startup tasks that write
// shared rows (for example installing built-in applications); waiting
// servers then see the result instead of racing to create the same rows.
func (s *SQLiteStore) WithStartupLock(ctx context.Context, name string, action func(context.Context) error) error {
	if s.startupLock == nil {
		return action(ctx)
	}
	backend, err := s.startupLock(ctx)
	if err != nil {
		return fmt.Errorf("%s startup lock: %w", name, err)
	}
	return withStartupLock(ctx, backend, startupLockOptions{name: name, timeout: s.startupLockTimeout}, func() error { return action(ctx) })
}

func configureSQLite(db *sql.DB, dsn string) error {
	// Set the timeout before changing the journal so startup also tolerates a
	// short-lived lock held by another process (for example a backup job).
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return err
	}
	// SQLite cannot enable WAL for an anonymous in-memory database. The other
	// pragmas are safe and useful for both file-backed and named in-memory DBs.
	if !strings.HasPrefix(strings.TrimSpace(dsn), ":memory:") {
		if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
			return err
		}
		if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	_, err := db.Exec(`PRAGMA temp_store=MEMORY`)
	return err
}

// sqliteDSN applies connection-scoped settings to every connection opened by
// database/sql, not just the first connection used during startup. This keeps
// busy_timeout/foreign_keys enabled when an operator opts into a larger pool.
func sqliteDSN(path string) string {
	trimmed := strings.TrimSpace(path)
	separator := "?"
	if strings.Contains(trimmed, "?") {
		separator = "&"
	}
	pragmas := []string{"_pragma=busy_timeout(5000)", "_pragma=foreign_keys(1)", "_pragma=temp_store(MEMORY)"}
	if !strings.Contains(trimmed, ":memory:") {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)")
	}
	return trimmed + separator + strings.Join(pragmas, "&")
}

func mysqlDSN(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		separator := "?"
		if strings.Contains(raw, "?") {
			separator = "&"
		}
		dsn := raw + separator + "parseTime=true"
		if !strings.Contains(raw, "interpolateParams=") {
			dsn += "&interpolateParams=true"
		}
		return dsn, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "mysql" || parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
		return "", fmt.Errorf("invalid mysql database URL")
	}
	password, _ := parsed.User.Password()
	credentials := parsed.User.Username()
	if password != "" {
		credentials += ":" + password
	}
	query := parsed.Query()
	query.Set("parseTime", "true")
	// Without client-side interpolation every parameterized statement costs
	// three round trips (prepare, execute, close); startup migrations and
	// batched writes issue tens of thousands of them.
	if !query.Has("interpolateParams") {
		query.Set("interpolateParams", "true")
	}
	return fmt.Sprintf("%s@tcp(%s)/%s?%s", credentials, parsed.Host, strings.TrimPrefix(parsed.Path, "/"), query.Encode()), nil
}

func (d *sqlDatabase) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == d {
		return nil, errors.New("cannot write inside a workspace read snapshot")
	}
	return d.DB.ExecContext(ctx, rewriteSQL(query, d.dialect), textArgs(d.dialect, args)...)
}

func (d *sqlDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == d {
		return snapshot.reader.QueryContext(ctx, query, args...)
	}
	return d.DB.QueryContext(ctx, rewriteSQL(query, d.dialect), textArgs(d.dialect, args)...)
}

func (d *sqlDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == d {
		return snapshot.reader.QueryRowContext(ctx, query, args...)
	}
	return d.DB.QueryRowContext(ctx, rewriteSQL(query, d.dialect), textArgs(d.dialect, args)...)
}

// claimTxOptions is the isolation for lease/claim transactions that select
// candidates with FOR UPDATE SKIP LOCKED. MySQL's default REPEATABLE READ takes
// gap and next-key locks on the scanned index range, which SKIP LOCKED does not
// skip, so concurrent claimers deadlock (error 1213) on the follow-up UPDATE.
// READ COMMITTED locks only the matched rows (and is PostgreSQL's default).
var claimTxOptions = &sql.TxOptions{Isolation: sql.LevelReadCommitted}

func (d *sqlDatabase) BeginTx(ctx context.Context, options *sql.TxOptions) (*sqlTx, error) {
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == d {
		return nil, errors.New("cannot start a transaction inside a workspace read snapshot")
	}
	tx, err := d.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlTx{Tx: tx, dialect: d.dialect, maxTransactionBytes: d.maxTransactionBytes, database: d, ctx: ctx}, nil
}

func (tx *sqlTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx.writeErr != nil {
		return nil, tx.writeErr
	}
	bytes := len(query)
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			bytes += len(value)
		case []byte:
			bytes += len(value)
		case json.RawMessage:
			bytes += len(value)
		default:
			bytes += 16
		}
	}
	if tx.maxTransactionBytes > 0 && bytes > tx.maxTransactionBytes-tx.writtenBytes {
		tx.writeErr = fmt.Errorf("%w: maximum %d bytes; split the import into smaller batches", ErrTransactionWriteBudget, tx.maxTransactionBytes)
		return nil, tx.writeErr
	}
	tx.writtenBytes += bytes
	return tx.Tx.ExecContext(ctx, rewriteSQL(query, tx.dialect), textArgs(tx.dialect, args)...)
}

func (tx *sqlTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, rewriteSQL(query, tx.dialect), textArgs(tx.dialect, args)...)
}

func (tx *sqlTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, rewriteSQL(query, tx.dialect), textArgs(tx.dialect, args)...)
}

var excludedColumn = regexp.MustCompile(`excluded\.([a-zA-Z_][a-zA-Z0-9_]*)`)
var conflictUpdateClause = regexp.MustCompile(`(?i)\s+ON\s+CONFLICT\s*\([^)]*\)\s+DO\s+UPDATE\s+SET\s+`)
var conflictIgnoreClause = regexp.MustCompile(`(?i)\s+ON\s+CONFLICT(?:\s*\([^)]*\))?\s+DO\s+NOTHING$`)

func rewriteSQL(query, dialect string) string {
	query = strings.TrimSpace(query)
	if dialect == "mysql" {
		if index := conflictIgnoreClause.FindStringIndex(query); index != nil {
			query = query[:index[0]]
			query = strings.Replace(query, "INSERT INTO ", "INSERT IGNORE INTO ", 1)
		}
		if index := conflictUpdateClause.FindStringIndex(query); index != nil {
			updates := excludedColumn.ReplaceAllString(query[index[1]:], "VALUES($1)")
			query = query[:index[0]] + " ON DUPLICATE KEY UPDATE " + updates
		}
	}
	if dialect == "postgres" {
		var builder strings.Builder
		argument := 1
		for index := 0; index < len(query); index++ {
			if query[index] == '?' {
				builder.WriteByte('$')
				builder.WriteString(strconv.Itoa(argument))
				argument++
			} else {
				builder.WriteByte(query[index])
			}
		}
		query = builder.String()
	}
	return query
}

func databaseMigrations(dialect string) []string {
	idType, emailType, queryType, blobType, claimsType := "TEXT", "TEXT", "TEXT", "BLOB", "TEXT"
	if dialect == "postgres" {
		blobType = "BYTEA"
	}
	if dialect == "mysql" {
		idType, emailType, queryType, blobType, claimsType = "VARCHAR(191)", "VARCHAR(320)", "VARCHAR(300)", "LONGBLOB", "LONGTEXT"
	}
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_state (id INTEGER PRIMARY KEY, data %s NOT NULL, updated_at VARCHAR(40) NOT NULL)`, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_states (workspace_key %s PRIMARY KEY, workspace_id %s NOT NULL UNIQUE, data %s NOT NULL, updated_at VARCHAR(40) NOT NULL)`, idType, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS account_state (id INTEGER PRIMARY KEY, last_workspace_key %s NOT NULL, viewer %s NOT NULL, updated_at VARCHAR(40) NOT NULL)`, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS domain_events (id %s PRIMARY KEY, event_type VARCHAR(191) NOT NULL, aggregate_id %s NOT NULL, payload %s NOT NULL, created_at VARCHAR(40) NOT NULL)`, idType, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_users (id %s PRIMARY KEY, email %s UNIQUE, name VARCHAR(320) NOT NULL, display_name VARCHAR(320) NOT NULL, avatar_url VARCHAR(2048) NOT NULL DEFAULT '', password_hash VARCHAR(255) NOT NULL, email_verified_at VARCHAR(40), active INTEGER NOT NULL DEFAULT 1, created_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`, idType, emailType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_identities (id %s PRIMARY KEY, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, provider VARCHAR(191) NOT NULL, issuer VARCHAR(2048) NOT NULL, subject VARCHAR(320) NOT NULL, identity_key VARCHAR(64) NOT NULL UNIQUE, username VARCHAR(320) NOT NULL DEFAULT '', claims_json %s NOT NULL, created_at VARCHAR(40) NOT NULL, last_login_at VARCHAR(40) NOT NULL)`, idType, idType, claimsType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_sessions (token_hash %s PRIMARY KEY, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, expires_at VARCHAR(40) NOT NULL, created_at VARCHAR(40) NOT NULL, last_seen_at VARCHAR(40) NOT NULL)`, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_account_state (user_id %s PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE, last_workspace_key %s NOT NULL, updated_at VARCHAR(40) NOT NULL)`, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS auth_tokens (token_hash %s PRIMARY KEY, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, kind VARCHAR(64) NOT NULL, expires_at VARCHAR(40) NOT NULL, used_at VARCHAR(40), created_at VARCHAR(40) NOT NULL)`, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS oauth_clients (client_id %s PRIMARY KEY, data %s NOT NULL, created_at VARCHAR(40) NOT NULL)`, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS oauth_authorization_codes (code_hash %s PRIMARY KEY, data %s NOT NULL, expires_at VARCHAR(40) NOT NULL, used_at VARCHAR(40), created_at VARCHAR(40) NOT NULL)`, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (token_hash %s PRIMARY KEY, data %s NOT NULL, expires_at VARCHAR(40) NOT NULL, revoked_at VARCHAR(40), created_at VARCHAR(40) NOT NULL)`, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_memberships (workspace_id %s NOT NULL, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, role VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL DEFAULT 'active', joined_at VARCHAR(40) NOT NULL, last_seen_at VARCHAR(40), PRIMARY KEY(workspace_id,user_id))`, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS team_memberships (workspace_id %s NOT NULL, team_id %s NOT NULL, user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, role VARCHAR(32) NOT NULL DEFAULT 'member', joined_at VARCHAR(40) NOT NULL, PRIMARY KEY(workspace_id,team_id,user_id))`, idType, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS workspace_invitations (id %s PRIMARY KEY, workspace_id %s NOT NULL, email %s NOT NULL, role VARCHAR(32) NOT NULL, team_ids %s NOT NULL, token_hash %s NOT NULL UNIQUE, inviter_id %s NOT NULL REFERENCES auth_users(id), status VARCHAR(32) NOT NULL DEFAULT 'pending', expires_at VARCHAR(40) NOT NULL, created_at VARCHAR(40) NOT NULL, accepted_at VARCHAR(40))`, idType, idType, emailType, blobType, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS search_history (user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, workspace_id %s NOT NULL, query %s NOT NULL, use_count INTEGER NOT NULL DEFAULT 1, last_used_at VARCHAR(40) NOT NULL, PRIMARY KEY(user_id,workspace_id,query))`, idType, idType, queryType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS recently_viewed (user_id %s NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, workspace_id %s NOT NULL, resource_type VARCHAR(64) NOT NULL, resource_id %s NOT NULL, last_viewed_at VARCHAR(40) NOT NULL, PRIMARY KEY(user_id,workspace_id,resource_type,resource_id))`, idType, idType, idType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS document_collaboration_updates (update_id %s PRIMARY KEY, workspace_key %s NOT NULL, document_id %s NOT NULL, client_id %s NOT NULL, update_data %s NOT NULL, created_at VARCHAR(40) NOT NULL)`, idType, idType, idType, idType, blobType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS scim_tokens (id %s PRIMARY KEY, workspace_id %s NOT NULL, token_hash %s NOT NULL UNIQUE, name VARCHAR(191) NOT NULL, created_at VARCHAR(40) NOT NULL, last_used_at VARCHAR(40), revoked_at VARCHAR(40))`, idType, idType, idType),
	}
	indexes := []string{
		"workspace_states_updated_idx ON workspace_states(updated_at)",
		"domain_events_aggregate_idx ON domain_events(aggregate_id,created_at)",
		"domain_events_created_idx ON domain_events(created_at)",
		"auth_sessions_user_idx ON auth_sessions(user_id,expires_at)",
		"auth_identities_user_idx ON auth_identities(user_id)",
		"workspace_memberships_user_idx ON workspace_memberships(user_id,status)",
		"team_memberships_role_idx ON team_memberships(workspace_id,team_id,role)",
		"workspace_invitations_email_idx ON workspace_invitations(email,status)",
		"workspace_invitations_workspace_created_idx ON workspace_invitations(workspace_id,created_at)",
		"search_history_recent_idx ON search_history(user_id,workspace_id,last_used_at)",
		"recently_viewed_recent_idx ON recently_viewed(user_id,workspace_id,last_viewed_at)",
		"document_collaboration_updates_document_idx ON document_collaboration_updates(workspace_key,document_id,created_at)",
		"scim_tokens_workspace_idx ON scim_tokens(workspace_id,revoked_at)",
	}
	for _, index := range indexes {
		prefix := "CREATE INDEX IF NOT EXISTS "
		if dialect == "mysql" {
			prefix = "CREATE INDEX "
		}
		statements = append(statements, prefix+index)
	}
	return statements
}
