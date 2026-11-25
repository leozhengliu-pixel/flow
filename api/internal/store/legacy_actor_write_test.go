package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW76Metadata struct {
	WorkspaceID string
	Root        string
	Rows        []scaleP21SW23MetadataRow
}

type scaleP21SW76MetadataReader interface {
	metadataReader
	metadataRowReader
}

func scaleP21SW76MetadataImage(t *testing.T, reader scaleP21SW76MetadataReader, workspace string) scaleP21SW76Metadata {
	t.Helper()
	result := scaleP21SW76Metadata{Rows: []scaleP21SW23MetadataRow{}}
	var raw []byte
	if err := reader.QueryRowContext(t.Context(), `SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&result.WorkspaceID, &raw); err != nil {
		t.Fatal(err)
	}
	result.Root = string(raw)
	rows, err := reader.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row scaleP21SW23MetadataRow
		if err := rows.Scan(&row.Field, &row.Key, &row.Order, &row.Data); err != nil {
			t.Fatal(err)
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW76Begin(t *testing.T, f scaleP09Fixture) *sqlTx {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func scaleP21SW76StagePeer(t *testing.T, tx *sqlTx, workspace, mode string, before domain.Bootstrap, actor *domain.User) {
	t.Helper()
	if mode == "" {
		return
	}
	if actor == nil {
		t.Fatal("peer fixture needs an actor")
	}
	if mode == "actor-deleted" {
		if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"users", actor.ID}); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "gapped-order" || mode == "unknown-rows-order" {
		for index, user := range before.Users {
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field='users' AND record_key=?`,
				17+index*31, workspace, user.ID); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "gapped-order" {
			return
		}
		raw := []byte(`{"id":"sw76-peer-only","name":"Unknown canonical user","jobTitle":"Peer-only body","peerUnknown":{"keep":true}}`)
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"users", "sw76-peer-only"}, metadataRecord{data: raw, order: -13}, nil); err != nil {
			t.Fatal(err)
		}
		var rootRaw []byte
		if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootRaw); err != nil {
			t.Fatal(err)
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(rootRaw, &root); err != nil {
			t.Fatal(err)
		}
		root["sw76-peer-root"] = json.RawMessage(`{"unknown":"retained"}`)
		rootRaw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, rootRaw, workspace); err != nil {
			t.Fatal(err)
		}
		return
	}
	user := scaleP21SW69OwnedActor(*actor)
	if mode != "actor-already-added" {
		found := false
		for _, candidate := range before.Users {
			if candidate.ID == actor.ID {
				user, found = scaleP21SW69OwnedActor(candidate), true
				break
			}
		}
		if !found {
			t.Fatal("canonical actor peer fixture is missing its native base")
		}
	}
	switch mode {
	case "actor-merged":
		user.JobTitle = "Canonical peer job title"
	case "actor-conflict":
		user.Name = "Canonical conflicting name"
	case "actor-already-added":
		user.Name, user.JobTitle = "Canonical added actor", "Canonical added body"
	default:
		t.Fatal("unknown canonical peer fixture", mode)
	}
	raw, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "actor-merged" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["peerUnknown"] = json.RawMessage(`{"keep":"canonical unknown member"}`)
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"users", actor.ID}, metadataRecord{data: raw, order: 73}, nil); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW76Compare(t *testing.T, f scaleP09Fixture, view *SQLiteStore, source *metadataLogicalSource, actor *domain.User, peer string, releaseInput func()) (int, int) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	scope := scaleP21SW72FullScope(t, source)
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	var originalActor *domain.User
	if actor != nil {
		value := scaleP21SW69OwnedActor(*actor)
		originalActor = &value
	}
	want := scaleP21SW69LiteralActorStep(before, actor)
	tx := scaleP21SW76Begin(t, f)
	scaleP21SW76StagePeer(t, tx, workspace, peer, before, actor)
	preimage := scaleP21SW76MetadataImage(t, tx, workspace)
	_, wantErr := f.writeWorkspaceMetadataDelta(t.Context(), tx, workspace, before, &want)
	wantSQL := scaleP21SW76MetadataImage(t, tx, workspace)
	scaleP21SW70Usable(t, tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx = scaleP21SW76Begin(t, f)
	scaleP21SW76StagePeer(t, tx, workspace, peer, before, actor)
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, preimage) {
		t.Fatal("literal and bounded actor writes did not start from the same SQL preimage")
	}
	peak := 0
	ctx, cancel := context.WithCancel(WithSQLStatementObserver(t.Context(), func(string) {
		peak = max(peak, view.MetadataWorkUsedForTesting())
	}))
	defer cancel()
	image, gotErr := view.persistLegacyActorImage(ctx, tx, workspace, source, actor)
	if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
		if image != nil {
			image.Close()
		}
		t.Fatalf("literal/bounded actor write errors differ: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
	}
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, wantSQL) {
		if image != nil {
			image.Close()
		}
		t.Fatal("full canonical root, metadata rows or user collection orders differ")
	}
	scaleP21SW72Compare(t, source, scope, before)
	if !reflect.DeepEqual(actor, originalActor) {
		t.Fatal("actor persistence mutated caller-owned slices, pointers or Via")
	}
	scaleP21SW70Usable(t, tx)
	if wantErr != nil {
		if image != nil {
			image.Close()
			t.Fatal("failed actor persistence exposed a partial native postimage")
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("failed actor persistence leaked work")
		}
		return peak, 0
	}
	if image == nil || image.Source() == nil {
		t.Fatal("actor persistence omitted its native postimage")
	}
	defer image.Close()
	scaleP21SW72Compare(t, image.Source(), scope, want)
	if image.Source().Stamp() != source.Stamp() || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("actor persistence changed metadata generation or decoded full workspace metadata")
	}
	held := view.MetadataWorkUsedForTesting()
	if actor != nil && held == 0 {
		t.Fatal("returned actor postimage has no owned allowance")
	}
	releaseInput()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	cancel()
	scaleP21SW72Compare(t, image.Source(), scope, want)
	if view.MetadataWorkUsedForTesting() != held {
		t.Fatal("rollback or cancellation discarded the owned actor postimage")
	}
	image.Close()
	image.Close()
	if image.Source() != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("actor postimage close retained source or work")
	}
	return peak, held
}

