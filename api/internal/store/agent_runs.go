package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// Agent runs are the queue of the out-of-process agent runtime (agent-worker):
// one row per turn to execute (a created or prompted session, a resume after an
// approval, a retry). Workers never touch the database; the API claims rows for
// them through the internal runtime API.
//
// Claims are row leases, like webhook_deliveries and loop_run_records: a
// worker holds a run while lease_until is in the future and renews it with
// heartbeats; a crashed worker's lease expires and the reconciler queues the
// run again (when it wrote nothing) or marks it interrupted. On MySQL 8 and
// PostgreSQL the candidate rows are locked with FOR UPDATE SKIP LOCKED inside
// a short transaction so concurrent claimers skip each other's rows; SQLite has
// a single writer, so a plain select followed by the conditional UPDATE (the
// CAS on status) is enough. Every claim is that CAS, so a duplicate claim (a
// retried request, a second API process) is a no-op.
//
// The other agent runtime tables hang off a run:
//   - agent_run_tokens: the short-lived bearer tokens a run calls /mcp and the
//     internal API with (only their SHA-256 is stored);
//   - agent_run_changes: the run's change set (what its writes changed, so they
//     can be undone);
//   - agent_run_keys: idempotency keys (client activity ids, retried tool calls);
//   - agent_session_states: the serialized harness state of a session that is
//     suspended on a question or an approval.

// Agent run statuses.
const (
	AgentRunQueued      = "queued"
	AgentRunLeased      = "leased"
	AgentRunRunning     = "running"
	AgentRunAwaiting    = "awaiting"
	AgentRunDone        = "done"
	AgentRunFailed      = "failed"
	AgentRunCanceled    = "canceled"
	AgentRunInterrupted = "interrupted"
)

// Agent run kinds; their priority lane is AgentRunPriority.
const (
	AgentRunKindChat       = "chat"
	AgentRunKindThread     = "thread"
	AgentRunKindDelegation = "delegation"
	AgentRunKindLoop       = "loop"
	AgentRunKindBackground = "background"
)

var (
	ErrAgentRunNotFound     = errors.New("agent run not found")
	ErrAgentRunLeaseLost    = errors.New("agent run lease lost")
	ErrAgentRunTokenInvalid = errors.New("agent run token is invalid or expired")
	ErrAgentRunInput        = errors.New("invalid agent run")
)

// AgentRunPriority is the lane of a run kind: 0 chat, 1 threads and
// delegation, 2 loops, 3 background work. Lower runs first.
func AgentRunPriority(kind string) (int, bool) {
	switch kind {
	case AgentRunKindChat:
		return 0, true
	case AgentRunKindThread, AgentRunKindDelegation:
		return 1, true
	case AgentRunKindLoop:
		return 2, true
	case AgentRunKindBackground:
		return 3, true
	}
	return 0, false
}

// AgentRunHeld reports whether a status means a worker holds the run.
func AgentRunHeld(status string) bool {
	return status == AgentRunLeased || status == AgentRunRunning
}

