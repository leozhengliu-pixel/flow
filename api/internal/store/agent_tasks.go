package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// The task and issue update share a commit: an accepted delegation cannot lose
// its execution request when a process exits before sending a webhook.
func syncApplicationTask(ctx context.Context, tx *sqlTx, workspace string, issue domain.Issue, previous []byte) error {
	var old domain.Issue
	if len(previous) > 0 {
		if err := json.Unmarshal(previous, &old); err != nil {
			return err
		}
	}
	if old.AgentSessionID != "" && (old.AgentSessionID != issue.AgentSessionID || issue.Delegate == nil) {
		if _, err := tx.ExecContext(ctx, `UPDATE application_agent_tasks SET status='canceled',version=version+1 WHERE id=? AND workspace_key=? AND status IN ('pending','active','awaitingInput')`, old.AgentSessionID, workspace); err != nil {
			return err
		}
	}
	if issue.Delegate == nil || !issue.Delegate.App || issue.AgentSessionID == "" || old.AgentSessionID == issue.AgentSessionID {
		return nil
	}
	var app domain.ApplicationInstallation
	var appRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND user_id=?`, workspace, issue.Delegate.ID).Scan(&appRaw); err != nil {
		return ErrAuthForbidden
	}
	if err := json.Unmarshal(appRaw, &app); err != nil {
		return err
	}
	if !app.User().CanDelegateTo(issue.Team.ID) {
		return ErrAuthForbidden
	}
	actor, _ := ctx.Value(actorContextKey{}).(domain.User)
	if actor.ID == "" {
		actor = issue.Creator
	}
	task := domain.AgentTask{ID: issue.AgentSessionID, WorkspaceKey: workspace, IssueID: issue.ID, TeamID: issue.Team.ID, AppUserID: issue.Delegate.ID, CreatorID: actor.ID, Status: "pending", Trigger: "delegation", Prompt: issue.Title, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?) ON CONFLICT(id) DO NOTHING`, task.ID, workspace, issue.ID, task.AppUserID, task.Status, raw, task.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) AgentTask(ctx context.Context, workspace, id string) (domain.AgentTask, error) {
	var task domain.AgentTask
	var raw []byte
	var status string
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT data,status,version FROM application_agent_tasks WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw, &status, &version)
	if err == nil {
		err = json.Unmarshal(raw, &task)
		task.Status = status
		task.Version = version
	}
	return task, err
}

