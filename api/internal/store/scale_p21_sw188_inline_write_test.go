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

type scaleP21SW188FieldImage struct {
	root []byte
	rows []metadataCacheRow
}

func scaleP21SW188FieldSQL(t *testing.T, ctx context.Context, tx *sqlTx, workspace, field string) scaleP21SW188FieldImage {
	t.Helper()
	var result scaleP21SW188FieldImage
	if err := tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&result.root); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key", workspace, field)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row metadataCacheRow
		if err := rows.Scan(&row.Field, &row.Key, &row.Order, &row.Data); err != nil {
			t.Fatal(err)
		}
		result.rows = append(result.rows, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW188LargeInlineWriteDifferential(t *testing.T) {
	f, _, _ := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	scope := MetadataRecordScope{Fields: []string{"projects"}}
	for _, reload := range []bool{false, true} {
		for _, operation := range []string{"edit-duplicates", "split-transition", "empty", "nil"} {
			name := operation
			if reload {
				name += "-reloaded"
			}
			t.Run(name, func(t *testing.T) {
				view, err := f.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer view.releaseMetadataLogicalSources()
				if reload {
					if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
						t.Fatal(err)
					}
				}
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				ctx, release, err := view.admitMetadataWork(t.Context(), 16<<20)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				before, err := source.Select(ctx, scope, 16<<20)
				if err != nil {
					t.Fatal(err)
				}
				next := cloneBootstrap(before)
				switch operation {
				case "edit-duplicates":
					next.Projects[0].Name, next.Projects[1].Name = "Changed first", "Changed second"
				case "split-transition":
					next.Projects = next.Projects[1:]
				case "empty":
					next.Projects = []domain.Project{}
				case "nil":
					next.Projects = nil
				}
				requested, err := metadataLogicalReplaceFields(ctx, source, next, []string{"projects"})
				if err != nil {
					t.Fatal(err)
				}
				defer requested.Release()
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				literal := cloneBootstrap(next)
				if _, err := f.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, &literal); err != nil {
					tx.Rollback()
					t.Fatal("literal field delta", err)
				}
				wantSQL := scaleP21SW188FieldSQL(t, ctx, tx, workspace, "projects")
				tx.Rollback()
				tx, err = f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				index, _, _ := metadataLogicalField("projects")
				actual, err := view.persistMetadataLogicalInlinePostimage(ctx, tx, workspace, "projects",
					requested, source.fields[index], requested.fields[index])
				if err != nil {
					tx.Rollback()
					t.Fatal("streamed inline delta", err)
				}
				gotSQL := scaleP21SW188FieldSQL(t, ctx, tx, workspace, "projects")
				got, err := actual.Select(ctx, scope, 16<<20)
				actual.Release()
				tx.Rollback()
				if err != nil || !reflect.DeepEqual(got.Projects, literal.Projects) ||
					!bytes.Equal(gotSQL.root, wantSQL.root) || !reflect.DeepEqual(gotSQL.rows, wantSQL.rows) {
					t.Fatalf("streamed inline delta changed native postimage, root or rows: err=%v rootBytes=%d/%d rows=%d/%d",
						err, len(gotSQL.root), len(wantSQL.root), len(gotSQL.rows), len(wantSQL.rows))
				}
				if view.metadataResidency.work.used != 16<<20 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatalf("streamed delta leaked work or used full hydration: work=%d", view.metadataResidency.work.used)
				}
			})
		}
	}
}

func TestScaleP21SW188LargeInlineWriteCancellation(t *testing.T) {
	f, _, original := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ctx, release, err := view.admitMetadataWork(t.Context(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	scope := MetadataRecordScope{Fields: []string{"projects"}}
	next, err := source.Select(ctx, scope, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	next.Projects[0].Name = "Must roll back"
	requested, err := metadataLogicalReplaceFields(ctx, source, next, []string{"projects"})
	if err != nil {
		t.Fatal(err)
	}
	defer requested.Release()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if len(query) >= 14 && query[:14] == "SELECT SUBSTR(" {
			chunks++
			if chunks == 2 {
				cancel()
			}
		}
	})
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	index, _, _ := metadataLogicalField("projects")
	actual, err := view.persistMetadataLogicalInlinePostimage(ctx, tx, workspace, "projects", requested,
		source.fields[index], requested.fields[index])
	if actual != nil {
		actual.Release()
	}
	tx.Rollback()
	if !errors.Is(err, context.Canceled) || actual != nil || view.metadataResidency.work.used != 16<<20 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatalf("cancelled inline write published or leaked: err=%v chunks=%d work=%d", err, chunks, view.metadataResidency.work.used)
	}
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, original) {
		t.Fatal("cancelled inline write changed SQL")
	}
}

