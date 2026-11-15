package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP12GatesBoundedInheritedResourceLockOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, journal := range []string{"off", "on"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			for _, f := range scaleP12ResourceSnapshotFixtures(t) {
				view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.legacy.Workspace.URLKey)
				if err != nil {
					t.Fatal(err)
				}
				defer view.releaseMetadataLogicalSources()
				for _, path := range []string{"direct", "adapter"} {
					for _, kind := range []string{"projects", "initiatives"} {
						t.Run(path+"/"+kind, func(t *testing.T) {
							ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
							defer cancel()
							id := f.project
							if kind == "initiatives" {
								id = f.initiative
							}
							read, release := scaleP12ResourceSnapshot(t, f, ctx, "scoped", kind, id)
							defer release()
							access, err := view.ResolveAccess(read, f.legacy.Workspace.URLKey, f.member.ID, nil)
							if err != nil {
								t.Fatal(err)
							}
							locked, writerDone := make(chan struct{}), make(chan error, 1)
							go func() {
								view.mu.Lock()
								defer view.mu.Unlock()
								close(locked)
								var one int
								writerDone <- view.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
							}()
							<-locked
							_, _, closeRead, readErr := scaleP12GatesBoundedResourceCall(read, view, access, path, kind, id)
							closeRead()
							var one int
							snapshotErr := f.db.QueryRowContext(read, `SELECT 1`).Scan(&one)
							release()
							writerErr := <-writerDone
							if ctx.Err() != nil || writerErr != nil || snapshotErr != nil || one != 1 {
								t.Fatalf("bounded read waited on SQL-held mutex or closed caller snapshot: context=%v writer=%v snapshot=%v", ctx.Err(), writerErr, snapshotErr)
							}
							if journal == "on" {
								if !errors.Is(readErr, ErrRecordJournalUnavailable) {
									t.Fatalf("pool-one live authority must fail closed before mutex/target work: %v", readErr)
								}
							} else if readErr != nil {
								t.Fatal(readErr)
							}
							if view.db.Stats().MaxOpenConnections != 1 || view.metadataResidency.work.used != 0 {
								t.Fatal("bounded read changed pool size or retained output admission")
							}
						})
					}
				}
			}
		})
	}
}