func (s *SQLiteStore) ListAgentTasks(ctx context.Context, workspace, issueID, appUserID string) ([]domain.AgentTask, error) {
	where, args := "workspace_key=?", []any{workspace}
	if issueID != "" {
		where += " AND issue_id=?"
		args = append(args, issueID)
	}
	if appUserID != "" {
		where += " AND app_user_id=?"
		args = append(args, appUserID)
		if issueID == "" {
			where += " AND status IN ('pending','active','awaitingInput')"
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data,status,version FROM application_agent_tasks WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AgentTask{}
	for rows.Next() {
		var task domain.AgentTask
		var raw []byte
		var status string
		var version int64
		if err := rows.Scan(&raw, &status, &version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, err
		}
		task.Status = status
		task.Version = version
		result = append(result, task)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) AgentActivities(ctx context.Context, workspace, id, after string) ([]domain.AgentActivity, error) {
	if _, err := s.AgentTask(ctx, workspace, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM application_agent_activities WHERE session_id=? AND id>? ORDER BY id LIMIT 100`, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AgentActivity{}
	for rows.Next() {
		var raw []byte
		var activity domain.AgentActivity
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &activity); err != nil {
			return nil, err
		}
		result = append(result, activity)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) AppendAgentActivity(ctx context.Context, workspace, id string, expected int64, activity domain.AgentActivity) (domain.AgentTask, error) {
	task, err := s.AgentTask(ctx, workspace, id)
	if err != nil {
		return task, err
	}
	if expected != task.Version {
		return task, ErrIssueVersion
	}
	terminal := task.Status == "complete" || task.Status == "error" || task.Status == "canceled"
	if terminal && activity.Type != "retry" || !terminal && activity.Type == "retry" {
		return task, ErrIssueVersion
	}
	if task.Status == "awaitingInput" && activity.Type != "prompt" && activity.Type != "canceled" {
		return task, ErrIssueVersion
	}
	if task.Status != "awaitingInput" && activity.Type == "prompt" {
		return task, ErrIssueVersion
	}
	switch activity.Type {
	case "thought", "action", "output":
		task.Status = "active"
	case "response":
		task.Status = "complete"
	case "error":
		task.Status = "error"
	case "elicitation":
		task.Status = "awaitingInput"
		task.PendingTool = activity.ToolCall
	case "prompt":
		task.Status = "pending"
		if task.PendingTool != nil {
			if activity.ToolCall == nil || (activity.ToolCall.Status != "approved" && activity.ToolCall.Status != "rejected") {
				return task, ErrIssueQuery
			}
			call := *task.PendingTool
			call.Status = activity.ToolCall.Status
			task.PendingTool = &call
		}
	case "retry":
		task.Status = "pending"
		task.PendingTool = nil
	case "canceled":
		task.Status = "canceled"
	default:
		return task, ErrIssueQuery
	}
	bodyLimit := 64 << 10
	if activity.Type == "response" {
		bodyLimit = 1 << 20
	}
	if len(activity.Body) > bodyLimit || len(activity.URL) > 2048 {
		return task, ErrIssueQuery
	}
	task.Version++
	task.UpdatedAt = time.Now().UTC()
	activity.CreatedAt = task.UpdatedAt
	activity.SessionID = id
	activity.ID = fmt.Sprintf("%020d", task.Version)
	if activity.Type == "prompt" && strings.TrimSpace(activity.Body) != "" {
		if task.InitialPrompt == "" {
			task.InitialPrompt = task.Prompt
		}
		task.Prompt = activity.Body
	}
	if activity.Type == "action" {
		task.PendingTool = nil
	}
	taskRaw, err := json.Marshal(task)
	if err != nil {
		return task, err
	}
	activityRaw, err := json.Marshal(activity)
	if err != nil {
		return task, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task, err
	}
	defer tx.Rollback()
	lease := ""
	if task.Status == "active" {
		lease = task.UpdatedAt.Add(5 * time.Minute).Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `UPDATE application_agent_tasks SET status=?,version=?,data=?,next_attempt='',lease_until=? WHERE workspace_key=? AND id=? AND version=?`, task.Status, task.Version, taskRaw, lease, workspace, id, expected)
	if err != nil {
		return task, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return task, err
	}
	if count != 1 {
		return task, ErrIssueVersion
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO application_agent_activities(session_id,id,data,created_at) VALUES(?,?,?,?)`, id, activity.ID, activityRaw, activity.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return task, err
	}
	if err = tx.Commit(); err != nil {
		return task, err
	}
	if sink := s.realtime(); sink != nil {
		sink(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("agent_%s_%d", id, task.Version), Type: "agent_task.updated", AggregateID: task.IssueID, CreatedAt: task.UpdatedAt})
	}
	return task, nil
}

func (s *SQLiteStore) PendingApplicationTasks(ctx context.Context) ([]domain.AgentTask, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `SELECT data,status,version FROM application_agent_tasks WHERE status IN ('pending','active') AND next_attempt<=? AND lease_until<=? ORDER BY id LIMIT 16`, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.AgentTask{}
	for rows.Next() {
		var raw []byte
		var status string
		var version int64
		var task domain.AgentTask
		if err := rows.Scan(&raw, &status, &version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, err
		}
		task.Status = status
		task.Version = version
		result = append(result, task)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ClaimApplicationTask(ctx context.Context, id string, version int64) (bool, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE application_agent_tasks SET lease_until=?,attempts=attempts+1 WHERE id=? AND version=? AND status IN ('pending','active') AND lease_until<=?`, now.Add(5*time.Minute).Format(time.RFC3339Nano), id, version, now.Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *SQLiteStore) FinishApplicationDelivery(ctx context.Context, id string, version int64, success bool) error {
	// Delivery acknowledgement does not imply task completion. Polling clients
	// can claim a pending task even when no webhook was configured.
	when := time.Now().UTC().Add(time.Minute)
	if success {
		when = when.Add(24 * time.Hour)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE application_agent_tasks SET lease_until='',next_attempt=? WHERE id=? AND version=?`, when.Format(time.RFC3339Nano), id, version)
	return err
}

func (s *SQLiteStore) ApplicationWebhookSecret(ctx context.Context, id string) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, `SELECT webhook_secret FROM application_installations WHERE id=?`, id).Scan(&secret)
	return secret, err
}

func (s *SQLiteStore) CreateMentionTask(ctx context.Context, task domain.AgentTask) error {
	if !strings.HasPrefix(task.ID, "mention_") {
		return ErrIssueQuery
	}
	task.Status = "pending"
	task.Version = 1
	task.CreatedAt = time.Now().UTC()
	task.UpdatedAt = task.CreatedAt
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data) VALUES(?,?,?,?,?,1,?) ON CONFLICT(id) DO NOTHING`, task.ID, task.WorkspaceKey, task.IssueID, task.AppUserID, "pending", raw)
	return err
}

func IsAgentTaskMissing(err error) bool { return errors.Is(err, sql.ErrNoRows) }