func TestScaleP21SW188ScopedInlineDifferential(t *testing.T) {
	f, _, original := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks"), workspace, "webhook.created", "sw188-hook", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "sw188-hook", Enabled: true, URL: "https://example.test/sw188"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	initial, ok := f.storedWorkspace(workspace)
	if !ok {
		t.Fatal("missing fixture")
	}
	initial = cloneBootstrap(initial)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	displayRows := scaleP21SW188FieldSQL(t, t.Context(), tx, workspace, "projectDisplayDefault").rows
	tx.Rollback()
	for _, reload := range []bool{false, true} {
		for _, mode := range []string{"complete", "point-duplicates", "raw-json"} {
			name := mode
			if reload {
				name += "-reloaded"
			}
			t.Run(name, func(t *testing.T) {
				type outcome struct {
					root                        []byte
					projects                    []domain.Project
					display                     json.RawMessage
					payload, previous, realtime json.RawMessage
				}
				var outcomes []outcome
				for _, bounded := range []bool{false, true} {
					if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", original, workspace); err != nil {
						t.Fatal(err)
					}
					if _, err := f.db.ExecContext(t.Context(), "DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projectDisplayDefault'", workspace); err != nil {
						t.Fatal(err)
					}
					for _, row := range displayRows {
						if _, err := f.db.ExecContext(t.Context(), "INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?)",
							workspace, row.Field, row.Key, row.Order, row.Data); err != nil {
							t.Fatal(err)
						}
					}
					f.dropMetadataCache(t.Context(), workspace)
					f.mu.Lock()
					f.setWorkspaceLocked(workspace, cloneBootstrap(initial))
					f.mu.Unlock()
					repo := f.SQLiteStore
					if bounded {
						var err error
						repo, err = f.BoundedMetadataViewForTesting(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						defer repo.releaseMetadataLogicalSources()
						if reload {
							if err := repo.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
								t.Fatal(err)
							}
						}
					}
					got := outcome{}
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.payload, got.previous = slices.Clone(event.Payload), slices.Clone(event.PreviousValues)
					})
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.realtime = slices.Clone(event.Payload) })
					scope := MetadataRecordScope{Fields: []string{"projects"}}
					fields := []string{"projects"}
					if mode == "point-duplicates" {
						scope = MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "sw188-duplicate"}}}
					} else if mode == "raw-json" {
						fields = []string{"projectDisplayDefault"}
						scope = MetadataRecordScope{Fields: fields}
					}
					ctx := WithMetadataFields(t.Context(), fields...)
					err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "sw188-no-aggregate", map[string]string{"name": "Changed"},
						scope, func(data *domain.Bootstrap) error {
							for index := range data.Projects {
								if data.Projects[index].ID == "sw188-duplicate" {
									data.Projects[index].Name += " Changed"
								}
							}
							if mode == "raw-json" {
								data.ProjectDisplayDefault = json.RawMessage(`{"layout":"board","options":{"title":"<native>"}}`)
							}
							got.projects = cloneSnapshotValue(reflect.ValueOf(data.Projects)).Interface().([]domain.Project)
							got.display = slices.Clone(data.ProjectDisplayDefault)
							return nil
						})
					if err != nil {
						t.Fatal("scoped duplicate write", bounded, err)
					}
					if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&got.root); err != nil {
						t.Fatal(err)
					}
					if bounded {
						source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
						ctx, release, err := repo.admitMetadataWork(t.Context(), 16<<20)
						if err != nil {
							source.Release()
							t.Fatal(err)
						}
						installed, err := source.Select(ctx, MetadataRecordScope{Fields: fields}, 16<<20)
						source.Release()
						release()
						if err != nil || !reflect.DeepEqual(installed.Projects, got.projects) || !bytes.Equal(installed.ProjectDisplayDefault, got.display) || repo.metadataResidency.work.used != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("scoped native postimage or ownership differs", err)
						}
					}
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					outcomes = append(outcomes, got)
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatal("scoped duplicate write changed root, callback or publication effects")
				}
			})
		}
	}
}

