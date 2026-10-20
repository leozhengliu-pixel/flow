package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Coding sessions store (track B, docs/coding-sessions-design.md §5-§6):
// coding_sessions with its lease and version CAS, the runner inbox, the
// session's coding_sandboxes rows (desired state; the sandbox reconciler of
// track A writes the observed state), sandbox token authentication, commits
// and runner event idempotency keys.
//
// coding_sandboxes.data holds the JSON of the whole sandbox.Record; the
// indexed columns override it when read. Sandbox tokens are stored as the
// hex SHA-256 of the token (sandboxTokenHash), as MintSandboxToken writes them.

// codingLeaseSentinel parks next_reconcile_at while a session is leased, so
// a write during the lease (which sets it to now) survives the release.
const codingLeaseSentinel = int64(1) << 62

const codingSessionColumns = `data,version,cost_micros,next_reconcile_at,lease_until`

// The store cannot import internal/coding (coding → config → store), so it
// speaks store-local row types; internal/codingstore adapts them to the
// coding.CoreStore interface.

// Coding store errors. They are the domain sentinels (domain.ErrCoding*),
// which internal/coding aliases too (coding.ErrNotFound, ...), so callers
// match store errors with errors.Is and no adapter translates them.
var (
	ErrCodingNotFound        = domain.ErrCodingNotFound
	ErrCodingVersionConflict = domain.ErrCodingVersionConflict
	ErrCodingLeaseLost       = domain.ErrCodingLeaseLost
	ErrCodingTokenInvalid    = domain.ErrCodingTokenInvalid
	ErrCodingInput           = domain.ErrCodingInvalidInput
)

// CodingSessionFilter narrows ListCodingSessions.
type CodingSessionFilter struct {
	IssueID   string
	CreatorID string
	States    []string
	Source    string
	Limit     int
}

// CodingInboxItem is a raw inbox row; Payload is the item JSON without seq and kind.
type CodingInboxItem struct {
	Seq     int64
	Kind    string
	Payload []byte
}

// CodingSandboxGrant is an authenticated sandbox token.
type CodingSandboxGrant struct {
	WorkspaceKey  string
	SandboxID     string
	SessionID     string
	SnapshotID    string
	Kind          string
	ExpiresAt     time.Time
	EnvironmentID string
}

func codingTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func codingOptionalMillis(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return millis(*value)
}

func scanCodingSession(scan func(...any) error) (domain.CodingSession, error) {
	var raw []byte
	var version, cost, next, leaseUntil int64
	if err := scan(&raw, &version, &cost, &next, &leaseUntil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.CodingSession{}, ErrCodingNotFound
		}
		return domain.CodingSession{}, err
	}
	var session domain.CodingSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return session, err
	}
	session.Version, session.CostMicros = version, cost
	if next != codingLeaseSentinel {
		session.NextReconcileAt = codingTime(next)
	}
	return session, nil
}

func codingSessionWorkspace(session domain.CodingSession, workspaceKey string) domain.CodingSession {
	session.WorkspaceKey = workspaceKey
	return session
}

