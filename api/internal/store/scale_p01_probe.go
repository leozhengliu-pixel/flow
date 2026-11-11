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
	"time"
)

// ScaleP01Commit is stamped immediately after the driver's successful commit,
// before sequencing and publication. CreatedAt is not a commit timestamp.
type ScaleP01Commit struct {
	ID, Type, AggregateID string
	At                    time.Time
}

type scaleP01RequestKey struct{}

func ScaleP01RequestContext(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, scaleP01RequestKey{}, requestID)
}

type ScaleP01SQL struct {
	Statements, Reads, Writes, Errors, Commits int64
	ExecuteSeconds                             float64
	ExecutionBuckets                           [64]int64
}

// ScaleP01Probe counts driver executions, including prepared statements and
// transactions. It excludes prepare/BEGIN/COMMIT from Statements. Query timing
// measures driver execution, not subsequent row iteration or server lock time.
type ScaleP01Probe struct {
	mu          sync.Mutex
	sql         ScaleP01SQL
	events      [10000]ScaleP01Commit
	next        int64
	firstCommit map[string]time.Time
	requests    [10000]string
	requestNext int
}

func (p *ScaleP01Probe) Snapshot() (ScaleP01SQL, []ScaleP01Commit, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make([]ScaleP01Commit, 0, min(p.next, int64(len(p.events))))
	for i := max(int64(0), p.next-int64(len(p.events))); i < p.next; i++ {
		events = append(events, p.events[i%int64(len(p.events))])
	}
	return p.sql, events, max(int64(0), p.next-int64(len(p.events)))
}

func (p *ScaleP01Probe) SQLSnapshot() ScaleP01SQL {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sql
}

func (p *ScaleP01Probe) executed(query string, began time.Time, err error) {
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

func (p *ScaleP01Probe) committed(events []ScaleP01Commit, at time.Time) {
	p.committedRequest(events, at, "", false)
}

func (p *ScaleP01Probe) committedRequest(events []ScaleP01Commit, at time.Time, requestID string, mutated bool) {
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

// EnableScaleP01Probe must be called after opening the store and before using
// it from handlers. The replacement pool uses the same driver, DSN and limits.
// This entire file is absent from production builds.
func (s *SQLiteStore) EnableScaleP01Probe(config DatabaseConfig) (*ScaleP01Probe, error) {
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
	probe := &ScaleP01Probe{}
	old := s.db.DB
	db := sql.OpenDB(&scaleP01Connector{inner: old.Driver(), dsn: dsn, probe: probe})
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
	return probe, nil
}

type scaleP01Connector struct {
	inner driver.Driver
	dsn   string
	probe *ScaleP01Probe
}

func (c *scaleP01Connector) Driver() driver.Driver { return c.inner }
func (c *scaleP01Connector) Connect(ctx context.Context) (driver.Conn, error) {
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
	return &scaleP01Conn{Conn: conn, probe: c.probe}, nil
}

type scaleP01Conn struct {
	driver.Conn
	probe     *ScaleP01Probe
	pending   []ScaleP01Commit
	inTx      bool
	requestID string
	mutated   bool
}

func (c *scaleP01Conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *scaleP01Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
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
	return &scaleP01Stmt{Stmt: stmt, conn: c, query: query}, nil
}
func (c *scaleP01Conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *scaleP01Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
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
	c.requestID, _ = ctx.Value(scaleP01RequestKey{}).(string)
	c.mutated = false
	return &scaleP01Tx{Tx: tx, conn: c}, nil
}
func (c *scaleP01Conn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}
func (c *scaleP01Conn) CheckNamedValue(v *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(v)
	}
	return driver.ErrSkip
}
func (c *scaleP01Conn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		return conn.ResetSession(ctx)
	}
	return nil
}
func (c *scaleP01Conn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}
func (c *scaleP01Conn) record(query string, args []driver.NamedValue, err error) {
	if err != nil {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(query))
	// Membership writes commit before their audit event. Link that first
	// business commit to the event using the test-only request context.
	for _, table := range []string{"team_memberships", "issue_records", "workspace_metadata_records", "workspace_content_records", "domain_events"} {
		if strings.HasPrefix(lower, "insert into "+table) || strings.HasPrefix(lower, "update "+table) || strings.HasPrefix(lower, "delete from "+table) {
			c.mutated = true
			break
		}
	}
	if !strings.HasPrefix(lower, "insert into domain_events(") || len(args) < 3 {
		return
	}
	event := ScaleP01Commit{ID: fmt.Sprint(args[0].Value), Type: fmt.Sprint(args[1].Value), AggregateID: fmt.Sprint(args[2].Value)}
	if c.inTx {
		c.pending = append(c.pending, event)
	} else {
		c.probe.committed([]ScaleP01Commit{event}, time.Now())
	}
}
func (c *scaleP01Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
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
func (c *scaleP01Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	began := time.Now()
	rows, err := conn.QueryContext(ctx, query, args)
	c.probe.executed(query, began, err)
	return rows, err
}

type scaleP01Tx struct {
	driver.Tx
	conn *scaleP01Conn
}

func (tx *scaleP01Tx) Commit() error {
	err := tx.Tx.Commit()
	if err == nil {
		tx.conn.probe.committedRequest(tx.conn.pending, time.Now(), tx.conn.requestID, tx.conn.mutated)
	}
	tx.conn.pending, tx.conn.inTx = nil, false
	return err
}
func (tx *scaleP01Tx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.conn.pending, tx.conn.inTx = nil, false
	return err
}

type scaleP01Stmt struct {
	driver.Stmt
	conn  *scaleP01Conn
	query string
}

func scaleP01Named(values []driver.Value) []driver.NamedValue {
	args := make([]driver.NamedValue, len(values))
	for i, value := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return args
}
func scaleP01Values(args []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return values
}
func (s *scaleP01Stmt) Exec(values []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), scaleP01Named(values))
}
func (s *scaleP01Stmt) Query(values []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), scaleP01Named(values))
}
func (s *scaleP01Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	began := time.Now()
	var result driver.Result
	var err error
	if stmt, ok := s.Stmt.(driver.StmtExecContext); ok {
		result, err = stmt.ExecContext(ctx, args)
	} else {
		result, err = s.Stmt.Exec(scaleP01Values(args))
	}
	s.conn.probe.executed(s.query, began, err)
	s.conn.record(s.query, args, err)
	return result, err
}
func (s *scaleP01Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	began := time.Now()
	var rows driver.Rows
	var err error
	if stmt, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err = stmt.QueryContext(ctx, args)
	} else {
		rows, err = s.Stmt.Query(scaleP01Values(args))
	}
	s.conn.probe.executed(s.query, began, err)
	return rows, err
}
