package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Coding sandboxes (track A): the persistence behind the
// sandbox reconciler, warm pool and orphan GC (docs/coding-sessions-design.md
// §5, §6, §11).
//
// Leases follow agent_runs: candidate rows are locked with FOR UPDATE SKIP
// LOCKED inside a short transaction on PostgreSQL and MySQL 8; SQLite has a
// single writer, so the conditional UPDATE (CAS on lease_until) is enough.
// lease_until doubles as the row's next reconcile time: releasing a row
// writes lease_owner='' and lease_until=<next observation>, so settled rows
// are re-observed every 15s without a separate column. Rows that are
// desired=stopped and observed missing are settled for good and never leased.
//
// Tokens are "fcs_" + 32 random bytes (base64url); only the SHA-256 hex
// (tokenHash) is stored, so AuthenticateSandboxToken (coding_sessions.go) looks
// them up with tokenHash(token). RevokeSandboxTokens revokes them.

const sandboxTokenPrefix = "fcs_"

// Store-level errors (ErrCodingLeaseLost, ErrCodingNotFound) are declared in
// coding_sessions.go.

// CodingSandbox is a coding_sandboxes row; it mirrors sandbox.Record
// field for field (see the adapter in cmd/server).
type CodingSandbox struct {
	ID            string
	WorkspaceKey  string
	SessionID     string
	SnapshotID    string
	Kind          string
	Desired       string
	Phase         string
	Reason        string
	Message       string
	Image         string
	Size          string
	Region        string
	VolumeID      string
	ArchiveURL    string
	Env           map[string]string
	DeadlineAt    time.Time
	Attempts      int
	ProviderRef   string
	CreatedAt     time.Time
	StartedAt     *time.Time
	EndedAt       *time.Time
	HeartbeatAt   *time.Time
	UserID        string
	LoopID        string
	BilledSeconds int64
	LogsKey       string
	KeepVolume    bool
}

