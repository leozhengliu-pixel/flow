package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Change-log cursors on MySQL and PostgreSQL are assigned after commit.
//
// The cursor (domain_events.seq) must let a reader that has seen seq S never
// later find a committed event below S: readers resume from the highest seq
// they read, and the realtime hub releases events in seq order. Allocating it
// inside the writing transaction from a per-workspace counter row guarantees
// that, but the row is then held from the allocation until commit, so every
// writer of a workspace, on every server, waits for the one before it to
// commit. Allocating it from anything that does not serialise commits (a
// database sequence, a Redis counter, blocks per server) breaks it: two
// writers take 7 and 8, 8 commits first, a reader reads 8 and resumes past 7.
//
// Instead a writer inserts its events without a seq and, as one of its last
// statements, a row in domain_event_sequencing whose order_id comes from an
// auto-increment counter (no row lock). After commit it numbers the
// workspace's committed, unnumbered events in a short transaction of its own
// (sequenceWorkspaceBatch): holding the workspace's numbering lock, it gives
// every sequencing row it can see the next seqs in order_id order, deletes
// those rows and moves the workspace_event_seq head on. Only these numbering
// transactions assign seqs, one at a time per workspace, and each starts after
// the previous one committed, so:
//
//   - seqs are gap-free and a numbering commits seqs above every seq
//     committed before it: a reader that sees seq S sees every seq below S,
//     so cursor readers never skip an event;
//   - events are numbered in an order consistent with commit order: an event
//     whose transaction read or overwrote another's data took its order_id
//     after that transaction committed, so it gets the higher seq (order_ids
//     rise with time, and the sequencing row is written after the
//     transaction's reads and writes);
//   - one numbering covers every writer that committed before it began, so a
//     writer waits for the numbering in progress, not for every writer ahead
//     of it, and numbers only events no numbering covered yet.
//
// Events become readable through the change log only once numbered. A writer
// numbers before it returns (and before its realtime and webhook fan-out,
// which stamps the seq), so a client that saw a write's response finds it in
// the log. A writer that dies between commit and numbering leaves its events
// to the next writer of the workspace, or to readers, which number events left
// unnumbered for staleSequencingAge (rescueStaleSequencing).
//
// SQLite runs one writer at a time, so it keeps allocating in the transaction.

// staleSequencingAge is how long a committed event may wait for its writer to
// number it before a reader or the retention sweep numbers it.
const staleSequencingAge = 2 * time.Second

// sequencingBatch bounds how many events one numbering transaction assigns.
const sequencingBatch = 500

type pendingEventSequence struct {
	workspaceID, eventID string
}

// sequencingLockClass is the first key of the PostgreSQL advisory locks
// numberings take; the second is a hash of the workspace (two workspaces
// that share one only take turns).
const sequencingLockClass int32 = 0x464c5751

func sequencingLockKey(workspaceID string) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(workspaceID))
	return int32(hash.Sum32())
}

func (s *SQLiteStore) createEventSequencingSchema(ctx context.Context) error {
	orderColumn := "order_id INTEGER PRIMARY KEY AUTOINCREMENT"
	switch s.dialect {
	case "mysql":
		orderColumn = "order_id BIGINT AUTO_INCREMENT PRIMARY KEY"
	case "postgres":
		orderColumn = "order_id BIGSERIAL PRIMARY KEY"
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		`CREATE TABLE IF NOT EXISTS domain_event_sequencing (` + orderColumn + `, workspace_id VARCHAR(191) NOT NULL, event_id VARCHAR(191) NOT NULL, created_ms BIGINT NOT NULL)`,
		prefix + "domain_event_sequencing_workspace_idx ON domain_event_sequencing(workspace_id,order_id)",
	})
}

// sequencesAfterCommit reports whether events written through db are
// numbered after commit; otherwise insertDomainEvent allocates their seq in
// the transaction.
func sequencesAfterCommit(db domainEventExecer) (*sqlTx, bool) {
	tx, ok := db.(*sqlTx)
	return tx, ok && tx.database != nil && tx.dialect != "sqlite"
}