// CreateCodingSession inserts the session and its AgentTask anchor in one
// transaction. An existing session with the same idempotency key, or already
// anchored to the same task, is returned with created=false.
func (s *SQLiteStore) CreateCodingSession(ctx context.Context, session domain.CodingSession, anchor domain.AgentTask, idempotencyKey string) (domain.CodingSession, bool, error) {
	if session.ID == "" || session.WorkspaceKey == "" || session.AgentSessionID == "" || anchor.ID != session.AgentSessionID {
		return domain.CodingSession{}, false, ErrCodingInput
	}
	existing := func() (domain.CodingSession, bool) {
		if idempotencyKey != "" {
			if found, err := scanCodingSession(s.db.QueryRowContext(ctx, `SELECT `+codingSessionColumns+` FROM coding_sessions WHERE workspace_key=? AND idempotency_key=?`, session.WorkspaceKey, idempotencyKey).Scan); err == nil {
				return codingSessionWorkspace(found, session.WorkspaceKey), true
			}
		}
		if found, err := scanCodingSession(s.db.QueryRowContext(ctx, `SELECT `+codingSessionColumns+` FROM coding_sessions WHERE workspace_key=? AND agent_session_id=?`, session.WorkspaceKey, session.AgentSessionID).Scan); err == nil {
			return codingSessionWorkspace(found, session.WorkspaceKey), true
		}
		return domain.CodingSession{}, false
	}
	if found, ok := existing(); ok {
		return found, false, nil
	}
	session.Version = 1
	raw, err := json.Marshal(session)
	if err != nil {
		return session, false, err
	}
	anchorRaw, err := json.Marshal(anchor)
	if err != nil {
		return session, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return session, false, err
	}
	defer tx.Rollback()
	resource := anchor.IssueID
	if resource == "" {
		resource = anchor.ResourceID
	}
	created := anchor.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	// The anchor is never delivered to the application worker: the coding
	// session drives it (next_attempt parks it like an agent-initiated session).
	if _, err := tx.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at,next_attempt,stale_at) VALUES(?,?,?,?,?,1,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		anchor.ID, anchor.WorkspaceKey, resource, anchor.AppUserID, anchor.Status, anchorRaw, created.Format(time.RFC3339Nano), agentSessionNeverDue, agentTaskStaleAt(anchor)); err != nil {
		return session, false, err
	}
	// A reused anchor (an existing delegation task, track L) was not
	// inserted above: link it to the session and park it so the application
	// worker never delivers it as well.
	var existingRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM application_agent_tasks WHERE id=? AND workspace_key=?`, anchor.ID, anchor.WorkspaceKey).Scan(&existingRaw); err != nil {
		return session, false, err
	}
	var linked domain.AgentTask
	if err := json.Unmarshal(existingRaw, &linked); err != nil {
		return session, false, err
	}
	if linked.CodingSessionID != session.ID {
		linked.CodingSessionID = session.ID
		if existingRaw, err = json.Marshal(linked); err != nil {
			return session, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE application_agent_tasks SET data=?,next_attempt=? WHERE id=? AND workspace_key=?`, existingRaw, agentSessionNeverDue, anchor.ID, anchor.WorkspaceKey); err != nil {
			return session, false, err
		}
	}
	key := sql.NullString{String: idempotencyKey, Valid: idempotencyKey != ""}
	next := millis(session.NextReconcileAt)
	if session.NextReconcileAt.IsZero() {
		next = millis(time.Now())
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,issue_id,environment_id,creator_id,source,source_id,idempotency_key,state,phase,phase_started_at,phase_deadline,attempts,stop_requested,error_code,next_reconcile_at,deadline_at,cost_micros,version,data,created_at,updated_at,ended_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,1,?,?,?,?)`,
		session.ID, session.WorkspaceKey, session.AgentSessionID, session.IssueID, session.EnvironmentID, session.CreatorID, session.Source, session.SourceID, key, string(session.State), string(session.Phase), millis(session.PhaseStartedAt), codingOptionalMillis(session.PhaseDeadline), session.Attempts, boolInt(session.StopRequested), string(session.ErrorCode), next, raw, millis(session.CreatedAt), millis(session.CreatedAt), codingOptionalMillis(session.EndedAt))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		if found, ok := existing(); ok {
			return found, false, nil // a concurrent create won the unique index
		}
		return session, false, err
	}
	if anchorTask, err := s.AgentTask(ctx, session.WorkspaceKey, anchor.ID); err == nil {
		s.publishAgentTaskUpdate(session.WorkspaceKey, anchorTask)
	}
	s.publishCodingSessionUpdate(session.WorkspaceKey, "coding_session.created", session)
	return session, true, nil
}

// CodingSession reads one session.
func (s *SQLiteStore) CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	session, err := scanCodingSession(s.db.QueryRowContext(ctx, `SELECT `+codingSessionColumns+` FROM coding_sessions WHERE workspace_key=? AND id=?`, workspaceKey, id).Scan)
	return codingSessionWorkspace(session, workspaceKey), err
}

// CodingSessionByAgentSession reads the session anchored to an AgentTask.
func (s *SQLiteStore) CodingSessionByAgentSession(ctx context.Context, workspaceKey, agentSessionID string) (domain.CodingSession, error) {
	session, err := scanCodingSession(s.db.QueryRowContext(ctx, `SELECT `+codingSessionColumns+` FROM coding_sessions WHERE workspace_key=? AND agent_session_id=?`, workspaceKey, agentSessionID).Scan)
	return codingSessionWorkspace(session, workspaceKey), err
}

// ListCodingSessions lists a workspace's sessions, newest first.
func (s *SQLiteStore) ListCodingSessions(ctx context.Context, workspaceKey string, filter CodingSessionFilter) ([]domain.CodingSession, error) {
	where, args := "workspace_key=?", []any{workspaceKey}
	if filter.IssueID != "" {
		where += " AND issue_id=?"
		args = append(args, filter.IssueID)
	}
	if filter.CreatorID != "" {
		where += " AND creator_id=?"
		args = append(args, filter.CreatorID)
	}
	if filter.Source != "" {
		where += " AND source=?"
		args = append(args, filter.Source)
	}
	if len(filter.States) > 0 {
		where += " AND state IN (" + strings.TrimSuffix(strings.Repeat("?,", len(filter.States)), ",") + ")"
		for _, state := range filter.States {
			args = append(args, state)
		}
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+codingSessionColumns+` FROM coding_sessions WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodingSession{}
	for rows.Next() {
		session, err := scanCodingSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, codingSessionWorkspace(session, workspaceKey))
	}
	return result, rows.Err()
}

