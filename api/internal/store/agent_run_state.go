package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Per-run state of a worker run that several API servers update at once: the
// run's /mcp calls, guard operations and activity posts may each reach a
// different server, so nothing about a run is kept in one process's memory.
//
//   - agent_run_tool_calls: one row per write call of the run (its ledger
//     entry: tool, target, outcome), keyed by the call. Recording is an
//     upsert, so a retried call updates its own entry instead of adding one,
//     and concurrent calls never overwrite each other's entries.
//   - agent_run_states: small state machines of a run (its tool budget, its
//     guard, its notices) with a version: a writer reads value and version,
//     computes the next value and stores it only if the version is unchanged
//     (SwapAgentRunState), retrying otherwise.
//   - agent_run_activities: the activities a chat run posts, one row per
//     (attempt, seq), where seq is the worker's own per-run counter. Saving is
//     insert-if-absent, so a repeated post is a no-op, and the reply is built
//     from the rows in seq order whatever order they arrived in.

func (s *SQLiteStore) createAgentRunStateSchema(ctx context.Context) error {
	blob := "BLOB"
	switch s.dialect {
	case "mysql":
		blob = "LONGBLOB"
	case "postgres":
		blob = "BYTEA"
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if s.dialect == "mysql" {
		prefix = "CREATE INDEX "
	}
	return s.applyMigrationStatements(ctx, []string{
		`CREATE TABLE IF NOT EXISTS agent_run_tool_calls(run_id VARCHAR(191) NOT NULL,call_key VARCHAR(191) NOT NULL,workspace_key VARCHAR(191) NOT NULL,seq BIGINT NOT NULL,outcome ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(run_id,call_key))`,
		`CREATE TABLE IF NOT EXISTS agent_run_states(run_id VARCHAR(191) NOT NULL,state_key VARCHAR(191) NOT NULL,workspace_key VARCHAR(191) NOT NULL,version BIGINT NOT NULL,state_data ` + blob + ` NOT NULL,updated_at BIGINT NOT NULL,PRIMARY KEY(run_id,state_key))`,
		`CREATE TABLE IF NOT EXISTS agent_run_activities(run_id VARCHAR(191) NOT NULL,attempt INTEGER NOT NULL,seq BIGINT NOT NULL,workspace_key VARCHAR(191) NOT NULL,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(run_id,attempt,seq))`,
		prefix + "agent_run_tool_call_order ON agent_run_tool_calls(run_id,seq)",
	})
}

// ---------------------------------------------------------------- tool calls

// AgentRunToolCallsMax bounds the entries AgentRunToolCalls returns (the
// first ones; the tool budget stops a run well before).
const AgentRunToolCallsMax = 500

// RecordAgentRunToolCall stores the ledger entry of one call of a run under
// key: a new key adds an entry (ordered by seq), a key recorded before has its
// outcome replaced and keeps its place.
func (s *SQLiteStore) RecordAgentRunToolCall(ctx context.Context, run AgentRun, key string, seq int64, outcome []byte) error {
	if run.ID == "" || key == "" || len(key) > 191 || len(outcome) == 0 {
		return ErrAgentRunInput
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_tool_calls(run_id,call_key,workspace_key,seq,outcome,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,call_key) DO UPDATE SET outcome=excluded.outcome`, run.ID, key, run.WorkspaceKey, seq, outcome, millis(time.Now()))
	return err
}

// AgentRunToolCalls lists the outcomes recorded for a run's calls in order
// (at most AgentRunToolCallsMax).
func (s *SQLiteStore) AgentRunToolCalls(ctx context.Context, runID string) ([][]byte, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT outcome FROM agent_run_tool_calls WHERE run_id=? ORDER BY seq,call_key LIMIT %d`, AgentRunToolCallsMax), runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	outcomes := [][]byte{}
	for rows.Next() {
		var outcome []byte
		if err := rows.Scan(&outcome); err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, rows.Err()
}

// CopyAgentRunToolCalls copies the entries of run from onto run to (a run that
// continues another: a resumed chat reply), keeping their keys and order.
func (s *SQLiteStore) CopyAgentRunToolCalls(ctx context.Context, from string, to AgentRun) error {
	if from == "" || to.ID == "" || from == to.ID {
		return ErrAgentRunInput
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_tool_calls(run_id,call_key,workspace_key,seq,outcome,created_at) SELECT ?,call_key,workspace_key,seq,outcome,created_at FROM agent_run_tool_calls WHERE run_id=? ON CONFLICT(run_id,call_key) DO NOTHING`, to.ID, from)
	return err
}

// DeleteAgentRunToolCalls forgets a run's entries (a run that starts over).
func (s *SQLiteStore) DeleteAgentRunToolCalls(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_run_tool_calls WHERE run_id=?`, runID)
	return err
}

// ---------------------------------------------------------------- versioned state

// AgentRunState returns the value stored under a run's state key and its
// version; found is false (version 0) when nothing is stored.
func (s *SQLiteStore) AgentRunState(ctx context.Context, runID, key string) (value []byte, version int64, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT state_data,version FROM agent_run_states WHERE run_id=? AND state_key=?`, runID, key).Scan(&value, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return value, version, true, nil
}

// SwapAgentRunState stores value under key if the stored version is still
// version (0: nothing is stored yet); swapped is false when another writer
// stored a value first, and the caller reads the state again.
func (s *SQLiteStore) SwapAgentRunState(ctx context.Context, run AgentRun, key string, version int64, value []byte) (swapped bool, err error) {
	if run.ID == "" || key == "" || len(key) > 191 || version < 0 {
		return false, ErrAgentRunInput
	}
	if value == nil {
		value = []byte{}
	}
	now := millis(time.Now())
	var result sql.Result
	if version == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO agent_run_states(run_id,state_key,workspace_key,version,state_data,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,state_key) DO NOTHING`, run.ID, key, run.WorkspaceKey, 1, value, now)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE agent_run_states SET state_data=?,version=?,updated_at=? WHERE run_id=? AND state_key=? AND version=?`, value, version+1, now, run.ID, key, version)
	}
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// ReplaceAgentRunState stores value under key whatever is stored (a state
// that starts over); writers holding the previous version then fail to swap.
func (s *SQLiteStore) ReplaceAgentRunState(ctx context.Context, run AgentRun, key string, value []byte) error {
	if run.ID == "" || key == "" || len(key) > 191 {
		return ErrAgentRunInput
	}
	if value == nil {
		value = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_states(run_id,state_key,workspace_key,version,state_data,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,state_key) DO UPDATE SET state_data=excluded.state_data,version=agent_run_states.version+1,updated_at=excluded.updated_at`, run.ID, key, run.WorkspaceKey, 1, value, millis(time.Now()))
	return err
}

