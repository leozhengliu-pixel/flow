package store

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalWriteRejectsNewUnavailableBeforeCommit(t *testing.T) {
	for _, kind := range []string{"map-existing", "map-new", "complete-field", "array-existing"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			key, emoji := "sw29RejectTimestamp", "sw29-reject-emoji"
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "customEmojis"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
				data.Settings[key] = "before"
				data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: emoji, Name: "before"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(view.releaseMetadataLogicalSources)
			var beforeRoot, beforeRows []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&beforeRoot); err != nil {
				t.Fatal(err)
			}
			field, record := "settings", key
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: field, ID: record}}}
			switch kind {
			case "map-new":
				scope.Write[0].ID += "New"
			case "complete-field":
				scope = MetadataRecordScope{Fields: []string{"settings"}}
			case "array-existing":
				field, record = "customEmojis", emoji
				scope = MetadataRecordScope{Write: []RecordKey{{Kind: field, ID: record}}}
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, record).Scan(&beforeRows); err != nil {
				t.Fatal(err)
			}
			beforeStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			events := func() int {
				var count int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				return count
			}
			beforeEvents, published := events(), 0
			view.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			view.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			unproven := time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("not-producer-receipted", 7331))
			err = view.MutateWorkspaceScoped(t.Context(), workspace, "integration.adapter_configured", "", nil, scope, func(data *domain.Bootstrap) error {
				if kind == "array-existing" {
					data.CustomEmojis[0].UpdatedAt = unproven
				} else {
					target := key
					if kind == "map-new" {
						target += "New"
					}
					data.Settings[target] = unproven
				}
				return nil
			})
			if !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("new unavailable native callback value reached commit", err)
			}
			var afterRoot, afterRows []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&afterRoot); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, record).Scan(&afterRows); err != nil {
				t.Fatal(err)
			}
			afterStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil || afterStamp != beforeStamp || !bytes.Equal(beforeRoot, afterRoot) || !bytes.Equal(beforeRows, afterRows) {
				t.Fatal("rejected native callback changed canonical SQL or revision", err)
			}
			if kind == "map-new" {
				var count int
				if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key=?`, workspace, key+"New").Scan(&count); err != nil || count != 0 {
					t.Fatal("rejected map creation retained a new row", count, err)
				}
			}
			if events() != beforeEvents || published != 0 || view.WorkspaceMetadataDecodes() != 0 ||
				view.metadataResidency.work.used != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("rejected native callback published, hydrated or leaked candidate work")
			}
		})
	}
}

func TestScaleP21SW29LogicalWriteKeepsUntouchedOpaqueArraySibling(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{CustomEmojis: []domain.CustomEmoji{
		{ID: "opaque", UpdatedAt: time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("unproven", 7331))},
		{ID: "safe", Name: "before"},
	}}
	source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "customEmojis", ID: "safe"}}}
	before, err := source.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	next.CustomEmojis[0].Name = "after"
	fork, err := source.Fork(ctx, before, next, scope, copyStamp{})
	if err != nil {
		t.Fatal("untouched opaque sibling blocked a safe native replacement", err)
	}
	defer fork.Release()
	actual, err := fork.Select(ctx, scope, 2<<20)
	if err != nil || !reflect.DeepEqual(actual.CustomEmojis, next.CustomEmojis) {
		t.Fatal("safe array replacement changed", err)
	}
	if _, err := fork.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "customEmojis", ID: "opaque"}}}, 2<<20); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("unavailable untouched sibling lost its opaque hold", err)
	}
}