// AgentRun is one row of the queue. Times are unix milliseconds.
type AgentRun struct {
	ID              string          `json:"id"`
	WorkspaceKey    string          `json:"workspaceKey"`
	SessionID       string          `json:"sessionId"`
	Kind            string          `json:"kind"`
	Priority        int             `json:"priority"`
	Status          string          `json:"status"`
	IdempotencyKey  string          `json:"idempotencyKey,omitempty"`
	LeaseOwner      string          `json:"leaseOwner,omitempty"`
	LeaseUntil      int64           `json:"leaseUntil,omitempty"`
	HeartbeatAt     int64           `json:"heartbeatAt,omitempty"`
	NextAttemptAt   int64           `json:"nextAttemptAt"`
	Attempts        int             `json:"attempts"`
	CancelRequested bool            `json:"cancelRequested"`
	Wrote           bool            `json:"wrote"`
	Principal       json.RawMessage `json:"-"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Usage           json.RawMessage `json:"usage,omitempty"`
	Error           string          `json:"error,omitempty"`
	CreatedAt       int64           `json:"createdAt"`
	UpdatedAt       int64           `json:"updatedAt"`
	FinishedAt      int64           `json:"finishedAt,omitempty"`
}

// AgentRunInput enqueues a run.
type AgentRunInput struct {
	// ID is generated when empty.
	ID           string
	WorkspaceKey string
	SessionID    string
	Kind         string
	// IdempotencyKey makes enqueueing the same turn twice (a retried request,
	// a redelivered event) return the first run. It is unique per workspace.
	IdempotencyKey string
	// Principal is who the run acts as (the server's run principal JSON); it is
	// never sent to workers.
	Principal json.RawMessage
	// Payload is what the worker reads (prompt, limits, model alias…).
	Payload json.RawMessage
	// NotBefore delays the run (a retry backoff); zero means now.
	NotBefore time.Time
}

// AgentRunClaim asks for runs to execute.
type AgentRunClaim struct {
	Owner string
	// Free is how many runs the worker can start in each priority lane.
	Free map[int]int
	// MaxPerWorkspace caps the runs one workspace holds at a time (leased or
	// running, across all workers); zero means no cap.
	MaxPerWorkspace int
	Lease           time.Duration
	Now             time.Time
}

// AgentRunResult ends a run.
type AgentRunResult struct {
	// Status is done, failed, canceled, awaiting or interrupted.
	Status string
	Error  string
	Usage  json.RawMessage
}

// AgentRunChange is one entry of a run's change set: what a write tool call
// changed (domain.AgentChange) and the call itself, so it can be undone.
type AgentRunChange struct {
	RunID     string             `json:"runId"`
	Seq       int64              `json:"seq"`
	Tool      string             `json:"tool"`
	Arguments json.RawMessage    `json:"arguments,omitempty"`
	Change    domain.AgentChange `json:"change"`
	CreatedAt int64              `json:"createdAt"`
	UpdatedAt int64              `json:"updatedAt"`
}

// AgentSessionState is the serialized harness state of a suspended session.
type AgentSessionState struct {
	WorkspaceKey string `json:"workspaceKey"`
	SessionID    string `json:"sessionId"`
	RunID        string `json:"runId"`
	ContentType  string `json:"contentType"`
	Data         []byte `json:"-"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// AgentSessionStateMaxBytes caps one stored session state.
const AgentSessionStateMaxBytes = 8 << 20

const agentRunColumns = `id,workspace_key,session_id,kind,priority,status,idempotency_key,lease_owner,lease_until,heartbeat_at,next_attempt_at,attempts,cancel_requested,wrote,principal,payload,usage_json,error_text,created_at,updated_at,finished_at`

func (s *SQLiteStore) ensureAgentRuns(ctx context.Context) error {
	blob, text := "BLOB", "TEXT"
	switch s.dialect {
	case "mysql":
		blob, text = "LONGBLOB", "MEDIUMTEXT"
	case "postgres":
		blob = "BYTEA"
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS agent_runs(id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,session_id VARCHAR(191) NOT NULL,kind VARCHAR(16) NOT NULL,priority INTEGER NOT NULL,status VARCHAR(16) NOT NULL,idempotency_key VARCHAR(191) NULL,lease_owner VARCHAR(191) NOT NULL DEFAULT '',lease_until BIGINT NOT NULL DEFAULT 0,heartbeat_at BIGINT NOT NULL DEFAULT 0,next_attempt_at BIGINT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,cancel_requested INTEGER NOT NULL DEFAULT 0,wrote INTEGER NOT NULL DEFAULT 0,principal ` + blob + ` NOT NULL,payload ` + blob + ` NOT NULL,usage_json ` + blob + ` NULL,error_text ` + text + ` NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,finished_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS agent_run_tokens(token_hash VARCHAR(191) PRIMARY KEY,run_id VARCHAR(191) NOT NULL,workspace_key VARCHAR(191) NOT NULL,expires_at BIGINT NOT NULL,revoked_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_run_changes(run_id VARCHAR(191) NOT NULL,seq BIGINT NOT NULL,workspace_key VARCHAR(191) NOT NULL,tool VARCHAR(128) NOT NULL,arguments ` + blob + ` NOT NULL,change_data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,PRIMARY KEY(run_id,seq))`,
		`CREATE TABLE IF NOT EXISTS agent_run_keys(run_id VARCHAR(191) NOT NULL,idem_key VARCHAR(191) NOT NULL,workspace_key VARCHAR(191) NOT NULL,result ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(run_id,idem_key))`,
		`CREATE TABLE IF NOT EXISTS agent_session_states(workspace_key VARCHAR(191) NOT NULL,session_id VARCHAR(191) NOT NULL,run_id VARCHAR(191) NOT NULL,content_type VARCHAR(128) NOT NULL,state ` + blob + ` NOT NULL,updated_at BIGINT NOT NULL,PRIMARY KEY(workspace_key,session_id))`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS agent_run_claim ON agent_runs(status,priority,next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS agent_run_workspace ON agent_runs(workspace_key,status)`,
		`CREATE INDEX IF NOT EXISTS agent_run_session ON agent_runs(workspace_key,session_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS agent_run_lease ON agent_runs(status,lease_until)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS agent_run_idempotency ON agent_runs(workspace_key,idempotency_key)`,
		`CREATE INDEX IF NOT EXISTS agent_run_token_run ON agent_run_tokens(run_id)`,
		`CREATE INDEX IF NOT EXISTS agent_run_token_expiry ON agent_run_tokens(expires_at)`,
	} {
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, " IF NOT EXISTS", "", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

// agentRunWaker wakes long-polling claims when a run becomes claimable.
type agentRunWaker struct {
	mu sync.Mutex
	ch chan struct{}
}

func (w *agentRunWaker) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch == nil {
		w.ch = make(chan struct{})
	}
	return w.ch
}

func (w *agentRunWaker) wake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ch != nil {
		close(w.ch)
		w.ch = nil
	}
}

// AgentRunWake returns a channel closed the next time a run is enqueued or
// queued again, so a claim can wait for work without polling.
func (s *SQLiteStore) AgentRunWake() <-chan struct{} { return s.agentRunWake.wait() }

func scanAgentRun(scan func(...any) error) (AgentRun, error) {
	var run AgentRun
	var idempotency, errorText sql.NullString
	var cancel, wrote int
	var principal, payload, usage []byte
	err := scan(&run.ID, &run.WorkspaceKey, &run.SessionID, &run.Kind, &run.Priority, &run.Status, &idempotency, &run.LeaseOwner, &run.LeaseUntil, &run.HeartbeatAt, &run.NextAttemptAt, &run.Attempts, &cancel, &wrote, &principal, &payload, &usage, &errorText, &run.CreatedAt, &run.UpdatedAt, &run.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return run, ErrAgentRunNotFound
	}
	if err != nil {
		return run, err
	}
	run.IdempotencyKey, run.Error = idempotency.String, errorText.String
	run.CancelRequested, run.Wrote = cancel != 0, wrote != 0
	run.Principal, run.Payload = json.RawMessage(principal), json.RawMessage(payload)
	if len(usage) > 0 {
		run.Usage = json.RawMessage(usage)
	}
	return run, nil
}

func validJSONOrEmpty(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), true
	}
	return raw, json.Valid(raw)
}

// EnqueueAgentRun queues a run. With an idempotency key already used in the
// workspace it returns that run and false instead.
func (s *SQLiteStore) EnqueueAgentRun(ctx context.Context, input AgentRunInput) (AgentRun, bool, error) {
	priority, ok := AgentRunPriority(input.Kind)
	if !ok || strings.TrimSpace(input.WorkspaceKey) == "" || strings.TrimSpace(input.SessionID) == "" || len(input.IdempotencyKey) > 191 || len(input.ID) > 191 {
		return AgentRun{}, false, ErrAgentRunInput
	}
	principal, principalOK := validJSONOrEmpty(input.Principal)
	payload, payloadOK := validJSONOrEmpty(input.Payload)
	if !principalOK || !payloadOK {
		return AgentRun{}, false, ErrAgentRunInput
	}
	if input.IdempotencyKey != "" {
		if existing, err := s.agentRunByIdempotencyKey(ctx, input.WorkspaceKey, input.IdempotencyKey); err == nil {
			return existing, false, nil
		} else if !errors.Is(err, ErrAgentRunNotFound) {
			return AgentRun{}, false, err
		}
	}
	id := input.ID
	if id == "" {
		token, err := randomToken()
		if err != nil {
			return AgentRun{}, false, err
		}
		// Time first, so runs queued in the same millisecond still claim in order (ORDER BY …, id).
		id = fmt.Sprintf("run_%016x%s", time.Now().UnixNano(), token[:12])
	}
	now := millis(time.Now())
	next := now
	if !input.NotBefore.IsZero() {
		next = millis(input.NotBefore)
	}
	key := sql.NullString{String: input.IdempotencyKey, Valid: input.IdempotencyKey != ""}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_runs(id,workspace_key,session_id,kind,priority,status,idempotency_key,lease_owner,lease_until,heartbeat_at,next_attempt_at,attempts,cancel_requested,wrote,principal,payload,created_at,updated_at,finished_at) VALUES(?,?,?,?,?,?,?,'',0,0,?,0,0,0,?,?,?,?,0)`,
		id, input.WorkspaceKey, input.SessionID, input.Kind, priority, AgentRunQueued, key, next, []byte(principal), []byte(payload), now, now)
	if err != nil {
		// A concurrent enqueue with the same key won the unique index.
		if input.IdempotencyKey != "" {
			if existing, lookupErr := s.agentRunByIdempotencyKey(ctx, input.WorkspaceKey, input.IdempotencyKey); lookupErr == nil {
				return existing, false, nil
			}
		}
		return AgentRun{}, false, err
	}
	s.agentRunWake.wake()
	run, err := s.AgentRun(ctx, id)
	return run, err == nil, err
}

func (s *SQLiteStore) agentRunByIdempotencyKey(ctx context.Context, workspace, key string) (AgentRun, error) {
	return scanAgentRun(s.db.QueryRowContext(ctx, `SELECT `+agentRunColumns+` FROM agent_runs WHERE workspace_key=? AND idempotency_key=?`, workspace, key).Scan)
}

// AgentRun reads one run.
func (s *SQLiteStore) AgentRun(ctx context.Context, id string) (AgentRun, error) {
	return scanAgentRun(s.db.QueryRowContext(ctx, `SELECT `+agentRunColumns+` FROM agent_runs WHERE id=?`, id).Scan)
}

// SessionAgentRuns lists a session's runs, newest first.
func (s *SQLiteStore) SessionAgentRuns(ctx context.Context, workspace, sessionID string, limit int) ([]AgentRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT `+agentRunColumns+` FROM agent_runs WHERE workspace_key=? AND session_id=? ORDER BY created_at DESC,id DESC LIMIT %d`, limit), workspace, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []AgentRun{}
	for rows.Next() {
		run, err := scanAgentRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// CountActiveAgentRuns counts the runs a workspace holds (leased or running).
func (s *SQLiteStore) CountActiveAgentRuns(ctx context.Context, workspace string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE workspace_key=? AND status IN ('leased','running')`, workspace).Scan(&count)
	return count, err
}

type agentRunCandidate struct {
	id, workspace string
	priority      int
}

// ClaimAgentRuns leases due runs to the caller: at most Free[lane] per lane,
// highest priority first, and within a lane the workspace holding the fewest
// runs first (oldest run on a tie), never more than MaxPerWorkspace per
// workspace. It returns the claimed runs with their payloads.
func (s *SQLiteStore) ClaimAgentRuns(ctx context.Context, claim AgentRunClaim) ([]AgentRun, error) {
	if strings.TrimSpace(claim.Owner) == "" || len(claim.Owner) > 191 {
		return nil, ErrAgentRunInput
	}
	free := map[int]int{}
	lanes := []any{}
	for lane, slots := range claim.Free {
		if slots > 0 && lane >= 0 && lane <= 3 {
			free[lane] = slots
			lanes = append(lanes, lane)
		}
	}
	if len(lanes) == 0 {
		return nil, nil
	}
	now := claim.Now
	if now.IsZero() {
		now = time.Now()
	}
	lease := claim.Lease
	if lease <= 0 {
		lease = time.Minute
	}
	at := millis(now)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(lanes)), ",")
	query := `SELECT id,workspace_key,priority FROM agent_runs WHERE status='queued' AND next_attempt_at<=? AND priority IN (` + marks + `) ORDER BY priority,next_attempt_at,id LIMIT 32`
	locking := s.dialect != "sqlite"
	if locking {
		// Rows another claimer has locked are skipped instead of waited on.
		query += " FOR UPDATE SKIP LOCKED"
	}
	var tx *sqlTx
	exec := func(query string, args ...any) (sql.Result, error) { return s.db.ExecContext(ctx, query, args...) }
	selectRows := func(query string, args ...any) (*sql.Rows, error) { return s.db.QueryContext(ctx, query, args...) }
	if locking {
		var err error
		tx, err = s.db.BeginTx(ctx, claimTxOptions)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		exec = func(query string, args ...any) (sql.Result, error) { return tx.ExecContext(ctx, query, args...) }
		selectRows = func(query string, args ...any) (*sql.Rows, error) { return tx.QueryContext(ctx, query, args...) }
	}
	rows, err := selectRows(query, append([]any{at}, lanes...)...)
	if err != nil {
		return nil, err
	}
	candidates := []agentRunCandidate{}
	workspaces := []any{}
	for rows.Next() {
		var candidate agentRunCandidate
		if err := rows.Scan(&candidate.id, &candidate.workspace, &candidate.priority); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
		if !slices.Contains(workspaces, any(candidate.workspace)) {
			workspaces = append(workspaces, candidate.workspace)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	active := map[string]int{}
	rows, err = selectRows(`SELECT workspace_key,COUNT(*) FROM agent_runs WHERE status IN ('leased','running') AND workspace_key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(workspaces)), ",")+`) GROUP BY workspace_key`, workspaces...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var workspace string
		var count int
		if err := rows.Scan(&workspace, &count); err != nil {
			rows.Close()
			return nil, err
		}
		active[workspace] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	claimed := []any{}
	taken := map[string]bool{}
	for lane := 0; lane <= 3; lane++ {
		for free[lane] > 0 {
			// The least busy workspace's oldest run in this lane.
			best := -1
			for index, candidate := range candidates {
				if candidate.priority != lane || taken[candidate.id] || claim.MaxPerWorkspace > 0 && active[candidate.workspace] >= claim.MaxPerWorkspace {
					continue
				}
				if best < 0 || active[candidate.workspace] < active[candidates[best].workspace] {
					best = index
				}
			}
			if best < 0 {
				break
			}
			candidate := candidates[best]
			taken[candidate.id] = true
			result, err := exec(`UPDATE agent_runs SET status='leased',lease_owner=?,lease_until=?,heartbeat_at=?,attempts=attempts+1,cancel_requested=0,updated_at=? WHERE id=? AND status='queued'`, claim.Owner, millis(now.Add(lease)), at, at, candidate.id)
			if err != nil {
				return nil, err
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				if err != nil {
					return nil, err
				}
				continue
			}
			claimed = append(claimed, candidate.id)
			active[candidate.workspace]++
			free[lane]--
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	rows, err = s.db.QueryContext(ctx, `SELECT `+agentRunColumns+` FROM agent_runs WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(claimed)), ",")+`) ORDER BY priority,next_attempt_at,id`, claimed...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []AgentRun{}
	for rows.Next() {
		run, err := scanAgentRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// HeartbeatAgentRun renews the owner's lease. The first heartbeat marks a
// leased run running. The returned run carries CancelRequested.
func (s *SQLiteStore) HeartbeatAgentRun(ctx context.Context, id, owner string, lease time.Duration, now time.Time) (AgentRun, error) {
	if lease <= 0 {
		lease = time.Minute
	}
	at := millis(now)
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET status='running',lease_until=?,heartbeat_at=?,updated_at=? WHERE id=? AND lease_owner=? AND status IN ('leased','running')`, millis(now.Add(lease)), at, at, id, owner)
	if err != nil {
		return AgentRun{}, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return AgentRun{}, err
		}
		if _, err := s.AgentRun(ctx, id); err != nil {
			return AgentRun{}, err
		}
		return AgentRun{}, ErrAgentRunLeaseLost
	}
	return s.AgentRun(ctx, id)
}

