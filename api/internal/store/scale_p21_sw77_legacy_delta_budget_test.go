package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW77SQLImage struct {
	WorkspaceID string
	Root        string
	Rows        map[metadataRecordKey]metadataRecord
}

func scaleP21SW77Image(t *testing.T, tx *sqlTx, workspace string) scaleP21SW77SQLImage {
	t.Helper()
	image := scaleP21SW77SQLImage{Rows: map[metadataRecordKey]metadataRecord{}}
	var root []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&image.WorkspaceID, &root); err != nil {
		t.Fatal(err)
	}
	image.Root = string(root)
	rows, err := tx.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key metadataRecordKey
		var value metadataRecord
		if err := rows.Scan(&key.field, &key.key, &value.order, &value.data); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		image.Rows[key] = value
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return image
}

func scaleP21SW77Fixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, domain.Bootstrap) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	f.db.SetMaxOpenConns(1)
	before := cloneBootstrap(collectionMetadata(f.data))
	first := before.Viewer
	first.Name, first.DisplayName = "Native first", "Native display"
	before.Viewer = first
	before.Users = []domain.User{first, {ID: idgen.New("sw77-second-")}}
	return f, view, before
}

func scaleP21SW77Begin(t *testing.T, f scaleP09Fixture, before domain.Bootstrap) (*sqlTx, context.Context) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: before.Workspace.URLKey, reader: tx})
	raw, err := f.encodeWorkspaceMetadata(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceMetadata(ctx, tx, before.Workspace.URLKey, before.Workspace.ID, raw); err != nil {
		t.Fatal(err)
	}
	return tx, ctx
}

func scaleP21SW77Put(t *testing.T, tx *sqlTx, workspace, key string, user domain.User, order int) []byte {
	t.Helper()
	raw, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"users", key}, metadataRecord{data: raw, order: order}, nil); err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW77Usable(t *testing.T, tx *sqlTx) {
	t.Helper()
	var one int
	if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal("delta read guard consumed the borrowed transaction", err)
	}
}

