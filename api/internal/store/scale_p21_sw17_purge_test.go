package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW17DeletionCoordinator struct{}

func (scaleP21SW17DeletionCoordinator) WithWorkspaceLock(ctx context.Context, _ string, action func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return action()
}

func TestScaleP21SW17PurgeMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_sw17.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues, domain.Issue{ID: idgen.New("issue_sw17_"), Title: "Retained issue", Description: strings.Repeat("unrelated issue body ", 65000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bounded, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, bounded} {
		run := func() {
			t.Helper()
			if _, started, err := source.BeginWorkspacePurge(t.Context(), f.data.Workspace.URLKey, time.Now()); err != nil || started {
				t.Fatalf("unscheduled purge started=%t err=%v", started, err)
			}
		}
		run()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 5 {
			run()
		}
		runtime.ReadMemStats(&after)
		t.Logf("bounded=%t purge-identity bytes/op=%d allocs/op=%d", source.BoundedMetadataEnabled(), (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5)
	}
}

func TestScaleP21SW17PurgeLifecycleDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	primary := f.data.Workspace.URLKey
	var primaryRoot []byte
	if err := f.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, primary).Scan(&primaryRoot); err != nil {
		t.Fatal(err)
	}
	for _, coordinated := range []bool{false, true} {
		t.Run(fmt.Sprintf("coordinated=%t", coordinated), func(t *testing.T) {
			var outcomes []map[string]int64
			for _, bounded := range []bool{false, true} {
				data, err := f.CreateWorkspace(ctx, "SW17 purge target", idgen.New("sw17-purge-"), "us")
				if err != nil {
					t.Fatal(err)
				}
				target := WorkspacePurgeTarget{Key: data.Workspace.URLKey, ID: data.Workspace.ID}
				t.Cleanup(func() {
					cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if err := f.ReloadAllWorkspaces(cleanup); err != nil {
						t.Error(err)
						return
					}
					if f.HasWorkspace(target.Key) {
						if err := f.DeleteWorkspace(cleanup, target.Key); err != nil {
							t.Error(err)
						}
					} else if err := f.PurgeWorkspaceRows(cleanup, target, map[string]int64{}); err != nil {
						t.Error(err)
					}
				})
				if err := f.MutateWorkspace(ctx, target.Key, "scale_p21_sw17.seed", "", nil, func(next *domain.Bootstrap) error {
					next.Issues = append(next.Issues, domain.Issue{ID: idgen.New("issue_sw17_"), Title: "Retained until purge", Description: strings.Repeat("legal unrelated issue ", 65000)})
					for range 65 {
						next.Projects = append(next.Projects, domain.Project{ID: idgen.New("project_sw17_"), Name: "Retained until purge"})
					}
					next.Projects[0].Description = strings.Repeat("unrelated metadata ", 18000)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				for index := range 405 {
					id := idgen.New("sw17-account-")
					_, err = tx.ExecContext(ctx, `INSERT INTO code_host_accounts(`+codeHostAccountColumns+`) VALUES(?,?,?,?,'',?,'fixture','','token','','active','',?,'','','','')`,
						id, f.data.Viewer.ID, domain.CodeHostGitLab, fmt.Sprintf("sw17-%d.example.test", index), fmt.Sprint(index), target.Key)
					if err == nil {
						_, err = tx.ExecContext(ctx, `INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,'')`, CodeHostAccountSecretID(id), []byte("owned purge fixture"))
					}
					if err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				// Populate every generated revision bucket so both fixtures have
				// the same physical deletion count regardless of random choices.
				for _, kind := range []string{cacheKindMetadata, cacheKindIssues} {
					for shard := range cacheRevisionShards {
						if _, err := f.db.ExecContext(ctx, `INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision) VALUES(?,?,?,0) ON CONFLICT DO NOTHING`, target.Key, kind, shard); err != nil {
							t.Fatal(err)
						}
					}
				}
				scheduled, err := f.ScheduleWorkspaceDeletion(ctx, target.Key)
				if err != nil {
					t.Fatal(err)
				}
				source := f.SQLiteStore
				if bounded {
					source, err = f.BoundedMetadataViewForTesting(ctx)
					if err != nil {
						t.Fatal(err)
					}
				}
				if coordinated {
					source.coordinator = scaleP21SW17DeletionCoordinator{}
				}
				if bounded {
					source.metadataResidency.cacheRow(metadataResidentCacheKey{workspace: target.Key, field: "projects", key: "owned"}, metadataCacheRow{Data: []byte(`{"id":"owned"}`)})
					source.metadataResidency.cacheRow(metadataResidentCacheKey{workspace: primary, field: "projects", key: "retained"}, metadataCacheRow{Data: []byte(`{"id":"retained"}`)})
				}
				if err := source.PurgeWorkspaceRows(ctx, WorkspacePurgeTarget{Key: target.Key, ID: "reused-workspace-id"}, map[string]int64{}); !errors.Is(err, ErrWorkspaceKeyReused) {
					t.Fatalf("reused key: %v", err)
				}
				if err := source.PurgeWorkspaceRows(ctx, target, map[string]int64{}); err == nil {
					t.Fatal("a live workspace was purged")
				}
				if _, started, err := source.BeginWorkspacePurge(ctx, target.Key, scheduled.DeletionRequestedAt.Add(-time.Second)); err != nil || started {
					t.Fatalf("early purge started=%t err=%v", started, err)
				}
				receipt, started, err := source.BeginWorkspacePurge(ctx, target.Key, scheduled.DeletionRequestedAt.Add(time.Second))
				if err != nil || !started || receipt.WorkspaceID != target.ID || receipt.RequestRef == "" ||
					receipt.Status != WorkspacePurgePurging || !receipt.RequestedAt.Equal(*scheduled.DeletionRequestedAt) {
					t.Fatalf("purge receipt lost identity or scheduling: started=%t err=%v", started, err)
				}
				again, started, err := source.BeginWorkspacePurge(ctx, target.Key, time.Now().Add(time.Hour))
				if err != nil || !started || !again.StartedAt.Equal(receipt.StartedAt) {
					t.Fatal("repeated purge replaced its receipt", err)
				}
				counts := map[string]int64{}
				var cacheRows int64
				if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_cache_revisions WHERE workspace_key=?`, target.Key).Scan(&cacheRows); err != nil {
					t.Fatal(err)
				}
				if err := source.RemoveWorkspaceFromCatalog(ctx, target.Key, counts); err != nil {
					t.Fatal(err)
				}
				if bounded {
					residency := source.metadataResidency
					residency.mu.Lock()
					_, policyKept := residency.policy[target.Key]
					_, stampKept := residency.stamps[target.Key]
					_, headKept := residency.applied[target.Key]
					ownedCache, unrelatedCache, summedCosts := 0, 0, 0
					for key := range residency.cache {
						if key.workspace == target.Key {
							ownedCache++
						}
						if key.workspace == primary {
							unrelatedCache++
						}
					}
					for _, cost := range residency.policy {
						summedCosts += cost
					}
					validCosts := summedCosts == residency.policyBytes
					residency.mu.Unlock()
					if policyKept || stampKept || headKept || ownedCache != 0 || unrelatedCache == 0 || !validCosts {
						t.Fatal("catalog removal retained owned residency state or removed unrelated cache data")
					}
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if err := source.PurgeWorkspaceRows(cancelled, target, counts); err == nil {
					t.Fatal("cancelled purge continued")
				}
				if err := source.PurgeWorkspaceRows(ctx, target, counts); err != nil {
					t.Fatal(err)
				}
				if counts["connector_secrets"] != 405 || counts["code_host_accounts"] != 405 || counts["issue_records"] != 1 {
					t.Fatalf("purge lost a paged set: secrets=%d accounts=%d issues=%d", counts["connector_secrets"], counts["code_host_accounts"], counts["issue_records"])
				}
				if counts["workspace_cache_revisions"] != cacheRows {
					t.Fatal("purge did not count every cache-revision row")
				}
				for _, table := range []string{"workspace_states", "workspace_metadata_records", "issue_records", "code_host_accounts"} {
					var rows int
					if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, target.Key).Scan(&rows); err != nil || rows != 0 {
						t.Fatalf("purged %s retains %d rows: %v", table, rows, err)
					}
				}
				before := make(map[string]int64, len(counts))
				for key, count := range counts {
					before[key] = count
				}
				if err := source.PurgeWorkspaceRows(ctx, target, counts); err != nil {
					t.Fatal("repeated purge failed", err)
				}
				for name, count := range counts {
					// Legacy housekeeping may recreate a cache-revision row
					// after its deletion. Repeating the purge counts that row.
					if name != "workspace_cache_revisions" && count != before[name] {
						t.Fatalf("repeated purge deleted new %s rows", name)
					}
				}
				var root []byte
				if err := f.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, primary).Scan(&root); err != nil || !bytes.Equal(primaryRoot, root) {
					t.Fatal("purge changed the unrelated workspace", err)
				}
				if bounded && (source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0) {
					t.Fatal("purge decoded complete metadata or leaked admission")
				}
				outcomes = append(outcomes, counts)
				f.coordinator = nil
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("purge side-effect counts differ: legacy=%v bounded=%v", outcomes[0], outcomes[1])
			}
		})
	}
}

func TestScaleP21SW17PurgeMissingAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	bounded, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, started, err := bounded.BeginWorkspacePurge(t.Context(), idgen.New("sw17-missing-"), time.Now()); err != nil || started {
		t.Fatal("missing workspace started a purge", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, started, err := bounded.BeginWorkspacePurge(cancelled, f.data.Workspace.URLKey, time.Now()); !errors.Is(err, context.Canceled) || started {
		t.Fatalf("cancelled identity read started=%t err=%v", started, err)
	}
	if err := bounded.PurgeWorkspaceRows(t.Context(), WorkspacePurgeTarget{}, map[string]int64{}); err == nil {
		t.Fatal("empty purge target was accepted")
	}
	if err := bounded.db.QueryRowContext(t.Context(), `SELECT workspace_key FROM workspace_states WHERE workspace_key=?`, f.data.Workspace.URLKey).Scan(new(string)); errors.Is(err, sql.ErrNoRows) {
		t.Fatal("rejected purge removed the workspace")
	}
	if bounded.WorkspaceMetadataDecodes() != 0 || bounded.metadataResidency.work.used != 0 {
		t.Fatal("failed purge decoded complete metadata or leaked admission")
	}
}