// FinishAgentRun ends a run the owner holds and revokes its tokens.
func (s *SQLiteStore) FinishAgentRun(ctx context.Context, id, owner string, outcome AgentRunResult, now time.Time) (AgentRun, error) {
	switch outcome.Status {
	case AgentRunDone, AgentRunFailed, AgentRunCanceled, AgentRunAwaiting, AgentRunInterrupted:
	default:
		return AgentRun{}, ErrAgentRunInput
	}
	var usage any
	if len(outcome.Usage) > 0 {
		if !json.Valid(outcome.Usage) || len(outcome.Usage) > 64<<10 {
			return AgentRun{}, ErrAgentRunInput
		}
		usage = []byte(outcome.Usage)
	}
	at := millis(now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRun{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status=?,lease_owner='',lease_until=0,finished_at=?,error_text=?,usage_json=?,updated_at=? WHERE id=? AND lease_owner=? AND status IN ('leased','running')`, outcome.Status, at, truncateText(outcome.Error, 2048), usage, at, id, owner)
	if err != nil {
		return AgentRun{}, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return AgentRun{}, err
		}
		// Release the transaction (and SQLite's only connection) before looking the run up.
		_ = tx.Rollback()
		if _, err := s.AgentRun(ctx, id); err != nil {
			return AgentRun{}, err
		}
		return AgentRun{}, ErrAgentRunLeaseLost
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tokens SET revoked_at=? WHERE run_id=? AND revoked_at=0`, at, id); err != nil {
		return AgentRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRun{}, err
	}
	return s.AgentRun(ctx, id)
}

// CancelAgentRun cancels a queued run, or one suspended waiting for a person
// (awaiting), at once and asks the worker holding a leased or running one to
// stop (heartbeats report CancelRequested).
func (s *SQLiteStore) CancelAgentRun(ctx context.Context, id string) (AgentRun, error) {
	at := millis(time.Now())
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET status='canceled',finished_at=?,updated_at=? WHERE id=? AND status IN ('queued','awaiting')`, at, at, id); err != nil {
		return AgentRun{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET cancel_requested=1,updated_at=? WHERE id=? AND status IN ('leased','running')`, at, id); err != nil {
		return AgentRun{}, err
	}
	s.agentRunCancel.wake()
	return s.AgentRun(ctx, id)
}