func TestScaleP21SW188ScopedInlineCancellation(t *testing.T) {
	f, _, original := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	ctx, cancel := context.WithCancel(WithMetadataFields(t.Context(), "projects"))
	defer cancel()
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.HasPrefix(query, "UPDATE workspace_states") {
			cancel()
		}
	})
	err = view.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "sw188-no-aggregate", nil,
		MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: "sw188-duplicate"}}}, func(data *domain.Bootstrap) error {
			data.Projects[0].Name = "Must roll back"
			return nil
		})
	if !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != 0 {
		t.Fatalf("cancelled scoped inline write changed error or leaked work: %v", err)
	}
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, stored) {
		t.Fatal("cancelled scoped inline write changed SQL")
	}
}

func TestScaleP21SW188ScopedInlineReadOnlyOccurrence(t *testing.T) {
	f, _, original := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	err = view.MutateWorkspaceScoped(WithMetadataFields(t.Context(), "projects", "settings"), workspace, "application_policy.updated", "sw188-no-aggregate", nil,
		MetadataRecordScope{
			Read:  []RecordKey{{Kind: "projects", ID: "sw188-duplicate"}},
			Write: []RecordKey{{Kind: "settings", ID: "sw188-other"}},
		}, func(data *domain.Bootstrap) error {
			data.Projects[1].Name = "Undeclared second occurrence"
			return nil
		})
	if !errors.Is(err, ErrMetadataRecordQuery) || view.metadataResidency.work.used != 0 {
		t.Fatalf("read-only duplicate occurrence changed or leaked work: %v", err)
	}
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, original) {
		t.Fatal("read-only duplicate occurrence reached SQL")
	}
}

func TestScaleP21SW188InlineRootSQLBound(t *testing.T) {
	f, view, raw := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	counts := [2]int{}
	headers := [2]map[string]metadataCollectionRange{}
	for index := range counts {
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
				counts[index]++
			}
		})
		tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			// The plain scanner rereads each short key after it has skipped
			// that property's complete value (served from the read windows).
			snapshot := &metadataCollectionSnapshot{store: view, tx: tx, workspace: workspace}
			snapshot.root = &metadataSnapshotReaderAt{ctx: ctx, snapshot: snapshot, size: int64(len(raw))}
			read := &MetadataCollectionRead{ctx: ctx, snapshot: snapshot, rootSize: int64(len(raw))}
			headers[index] = map[string]metadataCollectionRange{}
			scanner := read.scanner(metadataCollectionRange{size: int64(len(raw))})
			err = scanner.object(func(key, value metadataCollectionRange) error {
				name, err := read.shortString(key)
				headers[index][name] = value
				return err
			}, 0)
			if err == nil {
				err = scanner.end()
			}
		} else {
			var read *metadataInlineRoot
			read, err = view.scanMetadataInlineRoot(ctx, tx, workspace, int64(len(raw)))
			if err == nil {
				headers[index] = read.fields
				read.Close()
			}
		}
		tx.Rollback()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Both readers now go through the transaction's read windows: the raw
	// scanner no longer rereads each short key through SQL either, so the
	// inline page cache cannot issue fewer reads than it. Each must stay one
	// pass over the root's windows (each SUBSTR loads the whole row, so the
	// read count is what makes the scan O(root) rather than O(root^2)).
	width := metadataReadWindowWidth(int64(len(raw)))
	windows := (len(raw) + width - 1) / width
	if !reflect.DeepEqual(headers[0], headers[1]) || counts[1] > counts[0] || counts[0] > windows+6 || counts[1] > windows+6 {
		t.Fatalf("cached root changed headers or query bound: before=%d after=%d windows=%d", counts[0], counts[1], windows)
	}
	t.Logf("same %d-byte root, %d fields: root SQL reads raw=%d inline=%d; window=%d bytes, bound=ceil(root/window)+6",
		len(raw), len(headers[0]), counts[0], counts[1], width)
}
