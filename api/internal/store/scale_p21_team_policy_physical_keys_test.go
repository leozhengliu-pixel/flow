package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestScaleP21TeamPolicyPhysicalKeys(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 70 {
		physical := fmt.Sprintf("physical\x00%03d", index)
		if index == 0 {
			physical = ""
		}
		raw, err := json.Marshal(map[string]any{
			"id": fmt.Sprintf("native-team-%03d", index), "key": fmt.Sprintf("NK%03d", index),
			"private": index%2 == 0, "name": "ignored body",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"teams", physical}, metadataRecord{data: raw, order: -10}, nil); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	want, err := f.resourcePolicyRecords(ctx, workspace, f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := view.readResidentResourcePolicy(ctx, workspace, f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		t.Fatalf("physical key traversal changed canonical policy:\n%s\n%s", a, b)
	}
	if len(got.Teams) < 70 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("physical key pages skipped rows or retained admission", len(got.Teams), view.MetadataWorkUsedForTesting())
	}
}
