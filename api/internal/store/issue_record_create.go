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

type issueRecordMutationContext struct{}
type issueCreationKeyContext struct{}

func WithIssueCreationKey(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, issueCreationKeyContext{}, id)
}
func IssueCreationKey(ctx context.Context) string {
	id, _ := ctx.Value(issueCreationKeyContext{}).(string)
	return id
}

func WithIssueRecordMutations(ctx context.Context, ids ...string) context.Context {
	return context.WithValue(ctx, issueRecordMutationContext{}, ids)
}
func UsesIssueRecordMutations(ctx context.Context) bool {
	return ctx.Value(issueRecordMutationContext{}) != nil
}

func (s *SQLiteStore) createIssueRecords(ctx context.Context, workspace string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	metadata, ok := s.WorkspaceMetadata(workspace)
	if !ok {
		return fmt.Errorf("workspace not found")
	}
	workspace = metadata.Workspace.URLKey
	if actor, ok := actorFromContext(ctx); ok {
		metadata.Viewer = actor
	}
	var parent struct {
		ParentID *string `json:"parentId"`
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &parent); err != nil {
		return err
	}
	var event domain.DomainEvent
	var realtime json.RawMessage
	err = func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		actor := metadata.Viewer
		current, ok := s.workspaces[workspace]
		if !ok {
			return fmt.Errorf("workspace not found")
		}
		metadata = cloneBootstrap(collectionMetadata(current))
		var encoded []byte
		metadata.Viewer = actor
		originalRole := metadata.ViewerRole
		if _, ok := actorFromContext(ctx); ok {
			role, status, err := s.WorkspaceRole(ctx, metadata.Workspace.ID, actor.ID)
			if err != nil || status != "active" {
				return ErrAuthForbidden
			}
			metadata.ViewerRole = role
		}
		metadata.Issues = []domain.Issue{}
		metadata.Activities = map[string][]domain.ActivityEvent{}
		metadata.Comments = map[string][]domain.Comment{}
		metadata.Notifications = []domain.Notification{}
		metadata.NotificationDeliveries = []domain.NotificationDelivery{}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := writeIssueNumber(ctx, tx, workspace, 0); err != nil {
			return err
		}
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var last int
		if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`+lock, workspace).Scan(&last); err != nil {
			return err
		}
		metadata.NextIssueNumber = last + 1
		if id := IssueCreationKey(ctx); id != "" {
			var raw []byte
			err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw)
			if err == nil {
				var existing domain.Issue
				if err = json.Unmarshal(raw, &existing); err != nil {
					return err
				}
				metadata.Issues = append(metadata.Issues, existing)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		previous := map[string]domain.Issue{}
		if parent.ParentID != nil && *parent.ParentID != "" {
			for _, column := range []string{"id", "parent_id"} {
				rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND `+column+`=? ORDER BY id LIMIT 1001`+lock, workspace, *parent.ParentID)
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
			if len(previous) > 1000 {
				return fmt.Errorf("issue family exceeds synchronous limit")
			}
		}
		id, err := mutate(&metadata)
		if err != nil {
			return err
		}
		newCount := 0
		var created domain.Issue
		for _, issue := range metadata.Issues {
			if issue.ID == id {
				created = issue
			}
			old, exists := previous[issue.ID]
			if !exists {
				newCount++
			}
			last = max(last, issue.Number)
			if !exists || !reflect.DeepEqual(old, issue) {
				if err := s.writeIssueRecord(ctx, tx, workspace, issue, metadata); err != nil {
					return err
				}
			}
		}
		if created.ID == "" || newCount < 1 || newCount > 1000 {
			return fmt.Errorf("invalid issue create result")
		}
		if err := writeIssueNumber(ctx, tx, workspace, last); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=issue_collection_counts.total+excluded.total`, workspace, newCount); err != nil {
			return err
		}
		for resource, items := range metadata.Activities {
			for _, item := range items {
				if err := writeContentRecord(ctx, tx, workspace, "activity", resource, item); err != nil {
					return err
				}
			}
		}
		for resource, items := range metadata.Comments {
			for _, item := range items {
				if err := writeContentRecord(ctx, tx, workspace, "comment", resource, item); err != nil {
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
		for _, item := range metadata.NotificationDeliveries {
			if err := writeContentRecord(ctx, tx, workspace, "delivery", "", item); err != nil {
				return err
			}
		}
		metadata.ViewerRole = originalRole
		metadata.Viewer = current.Viewer
		metadata = collectionMetadata(metadata)
		encoded, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
		if len(encoded) > s.maxStateBytes {
			return fmt.Errorf("workspace metadata exceeds %d bytes", s.maxStateBytes)
		}
		if err := writeWorkspaceMetadata(ctx, tx, workspace, metadata.Workspace.ID, encoded); err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "issue.created", AggregateID: id, Payload: raw, CreatedAt: time.Now().UTC()}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, id, raw, event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		realtime = enrichRealtimePayload(raw, created, event.Type)
		if err := tx.Commit(); err != nil {
			return err
		}
		s.workspaces[workspace] = metadata
		return nil
	}()
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
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