// UpdateCodingSession applies mutate under optimistic concurrency (the
// version CAS), retrying lost races. The phase, its start and its deadline
// are written by the same statement, so they can never disagree.
// next_reconcile_at is written only when mutate changed NextReconcileAt.
//
// A mutation that changes nothing (an idempotent action repeated, a
// reconcile pass that decided the same state) writes nothing and keeps the
// version, and a coding_session.updated event is only published when a
// field a session view shows changed (bookkeeping such as retry counters
// or the phase deadline alone does not), so realtime volume follows real
// progress rather than the reconcile interval.
func (s *SQLiteStore) UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error) {
	for attempt := 0; attempt < 8; attempt++ {
		current, err := s.CodingSession(ctx, workspaceKey, id)
		if err != nil {
			return current, err
		}
		version, cost, next := current.Version, current.CostMicros, current.NextReconcileAt
		before := current
		beforeRaw, err := json.Marshal(before)
		if err != nil {
			return current, err
		}
		if err := mutate(&current); err != nil {
			return current, err
		}
		current.ID, current.WorkspaceKey, current.CostMicros = id, workspaceKey, cost
		if unchanged, err := sameCodingSessionDocument(beforeRaw, current, version, before.UpdatedAt); err != nil {
			return current, err
		} else if unchanged && (current.NextReconcileAt.Equal(next) || current.NextReconcileAt.IsZero()) {
			return before, nil
		}
		current.Version = version + 1
		current.UpdatedAt = time.Now().UTC()
		raw, err := json.Marshal(current)
		if err != nil {
			return current, err
		}
		query := `UPDATE coding_sessions SET issue_id=?,environment_id=?,state=?,phase=?,phase_started_at=?,phase_deadline=?,attempts=?,stop_requested=?,error_code=?,version=?,data=?,updated_at=?,ended_at=?`
		args := []any{current.IssueID, current.EnvironmentID, string(current.State), string(current.Phase), millis(current.PhaseStartedAt), codingOptionalMillis(current.PhaseDeadline), current.Attempts, boolInt(current.StopRequested), string(current.ErrorCode), current.Version, raw, millis(current.UpdatedAt), codingOptionalMillis(current.EndedAt)}
		if !current.NextReconcileAt.Equal(next) && !current.NextReconcileAt.IsZero() {
			query += `,next_reconcile_at=?`
			args = append(args, millis(current.NextReconcileAt))
		}
		args = append(args, workspaceKey, id, version)
		result, err := s.db.ExecContext(ctx, query+` WHERE workspace_key=? AND id=? AND version=?`, args...)
		if err != nil {
			return current, err
		}
		if count, err := result.RowsAffected(); err != nil {
			return current, err
		} else if count == 1 {
			if codingSessionVisiblyChanged(before, current) {
				s.publishCodingSessionUpdate(workspaceKey, "coding_session.updated", current)
			}
			return current, nil
		}
	}
	return domain.CodingSession{}, ErrCodingVersionConflict
}

// sameCodingSessionDocument reports whether mutated serializes to the stored
// document once the write-time fields (version, updated_at) are put back.
func sameCodingSessionDocument(beforeRaw []byte, mutated domain.CodingSession, version int64, updatedAt time.Time) (bool, error) {
	mutated.Version, mutated.UpdatedAt = version, updatedAt
	afterRaw, err := json.Marshal(mutated)
	if err != nil {
		return false, err
	}
	return bytes.Equal(beforeRaw, afterRaw), nil
}

