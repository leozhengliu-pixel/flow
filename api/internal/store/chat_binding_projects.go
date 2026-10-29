package store

import (
	"context"
	"encoding/json"
)

// migrateChatBindingProjectSets gives every group binding stored with the
// single-project form its project as a one-project set. The stored projectId
// stays as it was (non-destructive), and every other field is kept verbatim.
// Bindings are read in pages by id, so the migration is bounded and
// idempotent: a rewritten binding already has its set.
func (s *SQLiteStore) migrateChatBindingProjectSets(ctx context.Context) error {
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id,payload FROM chat_bindings WHERE id>? ORDER BY id LIMIT 200`, after)
		if err != nil {
			return err
		}
		type row struct {
			id  string
			raw []byte
		}
		var page []row
		for rows.Next() {
			var item row
			if err = rows.Scan(&item.id, &item.raw); err != nil {
				rows.Close()
				return err
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, item := range page {
			after = item.id
			rewritten, changed, err := chatBindingWithProjectSet(item.raw)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if _, err = s.db.ExecContext(ctx, `UPDATE chat_bindings SET payload=? WHERE id=?`, rewritten, item.id); err != nil {
				return err
			}
		}
	}
}

func chatBindingWithProjectSet(raw []byte) ([]byte, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, err
	}
	var projectID string
	if value, ok := fields["projectId"]; !ok || json.Unmarshal(value, &projectID) != nil || projectID == "" {
		return raw, false, nil
	}
	var existing []string
	if value, ok := fields["projectIds"]; ok && json.Unmarshal(value, &existing) == nil && len(existing) > 0 {
		return raw, false, nil
	}
	set, err := json.Marshal([]string{projectID})
	if err != nil {
		return nil, false, err
	}
	fields["projectIds"] = set
	rewritten, err := json.Marshal(fields)
	return rewritten, err == nil, err
}
