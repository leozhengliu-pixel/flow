package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"flow/api/internal/domain"
)

func scopedContent[T any](ctx context.Context, tx *sqlTx, workspace, kind string, ids []string) (map[string][]T, error) {
	clause, args := bindList("resource_id", ids)
	rows, err := tx.QueryContext(ctx, `SELECT resource_id,data FROM workspace_content_records WHERE workspace_key=? AND kind=? AND `+clause+` ORDER BY created_at,id`, append([]any{workspace, kind}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]T{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var value T
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		result[id] = append(result[id], value)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) mutateIssueScope(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	if len(ids) == 0 || len(ids) > 1000 {
		return fmt.Errorf("invalid issue mutation scope")
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtime json.RawMessage
	var batch domain.BatchIssueUpdateInput
	if eventType == "issue.batch_updated" {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &batch); err != nil {
			return err
		}
	}
	needsFamily := eventType == "issue.deleted" || eventType == "issue.batch_updated" && (batch.Update.StateID != nil || batch.Update.ParentID != nil)
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.workspaces[workspace]
		if !ok {
			return fmt.Errorf("workspace not found")
		}
		raw, err := json.Marshal(collectionMetadata(current))
		if err != nil {
			return err
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		originalRole := data.ViewerRole
		if actor, ok := actorFromContext(ctx); ok {
			data.Viewer = actor
			role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, actor.ID)
			if err != nil || status != "active" {
				return ErrAuthForbidden
			}
			data.ViewerRole = role
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
		loaded := map[string]domain.Issue{}
		pending := slices.Clone(ids)
		for len(pending) > 0 {
			id := pending[0]
			pending = pending[1:]
			if _, seen := loaded[id]; seen {
				continue
			}
			if len(loaded) >= 1000 {
				return fmt.Errorf("issue mutation scope exceeds 1000 records")
			}
			var raw []byte
			if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`+lock, workspace, id).Scan(&raw); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return err
			}
			var issue domain.Issue
			if err := json.Unmarshal(raw, &issue); err != nil {
				return err
			}
			normalizeIssueRecord(&issue)
			loaded[id] = issue
			if needsFamily && slices.Contains(ids, id) {
				if issue.ParentID != nil {
					pending = append(pending, *issue.ParentID)
				}
				if eventType == "issue.deleted" {
					for _, relation := range issue.Relations {
						pending = append(pending, relation.RelatedIssueID)
					}
				}
			}
		}
		if needsFamily {
			parentIDs := slices.Clone(ids)
			if eventType == "issue.batch_updated" {
				for _, issue := range loaded {
					if issue.ParentID != nil {
						parentIDs = append(parentIDs, *issue.ParentID)
					}
				}
			}
			for _, id := range parentIDs {
				rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND parent_id=? ORDER BY id LIMIT 1001`+lock, workspace, id)
				if err != nil {
					return err
				}
				for rows.Next() {
					var raw []byte
					if err := rows.Scan(&raw); err != nil {
						rows.Close()
						return err
					}
					var issue domain.Issue
					if err := json.Unmarshal(raw, &issue); err != nil {
						rows.Close()
						return err
					}
					loaded[issue.ID] = issue
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return err
				}
				rows.Close()
			}
			if len(loaded) > 1000 {
				return fmt.Errorf("issue deletion requires a batch job")
			}
		}
		if batch.Update.StateID != nil {
			teams := map[string]bool{}
			for _, id := range ids {
				if issue, ok := loaded[id]; ok {
					teams[issue.Team.ID] = true
				}
			}
			for team := range teams {
				for _, direction := range []string{"ASC", "DESC"} {
					var raw []byte
					err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=? ORDER BY sort_order `+direction+`,id `+direction+` LIMIT 1`, workspace, team, *batch.Update.StateID).Scan(&raw)
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					if err != nil {
						return err
					}
					var issue domain.Issue
					if err := json.Unmarshal(raw, &issue); err != nil {
						return err
					}
					loaded[issue.ID] = issue
				}
			}
		}
		keys := make([]string, 0, len(loaded))
		data.Issues = []domain.Issue{}
		for id, issue := range loaded {
			keys = append(keys, id)
			data.Issues = append(data.Issues, issue)
		}
		data.Comments, err = scopedContent[domain.Comment](ctx, tx, workspace, "comment", keys)
		if err != nil {
			return err
		}
		data.Activities, err = scopedContent[domain.ActivityEvent](ctx, tx, workspace, "activity", keys)
		if err != nil {
			return err
		}
		notifications, err := scopedContent[domain.Notification](ctx, tx, workspace, "notification", keys)
		if err != nil {
			return err
		}
		data.Notifications = []domain.Notification{}
		for _, items := range notifications {
			data.Notifications = append(data.Notifications, items...)
		}
		data.NotificationDeliveries, err = issueNotificationDeliveries(ctx, tx, workspace, data.Notifications)
		if err != nil {
			return err
		}
		oldDeliveries := map[string]bool{}
		for _, delivery := range data.NotificationDeliveries {
			oldDeliveries[delivery.ID] = true
		}
		aggregate, err := mutate(&data)
		if err != nil {
			return err
		}
		remaining := map[string]bool{}
		for _, issue := range data.Issues {
			remaining[issue.ID] = true
			if err := s.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
				return err
			}
		}
		removed := 0
		for id, issue := range loaded {
			if remaining[id] {
				continue
			}
			removed++
			deltas := map[issueStatsKey]int64{}
			addIssueStats(deltas, issueStatsOf(issue), -1)
			if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
				return err
			}
			for _, table := range []string{"issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_actor_records", "issue_attribute_records"} {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND issue_id=?", workspace, id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id); err != nil {
				return err
			}
		}
		if removed > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE issue_collection_counts SET total=total-? WHERE workspace_key=?`, removed, workspace); err != nil {
				return err
			}
		}
		if err := syncContentRecords(ctx, tx, workspace, "comment", data.Comments, keys); err != nil {
			return err
		}
		if err := syncContentRecords(ctx, tx, workspace, "activity", data.Activities, keys); err != nil {
			return err
		}
		if err := syncContentRecords(ctx, tx, workspace, "notification", notificationRecords(data.Notifications), keys); err != nil {
			return err
		}
		for _, delivery := range data.NotificationDeliveries {
			delete(oldDeliveries, delivery.ID)
			if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
				return err
			}
		}
		for id := range oldDeliveries {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, workspace, id); err != nil {
				return err
			}
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(payloadRaw, aggregateJSONValue(data, aggregate), eventType)
		data.ViewerRole = originalRole
		data = collectionMetadata(data)
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if len(encoded) > s.maxStateBytes {
			return fmt.Errorf("workspace metadata exceeds %d bytes", s.maxStateBytes)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_states SET data=?,updated_at=? WHERE workspace_key=?`, encoded, event.CreatedAt.Format(time.RFC3339Nano), workspace); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, aggregate, payloadRaw, event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.workspaces[workspace] = data
		return nil
	}()
	if err != nil {
		return err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: realtime, CreatedAt: event.CreatedAt})
	}
	return nil
}