// queueEventSequencing writes the sequencing rows of the events the
// transaction inserted, in insertion order. Commit calls it after the
// transaction's own statements, so the order_ids are taken after everything
// the events describe was read and written.
func (tx *sqlTx) queueEventSequencing() error {
	if len(tx.sequencing) == 0 {
		return nil
	}
	ctx := tx.context()
	now := time.Now().UnixMilli()
	for start := 0; start < len(tx.sequencing); start += sequencingBatch {
		batch := tx.sequencing[start:min(start+sequencingBatch, len(tx.sequencing))]
		values := strings.TrimSuffix(strings.Repeat("(?,?,?),", len(batch)), ",")
		args := make([]any, 0, len(batch)*3)
		for _, pending := range batch {
			args = append(args, pending.workspaceID, pending.eventID, now)
		}
		if _, err := tx.Tx.ExecContext(ctx, rewriteSQL(`INSERT INTO domain_event_sequencing(workspace_id,event_id,created_ms) VALUES`+values, tx.dialect), args...); err != nil {
			return err
		}
	}
	return nil
}

// sequenceCommittedEvents numbers the events the committed transaction
// inserted. A failure leaves them for a later numbering; the write itself
// has committed.
func (tx *sqlTx) sequenceCommittedEvents() {
	if len(tx.sequencing) == 0 {
		return
	}
	pending := tx.sequencing
	tx.sequencing = nil
	ctx, cancel := context.WithTimeout(context.WithoutCancel(tx.context()), 10*time.Second)
	defer cancel()
	byWorkspace := map[string][]string{}
	workspaces := []string{}
	for _, event := range pending {
		if byWorkspace[event.workspaceID] == nil {
			workspaces = append(workspaces, event.workspaceID)
		}
		byWorkspace[event.workspaceID] = append(byWorkspace[event.workspaceID], event.eventID)
	}
	for _, workspace := range workspaces {
		if err := tx.database.sequenceWorkspaceEvents(ctx, workspace, byWorkspace[workspace]); err != nil {
			slog.Warn("change log numbering deferred", "workspace", workspace, "error", err)
		}
	}
}

// sequenceWorkspaceEvents numbers the workspace's committed, unnumbered
// events. A writer passes its events' ids and returns once they are numbered,
// by its own numbering or another's; without ids (rescues) it numbers
// everything committed so far.
//
// On MySQL the writer first tries the numbering lock without waiting. When another
// numbering holds it, the writer waits for that one to commit in shared mode
// (every writer waiting so is released at once) and returns if it covered
// the writer's events. Only a writer it did not cover (one that committed
// after it began) queues for the lock, and one that the numbering ahead of it
// in that queue covered leaves without numbering events committed since,
// which are their own writers' to number. Writers queued behind one
// numbering thus do not take empty turns at the lock one after another.
// PostgreSQL queues directly: its single batch statement does no durable
// write on an empty queue, so preflight reads and shared-lock turns cost
// more than simply checking the queue after the exclusive lock is held.
func (d *sqlDatabase) sequenceWorkspaceEvents(ctx context.Context, workspaceID string, ids []string) error {
	if len(ids) > 0 && d.dialect != "postgres" {
		if numbered, err := eventsNumbered(ctx, d, ids); err != nil || numbered {
			return err
		}
	}
	mode := numberingTry
	if len(ids) == 0 || d.dialect == "postgres" {
		mode = numberingWait
	}
	for {
		consumed, covered, err := d.sequenceWorkspaceBatch(ctx, workspaceID, ids, mode)
		switch {
		case err != nil || covered:
			return err
		case mode == numberingTry && consumed == 0:
			mode = numberingWait
		case consumed < sequencingBatch:
			return nil
		default:
			ids, mode = nil, numberingWait
		}
	}
}

