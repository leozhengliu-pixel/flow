package main

import (
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/store"
)

// Shared by the workflow and authorization differentials: reload a
// fixture's SQL source through its actual loader in both metadata modes.
func metadataReloadSourceExec(t *testing.T, database *scaleDatabase, query string, args ...any) {
	t.Helper()
	database.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, database.key).Close()
	if database.config.Driver == "postgres" || database.config.Driver == "postgresql" {
		for index := range args {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index+1), 1)
		}
	}
	if _, err := database.reader.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func metadataReloadSourceReloadSource(t *testing.T, database *scaleDatabase, sources ...*store.SQLiteStore) {
	t.Helper()
	// Exact installed copies skip reload. Advance only this fixture's
	// metadata revision so both modes exercise their actual SQL loader.
	metadataReloadSourceExec(t, database, `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`, database.key)
	for _, source := range sources {
		decodes := source.WorkspaceMetadataDecodes()
		if err := source.ReloadWorkspace(t.Context(), database.key); err != nil {
			t.Fatal("workflow SQL source reload", err)
		}
		if !source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() <= decodes {
			t.Fatal("literal workflow SQL-loaded source did not decode")
		}
		if source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() != decodes {
			t.Fatal("bounded workflow SQL-loaded source used full hydration")
		}
	}
}