func TestScaleP12GatesOwnedResourceCompatibility(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, flavor := range []string{"legacy", "fresh", "status-ordinal", "missing-status-nonempty", "missing-status-empty"} {
		t.Run(flavor, func(t *testing.T) {
			for _, f := range scaleP12ResourceSnapshotFixtures(t) {
				scaleP12GatesBoundedResourceSeed(t, &f, flavor)
				for _, sourceState := range []string{"current", "absent", "stale"} {
					t.Run(sourceState, func(t *testing.T) {
						view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.legacy.Workspace.URLKey)
						if err != nil {
							t.Fatal(err)
						}
						defer view.releaseMetadataLogicalSources()
						workspace := f.legacy.Workspace.URLKey
						if sourceState == "stale" {
							if err := f.MutateWorkspace(t.Context(), workspace, "scale_p12.bounded_stale_source", "", nil, func(data *domain.Bootstrap) error {
								data.Workspace.Name += " changed"
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							// Preserve the flavor's raw-vs-normalized distinction
							// after the real writer advances the source generation.
							scaleP12GatesBoundedResourceSeed(t, &f, flavor)
							stamp, err := readMetadataStamp(t.Context(), f.db, workspace)
							if err != nil || view.metadataResidency.logical[workspace] == nil || view.metadataResidency.logical[workspace].Stamp() == stamp {
								t.Fatalf("stale source fixture did not advance SQL authority: %v", err)
							}
						}
						if sourceState == "absent" {
							view.metadataResidency.mu.Lock()
							source := view.metadataResidency.logical[workspace]
							delete(view.metadataResidency.logical, workspace)
							view.metadataResidency.mu.Unlock()
							if source == nil {
								t.Fatal("absent source fixture had no logical source to remove")
							}
							defer func() {
								view.metadataResidency.mu.Lock()
								view.metadataResidency.logical[workspace] = source
								view.metadataResidency.mu.Unlock()
							}()
						}
						shapes := []string{"full", "scoped", "low-level"}
						if flavor == "fresh" {
							// Exact fresh bodies require the pre-SQL installed
							// compatibility witness, which these shapes capture.
							shapes = []string{"full", "scoped"}
						}
						kinds := []string{"projects", "initiatives"}
						if flavor != "legacy" && flavor != "fresh" {
							kinds = []string{"projects"}
						}
						for _, kind := range kinds {
							id := f.project
							if kind == "initiatives" {
								id = f.initiative
							}
							for _, shape := range shapes {
								for _, path := range []string{"direct", "adapter"} {
									t.Run(fmt.Sprintf("%s/%s/%s", kind, shape, path), func(t *testing.T) {
										legacyAccess, err := f.ResolveAccess(t.Context(), workspace, f.member.ID, nil)
										if err != nil {
											t.Fatal(err)
										}
										want, allowed, err := f.ResourceReadMetadata(t.Context(), legacyAccess, kind, "old-"+id)
										if err != nil || !allowed {
											t.Fatalf("legacy resource oracle failed: allowed=%v err=%v", allowed, err)
										}
										read, release := scaleP12GatesBoundedResourceSnapshot(t, f, shape, kind, id)
										defer release()
										access, err := view.ResolveAccess(read, workspace, f.member.ID, nil)
										if err != nil {
											t.Fatal(err)
										}
										inheritedAccess, err := f.ResolveAccess(read, workspace, f.member.ID, nil)
										if err != nil {
											t.Fatal(err)
										}
										inherited, inheritedAllowed, err := f.ResourceReadMetadata(read, inheritedAccess, kind, "old-"+id)
										legacyWire, legacyWireErr := json.Marshal(inherited)
										wantWire, wantWireErr := json.Marshal(want)
										if err != nil || inheritedAllowed != allowed || legacyWireErr != nil || wantWireErr != nil || !bytes.Equal(legacyWire, wantWire) {
											t.Fatalf("legacy inherited resource changed complete JSON: allowed=%v/%v read=%v marshal=%v/%v", inheritedAllowed, allowed, err, legacyWireErr, wantWireErr)
										}
										got, gotAllowed, closeRead, err := scaleP12GatesBoundedResourceCall(read, view, access, path, kind, "old-"+id)
										defer closeRead()
										if err != nil || gotAllowed != allowed {
											t.Fatalf("owned read changed result: allowed=%v/%v err=%v", gotAllowed, allowed, err)
										}
										left, leftErr := json.Marshal(want)
										right, rightErr := json.Marshal(got)
										if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
											t.Fatalf("owned path changed complete resource JSON: marshal=%v/%v\nwant=%s\ngot=%s", leftErr, rightErr, left, right)
										}
										if kind == "initiatives" && flavor == "legacy" && got.Initiatives[0].Creator.ID != f.legacy.Viewer.ID {
											t.Fatal("missing creator used principal/zero viewer instead of installed viewer")
										}
										closeRead()
										var one int
										if err := f.db.QueryRowContext(read, `SELECT 1`).Scan(&one); err != nil || one != 1 {
											t.Fatalf("owned output closed caller SQL snapshot: %v", err)
										}
										if view.metadataResidency.work.used != 0 {
											t.Fatal("owned compatibility retained output admission")
										}
									})
								}
							}
						}
					})
				}
			}
		})
	}
}

func scaleP12GatesBoundedResourceCall(ctx context.Context, view *SQLiteStore, access AccessContext, path, kind, ref string) (domain.Bootstrap, bool, func(), error) {
	if path == "direct" {
		read, err := view.BeginResourceRead(ctx, access, kind, ref)
		if err != nil {
			return domain.Bootstrap{}, false, func() {}, err
		}
		return read.Data, read.Allowed, sync.OnceFunc(read.Close), nil
	}
	var closes []func()
	ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
		closes = append(closes, close)
		return true
	})
	data, allowed, err := view.ResourceReadMetadata(ctx, access, kind, ref)
	return data, allowed, sync.OnceFunc(func() {
		for _, close := range closes {
			close()
		}
	}), err
}

func scaleP12GatesBoundedResourceSnapshot(t *testing.T, f scaleP12ResourceSnapshotFixture, shape, kind, id string) (context.Context, func()) {
	t.Helper()
	if shape != "low-level" {
		return scaleP12ResourceSnapshot(t, f, t.Context(), shape, kind, id)
	}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: f.legacy.Workspace.URLKey, reader: tx})
	return ctx, sync.OnceFunc(func() { _ = tx.Rollback() })
}

