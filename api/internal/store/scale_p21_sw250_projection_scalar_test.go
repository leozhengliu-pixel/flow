package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestScaleP21SW250ProjectionScalarAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	from := `FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`
	args := []any{f.data.Workspace.ID, f.data.Viewer.ID}
	columns := []string{"u.active", "-7", "NULL", "'\u00e9'"}
	var active int
	if err := f.db.QueryRowContext(t.Context(), `SELECT u.active `+from, args...).Scan(&active); err != nil || active != 1 {
		t.Fatal("owned projection fixture is not an active member", active, err)
	}
	const want = (1+2+0+2)*32 + 4096
	read := &DocumentRead{owner: &ResourceRead{store: view}}
	defer read.Close()
	var observed string
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "SELECT COUNT(*),COALESCE(SUM(") {
			observed = query
		}
	})
	if err := read.documentProjectionRows(ctx, columns, from, args...); err != nil {
		t.Fatal("integer/negative/null/UTF-8 scalar projection failed", err)
	}
	if observed == "" || view.MetadataWorkUsedForTesting() != want {
		t.Fatal("scalar preflight did not retain exact textual-byte admission", observed, view.MetadataWorkUsedForTesting(), want)
	}
	if view.dialect == "postgres" && (!strings.Contains(observed, "OCTET_LENGTH(CAST(u.active AS TEXT))") ||
		!strings.Contains(observed, "OCTET_LENGTH(CAST(-7 AS TEXT))")) {
		t.Fatal("PostgreSQL projection counted numeric scalars without text conversion", observed)
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("scalar preflight retained work or hydrated metadata")
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = want - 1
	read = &DocumentRead{owner: &ResourceRead{store: view}}
	err = read.documentProjectionRows(t.Context(), columns, from, args...)
	read.Close()
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("scalar admission denial did not release", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	read = &DocumentRead{owner: &ResourceRead{store: view}}
	err = read.documentProjectionRows(cancelled, columns, from, args...)
	read.Close()
	if !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled scalar preflight retained work or succeeded", err)
	}
}
