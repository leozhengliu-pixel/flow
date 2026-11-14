package store

import (
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW23ResidentPolicyOrderCanonical(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 70 {
		key := idgen.New("sw23-order-")
		raw := []byte(fmt.Sprintf(`{"id":"body-%d","private":true}`, i%7))
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", key}, metadataRecord{order: (i % 5) - 3, data: raw}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	want, err := f.resourcePolicyRecords(t.Context(), workspace, f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	view.metadataResidency.requireCoverage = false
	if err := view.reloadResidentWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view.mu.RLock()
	got := resourcePolicyMetadata(view.workspaces[workspace])
	view.mu.RUnlock()
	if !reflect.DeepEqual(got.Teams, want.Teams) {
		t.Fatal("resident recovery changed collection_order/canonical-key ordering or duplicate body IDs")
	}
	if _, exact := view.copyRevisionOf(workspace); exact {
		t.Fatal("order repair certified a complete Bootstrap")
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("resident order repair decoded a full workspace or leaked work")
	}
}