func TestScaleP21SW77LegacyDeltaParity(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	workspace := before.Workspace.URLKey
	for _, variant := range []string{"sparse-headers", "append", "reorder-sync", "canonical-peer", "unknown-canonical", "deleted-canonical"} {
		t.Run(variant, func(t *testing.T) {
			type outcome struct {
				Written bool
				Native  domain.Bootstrap
				SQL     scaleP21SW77SQLImage
			}
			var outcomes []outcome
			for _, guarded := range []bool{false, true} {
				tx, ctx := scaleP21SW77Begin(t, f, before)
				peer := before.Users[1]
				peer.Name, peer.JobTitle = "Canonical second", "Canonical private field"
				scaleP21SW77Put(t, tx, workspace, peer.ID, peer, 41)
				if variant == "canonical-peer" {
					peer := before.Users[0]
					peer.JobTitle = "Peer-only field"
					scaleP21SW77Put(t, tx, workspace, peer.ID, peer, 19)
				}
				unknown := "sw77-unknown"
				if variant == "unknown-canonical" {
					scaleP21SW77Put(t, tx, workspace, unknown, domain.User{ID: unknown, Name: "Unknown canonical row", JobTitle: strings.Repeat("unknown raw ", 1000)}, 103)
				}
				if variant == "deleted-canonical" {
					if err := deleteMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{"users", before.Users[1].ID}); err != nil {
						t.Fatal(err)
					}
				}
				target := cloneBootstrap(before)
				target.Users[0].DisplayName = "Callback display"
				target.Viewer.Name, target.Viewer.DisplayName = "Callback viewer", "Callback viewer display"
				if variant == "append" {
					target.Users = append(target.Users, domain.User{ID: "sw77-appended", DisplayName: "Appended actor"})
				}
				if variant == "reorder-sync" || variant == "unknown-canonical" {
					slices.Reverse(target.Users)
				}
				owner := &ResourceRead{store: view}
				defer owner.Close()
				arrayHeaders := 0
				if guarded {
					var size int64
					if err := tx.QueryRowContext(ctx, `SELECT `+owner.byteLength("data")+` FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&size); err != nil {
						t.Fatal(err)
					}
					if err := owner.reserve(ctx, uint64(size)*32+64<<10); err != nil {
						t.Fatal(err)
					}
					ctx = WithSQLStatementObserver(ctx, func(query string) {
						if strings.Contains(query, "FROM workspace_metadata_records") && strings.Contains(query, "THEN record_key ELSE NULL END") &&
							!strings.Contains(query, "THEN data ELSE NULL END") && !strings.Contains(query, "THEN field ELSE NULL END") {
							arrayHeaders++
						}
					})
					ctx = withLegacyMetadataDeltaReadBudget(ctx, owner)
				}
				written, err := f.writeWorkspaceMetadataDelta(ctx, tx, workspace, cloneBootstrap(before), &target)
				if err != nil {
					owner.Close()
					t.Fatal("legacy/guarded metadata delta failed", guarded, err)
				}
				if guarded && view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("canonical rows were returned without an owner")
				}
				if guarded && variant == "append" && arrayHeaders == 0 {
					t.Fatal("array delta did not guard actual order-header reads")
				}
				image := scaleP21SW77Image(t, tx, workspace)
				if variant == "unknown-canonical" && !bytes.Contains(image.Rows[metadataRecordKey{"users", unknown}].data, []byte("Unknown canonical row")) {
					t.Fatal("unknown canonical row was discarded")
				}
				outcomes = append(outcomes, outcome{written, target, image})
				owner.Close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("metadata delta leaked guard work or decoded complete metadata")
				}
				scaleP21SW77Usable(t, tx)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("guard changed literal native target, root or canonical records", variant)
			}
		})
	}
}

func TestScaleP21SW77LegacyDeltaSelectedLocks(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	workspace := before.Workspace.URLKey
	tx, ctx := scaleP21SW77Begin(t, f, before)
	first := before.Users[0]
	scaleP21SW77Put(t, tx, workspace, first.ID, first, 73)
	scaleP21SW77Put(t, tx, workspace, "sw77-unselected", domain.User{ID: "sw77-unselected", JobTitle: "Not selected"}, 91)
	for _, keys := range [][]string{nil, {"missing"}, {first.ID}, {before.Users[1].ID, first.ID, first.ID}} {
		tx.journalOrder = recordJournalOrder{}
		want, err := lockMetadataRecords(ctx, tx, workspace, "users", keys)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder := tx.journalOrder
		tx.journalOrder = recordJournalOrder{}
		owner := &ResourceRead{store: view}
		defer owner.Close()
		guardedQueries := 0
		observed := WithSQLStatementObserver(ctx, func(query string) {
			if strings.Contains(query, "FROM workspace_metadata_records") && strings.Contains(query, "THEN data ELSE NULL END") {
				guardedQueries++
				if !strings.Contains(query, "THEN record_key ELSE NULL END") || !strings.Contains(query, "ORDER BY record_key") {
					t.Fatal("selected locking read lost its key guard or literal order")
				}
			}
		})
		got, err := lockMetadataRecords(withLegacyMetadataDeltaReadBudget(observed, owner), tx, workspace, "users", keys)
		if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(tx.journalOrder, wantOrder) {
			owner.Close()
			t.Fatal("selected rows or remembered journal order differ", keys, err)
		}
		if len(keys) > 0 && guardedQueries == 0 {
			t.Fatal("selected payload entered the driver without a SQL guard")
		}
		owner.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("selected locking read retained work after owner close")
		}
	}
	scaleP21SW77Usable(t, tx)
}

func TestScaleP21SW77LegacyDeltaActualReadChanges(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	workspace := before.Workspace.URLKey
	for _, variant := range []string{"append-after-preflight", "headers-exceed-earlier-count", "selected-body-grows", "unknown-body-grows", "cancel-after-preflight"} {
		t.Run(variant, func(t *testing.T) {
			tx, ctx := scaleP21SW77Begin(t, f, before)
			known := map[metadataRecordKey]metadataRecord{}
			for index, user := range before.Users {
				raw := scaleP21SW77Put(t, tx, workspace, user.ID, user, index)
				known[metadataRecordKey{"users", user.ID}] = metadataRecord{data: raw, order: index}
			}
			unknown := "sw77-dynamic-unknown"
			if variant == "unknown-body-grows" {
				scaleP21SW77Put(t, tx, workspace, unknown, domain.User{ID: unknown, Name: "Before size read"}, 7)
			}
			var oldCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='users'`, workspace).Scan(&oldCount); err != nil {
				t.Fatal(err)
			}
			budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			defer func() { budget.limit = limit }()
			if variant == "headers-exceed-earlier-count" {
				budget.limit = MetadataRecordPageMaxBytes*4 + (64 << 10) + (192 << 10)
			}
			owner := &ResourceRead{store: view}
			defer owner.Close()
			readCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			fired := false
			grown := before.Users[0]
			grown.JobTitle = strings.Repeat("Canonical body grew after preflight. ", 1000)
			observed := WithSQLStatementObserver(readCtx, func(query string) {
				if fired || !strings.Contains(query, "FROM workspace_metadata_records") {
					return
				}
				headerRead := strings.Contains(query, "THEN field ELSE NULL END")
				bodyRead := strings.Contains(query, "THEN data ELSE NULL END")
				if (variant == "append-after-preflight" || variant == "headers-exceed-earlier-count") && headerRead {
					fired = true
					count := 1
					if variant == "headers-exceed-earlier-count" {
						count = 256
					}
					for index := range count {
						key := unknown
						if index > 0 {
							key = idgen.New("sw77-extra-")
						}
						scaleP21SW77Put(t, tx, workspace, key, domain.User{ID: key, Name: "Added after preflight"}, index+7)
					}
				} else if variant == "selected-body-grows" && bodyRead && strings.Contains(query, "collection_order") {
					fired = true
					scaleP21SW77Put(t, tx, workspace, before.Users[0].ID, grown, 0)
				} else if variant == "unknown-body-grows" && bodyRead && !strings.Contains(query, "collection_order") {
					fired = true
					grown.ID = unknown
					scaleP21SW77Put(t, tx, workspace, unknown, grown, 7)
				} else if variant == "cancel-after-preflight" && bodyRead && strings.Contains(query, "collection_order") {
					fired = true
					cancel()
				}
			})
			guarded := withLegacyMetadataDeltaReadBudget(observed, owner)
			var err error
			if variant == "selected-body-grows" || variant == "cancel-after-preflight" {
				_, err = lockMetadataRecords(guarded, tx, workspace, "users", []string{before.Users[0].ID})
			} else {
				err = syncMetadataRecords(guarded, tx, workspace, newIssueReferences(before), map[string]string{"users": "array"}, known, []string{"users"}, known)
			}
			if !fired {
				t.Fatal("actual-read injection was not reached", variant)
			}
			switch variant {
			case "append-after-preflight":
				if err != nil {
					t.Fatal(err)
				}
				var current int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='users'`, workspace).Scan(&current); err != nil || current != oldCount+1 {
					t.Fatal("new unknown canonical row was lost", oldCount, current, err)
				}
			case "headers-exceed-earlier-count":
				if !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("actual header growth escaped the owner budget", err)
				}
			case "cancel-after-preflight":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled actual read changed its error", err)
				}
			default:
				if !errors.Is(err, ErrConcurrentWrite) {
					t.Fatal("post-size canonical growth did not request retry", variant, err)
				}
			}
			if view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("failed or successful read released its still-owned work early")
			}
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("actual-read failure leaked its dynamic reservations")
			}
			scaleP21SW77Usable(t, tx)
		})
	}
}

func TestScaleP21SW77LegacyDeltaBudgetCancellation(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	workspace := before.Workspace.URLKey
	tx, ctx := scaleP21SW77Begin(t, f, before)
	user := before.Users[0]
	user.JobTitle = strings.Repeat("Large canonical user body. ", 20000)
	scaleP21SW77Put(t, tx, workspace, user.ID, user, 0)
	image := scaleP21SW77Image(t, tx, workspace)
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	defer func() { budget.limit = limit }()
	budget.limit = 8 << 20
	owner := &ResourceRead{store: view}
	defer owner.Close()
	got, err := lockMetadataRecords(withLegacyMetadataDeltaReadBudget(ctx, owner), tx, workspace, "users", []string{user.ID})
	if !errors.Is(err, ErrMetadataWorkBudget) || got != nil {
		owner.Close()
		t.Fatal("large canonical JSON escaped actual-read admission", err)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(scaleP21SW77Image(t, tx, workspace), image) {
		t.Fatal("failed read changed canonical rows or leaked work")
	}
	budget.limit = limit
	owner = &ResourceRead{store: view}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := lockMetadataRecords(withLegacyMetadataDeltaReadBudget(canceled, owner), tx, workspace, "users", []string{user.ID}); !errors.Is(err, context.Canceled) {
		owner.Close()
		t.Fatal("canceled canonical read changed its error", err)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled canonical read retained work")
	}
	owner = &ResourceRead{store: view}
	owner.Close()
	if _, err := lockMetadataRecords(withLegacyMetadataDeltaReadBudget(ctx, owner), tx, workspace, "users", []string{user.ID}); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed owner accepted another canonical read", err)
	}
	owner = &ResourceRead{store: view}
	budget.limit = 64 << 10
	guard := legacyMetadataDeltaBudget(withLegacyMetadataDeltaReadBudget(ctx, owner))
	if _, err := guard.header(ctx, sql.NullString{String: "users", Valid: true}, sql.NullString{String: strings.Repeat("k", MetadataRecordPageMaxBytes), Valid: true}); !errors.Is(err, ErrMetadataWorkBudget) {
		owner.Close()
		t.Fatal("oversized physical header escaped dynamic key admission", err)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized header retained work after close")
	}
	scaleP21SW77Usable(t, tx)
}
