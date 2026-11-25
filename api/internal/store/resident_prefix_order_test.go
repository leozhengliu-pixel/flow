package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW23ResidentPolicyOrderInlinePrefix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	firstKey := ""
	for i := range 70 {
		key := idgen.New("sw23-prefix-order-")
		if i == 0 {
			firstKey = key
		}
		raw := []byte(fmt.Sprintf(`{"id":"body-%d","key":"row-%03d","private":%t}`, i%7, i, i%2 == 0))
		if i%17 == 0 {
			raw = []byte("null")
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", key}, metadataRecord{order: (i % 5) - 3, data: raw}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var original []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(original, &root); err != nil {
		t.Fatal(err)
	}
	prefix := []domain.Team{
		{ID: firstKey, Key: "inline-canonical-key-collision", Private: true},
		{ID: "body-0", Key: "inline-body-id-collision", Private: false},
		{ID: "", Key: "inline-empty-body-id"},
	}
	root["teams"], err = json.Marshal(prefix)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, encoded, workspace); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		restore, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer restore.Rollback()
		if _, err := restore.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, original, workspace); err != nil {
			t.Error(err)
			return
		}
		if err := restore.Commit(); err != nil {
			t.Error(err)
		}
	}()
	f.dropMetadataCache(t.Context(), workspace)
	read, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := read.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		read.Rollback()
		t.Fatal(err)
	}
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, read)
	read.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Teams) < len(prefix) || !reflect.DeepEqual(want.Teams[:len(prefix)], prefix) {
		t.Fatal("actual typed decoder did not retain the inline prefix fixture")
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	view.metadataResidency.requireCoverage = false
	if err := view.reloadResidentWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view.mu.RLock()
	got := cloneBootstrap(view.workspaces[workspace]).Teams
	view.mu.RUnlock()
	if !reflect.DeepEqual(got, want.Teams) {
		t.Fatalf("resident array differs from actual typed decoder: got=%d want=%d prefix=%d", len(got), len(want.Teams), len(prefix))
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("prefix/suffix reorder decoded full metadata or leaked work")
	}
}
