package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// issueSuggestionHistoryChunk is the number of scoped issues whose history
// one chunk loads, refreshes for display and writes back.
const issueSuggestionHistoryChunk = 32

// issueSuggestionHistoryHeads serves the scoped issues' history to a
// HistoryHeads callback and writes it back after it. The callback reads and
// edits history only through RecordIssueActivity, which loads an issue's
// latest activity and comment through data.ActivityHistoryLoader: here the
// display values the full writer's callback would see, selected by the same
// latest-entry rule, so its merge decisions are unchanged. After the callback
// every scoped issue's comments, activities and notifications are synced one
// chunk at a time from the same display values, with the callback's timeline
// edits applied to the complete history (see scoped_mutation.go: records of
// resources outside every chunk are written with the first one).
type issueSuggestionHistoryHeads struct {
	ctx       context.Context
	store     *SQLiteStore
	tx        *sqlTx
	workspace string
	image     *metadataLogicalSource
	chunks    [][]string
	chunkOf   map[string]int
	loaded    map[int]bool
	heads     map[string]issueSuggestionHistoryHead
	owner     *ResourceRead
}

type issueSuggestionHistoryHead struct {
	activity *domain.ActivityEvent
	comment  *domain.Comment
}

func (s *SQLiteStore) newIssueSuggestionHistoryHeads(ctx context.Context, tx *sqlTx, workspace string, image *metadataLogicalSource, keys []string) *issueSuggestionHistoryHeads {
	h := &issueSuggestionHistoryHeads{ctx: ctx, store: s, tx: tx, workspace: workspace, image: image,
		chunkOf: map[string]int{}, loaded: map[int]bool{}, heads: map[string]issueSuggestionHistoryHead{}, owner: &ResourceRead{store: s}}
	for start := 0; start < len(keys); start += issueSuggestionHistoryChunk {
		chunk := keys[start:min(start+issueSuggestionHistoryChunk, len(keys))]
		for _, key := range chunk {
			h.chunkOf[key] = len(h.chunks)
		}
		h.chunks = append(h.chunks, chunk)
	}
	return h
}

func (h *issueSuggestionHistoryHeads) close() {
	if h != nil {
		h.owner.Close()
		h.heads = nil
	}
}

// latestEntry is RecordIssueActivity's choice of the latest entry: the last
// of the entries with the greatest CreatedAt.
func latestEntry[T any](items []T, before func(T, T) bool) int {
	latest := -1
	for index := range items {
		if latest < 0 || !before(items[index], items[latest]) {
			latest = index
		}
	}
	return latest
}

func latestActivity(items []domain.ActivityEvent) int {
	return latestEntry(items, func(a, b domain.ActivityEvent) bool { return a.CreatedAt.Before(b.CreatedAt) })
}

func latestComment(items []domain.Comment) int {
	return latestEntry(items, func(a, b domain.Comment) bool { return a.CreatedAt.Before(b.CreatedAt) })
}

// chunkHistory reads one chunk's comments, activities, notifications and
// deliveries as the full writer reads the scope (issueChildContentRows) and
// decodes their display values, refreshed over image, on owner. It returns
// the stored delivery IDs too.
func (h *issueSuggestionHistoryHeads) chunkHistory(ctx context.Context, owner *ResourceRead, index int) (domain.Bootstrap, []string, error) {
	keys := h.chunks[index]
	content := h.store.newIssueChildContentRows(h.tx, h.workspace)
	defer content.Close()
	loaded, err := content.loadMutationScope(ctx, keys)
	if err != nil {
		return domain.Bootstrap{}, nil, err
	}
	// notificationRecords groups by the issue a notification names; a
	// stored row of another chunk's issue would be synced by that chunk.
	for _, notifications := range content.notificationMaps {
		for physical, items := range notifications {
			for _, item := range items {
				resource := item.IssueID
				if resource == "" {
					resource = item.SourceID
				}
				if other, scoped := h.chunkOf[resource]; resource != physical && scoped && other != index {
					return domain.Bootstrap{}, nil, fmt.Errorf("%w: notification %s is stored under another issue", ErrBoundedMetadataCoverage, item.ID)
				}
			}
		}
	}
	deliveries := make([]string, 0, len(loaded.NotificationDeliveries))
	for _, delivery := range loaded.NotificationDeliveries {
		deliveries = append(deliveries, delivery.ID)
	}
	if err := owner.reserve(ctx, uint64(len(deliveries))*64+4096); err != nil {
		return domain.Bootstrap{}, nil, err
	}
	selected, err := boundedDiscussionEventSource(ctx, h.image, domain.Bootstrap{Comments: loaded.Comments, Activities: loaded.Activities,
		Notifications: loaded.Notifications, NotificationDeliveries: loaded.NotificationDeliveries})
	if err != nil {
		return domain.Bootstrap{}, nil, err
	}
	defer selected.Release()
	// The selected image holds encoded copies; the SQL values end here.
	loaded = domain.Bootstrap{}
	content.Close()
	display, err := h.store.refreshLegacyDisplayImage(ctx, selected)
	if err != nil {
		return domain.Bootstrap{}, nil, err
	}
	defer display.Release()
	decoder := &boundedDiscussionAttempt{owner: owner}
	var result domain.Bootstrap
	for _, name := range []string{"comments", "activities", "notifications", "notificationDeliveries"} {
		field, _, _ := metadataLogicalField(name)
		value, err := decoder.collectionValue(ctx, display.fields[field])
		if err != nil {
			return domain.Bootstrap{}, nil, err
		}
		reflect.ValueOf(&result).Elem().Field(field).Set(value)
	}
	return result, deliveries, ctx.Err()
}

