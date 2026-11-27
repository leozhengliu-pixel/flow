//go:build loadtest

package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ScaleCommit is stamped immediately after the driver's successful commit,
// before sequencing and publication. CreatedAt is not a commit timestamp.
type ScaleCommit struct {
	ID, Type, AggregateID string
	At                    time.Time
}

type scaleRequestKey struct{}

func ScaleRequestContext(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, scaleRequestKey{}, requestID)
}

type ScaleSQL struct {
	Statements, Reads, Writes, Errors, Commits int64
	ExecuteSeconds                             float64
	ExecutionBuckets                           [64]int64
}

// ScaleProbe counts driver executions, including prepared statements and
// transactions. It excludes prepare/BEGIN/COMMIT from Statements. Query timing
// measures driver execution, not subsequent row iteration or server lock time.
type ScaleProbe struct {
	activeWrites atomic.Int64
	mu           sync.Mutex
	sql          ScaleSQL
	events       [10000]ScaleCommit
	next         int64
	firstCommit  map[string]time.Time
	requests     [10000]string
	requestNext  int
}

func (p *ScaleProbe) BeginLoadWrite()         { p.activeWrites.Add(1) }
func (p *ScaleProbe) EndLoadWrite()           { p.activeWrites.Add(-1) }
func (p *ScaleProbe) ActiveLoadWrites() int64 { return p.activeWrites.Load() }

func (p *ScaleProbe) Snapshot() (ScaleSQL, []ScaleCommit, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make([]ScaleCommit, 0, min(p.next, int64(len(p.events))))
	for i := max(int64(0), p.next-int64(len(p.events))); i < p.next; i++ {
		events = append(events, p.events[i%int64(len(p.events))])
	}
	return p.sql, events, max(int64(0), p.next-int64(len(p.events)))
}

func (p *ScaleProbe) SQLSnapshot() ScaleSQL {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sql
}

func (p *ScaleProbe) executed(query string, began time.Time, err error) {
	if errors.Is(err, driver.ErrSkip) {
		return
	}
	duration := time.Since(began)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sql.Statements++
	p.sql.ExecuteSeconds += duration.Seconds()
	elapsed := uint64(max(duration.Nanoseconds(), 1))
	bucket := 0
	for elapsed > 1 {
		elapsed >>= 1
		bucket++
	}
	p.sql.ExecutionBuckets[bucket]++
	verb := ""
	if fields := strings.Fields(query); len(fields) > 0 {
		verb = strings.ToUpper(fields[0])
	}
	switch verb {
	case "SELECT", "WITH", "PRAGMA", "SHOW":
		p.sql.Reads++
	case "INSERT", "UPDATE", "DELETE", "REPLACE":
		p.sql.Writes++
	}
	if err != nil {
		p.sql.Errors++
	}
}

func (p *ScaleProbe) committed(events []ScaleCommit, at time.Time) {
	p.committedRequest(events, at, "", false)
}

func (p *ScaleProbe) committedRequest(events []ScaleCommit, at time.Time, requestID string, mutated bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sql.Commits++
	if requestID != "" && mutated {
		if p.firstCommit == nil {
			p.firstCommit = map[string]time.Time{}
		}
		if first, exists := p.firstCommit[requestID]; exists {
			at = first
		} else {
			delete(p.firstCommit, p.requests[p.requestNext])
			p.requests[p.requestNext] = requestID
			p.requestNext = (p.requestNext + 1) % len(p.requests)
			p.firstCommit[requestID] = at
		}
	}
	for _, event := range events {
		event.At = at
		p.events[p.next%int64(len(p.events))] = event
		p.next++
	}
}

// EnableScaleProbe must be called after opening the store and before using
// it from handlers. The replacement pool uses the same driver, DSN and limits.
// This entire file is absent from production builds.
func (s *SQLiteStore) EnableScaleProbe(config DatabaseConfig) (*ScaleProbe, error) {
	var dsn string
	switch s.dialect {
	case "sqlite":
		path := config.Path
		if path == "" {
			var seq int
			var name string
			if err := s.db.QueryRowContext(context.Background(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
				return nil, err
			}
		}
		if path == "" || path == ":memory:" {
			return nil, errors.New("P01 probe requires a file-backed SQLite database")
		}
		dsn = sqliteDSN(path)
	case "mysql":
		var err error
		dsn, err = mysqlDSN(config.URL)
		if err != nil {
			return nil, errors.New("invalid P01 MySQL test DSN")
		}
	case "postgres":
		dsn = config.URL
	}
	if dsn == "" {
		return nil, errors.New("P01 probe requires the test database configuration")
	}
	probe := &ScaleProbe{}
	old := s.db.DB
	db := sql.OpenDB(&loadProbeScaleConnector{inner: old.Driver(), dsn: dsn, probe: probe})
	maxOpen := old.Stats().MaxOpenConnections
	db.SetMaxOpenConns(maxOpen)
	idle := config.MaxIdleConns
	if idle == 0 {
		idle = 5
		if s.dialect == "sqlite" {
			idle = 1
		}
	}
	if maxOpen > 0 {
		idle = min(idle, maxOpen)
	}
	db.SetMaxIdleConns(max(idle, 0))
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, errors.New("P01 instrumented database connection failed")
	}
	s.db.DB = db
	if err := old.Close(); err != nil {
		return nil, err
	}
	// Prepared journal statements belong to the pool that created them.
	if s.db.journalSQLFlush != nil {
		s.db.journalSQLFlush, s.db.journalWrites = nil, nil
		if err := s.ensureSQLiteRecordJournalCommit(context.Background()); err != nil {
			return nil, err
		}
	}
	return probe, nil
}

