package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// MutateLabelDeletion applies the existing label cascade to bounded projections.
// In particular, deleting an unused project label never reads issue bodies,
// discussion records, or serializes the workspace snapshot.
func (s *SQLiteStore) MutateLabelDeletion(ctx context.Context, workspace, eventType, id string, group bool, mutate func(*domain.Bootstrap) error) error {
	var event domain.DomainEvent
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		current, ok := s.workspaces[workspace]
		if !ok {
			return sql.ErrNoRows
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		text := "CAST(data AS TEXT)"
		if s.dialect == "mysql" {
			text = "CAST(data AS CHAR)"
		}
		if s.dialect == "postgres" {
			text = "convert_from(data,'UTF8')"
		}
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		field := "labels"
		if group {
			field = "labelGroups"
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`+lock, workspace, field, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				if validationErr := mutate(&domain.Bootstrap{}); validationErr != nil {
					return validationErr
				}
			}
			return err
		}
		base := domain.Bootstrap{}
		if err := appendLabelProjection(&base, field, raw); err != nil {
			return err
		}
		if group {
			rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND `+text+` LIKE ? ESCAPE '!'`+lock, workspace, labelReferencePattern(id))
			if err != nil {
				return err
			}
			for rows.Next() {
				var value []byte
				if err := rows.Scan(&value); err != nil {
					rows.Close()
					return err
				}
				var label domain.IssueLabel
				if err := json.Unmarshal(value, &label); err != nil {
					rows.Close()
					return err
				}
				if label.GroupID == id {
					base.Labels = append(base.Labels, label)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		validate := cloneBootstrap(base)
		if err := mutate(&validate); err != nil {
			return err
		}
		ids := make([]string, 0, len(base.Labels))
		issueLabels := false
		for _, label := range base.Labels {
			ids = append(ids, label.ID)
			issueLabels = issueLabels || label.ResourceType != "project"
		}
		changes := map[string]map[string]json.RawMessage{}
		remember := func(field, key string, value json.RawMessage) {
			if changes[field] == nil {
				changes[field] = map[string]json.RawMessage{}
			}
			changes[field][key] = value
		}
		// Metadata remains row-backed. SQL rejects unrelated rows before decoding;
		// the existing cascade validates any literal match in free-form content.
		referenceIDs := append([]string{}, ids...)
		if group {
			referenceIDs = append(referenceIDs, id)
		}
		for _, labelID := range referenceIDs {
			lastField, lastID := "", ""
			for {
				rows, err := tx.QueryContext(ctx, `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('projects','initiatives','issueTemplates','projectTemplates','triageRoutingRules','savedViews','favorites','subscriptions') AND (field>? OR (field=? AND record_key>?)) AND `+text+` LIKE ? ESCAPE '!' ORDER BY field,record_key LIMIT 100`+lock, workspace, lastField, lastField, lastID, labelReferencePattern(labelID))
				if err != nil {
					return err
				}
				type record struct {
					field, id string
					raw       []byte
				}
				batch := []record{}
				for rows.Next() {
					var item record
					if err := rows.Scan(&item.field, &item.id, &item.raw); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, item)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if len(batch) == 0 {
					break
				}
				for _, item := range batch {
					lastField, lastID = item.field, item.id
					next := cloneBootstrap(base)
					if err := appendLabelProjection(&next, item.field, item.raw); err != nil {
						return err
					}
					if err := mutate(&next); err != nil {
						return err
					}
					updated, err := labelProjectionRecord(next, item.field)
					if err != nil {
						return err
					}
					if bytes.Equal(updated, item.raw) {
						continue
					}
					if len(updated) == 0 {
						_, err = tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, item.field, item.id)
					} else {
						_, err = tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, []byte(updated), workspace, item.field, item.id)
					}
					if err != nil {
						return err
					}
					remember(item.field, item.id, updated)
				}
			}
		}
		if issueLabels {
			for _, labelID := range ids {
				// Applied labels use the existing reverse index. Suggested labels
				// have no reverse index yet; inspect only SQL-matched list projections.
				for _, suggested := range []bool{false, true} {
					lastID := ""
					for {
						query := `SELECT i.id FROM issue_label_records l JOIN issue_records i ON i.workspace_key=l.workspace_key AND i.id=l.issue_id WHERE l.workspace_key=? AND l.label_id=? AND i.id>? ORDER BY i.id LIMIT 100`
						args := []any{workspace, labelID, lastID}
						if suggested {
							projection := strings.ReplaceAll(text, "data", "COALESCE(list_data,data)")
							query = `SELECT id FROM issue_records WHERE workspace_key=? AND id>? AND ` + projection + ` LIKE ? ESCAPE '!' AND ` + projection + ` LIKE '%"suggestedLabelIds"%' ORDER BY id LIMIT 100`
							args = []any{workspace, lastID, labelReferencePattern(labelID)}
						}
						rows, err := tx.QueryContext(ctx, query, args...)
						if err != nil {
							return err
						}
						batch := []string{}
						for rows.Next() {
							var key string
							if err := rows.Scan(&key); err != nil {
								rows.Close()
								return err
							}
							batch = append(batch, key)
						}
						err = rows.Err()
						rows.Close()
						if err != nil {
							return err
						}
						if len(batch) == 0 {
							break
						}
						for _, key := range batch {
							lastID = key
							var value []byte
							if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`+lock, workspace, key).Scan(&value); err != nil {
								return err
							}
							next := cloneBootstrap(base)
							if err := appendLabelProjection(&next, "issues", value); err != nil {
								return err
							}
							if err := mutate(&next); err != nil {
								return err
							}
							updated, err := json.Marshal(next.Issues[0])
							if err != nil {
								return err
							}
							if bytes.Equal(value, updated) {
								continue
							}
							next.Issues[0].Version++
							next.Issues[0].UpdatedAt = time.Now().UTC()
							if err := s.writeIssueRecord(ctx, tx, workspace, next.Issues[0]); err != nil {
								return err
							}
						}
					}
				}
			}
		}
		for _, labelID := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND record_key=?`, workspace, labelID); err != nil {
				return err
			}
			remember("labels", labelID, nil)
		}
		if group {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='labelGroups' AND record_key=?`, workspace, id); err != nil {
				return err
			}
			remember("labelGroups", id, nil)
		}
		now := time.Now().UTC()
		payload, err := json.Marshal(map[string]any{"labelIds": ids})
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: eventType, AggregateID: id, Payload: payload, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, id, []byte(payload), []byte(nil), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		// Construct replacement slices before commit without copying unrelated bodies.
		next, err := applyLabelMetadataChanges(current, changes)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.workspaces[workspace] = next
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
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: id, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}

func labelReferencePattern(id string) string {
	encoded, _ := json.Marshal(id)
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(string(encoded[1:len(encoded)-1])) + "%"
}

func labelProjectionField(data *domain.Bootstrap, field string) (reflect.Value, error) {
	root := reflect.ValueOf(data).Elem()
	for i := 0; i < root.NumField(); i++ {
		if strings.Split(root.Type().Field(i).Tag.Get("json"), ",")[0] == field && root.Field(i).Kind() == reflect.Slice {
			return root.Field(i), nil
		}
	}
	return reflect.Value{}, fmt.Errorf("unsupported label reference field %q", field)
}

func appendLabelProjection(data *domain.Bootstrap, field string, raw []byte) error {
	target, err := labelProjectionField(data, field)
	if err != nil {
		return err
	}
	item := reflect.New(target.Type().Elem())
	if err := json.Unmarshal(raw, item.Interface()); err != nil {
		return err
	}
	target.Set(reflect.Append(target, item.Elem()))
	return nil
}

func labelProjectionRecord(data domain.Bootstrap, field string) (json.RawMessage, error) {
	target, err := labelProjectionField(&data, field)
	if err != nil {
		return nil, err
	}
	if target.Len() == 0 {
		return nil, nil
	}
	return json.Marshal(target.Index(0).Interface())
}

func applyLabelMetadataChanges(data domain.Bootstrap, changes map[string]map[string]json.RawMessage) (domain.Bootstrap, error) {
	for field, records := range changes {
		target, err := labelProjectionField(&data, field)
		if err != nil {
			return data, err
		}
		next := reflect.MakeSlice(target.Type(), 0, target.Len())
		for i := 0; i < target.Len(); i++ {
			item := target.Index(i)
			raw, changed := records[item.FieldByName("ID").String()]
			if !changed {
				next = reflect.Append(next, item)
				continue
			}
			if len(raw) == 0 {
				continue
			}
			replacement := reflect.New(item.Type())
			if err := json.Unmarshal(raw, replacement.Interface()); err != nil {
				return data, err
			}
			next = reflect.Append(next, replacement.Elem())
		}
		target.Set(next)
	}
	return data, nil
}
