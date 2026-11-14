package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW0SnapshotFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved, workspace); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('users','teamSettings','workspaceSettings')`, workspace); err != nil {
		t.Fatal(err)
	}
	root := `{"workspace":{"id":"original","name":"retained"},"WORKSPACE":{"id":"final"},"users":[{"id":"inline","name":"retained"}],"USERS":[{"id":"repeated"}],"teamSettings":{"discarded":{}},"TeamSettings":null,"TEAMSETTINGS":{"same":{"teamId":"old"}},"workspaceSettings":{"name":"inline"},"WORKSPACESETTINGS":null,"_flowCollections":{"users":"array","teamSettings":"map","workspaceSettings":"map"}}`
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(root), workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ kind, key, raw string }{
		{"users", "row", `{"id":"canonical","name":"` + strings.Repeat("unicode 名字 ", 4000) + `"}`},
		{"teamSettings", "same", `{"teamId":"canonical"}`},
		{"workspaceSettings", "name", `"canonical"`},
	} {
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, workspace, row.kind, row.key, []byte(row.raw)); err != nil {
			t.Fatal(err)
		}
	}
	return f, view
}

func TestScaleP21SW0SnapshotPoolOneReplayAndOwnership(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	f.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ctx, release, err := view.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := recordSpoolBytes.Load()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "1")
	read, err := view.MetadataCollectionsInSnapshot(ctx, tx, f.data.Workspace.URLKey, "users", "teamSettings", "workspaceSettings")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if ctx.Value(workspaceReadKey{}) != nil {
		t.Fatal("constructor changed caller context")
	}
	parentLease := ctx.Value(metadataWorkLeaseKey{})
	if read.root != nil || read.data != nil || recordSpoolBytes.Load() != before {
		t.Fatal("borrowed reader created source spools")
	}
	stamp, err := readMetadataStamp(ctx, tx, f.data.Workspace.URLKey)
	if err != nil || stamp != read.stamp {
		t.Fatalf("stamp=%+v expected=%+v error=%v", read.stamp, stamp, err)
	}
	for range 2 {
		var users []domain.User
		err = read.VisitArrayOccurrences("users", func(ctx context.Context, event MetadataArrayOccurrence) error {
			pinned, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
			if !ok || pinned.database != view.db || pinned.reader != tx || pinned.workspace != f.data.Workspace.URLKey {
				t.Fatal("callback context lost borrowed snapshot")
			}
			if ctx.Value(metadataWorkLeaseKey{}) != parentLease {
				t.Fatal("callback context lost parent lease")
			}
			var one int
			if err := view.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
				return err
			}
			if _, err := view.LiveEventContext(ctx); !errors.Is(err, ErrRecordJournalUnavailable) {
				t.Fatalf("live validation failed to detect sole pinned connection: %v", err)
			}
			switch {
			case event.Null:
				users = nil
			case event.Begin:
			case event.End:
				users = users[:event.Ordinal]
			default:
				if event.Append || event.Ordinal >= int64(len(users)) {
					users = append(users, domain.User{})
				}
				input := event.Reader.(io.ReadSeeker)
				first, err := io.ReadAll(input)
				if err != nil {
					return err
				}
				if _, err := input.Seek(0, io.SeekStart); err != nil {
					return err
				}
				second, err := io.ReadAll(input)
				if err != nil || string(first) != string(second) {
					t.Fatalf("rewind changed bytes: %v", err)
				}
				head := make([]byte, 1)
				if _, err := event.Reader.(io.ReaderAt).ReadAt(head, 0); err != nil || head[0] != first[0] {
					t.Fatalf("ReadAt failed: %v", err)
				}
				replay, err := ReplayMetadataReader(event.Reader, event.EncodedBytes)
				if err != nil {
					return err
				}
				return json.NewDecoder(replay).Decode(&users[event.Ordinal])
			}
			return nil
		})
		if err != nil || len(users) != 2 || users[0].ID != "repeated" || users[0].Name != "retained" || users[1].ID != "canonical" || len(users[1].Name) < 50000 {
			t.Fatalf("users=%#v error=%v", users, err)
		}
		var workspace domain.Workspace
		found, err := read.VisitRootFieldOccurrences("workspace", func(_ context.Context, _ int64, input io.Reader) error {
			return json.NewDecoder(input).Decode(&workspace)
		})
		if err != nil || !found || workspace.ID != "final" || workspace.Name != "retained" {
			t.Fatalf("workspace=%+v found=%v error=%v", workspace, found, err)
		}
		maps := map[string]map[string]json.RawMessage{}
		for _, kind := range []string{"teamSettings", "workspaceSettings"} {
			var values map[string]json.RawMessage
			if err := read.VisitMap(kind, func(_ context.Context, event MetadataMapOccurrence) error {
				if event.Reset {
					values = nil
					return nil
				}
				if event.BeginObject {
					if values == nil {
						values = map[string]json.RawMessage{}
					}
					return nil
				}
				key := event.Key
				if !event.CanonicalKey {
					if err := json.NewDecoder(event.RawKey).Decode(&key); err != nil {
						return err
					}
				}
				var value json.RawMessage
				if err := json.NewDecoder(event.Reader).Decode(&value); err != nil {
					return err
				}
				values[key] = value
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			maps[kind] = values
		}
		if string(maps["teamSettings"]["same"]) != `{"teamId":"canonical"}` || string(maps["workspaceSettings"]["name"]) != `"canonical"` {
			t.Fatalf("maps=%v", maps)
		}
	}
	if err := read.Close(); err != nil || recordSpoolBytes.Load() != before {
		t.Fatalf("close=%v spool before=%d after=%d", err, before, recordSpoolBytes.Load())
	}
	var one int
	if err := tx.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("Close ended caller transaction: %v", err)
	}
	view.metadataResidency.work.mu.Lock()
	live := view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	if live != 2<<20 {
		t.Fatalf("Close released caller lease: %d", live)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := view.LiveEventContext(ctx); err != nil {
		t.Fatalf("original context cannot validate after transaction close: %v", err)
	}
	if err := view.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("original context retained closed transaction: %v", err)
	}
}

func TestScaleP21SW0SnapshotRootOnly(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	workspace := f.data.Workspace.URLKey
	// Root-only callers do not apply unselected collection type validation.
	root := []byte(`{"workspace":{"id":"original","name":"retained"},"WORKSPACE":{"id":"final"},"users":17}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, root, workspace); err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	read, err := view.MetadataCollectionsInSnapshot(ctx, tx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var actual domain.Workspace
	found, err := read.VisitRootFieldOccurrences("workspace", func(_ context.Context, _ int64, input io.Reader) error {
		return json.NewDecoder(input).Decode(&actual)
	})
	if err != nil || !found || actual.ID != "final" || actual.Name != "retained" {
		t.Fatalf("workspace=%+v found=%v error=%v", actual, found, err)
	}
	if _, err := read.Shape("users"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatalf("unselected collection was available: %v", err)
	}
}

