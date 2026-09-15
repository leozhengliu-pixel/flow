package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Releases and release pipelines do not mirror favorite state onto their own
// records. Keep their preference writes scoped to the favorites collection so
// large workspaces do not scan every metadata row for a one-record change.
func standaloneFavoriteMutation(event string, payload any) bool {
	if event != "favorite.added" && event != "favorite.removed" {
		return false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	var fields struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	return fields.Type == "release" || fields.Type == "release_pipeline"
}

func readFavoriteRecords(ctx context.Context, tx *sqlTx, workspace, lock string) ([]domain.Favorite, map[string]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites'`+lock, workspace)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []domain.Favorite{}
	orders := map[string]int{}
	for rows.Next() {
		var id string
		var order int
		var raw []byte
		if err := rows.Scan(&id, &order, &raw); err != nil {
			return nil, nil, err
		}
		var item domain.Favorite
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, nil, err
		}
		items = append(items, item)
		orders[id] = order
	}
	return items, orders, rows.Err()
}

func favoriteByResource(items []domain.Favorite, userID, kind, resourceID string) (domain.Favorite, bool) {
	index := slices.IndexFunc(items, func(item domain.Favorite) bool {
		return item.UserID == userID && item.ResourceType == kind && item.ResourceID == resourceID
	})
	if index < 0 {
		return domain.Favorite{}, false
	}
	return items[index], true
}

func (s *SQLiteStore) mutateStandaloneFavorite(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
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
		favorites, orders, err := readFavoriteRecords(ctx, tx, workspace, lock)
		if err != nil {
			return err
		}
		beforeFavorites := slices.Clone(favorites)
		next := current
		next.Favorites = favorites
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
		}
		var fields struct {
			Type string `json:"type"`
		}
		raw, _ := json.Marshal(payload)
		_ = json.Unmarshal(raw, &fields)
		resourceID, err := mutate(&next)
		if err != nil {
			return err
		}
		before, existed := favoriteByResource(beforeFavorites, next.Viewer.ID, fields.Type, resourceID)
		after, exists := favoriteByResource(next.Favorites, next.Viewer.ID, fields.Type, resourceID)
		if exists && (!existed || !reflect.DeepEqual(before, after)) {
			order, known := orders[after.ID]
			if !known {
				order = -int(after.CreatedAt.UnixMicro())
			}
			itemRaw, err := json.Marshal(after)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'favorites',?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, after.ID, order, itemRaw); err != nil {
				return err
			}
			if !existed {
				if err := ensureOAuthMetadataShape(ctx, tx, workspace, "favorites", "array", lock); err != nil {
					return err
				}
			}
		}
		if existed && !exists {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND record_key=?`, workspace, before.ID); err != nil {
				return err
			}
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: eventType, AggregateID: resourceID, Payload: payloadRaw, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(nil), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		realtimePayload = enrichRealtimePayload(payloadRaw, aggregateJSONValue(next, resourceID), eventType)
		if err := tx.Commit(); err != nil {
			return err
		}
		current.Favorites = slices.Clone(next.Favorites)
		s.workspaces[workspace] = current
		s.lastWorkspaceKey = workspace
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
		if len(realtimePayload) == 0 {
			realtimePayload = event.Payload
		}
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: realtimePayload, CreatedAt: event.CreatedAt})
	}
	return nil
}