// CodingSandboxObservation is what a provider reported (sandbox.SandboxStatus).
type CodingSandboxObservation struct {
	Phase       string
	Reason      string
	Message     string
	ProviderRef string
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

func terminalSandboxPhase(phase string) bool {
	return phase == "succeeded" || phase == "failed" || phase == "missing"
}

// validSandboxID mirrors sandbox.ValidID.
func validSandboxID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// codingSandboxData is the JSON document in coding_sandboxes.data: the
// sandbox.Record fields that have no column.
type codingSandboxData struct {
	Message    string            `json:"message,omitempty"`
	Image      string            `json:"image,omitempty"`
	VolumeID   string            `json:"volumeId,omitempty"`
	ArchiveURL string            `json:"archiveUrl,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	UserID     string            `json:"userId,omitempty"`
	KeepVolume bool              `json:"keepVolume,omitempty"`
}

func optionalMillis(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// codingSandboxExec is a *sqlDatabase or a *sqlTx.
type codingSandboxExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertCodingSandbox writes a new coding_sandboxes row. Track B's
// CreateCodingSandbox should call it so every row has the same data layout.
// An existing id is a no-op (idempotent creation).
func (s *SQLiteStore) insertCodingSandbox(ctx context.Context, exec codingSandboxExec, record CodingSandbox, provider string) error {
	if !validSandboxID(record.ID) {
		return fmt.Errorf("coding sandbox: invalid id %q", record.ID)
	}
	data, err := json.Marshal(codingSandboxData{
		Message: record.Message, Image: record.Image, VolumeID: record.VolumeID, ArchiveURL: record.ArchiveURL,
		Env: record.Env, UserID: record.UserID, KeepVolume: record.KeepVolume,
	})
	if err != nil {
		return err
	}
	created := record.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	desired, phase := record.Desired, record.Phase
	if desired == "" {
		desired = "running"
	}
	if phase == "" {
		phase = "pending"
	}
	insert := `INSERT INTO coding_sandboxes(id,workspace_key,session_id,snapshot_id,kind,provider,provider_ref,desired,phase,reason,size,region,attempts,deadline_at,billed_seconds,data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	switch s.dialect {
	case "mysql":
		insert = strings.Replace(insert, "INSERT INTO", "INSERT IGNORE INTO", 1)
	default:
		insert += ` ON CONFLICT(id) DO NOTHING`
	}
	deadline := int64(0)
	if !record.DeadlineAt.IsZero() {
		deadline = record.DeadlineAt.UnixMilli()
	}
	_, err = exec.ExecContext(ctx, insert, record.ID, record.WorkspaceKey, record.SessionID, record.SnapshotID, record.Kind, provider, record.ProviderRef,
		desired, phase, record.Reason, record.Size, record.Region, record.Attempts, deadline, record.BilledSeconds, data, created.UnixMilli())
	return err
}

// CreateWarmSandbox inserts a warm-pool row (unbound: no session).
func (s *SQLiteStore) CreateWarmSandbox(ctx context.Context, record CodingSandbox) error {
	record.Kind, record.SessionID = "warm", ""
	return s.insertCodingSandbox(ctx, s.db, record, "")
}

const codingSandboxColumns = `sb.id,sb.workspace_key,sb.session_id,sb.snapshot_id,sb.kind,sb.provider_ref,sb.desired,sb.phase,sb.reason,sb.size,sb.region,sb.attempts,sb.heartbeat_at,sb.deadline_at,sb.billed_seconds,sb.logs_key,sb.data,sb.created_at,sb.started_at,sb.ended_at,COALESCE(cs.creator_id,''),COALESCE(cs.source,''),COALESCE(cs.source_id,''),es.data`

const codingSandboxJoins = ` FROM coding_sandboxes sb LEFT JOIN coding_sessions cs ON cs.id=sb.session_id LEFT JOIN coding_environment_snapshots es ON es.id=sb.snapshot_id AND sb.kind='snapshot'`

func scanCodingSandbox(scan func(...any) error) (CodingSandbox, error) {
	var record CodingSandbox
	var creator, source, sourceID string
	var heartbeat, deadline, created, started, ended int64
	var data, snapshotData []byte
	if err := scan(&record.ID, &record.WorkspaceKey, &record.SessionID, &record.SnapshotID, &record.Kind, &record.ProviderRef, &record.Desired, &record.Phase, &record.Reason,
		&record.Size, &record.Region, &record.Attempts, &heartbeat, &deadline, &record.BilledSeconds, &record.LogsKey, &data, &created, &started, &ended,
		&creator, &source, &sourceID, &snapshotData); err != nil {
		return record, err
	}
	record.HeartbeatAt, record.StartedAt, record.EndedAt = fromMillis(heartbeat), fromMillis(started), fromMillis(ended)
	if deadline > 0 {
		record.DeadlineAt = time.UnixMilli(deadline).UTC()
	}
	record.CreatedAt = time.UnixMilli(created).UTC()
	var extra codingSandboxData
	if len(data) > 0 {
		_ = json.Unmarshal(data, &extra)
	}
	record.Message, record.Image, record.Env, record.KeepVolume = extra.Message, extra.Image, extra.Env, extra.KeepVolume
	record.VolumeID, record.ArchiveURL = extra.VolumeID, extra.ArchiveURL
	// Runtime is billed to the session creator (the loop for loop sessions),
	// the snapshot requester, or the user stored on the row (debug terminal).
	record.UserID = extra.UserID
	if creator != "" {
		record.UserID = creator
	}
	if source == "loop" {
		record.LoopID = sourceID
	}
	if record.UserID == "" && len(snapshotData) > 0 {
		var snapshot struct {
			RequestedBy string `json:"requestedBy"`
		}
		if json.Unmarshal(snapshotData, &snapshot) == nil {
			record.UserID = snapshot.RequestedBy
		}
	}
	return record, nil
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func (s *SQLiteStore) codingSandboxesByID(ctx context.Context, ids []string) ([]CodingSandbox, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+codingSandboxColumns+codingSandboxJoins+` WHERE sb.id IN (`+placeholders(len(ids))+`) ORDER BY sb.created_at,sb.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []CodingSandbox{}
	for rows.Next() {
		record, err := scanCodingSandbox(rows.Scan)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// LeaseSandboxes leases up to limit rows that are due (lease_until passed)
// and not settled.
func (s *SQLiteStore) LeaseSandboxes(ctx context.Context, owner string, lease time.Duration, limit int) ([]CodingSandbox, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 191 {
		return nil, fmt.Errorf("coding sandbox: invalid lease owner")
	}
	if limit <= 0 {
		limit = 50
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	now := time.Now()
	at := millis(now)
	query := `SELECT id FROM coding_sandboxes WHERE NOT (desired='stopped' AND phase='missing') AND lease_until<? ORDER BY lease_until,id LIMIT ?`
	exec := func(query string, args ...any) (sql.Result, error) { return s.db.ExecContext(ctx, query, args...) }
	selectRows := func(query string, args ...any) (*sql.Rows, error) { return s.db.QueryContext(ctx, query, args...) }
	var tx *sqlTx
	if s.dialect != "sqlite" {
		query += " FOR UPDATE SKIP LOCKED"
		var err error
		if tx, err = s.db.BeginTx(ctx, claimTxOptions); err != nil {
			return nil, err
		}
		defer tx.Rollback()
		exec = func(query string, args ...any) (sql.Result, error) { return tx.ExecContext(ctx, query, args...) }
		selectRows = func(query string, args ...any) (*sql.Rows, error) { return tx.QueryContext(ctx, query, args...) }
	}
	rows, err := selectRows(query, at, limit)
	if err != nil {
		return nil, err
	}
	candidates := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	leased := []string{}
	until := millis(now.Add(lease))
	for _, id := range candidates {
		result, err := exec(`UPDATE coding_sandboxes SET lease_owner=?,lease_until=? WHERE id=? AND lease_until<? AND NOT (desired='stopped' AND phase='missing')`, owner, until, id, at)
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			leased = append(leased, id)
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return s.codingSandboxesByID(ctx, leased)
}

// SaveSandboxObservation stores an observation and schedules the next one
// in 15s.
func (s *SQLiteStore) SaveSandboxObservation(ctx context.Context, owner, id string, status CodingSandboxObservation, attempts int) error {
	return s.SaveSandboxObservationAt(ctx, owner, id, status, attempts, time.Now().Add(15*time.Second))
}

// SaveSandboxObservationAt stores what the provider reported, releases the
// lease and schedules the next reconcile at next. started_at and ended_at
// are written once (the first observation that has them).
func (s *SQLiteStore) SaveSandboxObservationAt(ctx context.Context, owner, id string, status CodingSandboxObservation, attempts int, next time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	var providerRef string
	var started, ended int64
	err = tx.QueryRowContext(ctx, `SELECT data,provider_ref,started_at,ended_at FROM coding_sandboxes WHERE id=? AND lease_owner=?`, id, owner).Scan(&data, &providerRef, &started, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCodingLeaseLost
	}
	if err != nil {
		return err
	}
	var extra codingSandboxData
	if len(data) > 0 {
		_ = json.Unmarshal(data, &extra)
	}
	extra.Message = status.Message
	if len(extra.Message) > 2000 {
		extra.Message = extra.Message[:2000]
	}
	if data, err = json.Marshal(extra); err != nil {
		return err
	}
	if status.ProviderRef != "" {
		providerRef = status.ProviderRef
	}
	if started == 0 {
		started = optionalMillis(status.StartedAt)
	}
	if ended == 0 && terminalSandboxPhase(status.Phase) {
		ended = optionalMillis(status.FinishedAt)
		if ended == 0 {
			ended = millis(time.Now())
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE coding_sandboxes SET phase=?,reason=?,provider_ref=?,attempts=?,started_at=?,ended_at=?,data=?,lease_owner='',lease_until=? WHERE id=? AND lease_owner=?`,
		status.Phase, status.Reason, providerRef, attempts, started, ended, data, millis(next), id, owner)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCodingLeaseLost
	}
	return tx.Commit()
}

// SetSandboxLogsKey records the archived runner log.
func (s *SQLiteStore) SetSandboxLogsKey(ctx context.Context, owner, id, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET logs_key=? WHERE id=? AND lease_owner=?`, key, id, owner)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCodingLeaseLost
	}
	return nil
}

// AddSandboxBilledSeconds advances billed_seconds with a CAS on its old value.
func (s *SQLiteStore) AddSandboxBilledSeconds(ctx context.Context, owner, id string, from, to int64) (bool, error) {
	if to <= from {
		return false, nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET billed_seconds=? WHERE id=? AND lease_owner=? AND billed_seconds=?`, to, id, owner, from)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	return n == 1, nil
}

