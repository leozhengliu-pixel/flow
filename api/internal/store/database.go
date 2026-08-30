package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type DatabaseConfig struct {
	Driver          string
	URL             string
	Path            string
	FixtureProfile  string
	FixturePassword string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	MaxStateBytes   int
}

type sqlDatabase struct {
	*sql.DB
	dialect string
}

type sqlTx struct {
	*sql.Tx
	dialect string
}

func OpenDatabase(config DatabaseConfig) (*SQLiteStore, error) {
	driver := strings.ToLower(strings.TrimSpace(config.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver == "postgresql" {
		driver = "postgres"
	}
	var sqlDriver, dsn string
	switch driver {
	case "sqlite":
		path := strings.TrimSpace(config.Path)
		if path == "" {
			path = "data/flow.db"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		sqlDriver, dsn = "sqlite", sqliteDSN(path)
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
	s := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: driver}, dialect: driver, fixtureProfile: strings.TrimSpace(config.FixtureProfile), fixturePassword: config.FixturePassword, maxStateBytes: maxStateBytes}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.loadOrSeed(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(config.FixtureProfile), "test") {
		if err := s.ensureAuthTestFixture(context.Background()); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
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
		return raw + separator + "parseTime=true", nil
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
	return fmt.Sprintf("%s@tcp(%s)/%s?%s", credentials, parsed.Host, strings.TrimPrefix(parsed.Path, "/"), query.Encode()), nil
}

func (d *sqlDatabase) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, rewriteSQL(query, d.dialect), args...)
}

func (d *sqlDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, rewriteSQL(query, d.dialect), args...)
}

func (d *sqlDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, rewriteSQL(query, d.dialect), args...)
}

func (d *sqlDatabase) BeginTx(ctx context.Context, options *sql.TxOptions) (*sqlTx, error) {
	tx, err := d.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlTx{Tx: tx, dialect: d.dialect}, nil
}

func (tx *sqlTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx *sqlTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx *sqlTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

var excludedColumn = regexp.MustCompile(`excluded\.([a-zA-Z_][a-zA-Z0-9_]*)`)

func rewriteSQL(query, dialect string) string {
	query = strings.TrimSpace(query)
	if dialect == "mysql" {
		if strings.HasSuffix(query, " ON CONFLICT DO NOTHING") {
			query = strings.TrimSuffix(query, " ON CONFLICT DO NOTHING")
			query = strings.Replace(query, "INSERT INTO ", "INSERT IGNORE INTO ", 1)
		}
		if index := strings.Index(query, " ON CONFLICT("); index >= 0 {
			rest := query[index+len(" ON CONFLICT("):]
			if closeIndex := strings.Index(rest, ") DO UPDATE SET "); closeIndex >= 0 {
				updates := rest[closeIndex+len(") DO UPDATE SET "):]
				updates = excludedColumn.ReplaceAllString(updates, "VALUES($1)")
				query = query[:index] + " ON DUPLICATE KEY UPDATE " + updates
			}
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
		idType, emailType, queryType, blobType, claimsType = "VARCHAR(191)", "VARCHAR(320)", "VARCHAR(300)", "LONGBLOB", "TEXT"
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
	}
	indexes := []string{
		"workspace_states_updated_idx ON workspace_states(updated_at)",
		"domain_events_aggregate_idx ON domain_events(aggregate_id,created_at)",
		"domain_events_created_idx ON domain_events(created_at)",
		"auth_sessions_user_idx ON auth_sessions(user_id,expires_at)",
		"auth_identities_user_idx ON auth_identities(user_id)",
		"workspace_memberships_user_idx ON workspace_memberships(user_id,status)",
		"workspace_invitations_email_idx ON workspace_invitations(email,status)",
		"workspace_invitations_workspace_created_idx ON workspace_invitations(workspace_id,created_at)",
		"search_history_recent_idx ON search_history(user_id,workspace_id,last_used_at)",
		"recently_viewed_recent_idx ON recently_viewed(user_id,workspace_id,last_viewed_at)",
		"document_collaboration_updates_document_idx ON document_collaboration_updates(workspace_key,document_id,created_at)",
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
