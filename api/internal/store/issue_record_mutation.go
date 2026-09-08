package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

var ErrIssueVersion = errors.New("issue version conflict")

type IssueMutationScope struct {
	RelatedIDs    []string
	IncludeFamily bool
	NewParentID   string
}

// UpdateIssueRecord is a bounded entity transaction. The callback may update
// the target, its immediate family, and the supplied related IDs. Large graph
// changes must be scheduled as batches rather than materialized in a request.
func (s *SQLiteStore) UpdateIssueRecord(ctx context.Context, workspace, id string, expected *int64, scope IssueMutationScope, mutate func(*domain.Bootstrap, *domain.Issue) error) (domain.Issue, error) {
	metadata, ok := s.WorkspaceMetadata(workspace)
	if !ok {
		return domain.Issue{}, sql.ErrNoRows
	}
	if len(scope.RelatedIDs) > 100 {
		return domain.Issue{}, fmt.Errorf("too many related issues")
	}
	if actor, ok := actorFromContext(ctx); ok {
		metadata.Viewer = actor
		role, _, err := s.WorkspaceRole(ctx, metadata.Workspace.ID, actor.ID)
		if err != nil {
			return domain.Issue{}, err
		}
		metadata.ViewerRole = role
	}
	var result domain.Issue
	var event domain.DomainEvent
	var realtime json.RawMessage
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`+lock, workspace, id).Scan(&raw); err != nil {
			return err
		}
		var original domain.Issue
		if err := json.Unmarshal(raw, &original); err != nil {
			return err
		}
		normalizeIssueRecord(&original)
		result = original
		if expected != nil && original.Version != *expected {
			return ErrIssueVersion
		}
		ids := append([]string{id}, scope.RelatedIDs...)
		if original.ParentID != nil {
			ids = append(ids, *original.ParentID)
		}
		parents := []string{scope.NewParentID}
		if original.ParentID != nil {
			parents = append(parents, *original.ParentID)
		}
		seenParents := map[string]bool{}
		for _, parent := range parents {
			for parent != "" && !seenParents[parent] {
				if len(seenParents) > 64 {
					return fmt.Errorf("issue ancestor limit exceeded")
				}
				seenParents[parent] = true
				var raw []byte
				if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, parent).Scan(&raw); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						break
					}
					return err
				}
				var ancestor domain.Issue
				if err := json.Unmarshal(raw, &ancestor); err != nil {
					return err
				}
				ids = append(ids, ancestor.ID)
				parent = ""
				if ancestor.ParentID != nil {
					parent = *ancestor.ParentID
				}
			}
		}
		clause, args := bindList("id", ids)
		family := "(parent_id=?"
		familyArgs := []any{id}
		if original.ParentID != nil {
			family += " OR parent_id=?"
			familyArgs = append(familyArgs, *original.ParentID)
		}
		family += ")"
		previous := map[string]domain.Issue{}
		metadata.Issues = []domain.Issue{}
		// Combining primary-key and parent-index predicates with OR makes MySQL
		// scan the workspace primary-key range. Keep both lookups independently indexed.
		lookups := []struct {
			where string
			args  []any
		}{{clause, args}}
		if scope.IncludeFamily {
			lookups = append(lookups, struct {
				where string
				args  []any
			}{family, familyArgs})
		}
		for _, lookup := range lookups {
			rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND `+lookup.where+` ORDER BY id LIMIT 1001`+lock, append([]any{workspace}, lookup.args...)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var encoded []byte
				if err := rows.Scan(&encoded); err != nil {
					rows.Close()
					return err
				}
				var issue domain.Issue
				if err := json.Unmarshal(encoded, &issue); err != nil {
					rows.Close()
					return err
				}
				normalizeIssueRecord(&issue)
				if _, seen := previous[issue.ID]; !seen {
					previous[issue.ID] = issue
					metadata.Issues = append(metadata.Issues, issue)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		if len(metadata.Issues) > 1000 {
			return fmt.Errorf("issue family exceeds the synchronous mutation limit")
		}
		var target *domain.Issue
		for i := range metadata.Issues {
			if metadata.Issues[i].ID == id {
				target = &metadata.Issues[i]
				break
			}
		}
		if target == nil {
			return sql.ErrNoRows
		}
		metadata.Activities = map[string][]domain.ActivityEvent{}
		metadata.Comments = map[string][]domain.Comment{}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND resource_id=? ORDER BY created_at DESC LIMIT 1000`, workspace, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var encoded []byte
			if err := rows.Scan(&encoded); err != nil {
				rows.Close()
				return err
			}
			var notification domain.Notification
			if err := json.Unmarshal(encoded, &notification); err != nil {
				rows.Close()
				return err
			}
			metadata.Notifications = append(metadata.Notifications, notification)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		metadata.NotificationDeliveries, err = issueNotificationDeliveries(ctx, tx, workspace, metadata.Notifications)
		if err != nil {
			return err
		}
		if err := mutate(&metadata, target); err != nil {
			return err
		}
		target.Version = original.Version + 1
		target.UpdatedAt = time.Now().UTC()
		result = *target
		for _, issue := range metadata.Issues {
			if !reflect.DeepEqual(previous[issue.ID], issue) {
				if err := s.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
					return err
				}
			}
		}
		for resource, items := range metadata.Activities {
			for _, item := range items {
				if err := writeContentRecord(ctx, tx, workspace, "activity", resource, item); err != nil {
					return err
				}
			}
		}
		for resource, items := range notificationRecords(metadata.Notifications) {
			for _, item := range items {
				if err := writeContentRecord(ctx, tx, workspace, "notification", resource, item); err != nil {
					return err
				}
			}
		}
		for _, delivery := range metadata.NotificationDeliveries {
			if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]any{"issue": result})
		before, _ := json.Marshal(original)
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "issue.updated", AggregateID: id, Payload: payload, PreviousValues: before, CreatedAt: time.Now().UTC()}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, id, payload, before, event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		realtime = enrichRealtimePayload(payload, result, event.Type)
		return tx.Commit()
	}()
	if err != nil {
		return result, err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: id, Payload: realtime, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), CreatedAt: event.CreatedAt})
	}
	return result, nil
}