// CancelUnclaimedAgentRun cancels a run that is still queued and has waited
// since queuedBefore (when it was queued or requeued, or its retry was due),
// with reason as its error; false when a worker took it, it ended, or it has
// not waited that long.
func (s *SQLiteStore) CancelUnclaimedAgentRun(ctx context.Context, id, reason string, queuedBefore time.Time) (AgentRun, bool, error) {
	at, before := millis(time.Now()), millis(queuedBefore)
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET status='canceled',finished_at=?,error_text=?,updated_at=? WHERE id=? AND status='queued' AND updated_at<=? AND next_attempt_at<=?`, at, truncateText(reason, 2048), at, id, before, before)
	if err != nil {
		return AgentRun{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return AgentRun{}, false, err
	}
	run, err := s.AgentRun(ctx, id)
	return run, count == 1, err
}

// AgentRunCancelWake returns a channel closed the next time a run may have to
// stop in this process: a cancel was requested or an agent session was
// canceled. Waiters re-check with AgentRunsToCancel (other API processes do not
// wake it, so they also re-check periodically).
func (s *SQLiteStore) AgentRunCancelWake() <-chan struct{} { return s.agentRunCancel.wait() }

// AgentRunsToCancel lists the runs a worker holds that should stop: a cancel
// was requested, or the person stopped the run's session (a stop signal moves
// the session to canceled).
func (s *SQLiteStore) AgentRunsToCancel(ctx context.Context, owner string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id FROM agent_runs r LEFT JOIN application_agent_tasks t ON t.id=r.session_id AND t.workspace_key=r.workspace_key WHERE r.status IN ('leased','running') AND r.lease_owner=? AND (r.cancel_requested=1 OR t.status='canceled') ORDER BY r.id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkAgentRunWrote records that a write tool succeeded during the run, so an
// expired lease is not retried automatically (it could repeat the write).
func (s *SQLiteStore) MarkAgentRunWrote(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET wrote=1 WHERE id=? AND wrote=0`, id)
	return err
}