func TestScaleP21SW76LegacyActorWriteLiteral(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	at := time.Now()
	for _, test := range []struct {
		name, shape, peer string
		append, noActor   bool
	}{
		{name: "nil-actor", noActor: true},
		{name: "first-replace"},
		{name: "append-missing", append: true},
		{name: "append-with-canonical-order-gaps", append: true, peer: "gapped-order"},
		{name: "first-duplicate", shape: "duplicate"},
		{name: "first-empty-id", shape: "empty-id"},
		{name: "long-id-inline", shape: "long-id"},
		{name: "append-nil-users", shape: "nil", append: true},
		{name: "append-empty-users", shape: "empty", append: true},
		{name: "nil-actor-nil-users", shape: "nil", noActor: true},
		{name: "canonical-actor-merged", peer: "actor-merged"},
		{name: "canonical-actor-conflict", peer: "actor-conflict"},
		{name: "canonical-actor-deleted", peer: "actor-deleted"},
		{name: "canonical-appended-actor-already-added", append: true, peer: "actor-already-added"},
		{name: "unknown-canonical-row-root-and-user-orders", peer: "unknown-rows-order"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := f.data.Viewer.ID
			if test.shape == "empty-id" {
				id = ""
			} else if test.shape == "long-id" {
				id = strings.Repeat("actor", 40)
			}
			base := domain.User{ID: id, Name: "Original actor", DisplayName: "Original display", Email: f.data.Viewer.Email, Active: true,
				JobTitle: "Original actor body", AppScopes: []string{"read"}, AppTeamIDs: []string{}, OutOfOfficeUntil: &at}
			data := domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "unchanged-role",
				Users:     []domain.User{{ID: "sw76-first", Name: "First user"}, base, {ID: "sw76-last", Name: "Last user", JobTitle: "Unchanged body"}},
				Documents: []domain.Document{{ID: "sw76-document", Content: "Unchanged document body"}},
				Projects:  []domain.Project{{ID: "sw76-project", IssueCount: 913}},
				Settings:  map[string]any{"sw76-native": map[string]any{"integer": int64(9007199254740993), "nil": []string(nil), "empty": []string{}}},
			}
			if test.shape == "duplicate" {
				data.Users = append(data.Users, domain.User{ID: id, Name: "Later duplicate actor", JobTitle: "Later duplicate body"})
			} else if test.shape == "nil" {
				data.Users = nil
			} else if test.shape == "empty" {
				data.Users = []domain.User{}
			}
			if test.append && test.shape != "nil" && test.shape != "empty" {
				data.Users = []domain.User{data.Users[0], data.Users[len(data.Users)-1]}
			}
			input := scaleP21SW69OwnedActor(base)
			input.Name, input.DisplayName = "Fresh actor", "Fresh display"
			input.Via = &domain.ActorBot{ID: "sw76-application", Type: domain.ActorBotTypeOAuthApplication, Name: "Native Via", OnBehalfOfID: "person"}
			if test.append {
				input.ID = "sw76-appended"
			}
			var actor *domain.User
			if !test.noActor {
				actor = &input
			}
			writeFullMetadata(t, f.SQLiteStore, f.data.Workspace.URLKey, data)
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			sourceOwned := true
			defer func() {
				if sourceOwned {
					source.Release()
				}
			}()
			f.db.SetMaxOpenConns(1)
			scaleP21SW76Compare(t, f, view, source, actor, test.peer, func() {
				source.Release()
				sourceOwned = false
			})
			f.db.SetMaxOpenConns(4)
		})
	}
}

