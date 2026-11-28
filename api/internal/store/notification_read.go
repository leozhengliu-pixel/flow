package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const dueDeliveryWork = 64 << 20

// DueNotificationDeliveryRead owns the due rows through all scheduling updates.
// Deliveries and its fields must not survive Close without another charged owner.
type DueNotificationDeliveryRead struct {
	Deliveries []domain.NotificationDelivery
	release    func()
	once       sync.Once
}

func (r *DueNotificationDeliveryRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Deliveries = nil
			if r.release != nil {
				r.release()
			}
		})
	}
}

// BeginDueNotificationDeliveryRead preserves NotificationDeliveries selection,
// order and installed workspace identity without cloning rendering metadata.
func (s *SQLiteStore) BeginDueNotificationDeliveryRead(ctx context.Context, workspace string, selection NotificationDeliverySelection, now time.Time) (_ *DueNotificationDeliveryRead, err error) {
	if !s.BoundedMetadataEnabled() {
		data, err := s.NotificationDeliveries(ctx, workspace, selection, now)
		if err != nil {
			return nil, err
		}
		return &DueNotificationDeliveryRead{Deliveries: data.NotificationDeliveries}, nil
	}
	// The legacy source lookup precedes cancellation and SQL errors.
	s.mu.RLock()
	selected := workspace
	if selected == "" {
		selected = s.lastWorkspaceKey
	}
	_, found := s.workspaces[selected]
	selected = ""
	s.mu.RUnlock()
	if !found {
		return nil, fmt.Errorf("workspace not found")
	}
	work, release, err := s.tryAdmitAdditionalMetadataWork(ctx, dueDeliveryWork)
	if err != nil {
		return nil, err
	}
	read := &DueNotificationDeliveryRead{release: release}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	used := 128 << 10
	charge := func(size int) error {
		if size < 0 || size > (dueDeliveryWork-used)/33 {
			return ErrMetadataWorkBudget
		}
		used += 33 * size
		return nil
	}
	s.mu.RLock()
	selected = workspace
	if selected == "" {
		selected = s.lastWorkspaceKey
	}
	source, found := s.workspaces[selected]
	if found {
		err = charge(len(source.Workspace.URLKey))
		if err == nil {
			workspace = strings.Clone(source.Workspace.URLKey)
		}
	}
	source = domain.Bootstrap{}
	selected = ""
	s.mu.RUnlock()
	if !found {
		return nil, fmt.Errorf("workspace not found")
	}
	if err != nil {
		return nil, err
	}
	if err := charge(len(selection.Channel) + 128); err != nil {
		return nil, err
	}
	for _, status := range selection.Statuses {
		if err := charge(len(status) + 128); err != nil {
			return nil, err
		}
	}
	query, args := s.dueNotificationDeliveryQuery(workspace, selection, now)
	length := "OCTET_LENGTH(data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(data AS BLOB))"
	}
	rows, err := s.db.QueryContext(work, `SELECT `+length+`,CASE WHEN `+length+`<=? THEN data ELSE NULL END`+query, append([]any{MetadataRecordPageMaxBytes}, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var size int
		var raw []byte
		if err := rows.Scan(&size, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		// NotificationDelivery is flat: string/scalar fields and
		// three time pointers. The fixed row charge covers structs, pointer
		// storage and slice growth; the multiplier covers owned string data.
		if size < 0 || size > MetadataRecordPageMaxBytes || len(raw) != size {
			rows.Close()
			return nil, ErrMetadataWorkBudget
		}
		if err := charge(size + 128); err != nil {
			rows.Close()
			return nil, err
		}
		var delivery domain.NotificationDelivery
		if err := json.Unmarshal(raw, &delivery); err != nil {
			rows.Close()
			return nil, err
		}
		read.Deliveries = append(read.Deliveries, delivery)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return read, nil
}
