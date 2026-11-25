package store

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW236InlineRootWriterTwoCapturesDifferential(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	if len(before.Users) < 2 || before.Users[1].ID == "" {
		t.Fatal("SW217 fixture lacks its split fast-row user")
	}
	before.Asks = []domain.Ask{
		{ID: "sw236-duplicate", Title: "First occurrence", Body: strings.Repeat("x", 300<<10)},
		{ID: "sw236-duplicate", Title: "Retained later occurrence", Body: "later"},
	}
	target := cloneBootstrap(before)
	target.Asks[0].Title = "Changed <>&"
	target.Users[1].Name = "SW236 changed fast row"
	workspace := before.Workspace.URLKey
	tx, ctx := scaleP21SW77Begin(t, f, before)
	want := cloneBootstrap(target)
	if _, err := f.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, &want); err != nil {
		t.Fatal("literal SW217 inline/fast-row delta", err)
	}
	expected := scaleP21SW76MetadataImage(t, tx, workspace)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx, ctx = scaleP21SW77Begin(t, f, before)
	defer tx.Rollback()
	rootBytes, shapes := scaleP21SW217Shapes(t, ctx, tx, workspace)
	if rootBytes <= MetadataRecordPageMaxBytes || shapes["asks"] != "" || shapes["users"] != "array" {
		t.Fatal("fixture lost its large inline root and split fast row", rootBytes, shapes)
	}
	var canonicalRows int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?",
		workspace, before.Users[1].ID).Scan(&canonicalRows); err != nil || canonicalRows != 1 {
		t.Fatal("fast-row fixture is not physically split", canonicalRows, err)
	}
	rootReads, rowWrites, rootWrites := 0, 0, 0
	measuring := true
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if !measuring {
			return
		}
		lower := strings.ToLower(strings.TrimSpace(query))
		if strings.HasPrefix(lower, "select substr(") && strings.Contains(lower, "from workspace_states") {
			rootReads++
		}
		row, root := scaleP21SW83MetadataWrite(query)
		if row && !root {
			rowWrites++
		}
		if root {
			rootWrites++
		}
	})
	ctx = context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"asks"})
	disk, descriptors, work := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), view.MetadataWorkUsedForTesting()
	var native domain.Bootstrap
	func() {
		left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer left.Release()
		right, err := captureMetadataLogicalSource(ctx, target, left.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		defer right.Release()
		image, err := view.persistLegacyMetadataImage(ctx, tx, workspace, left, right)
		if err != nil {
			t.Fatal("real generic inline/fast-row writer refused", err)
		}
		defer image.Release()
		measuring = false
		native, err = image.Select(ctx, MetadataRecordScope{Fields: []string{"users", "asks"}}, 8<<20)
		if err != nil {
			t.Fatal("generic writer returned an unreadable native postimage", err)
		}
	}()
	measuring = false
	if !reflect.DeepEqual(native.Users, want.Users) || !reflect.DeepEqual(native.Asks, want.Asks) {
		t.Fatal("generic writer changed exact native fast-row or inline occurrences")
	}
	actual := scaleP21SW76MetadataImage(t, tx, workspace)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("conservative fresh capture changed literal root or canonical rows")
	}
	pages := int((rootBytes + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes)
	if rootReads < 2*pages || rootReads > 2*pages+12 || rowWrites == 0 || rootWrites != 1 {
		t.Fatal("generic writer missed its fast row or exceeded two bounded root captures",
			rootReads, 2*pages+12, rowWrites, rootWrites)
	}
	if view.MetadataWorkUsedForTesting() != work || metadataLogicalDiskUsed.Load() != disk ||
		metadataLogicalDescriptorsUsed.Load() != descriptors || tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
		t.Fatal("generic inline/fast-row writer retained work, spools or SQL-stage owners")
	}
	var stages int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bounded_metadata_write_stage").Scan(&stages); err != nil || stages != 0 {
		t.Fatal("generic root writer left durable SQL staging rows", stages, err)
	}
	t.Logf("root_bytes=%d root_SUBSTR=%d bound=2*ceil(root/16KiB)+12=%d row_writes=%d root_writes=%d",
		rootBytes, rootReads, 2*pages+12, rowWrites, rootWrites)
}
