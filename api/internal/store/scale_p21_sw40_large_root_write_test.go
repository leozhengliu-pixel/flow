package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW40LargeRootScopedWriteDifferential(t *testing.T) {
	for _, fieldScoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "full-reference-refresh", true: "legacy-field-scoped"}[fieldScoped], func(t *testing.T) {
			f, _, _ := scaleP21SW33LargeRoot(t)
			workspace := f.data.Workspace.URLKey
			installed, _ := f.storedWorkspace(workspace)
			members := cloneBootstrap(installed).Members
			person := f.data.Viewer
			person.Name, person.DisplayName = "Current directory name", "Current directory display"
			stale := person
			stale.Name, stale.DisplayName = "Stale member name", "Stale member display"
			members[0].User = stale
			type outcome struct {
				root     map[string]json.RawMessage
				target   []byte
				event    domain.DomainEvent
				realtime domain.RealtimeEvent
				counts   [2]int
			}
			var results []outcome
			for _, bounded := range []bool{false, true} {
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members", "users", "settings", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Viewer = f.data.Viewer
					data.Members = cloneBootstrap(domain.Bootstrap{Members: members}).Members
					data.Users = append(slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == person.ID }), person)
					data.Settings["sw40-target"] = "before"
					data.Settings["sw40-unchanged"] = int64(9007199254740993)
					data.Webhooks = []domain.Webhook{{ID: "sw40-hook", Enabled: true, URL: "https://example.test/sw40"}}
					return nil
				}); err != nil {
					t.Fatal("literal accepted large inline producer", err)
				}
				repo := f.SQLiteStore
				if bounded {
					repo = &SQLiteStore{
						db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, viewer: f.viewer,
						workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency(),
						lastWorkspaceKey: workspace,
					}
					repo.metadataResidency.requireCoverage = false
					err := repo.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(repo.releaseMetadataLogicalSources)
				}
				current := outcome{}
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { current.event = event; current.counts[0]++ })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { current.realtime = event; current.counts[1]++ })
				statements, chunks, fullRoot := 0, 0, 0
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					statements++
					if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
						chunks++
					}
					if strings.Contains(query, "SELECT data FROM workspace_states") || strings.Contains(query, "SELECT workspace_id,data FROM workspace_states") {
						fullRoot++
					}
				})
				if fieldScoped {
					ctx = WithMetadataFields(ctx, "settings")
				}
				err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "sw40-target", map[string]string{"value": "after"},
					MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw40-target"}}}, func(data *domain.Bootstrap) error {
						data.Settings["sw40-target"] = "after"
						return nil
					})
				if err != nil {
					t.Fatalf("small write over accepted large Members root bounded=%v: %v", bounded, err)
				}
				var raw []byte
				if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &current.root); err != nil {
					t.Fatal(err)
				}
				if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='sw40-target'", workspace).Scan(&current.target); err != nil {
					t.Fatal(err)
				}
				if current.counts != [2]int{1, 1} {
					t.Fatal("write publication count", current.counts)
				}
				if bounded {
					if fullRoot != 0 || chunks == 0 || chunks > 3 {
						t.Fatalf("hot root transport unbounded or repeated: full=%d chunks=%d", fullRoot, chunks)
					}
					source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					ctx, release, err := repo.admitMetadataWork(t.Context(), 16<<20)
					if err != nil {
						source.Release()
						t.Fatal(err)
					}
					selection, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"members"}}, 16<<20)
					var stored []domain.WorkspaceMember
					decodeErr := json.Unmarshal(current.root["members"], &stored)
					equal := reflect.DeepEqual(selection.Members, stored)
					source.Release()
					release()
					if err != nil || decodeErr != nil || !equal {
						t.Fatal("postcommit installed Members differ from canonical write", err, decodeErr)
					}
					if repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0 {
						t.Fatal("small large-root write full decoded or leaked work")
					}
				}
				repo.SetWebhookSink(nil)
				repo.SetRealtimeSink(nil)
				results = append(results, current)
				t.Logf("bounded=%v request primary SQL=%d root chunks=%d", bounded, statements, chunks)
			}
			old, bounded := results[0], results[1]
			if !reflect.DeepEqual(old.root, bounded.root) || !bytes.Equal(old.target, bounded.target) ||
				old.event.Type != bounded.event.Type || old.event.AggregateID != bounded.event.AggregateID ||
				!bytes.Equal(old.event.Payload, bounded.event.Payload) || !bytes.Equal(old.event.PreviousValues, bounded.event.PreviousValues) ||
				!bytes.Equal(old.event.Snapshot, bounded.event.Snapshot) || !bytes.Equal(old.realtime.Payload, bounded.realtime.Payload) {
				t.Fatal("large-root write changed SQL/root/events")
			}
			var after []domain.WorkspaceMember
			if err := json.Unmarshal(bounded.root["members"], &after); err != nil {
				t.Fatal(err)
			}
			expected := person.Name
			if fieldScoped {
				expected = stale.Name
			}
			if len(after) != len(members) || after[0].User.Name != expected {
				t.Fatal("legacy reference-refresh boundary changed")
			}
		})
	}
}

