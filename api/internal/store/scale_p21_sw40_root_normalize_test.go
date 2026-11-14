package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW40RawMessageNormalizationDifferential(t *testing.T) {
	cases := [][]byte{
		[]byte(` { "first" : [ 1e+09, -0, 9007199254740993 ], "duplicate" : 1, "duplicate" : 2 } `),
		[]byte(`"<>&\\\" \t \n \r \b \f \/ \u003C \u0026 \ud800"`),
		[]byte("\"a\u2028b\u2029c\""),
		[]byte(` { "raw" : "a b", "quote" : "\\\"", "end" : [true, false, null] } `),
		[]byte(` { "\u0061" : "<\/script>", "a" : "\u2028\u2029", "slash" : "https:\/\/example.test\/a" } `),
		[]byte(" \t\r\n null \n"),
		[]byte(" \t\r\n [] \n"),
		[]byte(`"\uD800\uDFFF\u003E\u0026"`),
		[]byte(`-0.000e-09`),
		append([]byte{'"'}, []byte{0xff, 0xe2, 0x80, 0xa8, '"'}...),
	}
	for offset := -3; offset <= 3; offset++ {
		cases = append(cases, []byte("\""+strings.Repeat("x", metadataLogicalRootNormalizeBuffer+offset)+"\u2028<>&\u2029\""))
	}
	for index, raw := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			expected, err := json.Marshal(json.RawMessage(raw))
			if err != nil {
				t.Fatal("reference rejected fixture", err)
			}
			var actual bytes.Buffer
			if _, err := normalizeMetadataLogicalRaw(t.Context(), bytes.NewReader(raw), &actual); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(expected, actual.Bytes()) {
				t.Fatalf("RawMessage normalization changed bytes: expected %q actual %q", expected, actual.Bytes())
			}
			changed, err := normalizeMetadataLogicalRaw(t.Context(), bytes.NewReader(expected), io.Discard)
			if err != nil || changed {
				t.Fatal("canonical value was not stable", changed, err)
			}
		})
	}
}

func TestScaleP21SW40HistoricalRootPatchDifferential(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&original); err != nil {
		t.Fatal(err)
	}
	original = bytes.ReplaceAll(original, []byte("Member "), []byte("<Member &> "))
	original = bytes.ReplaceAll(original, []byte("Display "), []byte("\u2028Display \u2029"))
	if !json.Valid(original) {
		t.Fatal("historical HTML fixture is invalid")
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, original, " ", " \t"); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"compact-html": original, "indented-html": indented.Bytes()}
	repeated := append(append([]byte(`{"viewerRole":"discarded",`), original[1:len(original)-1]...), []byte(`,"viewerRole":"admin"}`)...)
	cases["duplicate-root-html"] = repeated
	cases["escaped-root-key"] = bytes.ReplaceAll(original, []byte(`"viewerRole":`), []byte(`"\u0076iewerRole":`))
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, workspace); err != nil {
				t.Fatal(err)
			}
			root := map[string]json.RawMessage{}
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			root["viewerRole"] = json.RawMessage(` "<guest&>" `)
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
			ctx, closeRoot := view.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
			defer closeRoot()
			if err := view.patchMetadataLogicalRoot(ctx, tx, workspace, f.data.Workspace.ID, map[string]metadataLogicalRootReplacement{
				"viewerRole": metadataLogicalRootBytes(json.RawMessage(` "<guest&>" `)),
			}, false); err != nil {
				t.Fatal(err)
			}
			var actual []byte
			if err := tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, expected) {
				t.Fatal("historical root differs from original RawMessage map merge")
			}
			t.Logf("historical root bytes=%d normalized=%d", len(raw), len(actual))
		})
	}
}

type scaleP21SW40CancelWriter struct {
	cancel context.CancelFunc
	bytes  int
}

func (w *scaleP21SW40CancelWriter) Write(raw []byte) (int, error) {
	w.bytes += len(raw)
	w.cancel()
	return len(raw), nil
}

func TestScaleP21SW40RootNormalizationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &scaleP21SW40CancelWriter{cancel: cancel}
	_, err := normalizeMetadataLogicalRaw(ctx, strings.NewReader(`"`+strings.Repeat("<", 1<<20)+`"`), output)
	if !errors.Is(err, context.Canceled) || output.bytes == 0 {
		t.Fatal("stream normalization ignored cancellation", err, output.bytes)
	}
}