// load is the callback's ActivityHistoryLoader. It loads the heads of the
// issue's whole chunk once and returns private copies.
func (h *issueSuggestionHistoryHeads) load(issueID string) (domain.ActivityHistorySnapshot, error) {
	ctx := h.ctx
	index, scoped := h.chunkOf[issueID]
	if !scoped {
		// The full writer loads no history for an issue outside its records.
		return domain.ActivityHistorySnapshot{}, ctx.Err()
	}
	if !h.loaded[index] {
		if err := func() error {
			work := &ResourceRead{store: h.store}
			defer work.Close()
			history, _, err := h.chunkHistory(ctx, work, index)
			if err != nil {
				return err
			}
			for _, key := range h.chunks[index] {
				var head issueSuggestionHistoryHead
				if activities := history.Activities[key]; len(activities) > 0 {
					value := cloneSnapshotValue(reflect.ValueOf(activities[latestActivity(activities)])).Interface().(domain.ActivityEvent)
					head.activity = &value
				}
				if comments := history.Comments[key]; len(comments) > 0 {
					value := cloneSnapshotValue(reflect.ValueOf(comments[latestComment(comments)])).Interface().(domain.Comment)
					head.comment = &value
				}
				cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(head), 0)
				if err != nil {
					return err
				}
				if err := h.owner.reserve(ctx, cost*2+512); err != nil {
					return err
				}
				h.heads[key] = head
			}
			h.loaded[index] = true
			return nil
		}(); err != nil {
			return domain.ActivityHistorySnapshot{}, err
		}
	}
	// RecordIssueActivity keeps and edits what it receives; the heads stay
	// the unedited preimages (the copies are admitted with them above).
	var snapshot domain.ActivityHistorySnapshot
	head := h.heads[issueID]
	if head.activity != nil {
		value := cloneSnapshotValue(reflect.ValueOf(*head.activity)).Interface().(domain.ActivityEvent)
		snapshot.Activity = &value
	}
	if head.comment != nil {
		value := cloneSnapshotValue(reflect.ValueOf(*head.comment)).Interface().(domain.Comment)
		snapshot.Comment = &value
	}
	return snapshot, ctx.Err()
}

// check admits the full writer's history effects on the heads the callback
// saw: comments and notifications unchanged (only the loaded latest comment
// may be present), and per issue its loaded head with entries appended, or
// with that head edited in place or removed when move allows a merge. It
// returns the callback's timelines by issue.
func (h *issueSuggestionHistoryHeads) check(data domain.Bootstrap, scope map[string]bool, move bool) (map[string][]domain.ActivityEvent, error) {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: suggestion callback %s", ErrIssueSuggestionWriteRefused, reason)
	}
	if len(data.Notifications) != 0 || len(data.NotificationDeliveries) != 0 {
		return nil, refuse("changed comments or notifications")
	}
	for key, recorded := range data.ActivityHistoryHeads {
		head, loaded := h.heads[key]
		var want domain.ActivityEvent
		if head.activity != nil {
			want = *head.activity
		}
		if !loaded || !reflect.DeepEqual(recorded, want) {
			return nil, refuse("rewrote activity history")
		}
		if _, present := data.Activities[key]; head.activity != nil && !present {
			return nil, refuse("removed activity history")
		}
	}
	for key, items := range data.Comments {
		_, recorded := data.ActivityHistoryHeads[key]
		head := h.heads[key]
		if !recorded || head.comment == nil || !reflect.DeepEqual(items, []domain.Comment{*head.comment}) {
			return nil, refuse("changed comments or notifications")
		}
	}
	timeline := make(map[string][]domain.ActivityEvent, len(data.Activities))
	for key, items := range data.Activities {
		var previous []domain.ActivityEvent
		if _, recorded := data.ActivityHistoryHeads[key]; recorded && h.heads[key].activity != nil {
			previous = []domain.ActivityEvent{*h.heads[key].activity}
		}
		if !scope[key] || !appendedActivityHistory(previous, items, move) {
			return nil, refuse("rewrote activity history")
		}
		if _, recorded := data.ActivityHistoryHeads[key]; recorded || len(items) > 0 {
			timeline[key] = items
		}
	}
	return timeline, nil
}