// eventsNumbered reports whether every event in ids has its seq.
func eventsNumbered(ctx context.Context, db domainEventExecer, ids []string) (bool, error) {
	clause, args := bindList("id", ids)
	var unnumbered int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE seq IS NULL AND `+clause, args...).Scan(&unnumbered)
	return unnumbered == 0, err
}

type numberingMode int

const (
	// numberingTry takes the numbering lock only if it is free, and
	// otherwise waits for the numbering holding it to commit.
	numberingTry numberingMode = iota
	// numberingWait queues for the numbering lock.
	numberingWait
)

// sequenceWorkspaceBatch runs one numbering transaction. It reports how many
// sequencing rows it consumed, and whether the events in ids turned out to
// be numbered by another numbering, in which case it numbered nothing.
func (d *sqlDatabase) sequenceWorkspaceBatch(ctx context.Context, workspaceID string, ids []string, mode numberingMode) (int, bool, error) {
	// READ COMMITTED: each statement sees what committed before it, so the
	// sequencing rows read once the lock is held include every event
	// committed before the previous numbering released it.
	tx, err := d.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	tx.numbering = true
	acquired, err := tx.lockNumbering(ctx, workspaceID, mode)
	if err != nil {
		return 0, false, err
	}
	if !acquired {
		// Another numbering held the lock; it has committed now.
		covered, err := eventsNumbered(ctx, tx, ids)
		if err != nil {
			return 0, false, err
		}
		return 0, covered, tx.Commit()
	}
	if d.dialect == "postgres" {
		return tx.sequencePostgresBatch(ctx, workspaceID)
	}
	if mode == numberingWait && len(ids) > 0 {
		if covered, err := eventsNumbered(ctx, tx, ids); err != nil || covered {
			if err == nil {
				err = tx.Commit()
			}
			return 0, covered, err
		}
	}
	// e.seq is not null for an event already numbered (by the startup
	// backfill or an older server); a missing event was deleted. Neither
	// takes a seq.
	rows, err := tx.QueryContext(ctx, `SELECT q.order_id,q.event_id,e.id,e.seq FROM domain_event_sequencing q LEFT JOIN domain_events e ON e.id=q.event_id WHERE q.workspace_id=? ORDER BY q.order_id LIMIT ?`, workspaceID, sequencingBatch)
	if err != nil {
		return 0, false, err
	}
	orders := []any{}
	assign := []string{}
	for rows.Next() {
		var order int64
		var eventID string
		var found sql.NullString
		var seq sql.NullInt64
		if err := rows.Scan(&order, &eventID, &found, &seq); err != nil {
			rows.Close()
			return 0, false, err
		}
		orders = append(orders, order)
		if found.Valid && !seq.Valid {
			assign = append(assign, eventID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, false, err
	}
	if len(orders) == 0 {
		return 0, false, tx.Commit()
	}
	if len(assign) > 0 {
		head, err := tx.lockEventHead(ctx, workspaceID)
		if err != nil {
			return 0, false, err
		}
		// The cast types the bound seqs for PostgreSQL, which cannot infer a
		// CASE result's type from its parameters.
		cast := "CAST(? AS BIGINT)"
		if d.dialect == "mysql" {
			cast = "CAST(? AS SIGNED)"
		}
		var cases strings.Builder
		args := []any{}
		for index, id := range assign {
			cases.WriteString(" WHEN ? THEN " + cast)
			args = append(args, id, head+int64(index)+1)
		}
		clause, idArgs := bindList("id", assign)
		args = append(args, idArgs...)
		result, err := tx.ExecContext(ctx, `UPDATE domain_events SET seq=CASE id`+cases.String()+` END WHERE seq IS NULL AND `+clause, args...)
		if err != nil {
			return 0, false, err
		}
		if n, err := result.RowsAffected(); err != nil || n != int64(len(assign)) {
			if err == nil {
				err = fmt.Errorf("numbered %d of %d change log events", n, len(assign))
			}
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_event_seq SET head=? WHERE workspace_id=?`, head+int64(len(assign)), workspaceID); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM domain_event_sequencing WHERE order_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(orders)), ",")+`)`, orders...); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return len(orders), false, nil
}

// sequencePostgresBatch assigns and retires a batch in one round trip. The
// advisory lock was taken by a separate statement, so this statement's
// snapshot includes the previous numbering's commit even if we waited for
// it. Taking the lock inside this CTE would reuse an older snapshot.
func (tx *sqlTx) sequencePostgresBatch(ctx context.Context, workspaceID string) (int, bool, error) {
	var consumed, assigned, expected int
	err := tx.QueryRowContext(ctx, `WITH queued AS MATERIALIZED (
		SELECT order_id,event_id FROM domain_event_sequencing WHERE workspace_id=? ORDER BY order_id LIMIT ?
	), pending AS MATERIALIZED (
		SELECT e.id,row_number() OVER (ORDER BY q.order_id) AS position
		FROM queued q JOIN domain_events e ON e.id=q.event_id WHERE e.seq IS NULL
	), advanced AS (
		INSERT INTO workspace_event_seq(workspace_id,head,pruned)
		SELECT ?,COUNT(*),0 FROM pending HAVING COUNT(*)>0
		ON CONFLICT(workspace_id) DO UPDATE SET head=workspace_event_seq.head+excluded.head
		RETURNING head
	), numbered AS (
		UPDATE domain_events e SET seq=h.head-(SELECT COUNT(*) FROM pending)+p.position
		FROM pending p CROSS JOIN advanced h WHERE e.id=p.id AND e.seq IS NULL RETURNING e.id
	), retired AS (
		DELETE FROM domain_event_sequencing WHERE order_id IN (SELECT order_id FROM queued) RETURNING order_id
	)
	SELECT (SELECT COUNT(*) FROM retired),(SELECT COUNT(*) FROM numbered),(SELECT COUNT(*) FROM pending)`, workspaceID, sequencingBatch, workspaceID).Scan(&consumed, &assigned, &expected)
	if err != nil {
		return 0, false, err
	}
	if assigned != expected {
		return 0, false, fmt.Errorf("numbered %d of %d change log events", assigned, expected)
	}
	return consumed, false, tx.Commit()
}

