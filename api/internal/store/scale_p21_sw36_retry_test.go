package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW36Coordinator struct{}

func (scaleP21SW36Coordinator) WithWorkspaceLock(_ context.Context, _ string, run func() error) error {
	return run()
}

func TestScaleP21SW36RetryAndMergedInstallDifferential(t *testing.T) {
	for _, test := range []struct {
		name                                                             string
		warm, peerConflict, peerMerge, deleted, image, coordinator, full bool
		calls                                                            int
	}{
		{name: "warm-conflict", warm: true, calls: 2},
		{name: "warm-unchanged-merged-install", warm: true, image: true, calls: 1},
		{name: "peer-conflict", peerConflict: true, calls: 2},
		{name: "peer-field-merge", peerMerge: true, calls: 1},
		{name: "peer-deletion-skipped-install", deleted: true, calls: 1},
		{name: "coordinator-warm-retry", warm: true, coordinator: true, calls: 2},
		{name: "full-warm-conflict", warm: true, full: true, calls: 2},
		{name: "full-warm-unchanged-merged-install", warm: true, image: true, full: true, calls: 1},
		{name: "full-peer-conflict", peerConflict: true, full: true, calls: 2},
		{name: "full-peer-field-merge", peerMerge: true, full: true, calls: 1},
		{name: "full-peer-deletion-skipped-install", deleted: true, full: true, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			stamp := time.Date(2026, 11, 13, 10, 20, 30, 0, time.UTC)
			native := time.Now()
			type outcome struct {
				callbacks            []domain.CustomEmoji
				returned             domain.CustomEmoji
				installed            []domain.CustomEmoji
				settings             map[string]any
				canonical            string
				event                domain.DomainEvent
				realtime             domain.RealtimeEvent
				webhooks, broadcasts int
			}
			var results []outcome
			for _, bounded := range []bool{false, true} {
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				f.SetWorkspaceCoordinator(nil)
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				name := "before"
				if test.warm {
					name = "warm\x00before"
				}
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "customEmojis", "settings", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.CustomEmojis = []domain.CustomEmoji{
						{ID: "sw36-target", Name: name, ImageURL: "before", CreatedAt: stamp, UpdatedAt: stamp},
						{ID: "sw36-untouched", Name: "untouched\x00native", CreatedAt: native, UpdatedAt: stamp},
					}
					data.Settings = map[string]any{"native": native, "integer": int64(9007199254740993)}
					data.Webhooks = []domain.Webhook{{ID: "sw36-hook", Enabled: true, URL: "https://example.test/sw36"}}
					return nil
				}); err != nil {
					t.Fatal("reset literal preimage", err)
				}
				repo := f.SQLiteStore
				if bounded {
					var err error
					repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
				}
				if test.peerConflict || test.peerMerge || test.deleted {
					tx, err := f.db.BeginTx(t.Context(), nil)
					if err != nil {
						t.Fatal(err)
					}
					if test.deleted {
						err = deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"customEmojis", "sw36-target"})
					} else {
						target := domain.CustomEmoji{ID: "sw36-target", Name: name, ImageURL: "before", CreatedAt: stamp, UpdatedAt: stamp}
						if test.peerConflict {
							target.Name = "peer"
							target.CreatedAt = stamp.In(time.FixedZone("", 20700))
						}
						if test.peerMerge {
							target.ImageURL = "peer-image"
						}
						raw, marshalErr := json.Marshal(target)
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}
						err = writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"customEmojis", "sw36-target"}, metadataRecord{data: raw}, nil)
					}
					if err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				}
				if test.coordinator {
					repo.SetWorkspaceCoordinator(scaleP21SW36Coordinator{})
				}
				result := outcome{}
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { result.event = event; result.webhooks++ })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { result.realtime = event; result.broadcasts++ })
				ctx := WithMetadataFields(t.Context(), "customEmojis")
				scope := MetadataRecordScope{Write: []RecordKey{{Kind: "customEmojis", ID: "sw36-target"}}}
				if test.full {
					scope = MetadataRecordScope{Fields: []string{"customEmojis"}}
				}
				err := repo.MutateWorkspaceScoped(ctx, workspace, "custom_emoji.updated", "sw36-target", map[string]any{"value": "after"},
					scope, func(data *domain.Bootstrap) error {
						target := &data.CustomEmojis[0]
						result.callbacks = append(result.callbacks, *target)
						if test.image {
							target.ImageURL = "after"
						} else {
							target.Name = "after"
						}
						result.returned = *target
						return nil
					})
				if err != nil {
					t.Fatal("producer", bounded, err)
				}
				var data domain.Bootstrap
				if bounded {
					var found bool
					data, found, err = repo.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"customEmojis", "settings"}})
					if err != nil || !found {
						t.Fatal(found, err)
					}
				} else {
					data, _ = repo.WorkspaceMetadata(workspace)
				}
				result.installed, result.settings = data.CustomEmojis, data.Settings
				var raw []byte
				err = f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='customEmojis' AND record_key='sw36-target'`, workspace).Scan(&raw)
				if errors.Is(err, sql.ErrNoRows) {
					result.canonical = "<missing>"
				} else if err != nil {
					t.Fatal(err)
				} else {
					result.canonical = string(raw)
				}
				if len(result.callbacks) != test.calls || result.webhooks != 1 || result.broadcasts != 1 {
					t.Fatal("retry/publication count", bounded, len(result.callbacks), result.webhooks, result.broadcasts)
				}
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				repo.SetWorkspaceCoordinator(nil)
				if bounded {
					if repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0 {
						t.Fatal("bounded producer full decoded or leaked work")
					}
					repo.releaseMetadataLogicalSources()
				}
				results = append(results, result)
			}
			old, new := results[0], results[1]
			if !reflect.DeepEqual(old.callbacks, new.callbacks) || !reflect.DeepEqual(old.returned, new.returned) ||
				!reflect.DeepEqual(old.installed, new.installed) || !reflect.DeepEqual(old.settings, new.settings) ||
				old.canonical != new.canonical && !scaleP21SW36JSONEqual([]byte(old.canonical), []byte(new.canonical)) {
				t.Fatalf("callback/canonical/installed differential: callbacks=%v returned=%v installed=%v settings=%v canonical old=%s new=%s",
					reflect.DeepEqual(old.callbacks, new.callbacks), reflect.DeepEqual(old.returned, new.returned),
					reflect.DeepEqual(old.installed, new.installed), reflect.DeepEqual(old.settings, new.settings), old.canonical, new.canonical)
			}
			if !scaleP21SW36JSONEqual(old.event.PreviousValues, new.event.PreviousValues) || !scaleP21SW36JSONEqual(old.event.Snapshot, new.event.Snapshot) || !scaleP21SW36JSONEqual(old.realtime.Payload, new.realtime.Payload) {
				t.Fatal("pre-persist event/previous/snapshot/realtime semantics changed")
			}
		})
	}
}

func scaleP21SW36JSONEqual(a, b []byte) bool {
	if string(a) == string(b) {
		return true
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func TestScaleP21SW36RefreshPreservesPrivateInputsAndNilAppend(t *testing.T) {
	native := time.Now()
	before := domain.Bootstrap{
		ExportJobs:    []domain.ExportJob{{ID: "export", Data: "native\x00private", CreatedAt: native}},
		ImportJobs:    []domain.ImportJob{{ID: "import", Rows: []map[string]string{{"native": "row\x00input"}}, CreatedAt: native}},
		MigrationJobs: []domain.MigrationJob{{ID: "migration", Bundle: json.RawMessage("{ \"native\" : true }"), CreatedAt: native}},
	}
	source, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	captured, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"exportJobs", "importJobs", "migrationJobs"}}, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	stamp := `"2026-11-13T12:34:56+05:45"`
	patches := []metadataLogicalRecordPatch{
		{metadataRecordKey{"exportJobs", "export"}, []byte(`{"id":"export","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"importJobs", "import"}, []byte(`{"id":"import","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"migrationJobs", "migration"}, []byte(`{"id":"migration","status":"done","createdAt":` + stamp + `}`)},
		{metadataRecordKey{"customEmojis", "new"}, []byte(`{"id":"new","name":"appended","createdAt":` + stamp + `}`)},
	}
	candidate, err := metadataLogicalPatchRecords(t.Context(), source, patches)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Release()
	next, err := candidate.Select(t.Context(), MetadataRecordScope{Fields: []string{"customEmojis", "exportJobs", "importJobs", "migrationJobs"}}, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if next.CustomEmojis == nil || len(next.CustomEmojis) != 1 || next.CustomEmojis[0].ID != "new" {
		t.Fatal("refresh append retained a nil array shape", next.CustomEmojis)
	}
	if next.ExportJobs[0].Data != captured.ExportJobs[0].Data ||
		!reflect.DeepEqual(next.ImportJobs[0].Rows, before.ImportJobs[0].Rows) ||
		!reflect.DeepEqual(next.MigrationJobs[0].Bundle, before.MigrationJobs[0].Bundle) {
		t.Fatal("refresh discarded native private/import inputs")
	}
	_, offset := next.ExportJobs[0].CreatedAt.Zone()
	if offset != 20700 {
		t.Fatal("fresh SQL timestamp registry import changed offset", offset)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := metadataLogicalPatchRecords(ctx, source, patches); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled refresh", err)
	}
}
