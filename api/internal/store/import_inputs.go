package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"flow/api/internal/domain"
)

type importInput struct {
	ID        string              `json:"id"`
	Rows      []map[string]string `json:"rows,omitempty"`
	Bundle    json.RawMessage     `json:"bundle,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
}

// Uploaded source files must not ride along in every workspace JSON update.
func compactImportInputs(data domain.Bootstrap) domain.Bootstrap {
	data.ImportJobs = slices.Clone(data.ImportJobs)
	for i := range data.ImportJobs {
		data.ImportJobs[i].Rows = nil
	}
	data.MigrationJobs = slices.Clone(data.MigrationJobs)
	for i := range data.MigrationJobs {
		data.MigrationJobs[i].Bundle = nil
	}
	return data
}

func writeImportInputs(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap) error {
	type input struct {
		kind  string
		value importInput
	}
	items := []input{}
	for _, job := range data.ImportJobs {
		if job.Status == "completed" {
			continue
		}
		items = append(items, input{"import_input", importInput{ID: job.ID, Rows: job.Rows, CreatedAt: job.CreatedAt}})
	}
	for _, job := range data.MigrationJobs {
		items = append(items, input{"migration_input", importInput{ID: job.ID, Bundle: job.Bundle, CreatedAt: job.CreatedAt}})
	}
	keys := map[string]bool{}
	for _, item := range items {
		keys[item.kind+":"+item.value.ID] = true
		if item.value.Rows != nil || item.value.Bundle != nil {
			if err := writeContentRecord(ctx, tx, workspace, item.kind, item.value.ID, item.value); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,id FROM workspace_content_records WHERE workspace_key=? AND kind IN ('import_input','migration_input')`, workspace)
	if err != nil {
		return err
	}
	removed := [][2]string{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return err
		}
		if !keys[kind+":"+id] {
			removed = append(removed, [2]string{kind, id})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND id=?`, workspace, item[0], item[1]); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) hydrateImportInputs(ctx context.Context, workspace string, data *domain.Bootstrap) error {
	for _, kind := range []string{"import_input", "migration_input"} {
		values, err := readContentRecords[importInput](ctx, s, workspace, kind, nil)
		if err != nil {
			return err
		}
		if kind == "import_input" {
			for i := range data.ImportJobs {
				job := &data.ImportJobs[i]
				if job.Rows == nil && job.Status != "completed" {
					if input := values[job.ID]; len(input) > 0 {
						job.Rows = input[0].Rows
					}
				}
			}
		} else {
			for i := range data.MigrationJobs {
				job := &data.MigrationJobs[i]
				if job.Bundle == nil {
					if input := values[job.ID]; len(input) > 0 {
						job.Bundle = input[0].Bundle
					}
				}
			}
		}
	}
	return nil
}
