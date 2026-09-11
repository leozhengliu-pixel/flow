package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// DeleteWorkflowStateRecords only loads the state catalog and matching issues.
// The transaction keeps the state, issue records, list projections and counters
// consistent; a failed batch cannot leave a partially migrated workflow.
func (s *SQLiteStore) DeleteWorkflowStateRecords(ctx context.Context, workspace, teamID string, validate func(*domain.Bootstrap, bool) (domain.WorkflowState, *domain.WorkflowState, error)) error {
	var event domain.DomainEvent
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		current, ok := s.workspaces[workspace]
		if !ok {
			return ErrAuthForbidden
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		metadata := domain.Bootstrap{}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='states'`+lock, workspace)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw []byte
			var state domain.WorkflowState
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal(raw, &state); err != nil {
				rows.Close()
				return err
			}
			metadata.States = append(metadata.States, state)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		state, replacement, err := validate(&metadata, false)
		if err != nil {
			return err
		}
		// Never delete a shared legacy default on behalf of one team.
		if state.TeamID != teamID {
			return ErrAuthForbidden
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=?`, workspace, teamID, state.ID).Scan(&count); err != nil {
			return err
		}
		if _, _, err := validate(&metadata, count > 0); err != nil {
			return err
		}
		if count > 0 && replacement == nil {
			return fmt.Errorf("replacement state missing")
		}
		if replacement != nil && (replacement.ID == state.ID || replacement.TeamID != teamID) {
			return ErrAuthForbidden
		}
		if count > 0 {
			lastOrder, lastID, started := float64(0), "", false
			for {
				query := `SELECT data,sort_order,id FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=?`
				args := []any{workspace, teamID, state.ID}
				if started {
					query += ` AND (sort_order>? OR (sort_order=? AND id>?))`
					args = append(args, lastOrder, lastOrder, lastID)
				}
				rows, err := tx.QueryContext(ctx, query+` ORDER BY sort_order,id LIMIT 100`+lock, args...)
				if err != nil {
					return err
				}
				batch := []domain.Issue{}
				for rows.Next() {
					var raw []byte
					var issue domain.Issue
					if err := rows.Scan(&raw, &lastOrder, &lastID); err != nil {
						rows.Close()
						return err
					}
					if err := json.Unmarshal(raw, &issue); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, issue)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if len(batch) == 0 {
					break
				}
				started = true
				for _, issue := range batch {
					now := time.Now().UTC()
					issue.State = *replacement
					issue.StatusChangedAt = &now
					if replacement.Type == "started" && issue.StartedAt == nil {
						issue.StartedAt = &now
					}
					switch replacement.Type {
					case "completed":
						issue.CompletedAt, issue.CanceledAt = &now, nil
					case "canceled":
						issue.CanceledAt, issue.CompletedAt = &now, nil
					default:
						issue.CompletedAt, issue.CanceledAt = nil, nil
					}
					issue.Version++
					issue.UpdatedAt = now
					if err := s.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
						return err
					}
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='states' AND record_key=?`, workspace, state.ID); err != nil {
			return err
		}
		replacementID := ""
		if replacement != nil {
			replacementID = replacement.ID
		}
		payload, err := json.Marshal(map[string]any{"teamId": teamID, "stateId": state.ID, "replacementStateId": replacementID, "affectedIssueCount": count})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: "workflow_state.deleted", AggregateID: state.ID, Payload: payload, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(payload), []byte(nil), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current.States = slices.DeleteFunc(slices.Clone(current.States), func(item domain.WorkflowState) bool { return item.ID == state.ID && item.TeamID == teamID })
		s.workspaces[workspace] = current
		return nil
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspace, apply)
	} else {
		err = apply()
	}
	if err != nil {
		return err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}