// codingSessionVisiblyChanged reports whether an update changed anything a
// session view shows. Reconciler bookkeeping that only moves on its own
// (retry counters, the phase deadline, the harness resume id, the version
// and timestamp of the write) does not count.
func codingSessionVisiblyChanged(before, after domain.CodingSession) bool {
	strip := func(session domain.CodingSession) domain.CodingSession {
		session.Version, session.UpdatedAt = 0, time.Time{}
		session.Attempts, session.SandboxAttempts = 0, 0
		session.PhaseDeadline, session.NextReconcileAt = nil, time.Time{}
		session.HarnessResumeID = ""
		return session
	}
	left, leftErr := json.Marshal(strip(before))
	right, rightErr := json.Marshal(strip(after))
	return leftErr != nil || rightErr != nil || !bytes.Equal(left, right)
}

// publishCodingSessionUpdate pushes a coding_session.* realtime event
// (aggregateId = session id) so open session views refresh at once; the
// payload carries only ids and the state, never prompts or errors.
func (s *SQLiteStore) publishCodingSessionUpdate(workspace, eventType string, session domain.CodingSession) {
	sink := s.realtime()
	if sink == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"sessionId": session.ID, "agentSessionId": session.AgentSessionID, "issueId": session.IssueID, "state": session.State, "phase": session.Phase, "version": session.Version})
	sink(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("coding_%s_%d_%d", session.ID, session.Version, time.Now().UnixNano()), Type: eventType, AggregateID: session.ID, Payload: payload, CreatedAt: time.Now().UTC()})
}

// LeaseCodingSessions leases non-terminal sessions that are due (or whose
// previous lease expired) to owner: FOR UPDATE SKIP LOCKED on PostgreSQL and
// MySQL, then a CAS on lease_until (the only guard on SQLite).
func (s *SQLiteStore) LeaseCodingSessions(ctx context.Context, owner string, lease time.Duration, limit int) ([]domain.CodingSession, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 191 {
		return nil, ErrCodingInput
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	now := millis(time.Now())
	until := now + lease.Milliseconds()
	query := `SELECT id,workspace_key FROM coding_sessions WHERE phase<>'done' AND lease_until<? AND (next_reconcile_at<=? OR lease_until>0) ORDER BY next_reconcile_at,id LIMIT ?`
	locking := s.dialect != "sqlite"
	if locking {
		query += " FOR UPDATE SKIP LOCKED"
	}
	type candidate struct{ id, workspace string }
	claim := func(exec func(string, ...any) (sql.Result, error), list []candidate) ([]candidate, error) {
		claimed := []candidate{}
		for _, item := range list {
			result, err := exec(`UPDATE coding_sessions SET lease_owner=?,lease_until=?,next_reconcile_at=? WHERE id=? AND phase<>'done' AND lease_until<?`, owner, until, codingLeaseSentinel, item.id, now)
			if err != nil {
				return nil, err
			}
			if count, _ := result.RowsAffected(); count == 1 {
				claimed = append(claimed, item)
			}
		}
		return claimed, nil
	}
	read := func(rows *sql.Rows) ([]candidate, error) {
		defer rows.Close()
		list := []candidate{}
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.workspace); err != nil {
				return nil, err
			}
			list = append(list, item)
		}
		return list, rows.Err()
	}
	var claimed []candidate
	if locking {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		rows, err := tx.QueryContext(ctx, query, now, now, limit)
		if err != nil {
			return nil, err
		}
		list, err := read(rows)
		if err != nil {
			return nil, err
		}
		if claimed, err = claim(func(q string, args ...any) (sql.Result, error) { return tx.ExecContext(ctx, q, args...) }, list); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	} else {
		rows, err := s.db.QueryContext(ctx, query, now, now, limit)
		if err != nil {
			return nil, err
		}
		list, err := read(rows)
		if err != nil {
			return nil, err
		}
		if claimed, err = claim(func(q string, args ...any) (sql.Result, error) { return s.db.ExecContext(ctx, q, args...) }, list); err != nil {
			return nil, err
		}
	}
	result := make([]domain.CodingSession, 0, len(claimed))
	for _, item := range claimed {
		session, err := s.CodingSession(ctx, item.workspace, item.id)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

// ReleaseCodingSession ends a lease. The next reconcile time is the earlier
// of nextReconcileAt and one written during the lease (a wake-up).
func (s *SQLiteStore) ReleaseCodingSession(ctx context.Context, owner, id string, nextReconcileAt time.Time) error {
	next := millis(nextReconcileAt)
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sessions SET lease_owner='',lease_until=0,next_reconcile_at=CASE WHEN next_reconcile_at<? THEN next_reconcile_at ELSE ? END WHERE id=? AND lease_owner=?`, next, next, id, owner)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrCodingLeaseLost
	}
	return nil
}

// codingActivePhases hold sandbox capacity (queued and idle sessions do not).
const codingActivePhases = `('provisioning','starting','setup','running','awaitingUser','publishing','stopping')`

// CountActiveCodingSessions counts sessions holding capacity in a workspace and in total.
func (s *SQLiteStore) CountActiveCodingSessions(ctx context.Context, workspaceKey string) (int, int, error) {
	var workspace, total int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN workspace_key=? THEN 1 ELSE 0 END),0),COUNT(*) FROM coding_sessions WHERE phase IN `+codingActivePhases, workspaceKey).Scan(&workspace, &total)
	return workspace, total, err
}

