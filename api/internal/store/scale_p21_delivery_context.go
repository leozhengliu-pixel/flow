package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// NotificationDeliveryContextRead owns the exact rendering inputs consumed by
// immediate and digest email delivery. Data is a rendering projection, not a
// complete workspace snapshot, and stays charged until Close.
type NotificationDeliveryContextRead struct {
	Data   domain.Bootstrap
	due    *DueNotificationDeliveryRead
	id     *InstalledWorkspaceIdentityRead
	source *InstalledMetadataRead
	owner  *ResourceRead
}

func (r *NotificationDeliveryContextRead) Close() {
	if r == nil {
		return
	}
	r.Data = domain.Bootstrap{}
	if r.due != nil {
		r.due.Close()
		r.due = nil
	}
	if r.source != nil {
		r.source.Close()
		r.source = nil
	}
	if r.id != nil {
		_ = r.id.Close()
		r.id = nil
	}
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
}

// BeginNotificationDeliveryContextRead keeps the legacy due-row query and its
// contextless installed-M rendering choice. Only recipients, referenced review
// headers, and native notification/issue display fields enter request memory.
func (s *SQLiteStore) BeginNotificationDeliveryContextRead(ctx context.Context, workspace string, statuses []string, now time.Time) (_ *NotificationDeliveryContextRead, err error) {
	if !s.BoundedMetadataEnabled() {
		data, err := s.NotificationDeliverySnapshot(ctx, workspace, statuses, now)
		if err != nil {
			return nil, err
		}
		return &NotificationDeliveryContextRead{Data: data}, nil
	}
	r := &NotificationDeliveryContextRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	r.due, err = s.BeginDueNotificationDeliveryRead(ctx, workspace, statuses, now)
	if err != nil {
		return nil, err
	}
	r.Data.NotificationDeliveries = r.due.Deliveries
	if len(r.due.Deliveries) == 0 {
		return r, nil
	}
	var found bool
	r.id, found, err = s.BeginInstalledWorkspaceIdentityRead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("workspace not found")
	}
	workspace = r.id.Identity.URLKey
	r.source, found, err = s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("workspace not found")
	}
	if err := r.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	err = r.source.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != "workspaceSettings" {
			return nil
		}
		value, err := r.owner.projectNode(ctx, node, "HIPAACompliance")
		if err == nil {
			r.Data.WorkspaceSettings = value.Interface().(domain.WorkspaceSettings)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	users, reviews, issues := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, delivery := range r.due.Deliveries {
		if !users[delivery.RecipientID] && delivery.RecipientID != "" {
			users[delivery.RecipientID] = true
			header, found, err := r.header(ctx, "users", delivery.RecipientID, "ID", "Email")
			if err != nil {
				return nil, err
			}
			if found {
				r.Data.Users = append(r.Data.Users, header.Interface().(domain.User))
			}
		}
		fields := []string{"id", "type", "issueId", "reviewId", "title", "text", "actor.id", "actor.name", "actor.displayName", "actor.email", "actor.avatarUrl", "actor.app", "actor.builtinAgent"}
		values, queryErr := s.deliveryDisplayColumns(ctx, r.owner, "workspace_content_records", "workspace_key=? AND kind='notification' AND id=?", []any{workspace, delivery.NotificationID}, fields)
		// The original full snapshot ignores missing/query-failed notification
		// lookups and lets the email dispatcher report unavailable sources.
		if queryErr != nil {
			if errors.Is(queryErr, ErrMetadataWorkBudget) || ctx.Err() != nil {
				return nil, queryErr
			}
			continue
		}
		notification := domain.Notification{ID: values[0], Type: values[1], IssueID: values[2], ReviewID: values[3], Title: values[4], Text: values[5],
			Actor: domain.User{ID: values[6], Name: values[7], DisplayName: values[8], Email: values[9], AvatarURL: values[10], App: values[11] == "true" || values[11] == "1", BuiltinAgent: values[12] == "true" || values[12] == "1"}}
		r.Data.Notifications = append(r.Data.Notifications, notification)
		if notification.ReviewID != "" && !reviews[notification.ReviewID] {
			reviews[notification.ReviewID] = true
			header, found, err := r.header(ctx, "reviews", notification.ReviewID, "ID", "SlugID", "Title")
			if err != nil {
				return nil, err
			}
			if found {
				r.Data.Reviews = append(r.Data.Reviews, header.Interface().(domain.CodeReview))
			}
		}
		if notification.IssueID != "" && !issues[notification.IssueID] {
			issues[notification.IssueID] = true
			fields := []string{"id", "identifier", "title"}
			values, queryErr := s.deliveryDisplayColumns(ctx, r.owner, "issue_records", "workspace_key=? AND id=?", []any{workspace, notification.IssueID}, fields)
			if queryErr == nil {
				r.Data.Issues = append(r.Data.Issues, domain.Issue{ID: values[0], Identifier: values[1], Title: values[2]})
			} else if errors.Is(queryErr, ErrMetadataWorkBudget) || ctx.Err() != nil {
				return nil, queryErr
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *NotificationDeliveryContextRead) header(ctx context.Context, kind, ref string, fields ...string) (reflect.Value, bool, error) {
	var result reflect.Value
	err := r.source.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != kind {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			matched := false
			err := node.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
				if key.String() != "ID" && (kind != "reviews" || key.String() != "SlugID") {
					return nil
				}
				equal, err := value.StringEqual(ctx, ref)
				matched = matched || equal
				return err
			})
			if err != nil || !matched {
				return err
			}
			result, err = r.owner.projectNode(ctx, node, fields...)
			if err != nil {
				return err
			}
			return ErrMetadataIterationDone
		})
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return result, result.IsValid(), err
}