func TestScaleP21SW40HistoricalDeltaDifferential(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21_sw40.large_member", "", nil, func(data *domain.Bootstrap) error {
		data.Members[0].User.Name = strings.Repeat("<&", 256<<10)
		return nil
	}); err != nil {
		t.Fatal("literal original producer rejected large complete Member", err)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) < 3<<20 {
		t.Fatal("producer fixture does not contain the legal large record", len(raw))
	}
	raw = bytes.ReplaceAll(raw, []byte(`\u003c`), []byte("<"))
	raw = bytes.ReplaceAll(raw, []byte(`\u0026`), []byte("&"))
	raw = bytes.ReplaceAll(raw, []byte("Member "), []byte("<Member &> "))
	raw = bytes.ReplaceAll(raw, []byte(`"viewerRole":`), []byte(`"\u0076iewerRole":`))
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "\t"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", indented.Bytes(), workspace); err != nil {
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
		ctx, closeRoot := repo.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
		before := domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "admin"}
		after := before
		after.ViewerRole = "guest"
		changed, err := repo.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, &after)
		if err != nil || !changed {
			closeRoot()
			_ = tx.Rollback()
			release()
			t.Fatal("historical actual delta path", err)
		}
		var actual []byte
		err = tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&actual)
		closeRoot()
		_ = tx.Rollback()
		release()
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, actual)
	}
	if !bytes.Equal(results[0], results[1]) {
		t.Fatal("historical bounded delta differs from literal original delta")
	}
}

func TestScaleP21SW40NormalizationBudgetsRollback(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("Member "), []byte("<Member &> "))
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", raw, workspace); err != nil {
		t.Fatal(err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	root["viewerRole"] = json.RawMessage(`"guest"`)
	expected, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	logical := len(expected) + len(workspace) + len("UPDATE workspace_states SET data=? WHERE workspace_key=?")
	for _, extra := range []int{0, -1} {
		for _, bounded := range []bool{false, true} {
			t.Run(fmt.Sprintf("extra%d/bounded%v", extra, bounded), func(t *testing.T) {
				disk := metadataLogicalDiskUsed.Load()
				ctx, release, err := view.admitMetadataWork(t.Context(), 8<<20)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					release()
					t.Fatal(err)
				}
				tx.maxTransactionBytes = logical + extra
				ctx, closeRoot := view.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
				if bounded {
					err = view.patchMetadataLogicalRoot(ctx, tx, workspace, f.data.Workspace.ID, map[string]metadataLogicalRootReplacement{
						"viewerRole": metadataLogicalRootBytes(json.RawMessage(`"guest"`)),
					}, false)
				} else {
					_, err = tx.ExecContext(ctx, "UPDATE workspace_states SET data=? WHERE workspace_key=?", expected, workspace)
				}
				written := tx.writtenBytes
				closeRoot()
				_ = tx.Rollback()
				release()
				if extra == 0 {
					if err != nil || written != logical {
						t.Fatal("original exact transaction-byte boundary changed", err, written, logical)
					}
				} else if !errors.Is(err, ErrTransactionWriteBudget) || tx.writeErr == nil {
					t.Fatal("root normalization did not preserve sticky transaction budget error", err)
				}
				if metadataLogicalDiskUsed.Load() != disk || view.metadataResidency.work.used != 0 {
					t.Fatal("normalization retained spool bytes or work")
				}
			})
		}
	}
	disk := metadataLogicalDiskUsed.Load()
	ctx, release, err := view.admitMetadataWork(t.Context(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		release()
		t.Fatal(err)
	}
	priorLimit := view.maxStateBytes
	view.maxStateBytes = len(raw) + 1
	ctx, closeRoot := view.withMetadataLogicalRootSnapshots(ctx, tx, workspace)
	err = view.patchMetadataLogicalRoot(ctx, tx, workspace, f.data.Workspace.ID, map[string]metadataLogicalRootReplacement{
		"viewerRole": metadataLogicalRootBytes(json.RawMessage(`"guest"`)),
	}, false)
	closeRoot()
	_ = tx.Rollback()
	release()
	view.maxStateBytes = priorLimit
	if err == nil || !strings.Contains(err.Error(), "workspace state exceeds") {
		t.Fatal("normalization expansion bypassed MaxStateBytes", err)
	}
	var unchanged []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&unchanged); err != nil || !bytes.Equal(raw, unchanged) {
		t.Fatal("normalization failure changed the physical root", err)
	}
	if metadataLogicalDiskUsed.Load() != disk || view.metadataResidency.work.used != 0 {
		t.Fatal("failed normalized expansion retained spool bytes or work")
	}
}