// EnqueueCodingInbox appends an item with the session's next sequence number.
func (s *SQLiteStore) EnqueueCodingInbox(ctx context.Context, workspaceKey, sessionID, kind string, payload []byte) (int64, error) {
	if kind == "" || sessionID == "" {
		return 0, ErrCodingInput
	}
	turnID := codingInboxTurnID(payload)
	for attempt := 0; attempt < 8; attempt++ {
		// A turn is enqueued once per Turn.ID (retried webhooks, trigger
		// redeliveries): the existing item's seq is returned. Two concurrent
		// inserts collide on the seq primary key, and the retry sees the winner.
		if turnID != "" {
			if seq, found, err := s.codingInboxTurnSeq(ctx, sessionID, kind, turnID); err != nil || found {
				return seq, err
			}
		}
		var seq int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM coding_session_inbox WHERE session_id=?`, sessionID).Scan(&seq); err != nil {
			return 0, err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO coding_session_inbox(session_id,seq,workspace_key,kind,payload,created_at,delivered_at) VALUES(?,?,?,?,?,?,0)`, sessionID, seq, workspaceKey, kind, payload, millis(time.Now()))
		if err == nil {
			return seq, nil
		}
		var existing int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_session_inbox WHERE session_id=? AND seq=?`, sessionID, seq).Scan(&existing) != nil || existing == 0 {
			return 0, err // not a sequence race
		}
	}
	return 0, ErrCodingVersionConflict
}

// codingInboxTurnID reads turn.id from an inbox payload ("" when none).
func codingInboxTurnID(payload []byte) string {
	var item struct {
		Turn *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(payload, &item) != nil || item.Turn == nil {
		return ""
	}
	return item.Turn.ID
}

// codingInboxTurnSeq finds the item of a session that carries turnID.
func (s *SQLiteStore) codingInboxTurnSeq(ctx context.Context, sessionID, kind, turnID string) (int64, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,payload FROM coding_session_inbox WHERE session_id=? AND kind=? ORDER BY seq`, sessionID, kind)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var payload []byte
		if err := rows.Scan(&seq, &payload); err != nil {
			return 0, false, err
		}
		if codingInboxTurnID(payload) == turnID {
			return seq, true, nil
		}
	}
	return 0, false, rows.Err()
}

