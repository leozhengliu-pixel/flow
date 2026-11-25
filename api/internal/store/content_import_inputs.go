package store

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

type scopedMetadataImportInputsKey struct{}

type scopedMetadataImportInputs struct {
	data  *domain.Bootstrap
	scope MetadataRecordScope
}

// A partial metadata callback cannot define either input collection's complete
// membership. Only complete declared fields may prune their own collection.
func writeScopedImportInputs(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap, scope MetadataRecordScope) error {
	for _, field := range []string{"importJobs", "migrationJobs"} {
		complete := slices.Contains(scope.Fields, field)
		var writes []string
		for _, key := range scope.Write {
			if key.Kind == field {
				writes = append(writes, key.ID)
			}
		}
		if !complete && len(writes) == 0 {
			continue
		}
		kind := "import_input"
		if field == "migrationJobs" {
			kind = "migration_input"
		}
		kept := map[string]bool{}
		storeInput := func(value importInput) error {
			if !complete && !slices.Contains(writes, value.ID) {
				return nil
			}
			kept[value.ID] = true
			if value.Rows == nil && value.Bundle == nil {
				return nil
			}
			return writeContentRecord(ctx, tx, workspace, kind, value.ID, value)
		}
		if field == "importJobs" {
			for _, job := range data.ImportJobs {
				if job.Status != "completed" {
					if err := storeInput(importInput{ID: job.ID, Rows: job.Rows, CreatedAt: job.CreatedAt}); err != nil {
						return err
					}
				}
			}
		} else {
			for _, job := range data.MigrationJobs {
				if err := storeInput(importInput{ID: job.ID, Bundle: job.Bundle, CreatedAt: job.CreatedAt}); err != nil {
					return err
				}
			}
		}
		// Match the legacy Go membership map's byte equality, including
		// SQLite TEXT/BLOB IDs, without fetching unrelated input bodies/IDs.
		column := "CAST(id AS BLOB)"
		if tx.dialect == "mysql" {
			column = "CAST(id AS BINARY)"
		} else if tx.dialect == "postgres" {
			column = "convert_to(id,'UTF8')"
		}
		if complete {
			keys := make([]string, 0, len(kept))
			for key := range kept {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			clause, args := bindList(column, keys)
			for i, key := range keys {
				args[i] = []byte(key)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND NOT (`+clause+`)`,
				append([]any{workspace, kind}, args...)...); err != nil {
				return err
			}
			continue
		}
		for _, key := range writes {
			if !kept[key] {
				if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND `+column+`=?`,
					workspace, kind, []byte(key)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