func scaleP12GatesBoundedResourceSeed(t *testing.T, f *scaleP12ResourceSnapshotFixture, flavor string) {
	t.Helper()
	workspace := f.legacy.Workspace.URLKey
	if flavor == "legacy" {
		return
	}
	if flavor == "fresh" {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p12.bounded_fresh", f.project, nil, func(data *domain.Bootstrap) error {
			for index := range data.Projects {
				if data.Projects[index].ID == f.project {
					data.Projects[index].Resources[0].PinnedTeamIDs = nil
				}
			}
			for index := range data.Initiatives {
				if data.Initiatives[index].ID == f.initiative {
					data.Initiatives[index].Creator = domain.User{}
					data.Initiatives[index].UpdateSchedule = domain.InitiativeUpdateSchedule{}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		target := f.legacy.ProjectStatuses[0]
		target.Name, target.Description, target.Position = "Embedded status retained", "Owned status body", 37
		earlier := target
		earlier.ID, earlier.Name, earlier.Position = "a-"+idgen.New("p12-owned-status-"), "Earlier status", 0
		if flavor == "status-ordinal" {
			target.ID, target.Name, target.Position = "b-"+idgen.New("p12-owned-status-"), "Target status", 0
		}
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p12.bounded_status", f.project, nil, func(data *domain.Bootstrap) error {
			data.ProjectStatuses = []domain.ProjectStatus{}
			if flavor == "status-ordinal" {
				data.ProjectStatuses = []domain.ProjectStatus{earlier, target}
			} else if flavor == "missing-status-nonempty" {
				data.ProjectStatuses = []domain.ProjectStatus{earlier}
			}
			for index := range data.Projects {
				if data.Projects[index].ID == f.project {
					data.Projects[index].Status = target
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if flavor == "status-ordinal" {
			for _, status := range []domain.ProjectStatus{earlier, target} {
				raw, err := json.Marshal(status)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=?,collection_order=500 WHERE workspace_key=? AND field='projectStatuses' AND `+f.metadataRecordKeyEquals("record_key", "?"), raw, workspace, status.ID); err != nil {
					t.Fatal(err)
				}
			}
			var raw []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND `+f.metadataRecordKeyEquals("record_key", "?"), workspace, f.project).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var project domain.Project
			if err := json.Unmarshal(raw, &project); err != nil {
				t.Fatal(err)
			}
			project.Status = target
			raw, err := json.Marshal(project)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND `+f.metadataRecordKeyEquals("record_key", "?"), raw, workspace, f.project); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='projectStatuses'`, workspace).Scan(&count); err != nil {
			t.Fatal(err)
		}
		expectedCount := 0
		if flavor == "status-ordinal" {
			expectedCount = 2
		} else if flavor == "missing-status-nonempty" {
			expectedCount = 1
		}
		if count != expectedCount {
			t.Fatalf("status fixture has %d rows, want %d", count, expectedCount)
		}
		// The complete reload is the existing product oracle, not the owned
		// compatibility implementation being tested.
		f.mu.Lock()
		f.forgetCopyRevisionLocked(workspace)
		f.mu.Unlock()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		oracle, ok := f.BootstrapFor(workspace)
		if !ok {
			t.Fatal("status oracle is missing")
		}
		found := false
		for _, project := range oracle.Projects {
			if project.ID != f.project {
				continue
			}
			found = true
			if flavor == "status-ordinal" && (project.Status.ID != target.ID || project.Status.Position != 1) {
				t.Fatal("status fixture did not normalize tied sparse orders to dense ordinal one")
			}
			if flavor != "status-ordinal" && (project.Status.Name == target.Name) != (flavor == "missing-status-nonempty") {
				t.Fatal("missing target/nonempty and genuinely empty catalog oracles must differ")
			}
		}
		if !found {
			t.Fatal("status fixture target is missing")
		}
	}
	var ok bool
	f.legacy, ok = f.BootstrapFor(workspace)
	if !ok || f.legacy.Viewer.ID == f.member.ID {
		t.Fatal("complete oracle missing or principal replaced installed viewer")
	}
	if flavor == "fresh" {
		projectFound, initiativeFound := false, false
		for _, project := range f.legacy.Projects {
			if project.ID == f.project {
				projectFound = true
				if project.Resources[0].PinnedTeamIDs != nil {
					t.Fatal("fresh project was normalized before its oracle was captured")
				}
			}
		}
		for _, initiative := range f.legacy.Initiatives {
			if initiative.ID == f.initiative {
				initiativeFound = true
				if initiative.Creator.ID != "" {
					t.Fatal("fresh initiative was normalized before its oracle was captured")
				}
			}
		}
		if !projectFound || !initiativeFound {
			t.Fatal("fresh resource fixture targets are missing")
		}
	}
}
