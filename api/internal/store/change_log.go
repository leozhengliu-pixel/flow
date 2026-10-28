package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// The change log is domain_events with a per-workspace cursor. Every domain
// event row carries seq, allocated from workspace_event_seq in the same
// transaction as the insert: the UPDATE takes the workspace's counter row, so
// concurrent writers of one workspace serialize on it and sequence order is
// commit order, gap-free. The counter is persisted, so it survives restarts,
// and multi-node deployments share it through the database (workspace writes
// are already serialized by the coordinator's workspace lock).
//
// Not in the log (they keep their own channels): presence, agent task and
// session streaming (agent_task.*), coding session state (coding_session.*),
// document collaboration updates, loop run
// progress (loop_run.*), notification.created signals and recurring-issue
// signals. Agent session and task state (agent_task.updated) is written
// outside the workspace transaction and is bursty, so it is not logged: after
// a resume, clients refetch agent sessions (agent_task.replied is logged).
// Those carry no seq and never advance a client's cursor.

// DefaultChangeLogMinRows is how many of a workspace's newest changes
// pruning always keeps, whatever their age.
const DefaultChangeLogMinRows = 10000

// MinChangeLogRetention is the shortest window the change log keeps events.
const MinChangeLogRetention = 7 * 24 * time.Hour

type domainEventExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// domainEventRow is one domain_events insert. WorkspaceKey resolves the
// workspace ID for writers that only know the key.
type domainEventRow struct {
	ID, Type, AggregateID string
	Payload               []byte
	PreviousValues        []byte
	CreatedAt             string
	WorkspaceID           string
	WorkspaceKey          string
}

// insertDomainEvent appends the event and assigns its change-log cursor. It
// must run inside the mutation's transaction so the cursor and the change
// commit together.
func insertDomainEvent(ctx context.Context, db domainEventExecer, row domainEventRow) (sql.Result, error) {
	var workspace any = row.WorkspaceID
	if row.WorkspaceID == "" && row.WorkspaceKey != "" {
		var id string
		err := db.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, row.WorkspaceKey).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		row.WorkspaceID = id
		workspace = nil
		if id != "" {
			workspace = id
		}
	}
	var seq, actor any
	if row.WorkspaceID != "" {
		next, err := nextWorkspaceSeq(ctx, db, row.WorkspaceID)
		if err != nil {
			return nil, err
		}
		seq = next
		if user, ok := actorFromContext(ctx); ok {
			actor = user.ID
		}
	}
	return db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at,workspace_id,seq,actor_id) VALUES(?,?,?,?,?,?,?,?,?)`, row.ID, row.Type, eventAggregateKey(row.AggregateID), row.Payload, row.PreviousValues, row.CreatedAt, workspace, seq, actor)
}

func nextWorkspaceSeq(ctx context.Context, db domainEventExecer, workspaceID string) (int64, error) {
	update := func() (bool, error) {
		result, err := db.ExecContext(ctx, `UPDATE workspace_event_seq SET head=head+1 WHERE workspace_id=?`, workspaceID)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		return n > 0, err
	}
	updated, err := update()
	if err == nil && !updated {
		if _, err = db.ExecContext(ctx, `INSERT INTO workspace_event_seq(workspace_id,head,pruned) VALUES(?,0,0) ON CONFLICT DO NOTHING`, workspaceID); err == nil {
			updated, err = update()
		}
	}
	if err != nil {
		return 0, err
	}
	if !updated {
		return 0, fmt.Errorf("allocate change cursor for workspace %s", workspaceID)
	}
	var seq int64
	if err := db.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=?`, workspaceID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

// SetChangeLogMinRows sets how many of each workspace's newest changes
// pruning keeps regardless of age; 0 disables the floor.
func (s *SQLiteStore) SetChangeLogMinRows(n int) {
	s.changeLogMinRows.Store(int64(max(n, 0)))
}

// ChangeRecord is one persisted change with its cursor.
type ChangeRecord struct {
	Seq     int64
	ActorID string
	Event   domain.DomainEvent
}

// ChangePage is the result of WorkspaceChanges. Head is the workspace's
// latest cursor. ResyncRequired means the caller's cursor is older than the
// retained log or ahead of the head, so Records is empty and the client must
// reload a snapshot and resume from Head.
type ChangePage struct {
	Records        []ChangeRecord
	Head           int64
	HasMore        bool
	ResyncRequired bool
}

