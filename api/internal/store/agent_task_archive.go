package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"flow/api/internal/domain"
)

// ErrAgentTaskArchived refuses a write to an archived agent session.
var ErrAgentTaskArchived = domain.ErrAgentSessionArchived

// AgentTaskArchive is the outcome of ArchiveAgentTask.
type AgentTaskArchive struct {
	Task domain.AgentTask
	// Archived is false when the session was already archived (nothing was
	// written and no event was published).
	Archived bool
	// Stopped is true when the session was still pending, working or waiting
	// for input and the archive canceled it.
	Stopped bool
	// PreviousStatus is the stored status before the archive.
	PreviousStatus string
}

// ArchiveAgentTask archives a whole agent session (soft): it leaves the
// resource's session list, and with it every message of the session, agent
// replies and people's replies alike. Its activities stay in
// application_agent_activities as the execution log; nothing else (comments,
// attachments, documents, coding sessions, linked issues) is touched.
//
// A session that is still pending, working or waiting for input is canceled
// in the same commit, with a "canceled" activity naming the person, so a
// worker running it stops (AgentRunsToCancel, the built-in run's status
// check). The archive bumps the version: a run that read the session before
// fails its compare-and-swap, and AppendAgentActivity refuses an archived
// session afterwards, so late activity can neither reopen the session nor
// show it again.
//
// Archiving an archived session returns Archived=false without a write.
func (s *SQLiteStore) ArchiveAgentTask(ctx context.Context, workspace, id, actorID string) (AgentTaskArchive, error) {
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentTaskArchive{}, err
	}
	defer tx.Rollback()
	// The guarded stamp is the claim: it takes the row (MySQL and PostgreSQL
	// hold its lock until commit, SQLite the database write lock), so the
	// state read next is the one this archive replaces, and a concurrent
	// archive finds archived_at set and changes nothing.
	result, err := tx.ExecContext(ctx, `UPDATE application_agent_tasks SET archived_at=?,archived_by=?,version=version+1 WHERE workspace_key=? AND id=? AND archived_at=''`, stamp, actorID, workspace, id)
	if err != nil {
		return AgentTaskArchive{}, err
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return AgentTaskArchive{}, err
	}
	if claimed != 1 {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return AgentTaskArchive{}, err
		}
		task, err := s.AgentTask(ctx, workspace, id)
		if err != nil {
			return AgentTaskArchive{}, err
		}
		return AgentTaskArchive{Task: task, PreviousStatus: task.Status}, nil
	}
	task, err := scanAgentTask(tx.QueryRowContext(ctx, `SELECT `+agentTaskColumns+` FROM application_agent_tasks WHERE workspace_key=? AND id=?`, workspace, id).Scan)
	if err != nil {
		return AgentTaskArchive{}, err
	}
	outcome := AgentTaskArchive{Archived: true, PreviousStatus: task.Status}
	task.UpdatedAt = now
	var stop *domain.AgentActivity
	switch task.Status {
	case "pending", "active", "awaitingInput":
		outcome.Stopped = true
		task.Status, task.PendingTool = "canceled", nil
		stop = &domain.AgentActivity{
			ID: fmt.Sprintf("%020d", task.Version), SessionID: id, ActorID: actorID, Type: "canceled", Signal: "stop", CreatedAt: now,
			Metadata: map[string]any{"reason": "archived"},
		}
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return AgentTaskArchive{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE application_agent_tasks SET status=?,data=?,lease_until='',stale_at=? WHERE workspace_key=? AND id=?`, task.Status, raw, agentTaskStaleAt(task), workspace, id); err != nil {
		return AgentTaskArchive{}, err
	}
	if stop != nil {
		stopRaw, err := json.Marshal(stop)
		if err != nil {
			return AgentTaskArchive{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO application_agent_activities(session_id,id,data,created_at) VALUES(?,?,?,?)`, id, stop.ID, stopRaw, stamp); err != nil {
			return AgentTaskArchive{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentTaskArchive{}, err
	}
	if outcome.Stopped {
		s.agentRunCancel.wake()
	}
	if sink := s.realtime(); sink != nil {
		// Every open thread reloads its resource's sessions on the push and
		// drops the archived one.
		resourceID := task.IssueID
		if resourceID == "" {
			resourceID = task.ResourceID
		}
		payload, _ := json.Marshal(map[string]any{"taskId": task.ID, "resourceId": resourceID, "resourceType": task.ResourceType, "version": task.Version, "archived": true})
		sink(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("agent_%s_%d", id, task.Version), Type: "agent_task.updated", AggregateID: resourceID, Payload: payload, CreatedAt: now})
	}
	outcome.Task = task
	return outcome, nil
}