// apply returns the complete history after the callback's timeline edit:
// the edited or removed loaded head in place, then the appended entries.
func (h *issueSuggestionHistoryHeads) apply(key string, recorded bool, stored, items []domain.ActivityEvent) ([]domain.ActivityEvent, error) {
	head := h.heads[key].activity
	if !recorded || head == nil {
		return slices.Concat(stored, items), nil
	}
	latest := latestActivity(stored)
	if latest < 0 || !reflect.DeepEqual(stored[latest], *head) {
		return nil, fmt.Errorf("%w: issue history changed during the write", ErrBoundedMetadataCoverage)
	}
	result := slices.Clone(stored)
	if len(items) > 0 && items[0].ID == head.ID {
		result[latest] = items[0]
		return append(result, items[1:]...), nil
	}
	return append(slices.Delete(result, latest, latest+1), items...), nil
}

// sync writes every scoped issue's history back chunk by chunk as the full
// writer's sync writes the scope: comments, activities (with timeline),
// notifications, then the notifications' deliveries. aggregate must not name
// a history record, which the event source here does not carry.
func (h *issueSuggestionHistoryHeads) sync(ctx context.Context, data domain.Bootstrap, recorded map[string]domain.ActivityEvent, timeline map[string][]domain.ActivityEvent, aggregate string) error {
	outside := map[string][]domain.ActivityEvent{}
	for key, items := range timeline {
		if _, scoped := h.chunkOf[key]; !scoped {
			outside[key] = items
		}
	}
	for index, keys := range h.chunks {
		if err := func() error {
			work := &ResourceRead{store: h.store}
			defer work.Close()
			history, stored, err := h.chunkHistory(ctx, work, index)
			if err != nil {
				return err
			}
			if history.Activities == nil {
				history.Activities = map[string][]domain.ActivityEvent{}
			}
			for _, key := range keys {
				items, touched := timeline[key]
				if !touched {
					continue
				}
				_, loaded := recorded[key]
				next, err := h.apply(key, loaded, history.Activities[key], items)
				if err != nil {
					return err
				}
				cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(next), 0)
				if err != nil {
					return err
				}
				if err := work.reserve(ctx, cost*2+512); err != nil {
					return err
				}
				history.Activities[key] = next
			}
			if index == 0 {
				for key, items := range outside {
					history.Activities[key] = items
				}
			}
			for _, field := range []any{history.Comments, history.Activities, history.Notifications, history.NotificationDeliveries} {
				found, err := metadataAggregateFind(ctx, metadataAggregateNativeNode{reflect.ValueOf(field)}, aggregate)
				if err != nil {
					return err
				}
				if found != nil {
					return fmt.Errorf("%w: event aggregate %s names a history record", ErrBoundedMetadataCoverage, aggregate)
				}
			}
			release, err := reserveIssueBatchContentEncoding(ctx, h.store, history)
			if err != nil {
				return err
			}
			defer release()
			if err := syncContentRecords(ctx, h.tx, h.workspace, "comment", history.Comments, data, keys); err != nil {
				return err
			}
			if err := syncContentRecords(ctx, h.tx, h.workspace, "activity", history.Activities, data, keys); err != nil {
				return err
			}
			if err := syncContentRecords(ctx, h.tx, h.workspace, "notification", notificationRecords(history.Notifications), data, keys); err != nil {
				return err
			}
			written := make(map[string]bool, len(history.NotificationDeliveries))
			for _, delivery := range history.NotificationDeliveries {
				written[delivery.ID] = true
				if err := writeContentRecord(ctx, h.tx, h.workspace, "delivery", "", delivery); err != nil {
					return err
				}
			}
			for _, id := range stored {
				if written[id] {
					continue
				}
				if _, err := h.tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, h.workspace, id); err != nil {
					return err
				}
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	if len(h.chunks) == 0 && len(outside) != 0 {
		// The full writer writes the timelines of unscoped issues too.
		if err := syncContentRecords(ctx, h.tx, h.workspace, "activity", outside, data, []string{}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