func (s *SQLiteStore) readCodingInbox(ctx context.Context, query string, args ...any) ([]CodingInboxItem, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CodingInboxItem{}
	for rows.Next() {
		var item CodingInboxItem
		if err := rows.Scan(&item.Seq, &item.Kind, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CodingInbox lists items after a sequence number.
func (s *SQLiteStore) CodingInbox(ctx context.Context, sessionID string, after int64) ([]CodingInboxItem, error) {
	return s.readCodingInbox(ctx, `SELECT seq,kind,payload FROM coding_session_inbox WHERE session_id=? AND seq>? ORDER BY seq LIMIT 200`, sessionID, after)
}

// UndeliveredCodingInbox lists undelivered items, oldest first.
func (s *SQLiteStore) UndeliveredCodingInbox(ctx context.Context, sessionID string) ([]CodingInboxItem, error) {
	return s.readCodingInbox(ctx, `SELECT seq,kind,payload FROM coding_session_inbox WHERE session_id=? AND delivered_at=0 ORDER BY seq LIMIT 200`, sessionID)
}

// PendingCodingTurns counts undelivered turns.
func (s *SQLiteStore) PendingCodingTurns(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_session_inbox WHERE session_id=? AND kind='turn' AND delivered_at=0`, sessionID).Scan(&count)
	return count, err
}

// MarkCodingInboxDelivered stamps an item delivered (the first stamp wins).
func (s *SQLiteStore) MarkCodingInboxDelivered(ctx context.Context, sessionID string, seq int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE coding_session_inbox SET delivered_at=? WHERE session_id=? AND seq=? AND delivered_at=0`, millis(at), sessionID, seq)
	return err
}

// The session layer's sandbox methods share track A's row type and data
// layout (CodingSandbox, insertCodingSandbox and scanCodingSandbox in
// coding_sandboxes.go), so there is one coding_sandboxes representation.

// CreateCodingSandbox inserts a sandbox row (desired state); creating the
// same id again is a no-op.
func (s *SQLiteStore) CreateCodingSandbox(ctx context.Context, record CodingSandbox) error {
	if record.ID == "" || record.Kind == "" {
		return ErrCodingInput
	}
	return s.insertCodingSandbox(ctx, s.db, record, "")
}

// CodingSandboxes lists a session's sandboxes, oldest first.
func (s *SQLiteStore) CodingSandboxes(ctx context.Context, workspaceKey, sessionID string) ([]CodingSandbox, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+codingSandboxColumns+codingSandboxJoins+` WHERE sb.workspace_key=? AND sb.session_id=? ORDER BY sb.created_at,sb.id`, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CodingSandbox{}
	for rows.Next() {
		record, err := scanCodingSandbox(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// CodingSandbox reads one sandbox row (ErrCodingNotFound when absent).
func (s *SQLiteStore) CodingSandbox(ctx context.Context, id string) (CodingSandbox, error) {
	records, err := s.codingSandboxesByID(ctx, []string{id})
	if err != nil {
		return CodingSandbox{}, err
	}
	if len(records) != 1 {
		return CodingSandbox{}, ErrCodingNotFound
	}
	return records[0], nil
}

// SetCodingSandboxDesired flips the desired state (the sandbox reconciler converges to it).
func (s *SQLiteStore) SetCodingSandboxDesired(ctx context.Context, id string, desired string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET desired=?,lease_until=CASE WHEN lease_owner='' THEN 0 ELSE lease_until END WHERE id=?`, desired, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_sandboxes WHERE id=?`, id).Scan(&exists); err != nil || exists == 0 {
			return ErrCodingNotFound
		}
	}
	return nil
}

// Warm sandboxes are bound by BindWarmCodingSandbox (coding_sandboxes.go).

// GitRelayPlaceholderPrefix marks a sandbox's git relay placeholder: the
// opaque password the sandbox's git credential helper gets for pushes
// through Flow's git relay (coding.GitRelay). A placeholder row lives in
// coding_sandbox_tokens next to its sandbox token (same sandbox, session,
// expiry and revocation) but only AuthenticateGitRelayPlaceholder accepts
// it: it is never a runtime, LLM proxy or egress proxy credential.
const GitRelayPlaceholderPrefix = "fgr_"

// RegisterGitRelayPlaceholder stores the placeholder derived from a live
// sandbox token (idempotent), bound to that token's sandbox and session and
// expiring with it.
func (s *SQLiteStore) RegisterGitRelayPlaceholder(ctx context.Context, token, placeholder string) error {
	if !strings.HasPrefix(token, sandboxTokenPrefix) || !strings.HasPrefix(placeholder, GitRelayPlaceholderPrefix) || len(placeholder) > 512 {
		return ErrCodingTokenInvalid
	}
	var sandboxID, sessionID, workspace string
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT sandbox_id,session_id,workspace_key,expires_at FROM coding_sandbox_tokens WHERE token_hash=? AND revoked_at=0 AND expires_at>?`, tokenHash(token), millis(time.Now())).
		Scan(&sandboxID, &sessionID, &workspace, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCodingTokenInvalid
	}
	if err != nil {
		return err
	}
	// setup_secrets_used=1: a placeholder can never fetch setup secrets.
	_, err = s.db.ExecContext(ctx, `INSERT INTO coding_sandbox_tokens(token_hash,sandbox_id,session_id,workspace_key,setup_secrets_used,expires_at,created_at) VALUES(?,?,?,?,1,?,?) ON CONFLICT(token_hash) DO NOTHING`,
		tokenHash(placeholder), sandboxID, sessionID, workspace, expires, millis(time.Now()))
	return err
}

// AuthenticateGitRelayPlaceholder resolves a live git relay placeholder
// (not expired, not revoked) to its sandbox's grant.
func (s *SQLiteStore) AuthenticateGitRelayPlaceholder(ctx context.Context, placeholder string) (CodingSandboxGrant, error) {
	if !strings.HasPrefix(placeholder, GitRelayPlaceholderPrefix) || len(placeholder) > 512 {
		return CodingSandboxGrant{}, ErrCodingTokenInvalid
	}
	return s.authenticateSandboxCredential(ctx, placeholder)
}

// AuthenticateSandboxToken resolves a live token (not expired, not revoked).
// Git relay placeholders are refused.
func (s *SQLiteStore) AuthenticateSandboxToken(ctx context.Context, token string) (CodingSandboxGrant, error) {
	if strings.HasPrefix(token, GitRelayPlaceholderPrefix) {
		return CodingSandboxGrant{}, ErrCodingTokenInvalid
	}
	return s.authenticateSandboxCredential(ctx, token)
}

func (s *SQLiteStore) authenticateSandboxCredential(ctx context.Context, token string) (CodingSandboxGrant, error) {
	var grant CodingSandboxGrant
	var tokenSession, tokenWorkspace string
	var sandboxSession, sandboxWorkspace, snapshot, kind, sessionEnv, snapshotEnv sql.NullString
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT t.sandbox_id,t.session_id,t.workspace_key,t.expires_at,b.session_id,b.workspace_key,b.snapshot_id,b.kind,cs.environment_id,sn.environment_id FROM coding_sandbox_tokens t LEFT JOIN coding_sandboxes b ON b.id=t.sandbox_id LEFT JOIN coding_sessions cs ON cs.id=b.session_id LEFT JOIN coding_environment_snapshots sn ON sn.id=b.snapshot_id WHERE t.token_hash=? AND t.revoked_at=0 AND t.expires_at>?`, tokenHash(token), millis(time.Now())).
		Scan(&grant.SandboxID, &tokenSession, &tokenWorkspace, &expires, &sandboxSession, &sandboxWorkspace, &snapshot, &kind, &sessionEnv, &snapshotEnv)
	if errors.Is(err, sql.ErrNoRows) {
		return grant, ErrCodingTokenInvalid
	}
	if err != nil {
		return grant, err
	}
	if !kind.Valid {
		return grant, ErrCodingTokenInvalid // the sandbox row is gone
	}
	grant.SessionID, grant.WorkspaceKey = tokenSession, tokenWorkspace
	if sandboxSession.String != "" {
		grant.SessionID, grant.WorkspaceKey = sandboxSession.String, sandboxWorkspace.String
	}
	grant.SnapshotID, grant.Kind, grant.ExpiresAt = snapshot.String, kind.String, codingTime(expires)
	grant.EnvironmentID = sessionEnv.String
	if grant.EnvironmentID == "" {
		grant.EnvironmentID = snapshotEnv.String
	}
	if grant.EnvironmentID == "" && grant.SessionID == "" {
		// Debug sandboxes without a snapshot: the launcher stores the
		// environment in the sandbox env (sandbox.EnvEnvironmentID).
		if record, err := s.CodingSandbox(ctx, grant.SandboxID); err == nil {
			grant.EnvironmentID = record.Env["FLOW_ENVIRONMENT_ID"]
		}
	}
	return grant, nil
}

// RevokeSandboxTokens revokes every token of a sandbox.
func (s *SQLiteStore) RevokeSandboxTokens(ctx context.Context, sandboxID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE coding_sandbox_tokens SET revoked_at=? WHERE sandbox_id=? AND revoked_at=0`, millis(time.Now()), sandboxID)
	return err
}

// MarkSetupSecretsUsed flips setup_secrets_used once per token.
func (s *SQLiteStore) MarkSetupSecretsUsed(ctx context.Context, token string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandbox_tokens SET setup_secrets_used=1 WHERE token_hash=? AND setup_secrets_used=0 AND revoked_at=0`, tokenHash(token))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// TouchCodingSandboxHeartbeat records a runner heartbeat.
func (s *SQLiteStore) TouchCodingSandboxHeartbeat(ctx context.Context, sandboxID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET heartbeat_at=? WHERE id=? AND heartbeat_at<?`, millis(at), sandboxID, millis(at))
	return err
}

// AddCodingCommits records pushed commits (idempotent by SHA).
func (s *SQLiteStore) AddCodingCommits(ctx context.Context, sessionID string, commits []domain.CodingCommit) error {
	if len(commits) == 0 {
		return nil
	}
	var workspace string
	if err := s.db.QueryRowContext(ctx, `SELECT workspace_key FROM coding_sessions WHERE id=?`, sessionID).Scan(&workspace); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCodingNotFound
		}
		return err
	}
	for _, commit := range commits {
		raw, err := json.Marshal(commit)
		if err != nil {
			return err
		}
		created := commit.CreatedAt
		if created.IsZero() {
			created = time.Now()
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO coding_session_commits(session_id,sha,workspace_key,turn_activity_id,data,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,sha) DO NOTHING`, sessionID, commit.SHA, workspace, commit.TurnActivityID, raw, millis(created)); err != nil {
			return err
		}
	}
	return nil
}