func TestScaleP21SW0SnapshotPhysicalNULWorkspace(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	if f.dialect == "postgres" {
		t.Skip("Postgres text storage excludes NUL")
	}
	workspace := f.data.Workspace.URLKey
	nulWorkspace := workspace + "\x00physical"
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	options := *metadataReadOptions(view.dialect)
	options.ReadOnly = false
	tx, err := view.db.BeginTx(ctx, &options)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Move only this fixture's owned rows inside a transaction that is
	// always rolled back. Physical keys need not obey HTTP slug rules.
	for _, table := range []string{"workspace_states", "workspace_metadata_records"} {
		if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET workspace_key=? WHERE workspace_key=?", nulWorkspace, workspace); err != nil {
			t.Fatal(err)
		}
	}
	read, err := view.MetadataCollectionsInSnapshot(ctx, tx, nulWorkspace, "teamSettings")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	shape, err := read.Shape("teamSettings")
	if err != nil || !shape.WorkspaceFound {
		t.Fatalf("physical NUL workspace shape=%+v error=%v", shape, err)
	}
	var canonical []string
	if err := read.VisitMap("teamSettings", func(callbackCtx context.Context, event MetadataMapOccurrence) error {
		var exists int
		if err := view.db.QueryRowContext(callbackCtx, "SELECT 1").Scan(&exists); err != nil {
			return err
		}
		if !event.CanonicalKey {
			return nil
		}
		var settings domain.TeamSettings
		if err := json.NewDecoder(event.Reader).Decode(&settings); err != nil {
			return err
		}
		canonical = append(canonical, settings.TeamID)
		return nil
	}); err != nil || !reflect.DeepEqual(canonical, []string{"canonical"}) {
		t.Fatalf("physical NUL canonical=%v error=%v", canonical, err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := view.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?", workspace).Scan(&restored); err != nil || restored != 1 {
		t.Fatalf("fixture row not restored by rollback: count=%d error=%v", restored, err)
	}
}