// WorkspaceChanges returns up to limit changes after since, oldest first.
func (s *SQLiteStore) WorkspaceChanges(ctx context.Context, workspaceID string, since int64, limit int) (ChangePage, error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return ChangePage{}, err
	}
	defer tx.Rollback()
	var page ChangePage
	var pruned int64
	if err := tx.QueryRowContext(ctx, `SELECT head,pruned FROM workspace_event_seq WHERE workspace_id=?`, workspaceID).Scan(&page.Head, &pruned); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ChangePage{}, err
	}
	if since < 0 || since > page.Head || since < pruned {
		page.ResyncRequired = true
		return page, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,id,event_type,aggregate_id,payload,created_at,actor_id FROM domain_events WHERE workspace_id=? AND seq>? ORDER BY seq ASC LIMIT ?`, workspaceID, since, limit+1)
	if err != nil {
		return ChangePage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(page.Records) == limit {
			page.HasMore = true
			break
		}
		var record ChangeRecord
		var created string
		var actor sql.NullString
		if err := rows.Scan(&record.Seq, &record.Event.ID, &record.Event.Type, &record.Event.AggregateID, &record.Event.Payload, &created, &actor); err != nil {
			return ChangePage{}, err
		}
		record.Event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		record.ActorID = actor.String
		page.Records = append(page.Records, record)
	}
	return page, rows.Err()
}

// WorkspaceChangeHead returns the latest cursor of the workspace with the
// given URL key ("" is the last used workspace); 0 when it has no changes.
func (s *SQLiteStore) WorkspaceChangeHead(ctx context.Context, workspaceKey string) (int64, error) {
	if workspaceKey == "" {
		s.mu.RLock()
		workspaceKey = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var head sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT e.head FROM workspace_states w JOIN workspace_event_seq e ON e.workspace_id=w.workspace_id WHERE w.workspace_key=?`, workspaceKey).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return head.Int64, err
}

// DomainEventSeq returns the cursor of a persisted event, or 0 when the event
// is not in the change log.
func (s *SQLiteStore) DomainEventSeq(ctx context.Context, eventID string) int64 {
	var seq sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT seq FROM domain_events WHERE id=?`, eventID).Scan(&seq); err != nil {
		return 0
	}
	return seq.Int64
}

func (s *SQLiteStore) addChangeLog(ctx context.Context) error {
	for _, statement := range []string{
		`ALTER TABLE domain_events ADD COLUMN seq BIGINT`,
		`ALTER TABLE domain_events ADD COLUMN actor_id VARCHAR(191)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, "duplicate column") && !strings.Contains(message, "already exists") {
				return err
			}
		}
	}
	prefix := "CREATE UNIQUE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE UNIQUE INDEX "
	}
	if err := s.applyMigrationStatements(ctx, []string{
		`CREATE TABLE IF NOT EXISTS workspace_event_seq (workspace_id VARCHAR(191) PRIMARY KEY, head BIGINT NOT NULL, pruned BIGINT NOT NULL DEFAULT 0)`,
		prefix + "domain_events_seq_idx ON domain_events(workspace_id,seq)",
	}); err != nil {
		return err
	}
	return s.backfillChangeLog(ctx)
}

// backfillChangeLog numbers events written before the change log existed, per
// workspace in (created_at,id) order. It resumes after a partial run: rows
// already numbered are kept and the rest continue from the highest seq.
func (s *SQLiteStore) backfillChangeLog(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM domain_events WHERE seq IS NULL AND workspace_id IS NOT NULL AND workspace_id<>''`)
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
	for _, workspaceID := range workspaces {
		var last sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM domain_events WHERE workspace_id=?`, workspaceID).Scan(&last); err != nil {
			return err
		}
		idRows, err := s.db.QueryContext(ctx, `SELECT id FROM domain_events WHERE workspace_id=? AND seq IS NULL ORDER BY created_at,id`, workspaceID)
		if err != nil {
			return err
		}
		ids := []string{}
		for idRows.Next() {
			var id string
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = idRows.Err()
		idRows.Close()
		if err != nil {
			return err
		}
		seq := last.Int64
		for start := 0; start < len(ids); start += 500 {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			for _, id := range ids[start:min(start+500, len(ids))] {
				seq++
				if _, err := tx.ExecContext(ctx, `UPDATE domain_events SET seq=? WHERE id=?`, seq, id); err != nil {
					tx.Rollback()
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_event_seq(workspace_id,head,pruned) VALUES(?,?,0) ON CONFLICT(workspace_id) DO UPDATE SET head=excluded.head`, workspaceID, seq); err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}