// CodingCommits lists a session's commits in push order.
func (s *SQLiteStore) CodingCommits(ctx context.Context, sessionID string) ([]domain.CodingCommit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM coding_session_commits WHERE session_id=? ORDER BY created_at,sha LIMIT 1000`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodingCommit{}
	for rows.Next() {
		var raw []byte
		var commit domain.CodingCommit
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &commit); err != nil {
			return nil, err
		}
		result = append(result, commit)
	}
	return result, rows.Err()
}

// ClaimRunnerEvent reserves an event id; a claim that never completed and is
// older than staleBefore can be taken over (the earlier attempt crashed).
func (s *SQLiteStore) ClaimRunnerEvent(ctx context.Context, workspaceKey, sessionID, eventID string, now, staleBefore time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO coding_runner_events(session_id,event_id,workspace_key,activity_id,claimed_at,completed_at) VALUES(?,?,?,'',?,0) ON CONFLICT(session_id,event_id) DO NOTHING`, sessionID, eventID, workspaceKey, millis(now))
	if err != nil {
		return false, err
	}
	if count, _ := result.RowsAffected(); count == 1 {
		return true, nil
	}
	result, err = s.db.ExecContext(ctx, `UPDATE coding_runner_events SET claimed_at=? WHERE session_id=? AND event_id=? AND completed_at=0 AND claimed_at<?`, millis(now), sessionID, eventID, millis(staleBefore))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// CompleteRunnerEvent marks an event ingested.