// ReleaseExpiredAgentRuns recovers runs whose lease expired while leased or
// running: a run that wrote nothing and has attempts left is queued again;
// the others become interrupted (returned, so the caller can tell the
// session). Tokens of every released run are revoked.
func (s *SQLiteStore) ReleaseExpiredAgentRuns(ctx context.Context, now time.Time, maxAttempts int) (requeued, interrupted []AgentRun, err error) {
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	at := millis(now)
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentRunColumns+` FROM agent_runs WHERE status IN ('leased','running') AND lease_until<? ORDER BY lease_until LIMIT 100`, at)
	if err != nil {
		return nil, nil, err
	}
	expired := []AgentRun{}
	for rows.Next() {
		run, err := scanAgentRun(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		expired = append(expired, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, run := range expired {
		retry := !run.Wrote && run.Attempts < maxAttempts && !run.CancelRequested
		var result sql.Result
		if retry {
			result, err = s.db.ExecContext(ctx, `UPDATE agent_runs SET status='queued',lease_owner='',lease_until=0,next_attempt_at=?,updated_at=? WHERE id=? AND status=? AND lease_owner=? AND lease_until<?`, at, at, run.ID, run.Status, run.LeaseOwner, at)
		} else {
			status, message := AgentRunInterrupted, "The worker stopped responding (lease expired)."
			if run.CancelRequested {
				status, message = AgentRunCanceled, ""
			}
			result, err = s.db.ExecContext(ctx, `UPDATE agent_runs SET status=?,lease_owner='',lease_until=0,finished_at=?,error_text=?,updated_at=? WHERE id=? AND status=? AND lease_owner=? AND lease_until<?`, status, at, message, at, run.ID, run.Status, run.LeaseOwner, at)
		}
		if err != nil {
			return requeued, interrupted, err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			if err != nil {
				return requeued, interrupted, err
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE agent_run_tokens SET revoked_at=? WHERE run_id=? AND revoked_at=0`, at, run.ID); err != nil {
			return requeued, interrupted, err
		}
		current, err := s.AgentRun(ctx, run.ID)
		if err != nil {
			return requeued, interrupted, err
		}
		if retry {
			requeued = append(requeued, current)
		} else if current.Status == AgentRunInterrupted {
			interrupted = append(interrupted, current)
		}
	}
	if len(requeued) > 0 {
		s.agentRunWake.wake()
	}
	// Tokens are useless a day after they expired.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM agent_run_tokens WHERE expires_at<?`, millis(now.Add(-24*time.Hour)))
	return requeued, interrupted, nil
}

// ---------------------------------------------------------------- tokens

// CreateAgentRunToken stores a run token's hash.
func (s *SQLiteStore) CreateAgentRunToken(ctx context.Context, run AgentRun, hash string, expires time.Time) error {
	if hash == "" || run.ID == "" {
		return ErrAgentRunInput
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_tokens(token_hash,run_id,workspace_key,expires_at,revoked_at,created_at) VALUES(?,?,?,?,0,?)`, hash, run.ID, run.WorkspaceKey, millis(expires), millis(time.Now()))
	return err
}

// AgentRunForToken returns the run a token belongs to while the token is
// unexpired, not revoked, and a worker holds the run.
func (s *SQLiteStore) AgentRunForToken(ctx context.Context, hash string, now time.Time) (AgentRun, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM agent_run_tokens WHERE token_hash=? AND revoked_at=0 AND expires_at>?`, hash, millis(now)).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRun{}, ErrAgentRunTokenInvalid
	}
	if err != nil {
		return AgentRun{}, err
	}
	run, err := s.AgentRun(ctx, runID)
	if errors.Is(err, ErrAgentRunNotFound) || err == nil && !AgentRunHeld(run.Status) {
		return AgentRun{}, ErrAgentRunTokenInvalid
	}
	return run, err
}

// RevokeAgentRunTokens revokes every token of a run.
func (s *SQLiteStore) RevokeAgentRunTokens(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_run_tokens SET revoked_at=? WHERE run_id=? AND revoked_at=0`, millis(time.Now()), runID)
	return err
}

// ---------------------------------------------------------------- change set

// AppendAgentRunChange adds an entry to a run's change set and returns its
// sequence number (1, 2, …).
func (s *SQLiteStore) AppendAgentRunChange(ctx context.Context, run AgentRun, tool string, arguments json.RawMessage, change domain.AgentChange) (int64, error) {
	arguments, ok := validJSONOrEmpty(arguments)
	if !ok || tool == "" || len(tool) > 128 {
		return 0, ErrAgentRunInput
	}
	raw, err := json.Marshal(change)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Lock the run row so concurrent tool calls of one run number their changes in turn.
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM agent_runs WHERE id=?`+s.lockClause(), run.ID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrAgentRunNotFound
		}
		return 0, err
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM agent_run_changes WHERE run_id=?`, run.ID).Scan(&last); err != nil {
		return 0, err
	}
	seq := last.Int64 + 1
	at := millis(time.Now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_changes(run_id,seq,workspace_key,tool,arguments,change_data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, run.ID, seq, run.WorkspaceKey, tool, []byte(arguments), raw, at, at); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

// AgentRunChanges lists a run's change set in order.
func (s *SQLiteStore) AgentRunChanges(ctx context.Context, runID string) ([]AgentRunChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,tool,arguments,change_data,created_at,updated_at FROM agent_run_changes WHERE run_id=? ORDER BY seq LIMIT 1000`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []AgentRunChange{}
	for rows.Next() {
		item := AgentRunChange{RunID: runID}
		var arguments, raw []byte
		if err := rows.Scan(&item.Seq, &item.Tool, &arguments, &raw, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Change); err != nil {
			return nil, err
		}
		item.Arguments = json.RawMessage(arguments)
		changes = append(changes, item)
	}
	return changes, rows.Err()
}

// UpdateAgentRunChange replaces the recorded change of an entry (after an
// undo or a restore).
func (s *SQLiteStore) UpdateAgentRunChange(ctx context.Context, runID string, seq int64, change domain.AgentChange) error {
	raw, err := json.Marshal(change)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_run_changes SET change_data=?,updated_at=? WHERE run_id=? AND seq=?`, raw, millis(time.Now()), runID, seq)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return err
		}
		return ErrAgentRunNotFound
	}
	return nil
}

// ---------------------------------------------------------------- idempotency keys

// AgentRunKey returns what was stored under an idempotency key of the run.
func (s *SQLiteStore) AgentRunKey(ctx context.Context, runID, key string) ([]byte, bool, error) {
	var result []byte
	err := s.db.QueryRowContext(ctx, `SELECT result FROM agent_run_keys WHERE run_id=? AND idem_key=?`, runID, key).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return result, err == nil, err
}

// SaveAgentRunKey stores the result of an idempotent request; the first
// result stored under a key wins.
func (s *SQLiteStore) SaveAgentRunKey(ctx context.Context, run AgentRun, key string, result []byte) error {
	if key == "" || len(key) > 191 {
		return ErrAgentRunInput
	}
	if result == nil {
		result = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_keys(run_id,idem_key,workspace_key,result,created_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id,idem_key) DO NOTHING`, run.ID, key, run.WorkspaceKey, result, millis(time.Now()))
	return err
}

// ReplaceAgentRunKey stores a value of a run under key, replacing what was
// stored before (run-scoped state that changes during the run, such as the
// delegated-run guard of a worker run).
func (s *SQLiteStore) ReplaceAgentRunKey(ctx context.Context, run AgentRun, key string, result []byte) error {
	if key == "" || len(key) > 191 {
		return ErrAgentRunInput
	}
	if result == nil {
		result = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_keys(run_id,idem_key,workspace_key,result,created_at) VALUES(?,?,?,?,?) ON CONFLICT(run_id,idem_key) DO UPDATE SET result=excluded.result`, run.ID, key, run.WorkspaceKey, result, millis(time.Now()))
	return err
}

// ---------------------------------------------------------------- session state

// SaveAgentSessionState stores (replaces) a session's serialized harness state.
func (s *SQLiteStore) SaveAgentSessionState(ctx context.Context, state AgentSessionState) error {
	if state.WorkspaceKey == "" || state.SessionID == "" || len(state.Data) > AgentSessionStateMaxBytes || len(state.ContentType) > 128 {
		return ErrAgentRunInput
	}
	if state.ContentType == "" {
		state.ContentType = "application/octet-stream"
	}
	if state.Data == nil {
		state.Data = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_session_states(workspace_key,session_id,run_id,content_type,state,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_key,session_id) DO UPDATE SET run_id=excluded.run_id,content_type=excluded.content_type,state=excluded.state,updated_at=excluded.updated_at`,
		state.WorkspaceKey, state.SessionID, state.RunID, state.ContentType, state.Data, millis(time.Now()))
	return err
}

// AgentSessionStateFor reads a session's stored state.
func (s *SQLiteStore) AgentSessionStateFor(ctx context.Context, workspace, sessionID string) (AgentSessionState, error) {
	state := AgentSessionState{WorkspaceKey: workspace, SessionID: sessionID}
	err := s.db.QueryRowContext(ctx, `SELECT run_id,content_type,state,updated_at FROM agent_session_states WHERE workspace_key=? AND session_id=?`, workspace, sessionID).Scan(&state.RunID, &state.ContentType, &state.Data, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrAgentRunNotFound
	}
	return state, err
}

// DeleteAgentSessionState removes a session's stored state.
func (s *SQLiteStore) DeleteAgentSessionState(ctx context.Context, workspace, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_session_states WHERE workspace_key=? AND session_id=?`, workspace, sessionID)
	return err
}