// MintSandboxToken issues a runtime token for a sandbox; only its hash is
// stored. Each start mints a fresh token (the provider rewrites the Secret).
func (s *SQLiteStore) MintSandboxToken(ctx context.Context, record CodingSandbox, ttl time.Duration) (string, error) {
	if record.ID == "" {
		return "", fmt.Errorf("coding sandbox token: sandbox id is required")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := sandboxTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO coding_sandbox_tokens(token_hash,sandbox_id,session_id,workspace_key,expires_at,created_at) VALUES(?,?,?,?,?,?)`,
		tokenHash(token), record.ID, record.SessionID, record.WorkspaceKey, millis(now.Add(ttl)), millis(now))
	if err != nil {
		return "", err
	}
	return token, nil
}

// KnownSandboxIDs reports which ids have a row that is not settled-stopped.
func (s *SQLiteStore) KnownSandboxIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	known := map[string]bool{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM coding_sandboxes WHERE id IN (`+placeholders(len(chunk))+`) AND NOT (desired='stopped' AND phase='missing')`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			known[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return known, nil
}

// WarmSandboxes counts unbound, healthy warm sandboxes per sandbox.WarmKey.
func (s *SQLiteStore) WarmSandboxes(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT size,region,COUNT(*) FROM coding_sandboxes WHERE kind='warm' AND session_id='' AND desired='running' AND phase IN ('pending','starting','running') GROUP BY size,region`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var size, region string
		var n int
		if err := rows.Scan(&size, &region, &n); err != nil {
			return nil, err
		}
		counts[size+"/"+region] = n // sandbox.WarmKey
	}
	return counts, rows.Err()
}

// BindWarmCodingSandbox claims a running, unbound warm sandbox of a size (and
// region, when given) for a session: a CAS on session_id=”. The row becomes
// a session sandbox, its tokens move to the session (the same token gains the
// session scope), and its idle time is marked as already billed, because the
// warm pool is an operator cost. snapshotID is the snapshot the runner
// restores after binding: it is stored on the row because the token grant
// (and so the snapshot archive route) reads the sandbox's snapshot_id.
// ErrCodingNotFound means no warm sandbox is available and the caller creates
// a fresh one.
func (s *SQLiteStore) BindWarmCodingSandbox(ctx context.Context, size, region, sessionID, snapshotID string) (CodingSandbox, error) {
	var workspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_key FROM coding_sessions WHERE id=?`, sessionID).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return CodingSandbox{}, ErrCodingNotFound
	}
	if err != nil {
		return CodingSandbox{}, err
	}
	query := `SELECT id,started_at FROM coding_sandboxes WHERE kind='warm' AND session_id='' AND desired='running' AND phase='running' AND size=?`
	args := []any{size}
	if region != "" {
		query += ` AND region=?`
		args = append(args, region)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at,id LIMIT 5`, args...)
	if err != nil {
		return CodingSandbox{}, err
	}
	type candidate struct {
		id      string
		started int64
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.started); err != nil {
			rows.Close()
			return CodingSandbox{}, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CodingSandbox{}, err
	}
	now := time.Now()
	for _, c := range candidates {
		idle := int64(0)
		if c.started > 0 {
			idle = max(0, (millis(now)-c.started)/1000)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return CodingSandbox{}, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE coding_sandboxes SET session_id=?,workspace_key=?,snapshot_id=?,kind='session',billed_seconds=? WHERE id=? AND kind='warm' AND session_id='' AND desired='running'`, sessionID, workspace, snapshotID, idle, c.id)
		if err != nil {
			tx.Rollback()
			return CodingSandbox{}, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			tx.Rollback()
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE coding_sandbox_tokens SET session_id=?,workspace_key=? WHERE sandbox_id=? AND revoked_at=0`, sessionID, workspace, c.id); err != nil {
			tx.Rollback()
			return CodingSandbox{}, err
		}
		if err := tx.Commit(); err != nil {
			return CodingSandbox{}, err
		}
		records, err := s.codingSandboxesByID(ctx, []string{c.id})
		if err != nil || len(records) != 1 {
			return CodingSandbox{}, errors.Join(err, ErrCodingNotFound)
		}
		return records[0], nil
	}
	return CodingSandbox{}, ErrCodingNotFound
}

// RecycleWarmSandboxes stops unbound warm sandboxes that are older than the
// cutoff or no longer healthy.
func (s *SQLiteStore) RecycleWarmSandboxes(ctx context.Context, createdBefore time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE coding_sandboxes SET desired='stopped',lease_until=CASE WHEN lease_owner='' THEN 0 ELSE lease_until END WHERE kind='warm' AND session_id='' AND desired='running' AND (created_at<? OR phase IN ('failed','succeeded','missing'))`, millis(createdBefore))
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// DeleteExpiredSandboxTokens deletes tokens that expired before t.
func (s *SQLiteStore) DeleteExpiredSandboxTokens(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM coding_sandbox_tokens WHERE expires_at<?`, millis(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SnapshotStates returns the status of each snapshot id that has a row.
func (s *SQLiteStore) SnapshotStates(ctx context.Context, ids []string) (map[string]string, error) {
	states := map[string]string{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id,status FROM coding_environment_snapshots WHERE id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err != nil {
				rows.Close()
				return nil, err
			}
			states[id] = status
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return states, nil
}

// SandboxCounts counts rows that are not settled-stopped by kind and phase.
func (s *SQLiteStore) SandboxCounts(ctx context.Context) (map[string]map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,phase,COUNT(*) FROM coding_sandboxes WHERE NOT (desired='stopped' AND phase='missing') GROUP BY kind,phase`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]map[string]int{}
	for rows.Next() {
		var kind, phase string
		var n int
		if err := rows.Scan(&kind, &phase, &n); err != nil {
			return nil, err
		}
		if counts[kind] == nil {
			counts[kind] = map[string]int{}
		}
		counts[kind][phase] = n
	}
	return counts, rows.Err()
}