func TestScaleP21SW0SnapshotMissingCancellationAndAdmission(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, workspace := range []string{f.data.Workspace.URLKey + "-missing", strings.ToUpper(f.data.Workspace.URLKey), f.data.Workspace.URLKey + "\x00"} {
		read, err := view.MetadataCollectionsInSnapshot(ctx, tx, workspace, "users", "teamSettings")
		if err != nil {
			t.Fatal(err)
		}
		shape, err := read.Shape("users")
		if err != nil || shape.WorkspaceFound {
			t.Fatalf("missing %q shape=%+v error=%v", workspace, shape, err)
		}
		if err := read.VisitArrayOccurrences("users", func(context.Context, MetadataArrayOccurrence) error {
			t.Fatal("missing array emitted events")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := read.VisitMap("teamSettings", func(context.Context, MetadataMapOccurrence) error {
			t.Fatal("missing map emitted events")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		read.Close()
	}
	start := time.Now()
	if _, err := view.MetadataCollectionsInSnapshot(t.Context(), tx, f.data.Workspace.URLKey, "users"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("missing lease=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("admission queued while transaction held")
	}
	smallCtx, smallRelease, err := view.admitMetadataWork(t.Context(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.MetadataCollectionsInSnapshot(smallCtx, tx, f.data.Workspace.URLKey, "users"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("undersized lease=%v", err)
	}
	smallRelease()
	cancelCtx, cancel := context.WithCancel(ctx)
	read, err := view.MetadataCollectionsInSnapshot(cancelCtx, tx, f.data.Workspace.URLKey, "users")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := read.VisitArrayOccurrences("users", func(context.Context, MetadataArrayOccurrence) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled visit=%v", err)
	}
	read.Close()
	if _, err := view.MetadataCollectionsInSnapshot(cancelCtx, tx, f.data.Workspace.URLKey, "users"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor=%v", err)
	}
	release()
	if _, err := view.MetadataCollectionsInSnapshot(ctx, tx, f.data.Workspace.URLKey, "users"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("released lease=%v", err)
	}
	var one int
	if err := tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("failure ended caller transaction: %v", err)
	}
}

func TestScaleP21SW0SnapshotInvalidSourceKeepsCallerOwnership(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(`{"users":{},"Users":[]}`), workspace); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := recordSpoolBytes.Load()
	if _, err := view.MetadataCollectionsInSnapshot(ctx, tx, workspace, "users"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatalf("discarded wrong-shaped inline input=%v", err)
	}
	var one int
	if err := tx.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("invalid input ended transaction: %v", err)
	}
	if recordSpoolBytes.Load() != before {
		t.Fatal("invalid input changed spool use")
	}
	view.metadataResidency.work.mu.Lock()
	used := view.metadataResidency.work.used
	view.metadataResidency.work.mu.Unlock()
	if used != 2<<20 {
		t.Fatalf("invalid input released caller lease: %d", used)
	}
}

func TestScaleP21SW0SnapshotRepeatableRead(t *testing.T) {
	f, view := scaleP21SW0SnapshotFixture(t)
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	read, err := view.MetadataCollectionsInSnapshot(ctx, tx, f.data.Workspace.URLKey, "teamSettings")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='teamSettings' AND record_key='same'`, []byte(`{"teamId":"later"}`), f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var ids []string
		if err := read.VisitMap("teamSettings", func(_ context.Context, event MetadataMapOccurrence) error {
			if event.Reset || event.BeginObject {
				return nil
			}
			var value domain.TeamSettings
			if err := json.NewDecoder(event.Reader).Decode(&value); err != nil {
				return err
			}
			ids = append(ids, value.TeamID)
			return nil
		}); err != nil || !reflect.DeepEqual(ids, []string{"", "old", "canonical"}) {
			t.Fatalf("snapshot ids=%v error=%v", ids, err)
		}
	}
}
