package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW297: a conflicted large canonical row is refreshed into the installed
// logical image (no 256 KiB page refusal) exactly as the literal typed
// refresh decodes it, and a large-row conflict is retried to success.

func scaleP21SW297Node(t *testing.T, f scaleP09Fixture, field string, kind reflect.Type, raw []byte, old metadataAggregateNode, large bool) (reflect.Value, []byte, error) {
	t.Helper()
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	var node metadataAggregateNode
	if large {
		spool, err := newMetadataLogicalRecordSpool(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer spool.Close()
		if _, err := spool.Write(raw); err != nil {
			t.Fatal(err)
		}
		var decodeErr error
		node, decodeErr = metadataLogicalRetryLargeRecord(t.Context(), builder, field, kind, &largeRowRefresh{store: f.SQLiteStore, spool: spool, size: int64(len(raw))}, old)
		if decodeErr != nil {
			return reflect.Value{}, nil, decodeErr
		}
	} else {
		node, err = metadataLogicalRetryRecordLimit(t.Context(), builder, field, kind, raw, old, 1<<30, true)
	}
	if err != nil {
		return reflect.Value{}, nil, err
	}
	stored, err := builder.writeAggregateNode(node, 0)
	if err != nil {
		t.Fatal(err)
	}
	value, err := stored.Value(t.Context(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value.Interface())
	if err != nil {
		t.Fatal(err)
	}
	return value, encoded, nil
}

// The large decoder frames every top-level string from its token. Repeated,
// case-folded, null, escaped, invalid-UTF-8, mistyped and unknown members
// decode exactly as the typed decoder the literal refresh uses.
func TestScaleP21SW297LargeRetryRecordDecode(t *testing.T) {
	_, view := scaleP21SW78Fixture(t)
	f := scaleP09Fixture{SQLiteStore: view}
	big := scaleP21SW296Body("framed", 600<<10)
	quoted, _ := json.Marshal(big)
	loop := string(quoted)
	invalid := `"invalid é \ud800 ` + "\xff\xfe" + ` \u0000 tail"`
	type row struct {
		name, field, raw string
		kind             reflect.Type
		old              any
	}
	rows := []row{
		{name: "plain", field: "loops", kind: reflect.TypeFor[domain.Loop](), raw: `{"id":"loop","name":"Loop","instructions":` + loop + `,"description":"small","connectorIds":["c"],"creator":{"id":"u","name":"N"}}`},
		{name: "repeated-and-null", field: "loops", kind: reflect.TypeFor[domain.Loop](), raw: `{"id":"loop","instructions":` + loop + `,"instructions":null,"Instructions":"folded later","description":null,"name":` + invalid + `}`},
		{name: "folded-only", field: "loops", kind: reflect.TypeFor[domain.Loop](), raw: `{"ID":"loop","INSTRUCTIONS":` + loop + `,"unknown":{"x":[1,2]},"triggerConfig":{"b":1,"a":"<"}}`},
		{name: "whitespace", field: "releases", kind: reflect.TypeFor[domain.Release](), raw: " {\n\t\"id\" : \"release\" ,\"description\" :\t" + loop + " , \"issueIds\" : [\"a\",\"b\",\"a\"] }\n"},
		{name: "mistyped-string", field: "releases", kind: reflect.TypeFor[domain.Release](), raw: `{"id":"release","description":` + loop + `,"name":17}`},
		{name: "import-rows-kept", field: "importJobs", kind: reflect.TypeFor[domain.ImportJob](), raw: `{"id":"job","filename":` + loop + `,"error":"x"}`,
			old: domain.ImportJob{ID: "job", Rows: []map[string]string{{"title": "kept native input"}}}},
		{name: "document-marker", field: "documents", kind: reflect.TypeFor[domain.Document](), raw: `{"id":"document","content":` + loop + `,"revisions":[{"id":"r1","_offloaded":{"body":"document"}}]}`},
		{name: "document-marker-small", field: "documents", kind: reflect.TypeFor[domain.Document](), raw: `{"id":"document","title":` + loop + `,"content":"small body","revisions":[{"id":"r1","_offloaded":{"body":"document"}}]}`},
	}
	for _, test := range rows {
		t.Run(test.name, func(t *testing.T) {
			var old metadataAggregateNode
			if test.old != nil {
				old = metadataAggregateNativeNode{value: reflect.ValueOf(test.old)}
			}
			wantValue, wantJSON, wantErr := scaleP21SW297Node(t, f, test.field, test.kind, []byte(test.raw), old, false)
			gotValue, gotJSON, gotErr := scaleP21SW297Node(t, f, test.field, test.kind, []byte(test.raw), old, true)
			if (wantErr == nil) != (gotErr == nil) || wantErr != nil && wantErr.Error() != gotErr.Error() {
				t.Fatalf("large retry decode error differs: want %v, got %v", wantErr, gotErr)
			}
			if wantErr != nil {
				return
			}
			if !reflect.DeepEqual(gotValue.Interface(), wantValue.Interface()) || string(gotJSON) != string(wantJSON) {
				t.Fatalf("large retry decode differs\nwant %.400s\ngot  %.400s", wantJSON, gotJSON)
			}
		})
	}
	// A malformed row fails like the typed decoder.
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	spool, err := newMetadataLogicalRecordSpool(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	malformed := `{"id":"loop","instructions":` + loop + `,"name":}`
	if _, err := spool.Write([]byte(malformed)); err != nil {
		t.Fatal(err)
	}
	if _, err := metadataLogicalRetryLargeRecord(t.Context(), builder, "loops", reflect.TypeFor[domain.Loop](), &largeRowRefresh{store: f.SQLiteStore, spool: spool, size: int64(len(malformed))}, nil); err == nil {
		t.Fatal("malformed large row decoded")
	}
}

func TestScaleP21SW297LargeRowConflictRefreshAndRetry(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	f.db.SetMaxOpenConns(4)
	workspace := f.data.Workspace.URLKey
	base := scaleP21SW296Base(f)
	loop := scaleP21SW296Loop(&base)
	loop.Instructions = scaleP21SW296Body("legal large instructions", 6<<20)
	release := scaleP21SW296Release(&base)
	release.IssueIDs = scaleP21SW296IDs("issue", 60_000)
	scaleP21SW296Seed(t, f, base)
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	// A peer commits changes to both large rows and deletes the small loop.
	peer := func(edit func(tx *sqlTx)) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		edit(tx)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	peerLoop := *loop
	peerAt := scaleP21SW296At.Add(3 * time.Hour)
	peerLoop.Name, peerLoop.LastRunAt = "Peer renamed loop", &peerAt
	peerRelease := *release
	peerRelease.IssueIDs = append(append([]string{}, release.IssueIDs...), "peer-issue")
	peer(func(tx *sqlTx) {
		scaleP21SW296Put(t, tx, workspace, "loops", peerLoop.ID, peerLoop, scaleP21SW296Order(t, tx, workspace, "loops", peerLoop.ID))
		scaleP21SW296Put(t, tx, workspace, "releases", peerRelease.ID, peerRelease, scaleP21SW296Order(t, tx, workspace, "releases", peerRelease.ID))
		if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"loops", "sw296-small-loop"}); err != nil {
			t.Fatal(err)
		}
	})
	keys := []metadataRecordKey{{"loops", peerLoop.ID}, {"releases", peerRelease.ID}, {"loops", "sw296-small-loop"}}
	if err := view.refreshConflictedLogicalMetadata(t.Context(), workspace)(&metadataConflictError{keys: keys}); err != nil {
		t.Fatal("large-row refresh refused", err)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatal("refresh retained work", used)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"loops", "releases"}}, 1<<30)
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	// The literal refresh decodes each canonical row with its typed decoder.
	wantLoop, wantRelease := domain.Loop{}, domain.Release{}
	for _, item := range []struct {
		key    metadataRecordKey
		target any
	}{{keys[0], &wantLoop}, {keys[1], &wantRelease}} {
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, item.key.field, item.key.key).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) <= MetadataRecordPageMaxBytes {
			t.Fatal("refresh fixture row is not above the page cap", item.key)
		}
		if err := json.Unmarshal(raw, item.target); err != nil {
			t.Fatal(err)
		}
	}
	if len(refreshed.Loops) != 1 || !reflect.DeepEqual(refreshed.Loops[0], wantLoop) {
		t.Fatal("refreshed large loop differs from the canonical row or the deleted loop remained", len(refreshed.Loops))
	}
	if !reflect.DeepEqual(refreshed.Releases[0], wantRelease) {
		t.Fatal("refreshed large release differs from the canonical row")
	}

	// Retry: the writer's base is stale (the peer changed LastRunAt), so the
	// first attempt conflicts; the refreshed second attempt succeeds and
	// keeps the peer's name.
	scaleP21SW296Seed(t, f, base)
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	peer(func(tx *sqlTx) {
		scaleP21SW296Put(t, tx, workspace, "loops", peerLoop.ID, peerLoop, scaleP21SW296Order(t, tx, workspace, "loops", peerLoop.ID))
	})
	ours := scaleP21SW296At.Add(4 * time.Hour)
	attempts := 0
	var seen []*time.Time
	err = mutationAttempts(t.Context(), fullMutationAttempts, func(ctx context.Context) error {
		attempts++
		stored, err := view.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer stored.Release()
		data, err := stored.Select(ctx, scaleP21SW72FullScope(t, stored), 1<<30)
		if err != nil {
			return err
		}
		next := cloneBootstrap(data)
		target := scaleP21SW296Loop(&next)
		seen = append(seen, target.LastRunAt)
		target.LastRunAt = &ours
		after, err := captureMetadataLogicalSource(ctx, next, stored.Stamp())
		if err != nil {
			return err
		}
		defer after.Release()
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		merged, err := view.persistLegacyMetadataImage(context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"loops"}), tx, workspace, stored, after)
		if err != nil {
			return err
		}
		merged.Release()
		return tx.Commit()
	}, view.refreshConflictedLogicalMetadata(t.Context(), workspace))
	if err != nil {
		t.Fatal("large-row conflict was not retried to success", err)
	}
	if attempts != 2 || seen[0] != nil || seen[1] == nil || !seen[1].Equal(peerAt) {
		t.Fatal("retry did not refresh the conflicted large row", attempts, seen)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, peerLoop.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	want := peerLoop
	want.LastRunAt = &ours
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(StripJSONNUL(expected)) {
		t.Fatal("retried large row lost the peer's change or ours")
	}
	if used := view.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatal("retry retained work", used)
	}
}