func TestScaleP21SW40LargeRootPatchRollback(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := view.admitMetadataWork(t.Context(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx.maxTransactionBytes = 64 << 10
	ctx, closeRoots := view.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
	err = view.patchMetadataLogicalRoot(ctx, tx, workspace, f.data.Workspace.ID, map[string]metadataLogicalRootReplacement{
		"viewerRole": metadataLogicalRootBytes(json.RawMessage(`"member"`)),
	}, true)
	closeRoots()
	_ = tx.Rollback()
	if !errors.Is(err, ErrTransactionWriteBudget) {
		t.Fatal("root SQL substring copy bypassed the original complete-root transaction byte charge", err)
	}
	var unchanged []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&unchanged); err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("failed transaction budget altered root", err)
	}
	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.validateMetadataLogicalRootSnapshot(cancelCtx, nil, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("root cancellation was lost", err)
	}
}

func TestScaleP21SW40CanonicalRootPatchDifferential(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for _, members := range []string{"null", "[]", "large"} {
		for _, repeated := range []bool{false, true} {
			t.Run(members+map[bool]string{false: "/single", true: "/repeated"}[repeated], func(t *testing.T) {
				root := map[string]json.RawMessage{}
				if err := json.Unmarshal(original, &root); err != nil {
					t.Fatal(err)
				}
				if members != "large" {
					root["members"] = json.RawMessage(members)
				}
				raw, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				if repeated {
					raw = append(append(raw[:len(raw)-1], []byte(`,"viewerRole":"member","viewerRole":"admin"`)...), '}')
				}
				if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, workspace); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &root); err != nil {
					t.Fatal(err)
				}
				root["viewerRole"] = json.RawMessage(`"guest"`)
				expected, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				ctx, release, err := view.admitMetadataWork(t.Context(), 8<<20)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				ctx, closeRoots := view.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
				defer closeRoots()
				if err := view.patchMetadataLogicalRoot(ctx, tx, workspace, f.data.Workspace.ID, map[string]metadataLogicalRootReplacement{
					"viewerRole": metadataLogicalRootBytes(json.RawMessage(`"guest"`)),
				}, false); err != nil {
					t.Fatal(err)
				}
				var after []byte
				if err := tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after, expected) {
					t.Fatal("root SQL patch differs from original raw-message root merge")
				}
			})
		}
	}
}

func TestScaleP21SW40NullManifestDeltaDifferential(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(original, &all); err != nil {
		t.Fatal(err)
	}
	root := map[string]json.RawMessage{
		metadataCollectionsKey: json.RawMessage(`null`),
		"workspace":            all["workspace"],
		"viewer":               all["viewer"],
		"viewerRole":           json.RawMessage(`"admin"`),
		"members":              all["members"],
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	var results [][]byte
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		ctx, release, err := view.admitMetadataWork(t.Context(), 8<<20)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			release()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM workspace_metadata_records WHERE workspace_key=?", workspace); err != nil {
			_ = tx.Rollback()
			release()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, workspace); err != nil {
			_ = tx.Rollback()
			release()
			t.Fatal(err)
		}
		ctx, closeRoots := repo.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
		before := domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "admin"}
		after := before
		after.ViewerRole = "guest"
		changed, err := repo.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, &after)
		if err != nil || !changed {
			closeRoots()
			_ = tx.Rollback()
			release()
			t.Fatal("null-manifest scalar delta", err)
		}
		var actual []byte
		err = tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&actual)
		closeRoots()
		_ = tx.Rollback()
		release()
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, actual)
	}
	if !bytes.Equal(results[0], results[1]) {
		t.Fatal("bounded delta changed the original null manifest")
	}
}
