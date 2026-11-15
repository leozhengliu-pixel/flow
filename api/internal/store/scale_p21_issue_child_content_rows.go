package store

import (
	"context"
	"encoding/json"
	"reflect"

	"flow/api/internal/domain"
)

// issueChildContentRows owns one issue's complete selected content. The caller
// owns the transaction and must keep this owner through callback result use.
type issueChildContentRows struct {
	rows               *issueMutationRows
	results            []*domain.Bootstrap
	commentSlices      [][]domain.Comment
	activitySlices     [][]domain.ActivityEvent
	notificationMaps   []map[string][]domain.Notification
	notificationSlices [][]domain.Notification
	closed             bool
}

func (s *SQLiteStore) newIssueChildContentRows(tx *sqlTx, workspace string) *issueChildContentRows {
	return &issueChildContentRows{rows: s.newIssueMutationRows(tx, workspace)}
}

func (r *issueChildContentRows) Close() {
	if r == nil || r.rows == nil {
		return
	}
	r.rows.mu.Lock()
	if !r.closed {
		r.closed = true
		for _, result := range r.results {
			for _, comments := range result.Comments {
				clear(comments)
			}
			clear(result.Comments)
			for _, activities := range result.Activities {
				clear(activities)
			}
			clear(result.Activities)
			clear(result.Notifications)
			clear(result.NotificationDeliveries)
			*result = domain.Bootstrap{}
		}
		for _, comments := range r.commentSlices {
			clear(comments)
		}
		for _, activities := range r.activitySlices {
			clear(activities)
		}
		for _, notifications := range r.notificationMaps {
			for _, items := range notifications {
				clear(items)
			}
			clear(notifications)
		}
		for _, notifications := range r.notificationSlices {
			clear(notifications)
		}
		r.results, r.commentSlices, r.activitySlices = nil, nil, nil
		r.notificationMaps, r.notificationSlices = nil, nil
	}
	r.rows.mu.Unlock()
	r.rows.Close()
}

func (r *issueChildContentRows) load(ctx context.Context, issueID string) (domain.Bootstrap, error) {
	if r == nil || r.rows == nil {
		return domain.Bootstrap{}, errMetadataLogicalClosed
	}
	r.rows.mu.Lock()
	locked := true
	defer func() {
		if locked {
			r.rows.mu.Unlock()
		}
	}()
	if r.closed || r.rows.owner == nil {
		return domain.Bootstrap{}, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return domain.Bootstrap{}, err
	}
	if r.rows.owner.store == nil || r.rows.tx == nil || r.rows.tx.database != r.rows.owner.store.db {
		return domain.Bootstrap{}, ErrMetadataRecordQuery
	}
	if err := r.rows.beginDiscussion(ctx, r.rows.workspace, issueID); err != nil {
		return domain.Bootstrap{}, err
	}
	if err := r.rows.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Bootstrap]().Size())*2+4096); err != nil {
		return domain.Bootstrap{}, err
	}
	result := &domain.Bootstrap{Notifications: []domain.Notification{}}
	r.results = append(r.results, result)
	var err error
	result.Comments, err = issueChildSelectedContent[domain.Comment](ctx, r.rows, "comment", issueID)
	for _, comments := range result.Comments {
		r.commentSlices = append(r.commentSlices, comments)
	}
	if err != nil {
		return *result, err
	}
	result.Activities, err = issueChildSelectedContent[domain.ActivityEvent](ctx, r.rows, "activity", issueID)
	for _, activities := range result.Activities {
		r.activitySlices = append(r.activitySlices, activities)
	}
	if err != nil {
		return *result, err
	}
	notifications, err := issueChildSelectedContent[domain.Notification](ctx, r.rows, "notification", issueID)
	r.notificationMaps = append(r.notificationMaps, notifications)
	for _, items := range notifications {
		r.notificationSlices = append(r.notificationSlices, items)
	}
	if err != nil {
		return *result, err
	}
	// Preserve scopedContent's physical map keys and the original flattening
	// operation; never regroup on a decoded JSON issue/resource identifier.
	for _, items := range notifications {
		if err := r.rows.owner.reserve(ctx, uint64(len(items))*uint64(reflect.TypeFor[domain.Notification]().Size())*2+4096); err != nil {
			return *result, err
		}
		result.Notifications = append(result.Notifications, items...)
	}
	selected := result.Notifications
	r.rows.mu.Unlock()
	locked = false
	deliveries, err := r.rows.issueNotificationDeliveries(ctx, selected)
	r.rows.mu.Lock()
	locked = true
	if r.closed {
		return domain.Bootstrap{}, errMetadataLogicalClosed
	}
	result.NotificationDeliveries = deliveries
	return *result, err
}

// Keep the proven guarded physical headers/payload batches, while retaining
// resource_id alongside each typed value as scopedContent does. The generic
// discussionSelectedRows visitor deliberately exposes only decoded values.
// The caller holds rows.mu and has initialized its discussion owner.
func issueChildSelectedContent[T any](ctx context.Context, rows *issueMutationRows, kind, issueID string) (map[string][]T, error) {
	result := map[string][]T{}
	headers, err := rows.discussionHeaders(ctx,
		"workspace_key=? AND kind=? AND resource_id=?", []any{rows.workspace, kind, issueID},
		" ORDER BY created_at,id", 0)
	if err != nil {
		return result, err
	}
	for start := 0; start < len(headers); {
		end, size := start+1, headers[start].size
		for end < len(headers) && end-start < issueMutationRowsBatchSize && size <= issueMutationRowsBatchBytes && headers[end].size <= issueMutationRowsBatchBytes-size {
			size += headers[end].size
			end++
		}
		index := start
		err := rows.discussionPayloads(ctx, kind, headers[start:end], size, func(raw []byte) error {
			work, err := metadataAggregatePayloadJSONWork(ctx, raw)
			if err != nil {
				return err
			}
			fixed := uint64(reflect.TypeFor[T]().Size())*4 + 4096
			if fixed > metadataWorkBytes || work > metadataWorkBytes-fixed {
				return ErrMetadataWorkBudget
			}
			if err := rows.owner.reserve(ctx, work+fixed); err != nil {
				return err
			}
			var value T
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			resource := headers[index].resource
			result[resource] = append(result[resource], value)
			index++
			return nil
		})
		if err != nil {
			return result, err
		}
		start = end
	}
	return result, ctx.Err()
}