// lockNumbering takes the workspace's numbering lock, which serialises the
// numbering transactions of one workspace. With numberingTry and the lock
// held elsewhere it waits for the holder to commit, in shared mode, and
// reports false.
//
// MySQL locks the workspace_event_seq row; InnoDB keeps row locks in memory.
// PostgreSQL writes a row lock to the WAL, so a numbering that finds nothing
// to do would wait for a log flush before letting the next one in; there the
// lock is a transaction-scoped advisory lock (held in memory), and the row is
// locked only to assign seqs (lockEventHead).
func (tx *sqlTx) lockNumbering(ctx context.Context, workspaceID string, mode numberingMode) (bool, error) {
	switch tx.dialect {
	case "postgres":
		class, key := sequencingLockClass, sequencingLockKey(workspaceID)
		if mode == numberingWait {
			_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?,?)`, class, key)
			return err == nil, err
		}
		var acquired bool
		if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(?,?)`, class, key).Scan(&acquired); err != nil || acquired {
			return acquired, err
		}
		_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(?,?)`, class, key)
		return false, err
	case "mysql":
		if mode == numberingWait {
			_, err := tx.lockEventHead(ctx, workspaceID)
			return err == nil, err
		}
		var head int64
		err := tx.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=? FOR UPDATE SKIP LOCKED`, workspaceID).Scan(&head)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		// Locked elsewhere, or not created yet.
		err = tx.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=? FOR SHARE`, workspaceID).Scan(&head)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.lockEventHead(ctx, workspaceID)
			return err == nil, err
		}
		return false, err
	default:
		return true, nil
	}
}

// lockEventHead locks the workspace's counter row (creating it first) and
// reads the head. The lock also waits for an older server that still
// allocates seqs inside its write transaction.
func (tx *sqlTx) lockEventHead(ctx context.Context, workspaceID string) (int64, error) {
	lock := " FOR UPDATE"
	if tx.dialect == "sqlite" {
		lock = ""
	}
	var head int64
	err := tx.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=?`+lock, workspaceID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_event_seq(workspace_id,head,pruned) VALUES(?,0,0) ON CONFLICT DO NOTHING`, workspaceID); err == nil {
			err = tx.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=?`+lock, workspaceID).Scan(&head)
		}
	}
	return head, err
}

// rescueStaleSequencing numbers the workspace's events whose writer has not
// numbered them within staleSequencingAge (it failed or died after commit).
// Cursor readers call it, so such events are delayed, never lost.
func (s *SQLiteStore) rescueStaleSequencing(ctx context.Context, workspaceID string) error {
	if s.dialect == "sqlite" || workspaceID == "" {
		return nil
	}
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(created_ms) FROM domain_event_sequencing WHERE workspace_id=?`, workspaceID).Scan(&oldest); err != nil {
		return err
	}
	if !oldest.Valid || time.Since(time.UnixMilli(oldest.Int64)) < staleSequencingAge {
		return nil
	}
	return s.db.sequenceWorkspaceEvents(ctx, workspaceID, nil)
}

// rescueAllStaleSequencing numbers every workspace's stale events; the
// retention sweep runs it for workspaces nobody reads or writes.
func (s *SQLiteStore) rescueAllStaleSequencing(ctx context.Context) error {
	if s.dialect == "sqlite" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM domain_event_sequencing WHERE created_ms<?`, time.Now().Add(-staleSequencingAge).UnixMilli())
	if err != nil {
		return err
	}
	workspaces := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		workspaces = append(workspaces, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	slices.Sort(workspaces)
	for _, id := range workspaces {
		if err := s.db.sequenceWorkspaceEvents(ctx, id, nil); err != nil {
			return err
		}
	}
	return nil
}