// ---------------------------------------------------------------- activities

// AgentRunActivity is one stored activity of a run attempt.
type AgentRunActivity struct {
	Seq       int64
	Data      []byte
	CreatedAt int64
}

// AgentRunActivitiesMax bounds the activities one AgentRunActivities call
// returns.
const AgentRunActivitiesMax = 1000

// SaveAgentRunActivity stores an activity of a run attempt under seq (≥ 1) if
// none is stored there yet; stored is false for a repeated post.
func (s *SQLiteStore) SaveAgentRunActivity(ctx context.Context, run AgentRun, attempt int, seq int64, data []byte) (stored bool, err error) {
	if run.ID == "" || seq < 1 || len(data) == 0 {
		return false, ErrAgentRunInput
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_run_activities(run_id,attempt,seq,workspace_key,data,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,attempt,seq) DO NOTHING`, run.ID, attempt, seq, run.WorkspaceKey, data, millis(time.Now()))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// AppendAgentRunActivity stores an activity of a run attempt after the last
// one stored (for a poster that numbers nothing, and posts one at a time) and
// returns its seq. Concurrent appends each take the next free seq.
func (s *SQLiteStore) AppendAgentRunActivity(ctx context.Context, run AgentRun, attempt int, data []byte) (int64, error) {
	for round := range 64 {
		var last sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM agent_run_activities WHERE run_id=? AND attempt=?`, run.ID, attempt).Scan(&last); err != nil {
			return 0, err
		}
		stored, err := s.SaveAgentRunActivity(ctx, run, attempt, last.Int64+1, data)
		if err != nil || stored {
			return last.Int64 + 1, err
		}
		time.Sleep(time.Duration(1+rand.IntN(1+round)) * time.Millisecond)
	}
	return 0, fmt.Errorf("agent run %s: activity could not be numbered", run.ID)
}

// AgentRunActivities lists the activities of a run attempt after seq, in
// order (at most AgentRunActivitiesMax).
func (s *SQLiteStore) AgentRunActivities(ctx context.Context, runID string, attempt int, after int64) ([]AgentRunActivity, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT seq,data,created_at FROM agent_run_activities WHERE run_id=? AND attempt=? AND seq>? ORDER BY seq LIMIT %d`, AgentRunActivitiesMax), runID, attempt, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activities := []AgentRunActivity{}
	for rows.Next() {
		var item AgentRunActivity
		if err := rows.Scan(&item.Seq, &item.Data, &item.CreatedAt); err != nil {
			return nil, err
		}
		activities = append(activities, item)
	}
	return activities, rows.Err()
}
