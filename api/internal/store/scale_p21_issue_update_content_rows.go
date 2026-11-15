package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// issueUpdateContentRows owns UpdateIssueRecord's selected content preimages.
// Its private row owner borrows the caller's transaction; Close never ends it.
type issueUpdateContentRows struct {
	rows          *issueMutationRows
	notifications []*[]domain.Notification
	history       []*domain.ActivityHistorySnapshot
	closed        bool
}

func (s *SQLiteStore) newIssueUpdateContentRows(tx *sqlTx, workspace string) *issueUpdateContentRows {
	return &issueUpdateContentRows{rows: s.newIssueMutationRows(tx, workspace)}
}

func (r *issueUpdateContentRows) Close() {
	if r == nil || r.rows == nil {
		return
	}
	// Reuse the row owner's serialization. Mark closed before unlocking so
	// another load cannot enter between clearing outputs and closing rows.
	r.rows.mu.Lock()
	if !r.closed {
		r.closed = true
		for _, result := range r.notifications {
			clear(*result)
			*result = nil
		}
		for _, snapshot := range r.history {
			if snapshot.Activity != nil {
				*snapshot.Activity = domain.ActivityEvent{}
			}
			if snapshot.Comment != nil {
				*snapshot.Comment = domain.Comment{}
			}
			*snapshot = domain.ActivityHistorySnapshot{}
		}
		r.notifications, r.history = nil, nil
	}
	r.rows.mu.Unlock()
	r.rows.Close()
}

// begin runs with rows.mu held. Every call is a fresh SQL selection: lazy
// history lookup caching remains the enclosing activity callback's contract.
func (r *issueUpdateContentRows) begin(ctx context.Context, issueID string) error {
	if r.closed || r.rows.owner == nil {
		return errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.rows.owner.store == nil || r.rows.tx == nil || r.rows.tx.database != r.rows.owner.store.db {
		return ErrMetadataRecordQuery
	}
	return r.rows.beginDiscussion(ctx, r.rows.workspace, issueID)
}

// loadNotifications returns a standalone transport slice. Append its elements
// into metadata.Notifications; replacing that field would impose this slice's
// empty shape instead of preserving the enclosing callback's existing shape.
func (r *issueUpdateContentRows) loadNotifications(ctx context.Context, issueID string) ([]domain.Notification, error) {
	if r == nil || r.rows == nil {
		return nil, errMetadataLogicalClosed
	}
	r.rows.mu.Lock()
	defer r.rows.mu.Unlock()
	if err := r.begin(ctx, issueID); err != nil {
		return nil, err
	}
	result := []domain.Notification{}
	r.notifications = append(r.notifications, &result)
	err := discussionSelectedRows(ctx, r.rows, "notification",
		"workspace_key=? AND kind='notification' AND resource_id=?",
		[]any{r.rows.workspace, issueID}, " ORDER BY created_at DESC", 1000,
		func(notification domain.Notification) error {
			result = append(result, notification)
			return nil
		})
	return result, err
}

func (r *issueUpdateContentRows) loadHistoryHead(ctx context.Context, issueID string) (domain.ActivityHistorySnapshot, error) {
	if r == nil || r.rows == nil {
		return domain.ActivityHistorySnapshot{}, errMetadataLogicalClosed
	}
	r.rows.mu.Lock()
	defer r.rows.mu.Unlock()
	if err := r.begin(ctx, issueID); err != nil {
		return domain.ActivityHistorySnapshot{}, err
	}
	if err := r.rows.owner.reserve(ctx, uint64(reflect.TypeFor[domain.ActivityHistorySnapshot]().Size())*4+1024); err != nil {
		return domain.ActivityHistorySnapshot{}, err
	}
	snapshot := &domain.ActivityHistorySnapshot{}
	r.history = append(r.history, snapshot)
	err := discussionSelectedRows(ctx, r.rows, "activity",
		"workspace_key=? AND kind='activity' AND resource_id=?",
		[]any{r.rows.workspace, issueID}, " ORDER BY created_at DESC,id DESC", 1,
		func(event domain.ActivityEvent) error {
			snapshot.Activity = &event
			return nil
		})
	if err != nil {
		return *snapshot, err
	}
	err = discussionSelectedRows(ctx, r.rows, "comment",
		"workspace_key=? AND kind='comment' AND resource_id=?",
		[]any{r.rows.workspace, issueID}, " ORDER BY created_at DESC,id DESC", 1,
		func(comment domain.Comment) error {
			snapshot.Comment = &comment
			return nil
		})
	return *snapshot, err
}