func TestScaleP21SW76LegacyActorWriteWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	peaks, outputs := map[bool]int{}, map[bool]int{}
	for _, size := range []int{2000, 20000} {
		f.db.SetMaxOpenConns(4)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "documents", "settings"), workspace, "scale_p21.sw76_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Users = []domain.User{
				{ID: data.Viewer.ID, Name: "Original actor", JobTitle: "Small actor body", Active: true},
				{ID: "sw76-body-user", Name: "Unchanged user", JobTitle: strings.Repeat("Known unchanged user body. ", size)},
				{ID: "sw76-other-user", Name: "Other unchanged user", JobTitle: strings.Repeat("Other known unchanged user body. ", size)},
			}
			data.Documents = []domain.Document{{ID: "sw76-unrelated", Content: strings.Repeat("Unrelated document body. ", size)}}
			data.Settings["sw76-unrelated"] = strings.Repeat("Unrelated settings body. ", size)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				f.db.SetMaxOpenConns(4)
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				sourceOwned := true
				defer func() {
					if sourceOwned {
						source.Release()
					}
				}()
				before, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				var original *domain.User
				for index := range before.Users {
					if before.Users[index].ID == f.data.Viewer.ID {
						original = &before.Users[index]
						break
					}
				}
				if original == nil {
					t.Fatal("small actor fixture missing from installed source")
				}
				input := scaleP21SW69OwnedActor(*original)
				input.Name = "Fresh actor"
				input.Via = &domain.ActorBot{ID: "sw76-body-app", Name: "Native Via"}
				f.db.SetMaxOpenConns(1)
				peak, held := scaleP21SW76Compare(t, f, view, source, &input, "", func() {
					source.Release()
					sourceOwned = false
				})
				if peak == 0 || held == 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("actor write was unowned or hydrated complete workspace metadata", peak, held)
				}
				if peaks[reload] == 0 {
					peaks[reload], outputs[reload] = peak, held
				}
				if peak != peaks[reload] || held != outputs[reload] {
					t.Fatal("10x known unchanged bodies increased actor write/output work", reload, peak, peaks[reload], held, outputs[reload])
				}
				t.Logf("body_size=%d reload=%t write_sql_peak=%d output_work=%d", size, reload, peak, held)
			})
		}
	}
}

func TestScaleP21SW76LegacyActorWriteFailureRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	data := domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer, Users: []domain.User{f.data.Viewer}, Settings: map[string]any{"unchanged": true}}
	writeFullMetadata(t, f.SQLiteStore, workspace, data)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := scaleP21SW72FullScope(t, source)
	nativeBefore, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	actor := scaleP21SW69OwnedActor(f.data.Viewer)
	actor.Name, actor.Via = "Fresh actor", &domain.ActorBot{ID: "sw76-failure-app", Name: "Native Via"}
	for _, failure := range []string{"cancel-before", "cancel-after-start", "quota"} {
		t.Run(failure, func(t *testing.T) {
			f.db.SetMaxOpenConns(1)
			tx := scaleP21SW76Begin(t, f)
			before := scaleP21SW76MetadataImage(t, tx, workspace)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var want error = context.Canceled
			if failure == "cancel-before" {
				cancel()
			} else if failure == "cancel-after-start" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					lower := strings.ToLower(query)
					if strings.Contains(lower, "from workspace_states") || strings.Contains(lower, "from workspace_metadata_records") {
						cancel()
					}
				})
			}
			budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			if failure == "quota" {
				budget.limit, want = 1, ErrMetadataWorkBudget
			}
			defer func() { budget.limit = limit }()
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			image, err := view.persistLegacyActorImage(ctx, tx, workspace, source, &actor)
			budget.limit = limit
			if image != nil {
				image.Close()
				t.Fatal("failed actor write exposed a native postimage")
			}
			if !errors.Is(err, want) || view.MetadataWorkUsedForTesting() != 0 ||
				metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("failed actor write changed its error or retained source/work", failure, err)
			}
			if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, before) {
				t.Fatal("preflight failure wrote canonical actor metadata", failure)
			}
			scaleP21SW72Compare(t, source, scope, nativeBefore)
			scaleP21SW70Usable(t, tx)
		})
	}
}