func (s *SQLiteStore) CompleteRunnerEvent(ctx context.Context, sessionID, eventID, activityID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE coding_runner_events SET activity_id=?,completed_at=? WHERE session_id=? AND event_id=?`, activityID, millis(time.Now()), sessionID, eventID)
	return err
}

// ReleaseRunnerEvent drops an unfinished claim so a retry can apply the event.
func (s *SQLiteStore) ReleaseRunnerEvent(ctx context.Context, sessionID, eventID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coding_runner_events WHERE session_id=? AND event_id=? AND completed_at=0`, sessionID, eventID)
	return err
}

// BuiltinAgentUserID is the built-in Flow application's user in a workspace.
func (s *SQLiteStore) BuiltinAgentUserID(ctx context.Context, workspaceKey string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM application_installations WHERE workspace_key=? AND client_id=?`, workspaceKey, BuiltinApplicationClientID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return userID, err
}

// IsCodingAnchor reports whether an AgentTask anchors a coding session (the
// application worker never delivers those: the coding session drives them).
func (s *SQLiteStore) IsCodingAnchor(ctx context.Context, workspaceKey, agentSessionID string) bool {
	var count int
	return s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_sessions WHERE workspace_key=? AND agent_session_id=?`, workspaceKey, agentSessionID).Scan(&count) == nil && count > 0
}

// codingAnchorMidTurn reports whether task anchors a coding session whose turn
// is underway (starting through publishing): its progress may reopen the
// anchor even after another writer completed it.
func (s *SQLiteStore) codingAnchorMidTurn(ctx context.Context, workspaceKey string, task domain.AgentTask) bool {
	if task.CodingSessionID == "" {
		return false
	}
	var phase string
	if err := s.db.QueryRowContext(ctx, `SELECT phase FROM coding_sessions WHERE workspace_key=? AND id=? AND agent_session_id=?`, workspaceKey, task.CodingSessionID, task.ID).Scan(&phase); err != nil {
		return false
	}
	switch domain.CodingSessionPhase(phase) {
	case domain.CodingPhaseStarting, domain.CodingPhaseSetup, domain.CodingPhaseRunning, domain.CodingPhasePublishing:
		return true
	}
	return false
}
