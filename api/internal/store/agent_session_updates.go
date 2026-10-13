package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// agentSessionNeverDue parks a session the agent created itself: the worker
// only delivers it once a person prompts it (which clears next_attempt).
const agentSessionNeverDue = "9999-12-31T00:00:00Z"

// agentActivityFromAgent reports activity types only the session's agent emits.
func agentActivityFromAgent(kind string) bool {
	switch kind {
	case "thought", "action", "output", "response", "error", "elicitation":
		return true
	}
	return false
}

// UpdateAgentTaskSession changes session fields that are not part of the
// activity state machine (external URLs, plan, delivery and acknowledgement
// times). It does not bump the version, so an agent's expectedVersion for its
// next activity stays valid.
func (s *SQLiteStore) UpdateAgentTaskSession(ctx context.Context, workspace, id string, mutate func(*domain.AgentTask) error) (domain.AgentTask, error) {
	for attempt := 0; attempt < 5; attempt++ {
		task, err := s.AgentTask(ctx, workspace, id)
		if err != nil {
			return task, err
		}
		if err := mutate(&task); err != nil {
			return task, err
		}
		raw, err := json.Marshal(task)
		if err != nil {
			return task, err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE application_agent_tasks SET data=?,stale_at=? WHERE workspace_key=? AND id=? AND version=?`, raw, agentTaskStaleAt(task), workspace, id, task.Version)
		if err != nil {
			return task, err
		}
		if count, err := result.RowsAffected(); err != nil {
			return task, err
		} else if count == 1 {
			s.publishAgentTaskUpdate(workspace, task)
			return task, nil
		}
	}
	return domain.AgentTask{}, ErrIssueVersion
}

func (s *SQLiteStore) publishAgentTaskUpdate(workspace string, task domain.AgentTask) {
	sink := s.realtime()
	if sink == nil {
		return
	}
	resourceID := task.IssueID
	if resourceID == "" {
		resourceID = task.ResourceID
	}
	payload, _ := json.Marshal(map[string]any{"taskId": task.ID, "resourceId": resourceID, "resourceType": task.ResourceType, "version": task.Version})
	sink(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("agent_%s_%d_%d", task.ID, task.Version, time.Now().UnixNano()), Type: "agent_task.updated", AggregateID: resourceID, Payload: payload, CreatedAt: time.Now().UTC()})
}

// MarkAgentTaskDelivered records when the latest event reached the agent;
// the agent then has domain.AgentAcknowledgementWindow to respond.
func (s *SQLiteStore) MarkAgentTaskDelivered(ctx context.Context, workspace, id string, version int64, at time.Time) error {
	_, err := s.UpdateAgentTaskSession(ctx, workspace, id, func(task *domain.AgentTask) error {
		if task.Version != version {
			return ErrNoMutation
		}
		at := at.UTC()
		task.DeliveredAt = &at
		return nil
	})
	if err == ErrNoMutation {
		return nil
	}
	return err
}

// CreateAgentInitiatedTask stores a session an agent opened itself on an
// issue or a comment. No created event is sent to the agent.
func (s *SQLiteStore) CreateAgentInitiatedTask(ctx context.Context, task domain.AgentTask) (domain.AgentTask, error) {
	if task.ID == "" || task.WorkspaceKey == "" || task.AppUserID == "" || task.IssueID == "" && task.ResourceID == "" {
		return task, ErrIssueQuery
	}
	now := time.Now().UTC()
	task.Status, task.Version, task.Trigger = "pending", 1, "agent"
	task.CreatedAt, task.UpdatedAt, task.LastActivityAt = now, now, &now
	raw, err := json.Marshal(task)
	if err != nil {
		return task, err
	}
	resource := task.IssueID
	if resource == "" {
		resource = task.ResourceID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at,next_attempt,stale_at) VALUES(?,?,?,?,?,1,?,?,?,?)`, task.ID, task.WorkspaceKey, resource, task.AppUserID, task.Status, raw, now.Format(time.RFC3339Nano), agentSessionNeverDue, agentTaskStaleAt(task))
	if err != nil {
		return task, err
	}
	s.publishAgentTaskUpdate(task.WorkspaceKey, task)
	return task, nil
}

// applyAgentActivityTiming stamps the agent's latest activity and replaces
// its previous ephemeral activity. It runs inside AppendAgentActivity's
// transaction and returns the ephemeral activity to delete.
func applyAgentActivityTiming(task *domain.AgentTask, activity *domain.AgentActivity) string {
	if !agentActivityFromAgent(activity.Type) {
		activity.Ephemeral = false
		return ""
	}
	at := activity.CreatedAt
	task.LastActivityAt = &at
	previous := task.EphemeralActivityID
	task.EphemeralActivityID = ""
	if activity.Ephemeral && (activity.Type == "thought" || activity.Type == "action") {
		task.EphemeralActivityID = activity.ID
	} else {
		activity.Ephemeral = false
	}
	return previous
}

func (s *SQLiteStore) deleteEphemeralActivity(ctx context.Context, tx *sqlTx, sessionID, activityID string) error {
	if strings.TrimSpace(activityID) == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM application_agent_activities WHERE session_id=? AND id=?`, sessionID, activityID)
	return err
}