type loadProbeScaleConnector struct {
	inner driver.Driver
	dsn   string
	probe *ScaleProbe
}

func (c *loadProbeScaleConnector) Driver() driver.Driver { return c.inner }
func (c *loadProbeScaleConnector) Connect(ctx context.Context) (driver.Conn, error) {
	var conn driver.Conn
	var err error
	if d, ok := c.inner.(driver.DriverContext); ok {
		var connector driver.Connector
		connector, err = d.OpenConnector(c.dsn)
		if err == nil {
			conn, err = connector.Connect(ctx)
		}
	} else {
		conn, err = c.inner.Open(c.dsn)
	}
	if err != nil {
		return nil, err
	}
	return &loadProbeScaleConn{Conn: conn, probe: c.probe}, nil
}

type loadProbeScaleConn struct {
	driver.Conn
	probe     *ScaleProbe
	pending   []ScaleCommit
	inTx      bool
	requestID string
	mutated   bool
}

func (c *loadProbeScaleConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *loadProbeScaleConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = conn.PrepareContext(ctx, query)
	} else {
		stmt, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &scaleStmt{Stmt: stmt, conn: c, query: query}, nil
}
func (c *loadProbeScaleConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *loadProbeScaleConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var tx driver.Tx
	var err error
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = conn.BeginTx(ctx, opts)
	} else if opts.Isolation != 0 || opts.ReadOnly {
		return nil, errors.New("P01 driver does not support transaction options")
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	c.pending, c.inTx = nil, true
	c.requestID, _ = ctx.Value(scaleRequestKey{}).(string)
	c.mutated = false
	return &scaleTx{Tx: tx, conn: c}, nil
}
func (c *loadProbeScaleConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}
func (c *loadProbeScaleConn) CheckNamedValue(v *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (c *loadProbeScaleConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		return conn.ResetSession(ctx)
	}
	return nil
}
func (c *loadProbeScaleConn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}
func (c *loadProbeScaleConn) record(query string, args []driver.NamedValue, err error) {
	if err != nil {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(query))
	words := strings.Fields(lower)
	verb, tableName := "", ""
	if len(words) >= 2 {
		verb = words[0]
		if verb == "update" {
			tableName = words[1]
		}
		if len(words) >= 3 && (verb == "insert" && words[1] == "into" || verb == "delete" && words[1] == "from") {
			tableName = strings.SplitN(words[2], "(", 2)[0]
		}
	}
	// Membership writes commit before their audit event. Link that first
	// business commit to the event using the test-only request context.
	for _, table := range []string{"team_memberships", "issue_records", "workspace_metadata_records", "workspace_content_records", "domain_events"} {
		if tableName == table {
			c.mutated = true
			break
		}
	}
	if verb != "insert" || tableName != "domain_events" || len(args) < 3 {
		return
	}
	event := ScaleCommit{ID: fmt.Sprint(args[0].Value), Type: fmt.Sprint(args[1].Value), AggregateID: fmt.Sprint(args[2].Value)}
	if c.inTx {
		c.pending = append(c.pending, event)
	} else {
		c.probe.committed([]ScaleCommit{event}, time.Now())
	}
}
func (c *loadProbeScaleConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	conn, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	began := time.Now()
	result, err := conn.ExecContext(ctx, query, args)
	c.probe.executed(query, began, err)
	c.record(query, args, err)
	return result, err
}
func (c *loadProbeScaleConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	began := time.Now()
	rows, err := conn.QueryContext(ctx, query, args)
	c.probe.executed(query, began, err)
	return rows, err
}

type scaleTx struct {
	driver.Tx
	conn *loadProbeScaleConn
}

func (tx *scaleTx) Commit() error {
	err := tx.Tx.Commit()
	if err == nil {
		tx.conn.probe.committedRequest(tx.conn.pending, time.Now(), tx.conn.requestID, tx.conn.mutated)
	}
	tx.conn.pending, tx.conn.inTx = nil, false
	return err
}
func (tx *scaleTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.conn.pending, tx.conn.inTx = nil, false
	return err
}

type scaleStmt struct {
	driver.Stmt
	conn  *loadProbeScaleConn
	query string
}

func scaleNamed(values []driver.Value) []driver.NamedValue {
	args := make([]driver.NamedValue, len(values))
	for i, value := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return args
}
func scaleValues(args []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return values
}
func (s *scaleStmt) Exec(values []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), scaleNamed(values))
}
func (s *scaleStmt) Query(values []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), scaleNamed(values))
}
func (s *scaleStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	began := time.Now()
	var result driver.Result
	var err error
	if stmt, ok := s.Stmt.(driver.StmtExecContext); ok {
		result, err = stmt.ExecContext(ctx, args)
	} else {
		result, err = s.Stmt.Exec(scaleValues(args))
	}
	s.conn.probe.executed(s.query, began, err)
	s.conn.record(s.query, args, err)
	return result, err
}
func (s *scaleStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	began := time.Now()
	var rows driver.Rows
	var err error
	if stmt, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err = stmt.QueryContext(ctx, args)
	} else {
		rows, err = s.Stmt.Query(scaleValues(args))
	}
	s.conn.probe.executed(s.query, began, err)
	return rows, err
}