func (s *SQLiteStore) deliveryDisplayColumns(ctx context.Context, owner *ResourceRead, table, where string, args []any, fields []string) ([]string, error) {
	var reader metadataRowReader
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && snapshot.reader != nil {
		reader = snapshot.reader
	} else {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		reader = tx
	}
	columns := make([]string, len(fields))
	for index, field := range fields {
		columns[index] = "COALESCE(" + s.jsonText("data", field) + ",'')"
	}
	var size int64
	if err := reader.QueryRowContext(ctx, "SELECT "+s.scaleP21SW22VisibilityLength(columns...)+" FROM "+table+" WHERE "+where, args...).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > (metadataWorkBytes-8192)/32 {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(size)*32+8192); err != nil {
		return nil, err
	}
	result, scans := make([]string, len(fields)), make([]any, len(fields))
	for index := range result {
		scans[index] = &result[index]
	}
	err := reader.QueryRowContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+table+" WHERE "+where, args...).Scan(scans...)
	return result, err
}

// DueIntegrationDeliveryIDs preserves installed collection order while
// transferring only the scalar headers consumed by the scheduler tick.
func (s *SQLiteStore) DueIntegrationDeliveryIDs(ctx context.Context, workspace string, now time.Time, visit func(string) error) error {
	if !s.BoundedMetadataEnabled() {
		data, ok := s.WorkspaceMetadataFields(workspace, "integrationDeliveries")
		if !ok {
			return nil
		}
		for _, item := range data.IntegrationDeliveries {
			if item.Attempts >= 8 || item.Status != "pending" && !(item.Status == "failed" && item.NextAttemptAt != nil && !item.NextAttemptAt.After(now)) {
				continue
			}
			if err := visit(item.ID); err != nil {
				return err
			}
		}
		return nil
	}
	read, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return err
	}
	defer read.Close()
	return read.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "integrationDeliveries" {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			scratch := &ResourceRead{store: s}
			defer scratch.Close()
			value, err := scratch.projectNode(ctx, node, "ID", "Status", "Attempts", "NextAttemptAt")
			if err != nil {
				return err
			}
			item := value.Interface().(domain.IntegrationDelivery)
			if item.Attempts >= 8 || item.Status != "pending" && !(item.Status == "failed" && item.NextAttemptAt != nil && !item.NextAttemptAt.After(now)) {
				return nil
			}
			return visit(item.ID)
		})
	})
}
