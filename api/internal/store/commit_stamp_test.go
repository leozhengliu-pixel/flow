package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/idgen"
)

func TestScaleP21SW27MetadataCommitStampPublication(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	base, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	captureReads := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(query, "SELECT "+metadataStampColumns) {
			captureReads++
		}
	})
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	capture := tx.captureMetadataCommitStamp(workspace)
	if capture != tx.captureMetadataCommitStamp(workspace) {
		t.Fatal("duplicate captures did not share one publication request")
	}
	if value, ready := capture.result(); ready || value != (copyStamp{}) {
		t.Fatal("capture exposed uncommitted state", value, ready)
	}
	tx.queueCacheRevision(workspace, cacheKindMetadata)
	if err := tx.flushCacheRevisions(); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	f.db.commitHooks.Store(&commitHooks{
		beforeCommit: func(current *sqlTx) {
			if current == tx {
				order = append(order, "before")
				if _, ready := capture.result(); ready {
					t.Error("capture visible in beforeCommit")
				}
				current.queueCacheRevision(workspace, cacheKindMetadata)
				current.queueCacheRevision(workspace, cacheKindMetadata)
				current.queueCacheRevision(workspace, cacheKindIssues)
			}
		},
		afterCommit: func(current *sqlTx) {
			if current == tx {
				order = append(order, "after")
				if _, ready := capture.result(); ready {
					t.Error("capture visible before afterCommit completed")
				}
			}
		},
		afterSequence: func(current *sqlTx) {
			if current == tx {
				order = append(order, "sequence")
				if _, ready := capture.result(); ready {
					t.Error("capture visible before afterSequence completed")
				}
			}
		},
	})
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.db.commitHooks.Store(nil)
	actual, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	got, ready := capture.result()
	if !ready || got != actual || got.workspaceID != base.workspaceID || got.revision != base.revision+2 ||
		captureReads != 1 || !reflect.DeepEqual(order, []string{"before", "after", "sequence"}) {
		t.Fatal("capture changed revision accounting, hook order, or SQL bound", got, actual, base, captureReads, order)
	}
	t.Log("one final stamp SELECT; duplicate queued metadata increment coalesced; nonmetadata increment excluded")
}

func TestScaleP21SW27MetadataCommitStampGuard(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	base, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	capture := tx.captureMetadataCommitStampFrom(workspace, base)
	key := metadataRecordKey{"settings", idgen.New("sw27-guard-")}
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, key, metadataRecord{data: json.RawMessage(`"own-write"`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("own journal flush incorrectly rejected exact logical base", err)
	}
	first, ready := capture.result()
	if !ready || capture.ownAdvance != 1 || first.revision != base.revision+1 {
		t.Fatal("own advance was sampled after the SQLite late-journal flush cleared it", first, base, capture.ownAdvance)
	}
	peer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Rollback()
	if err := writeMetadataRecordRow(t.Context(), peer, workspace, key, metadataRecord{data: json.RawMessage(`"peer-write"`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := peer.Commit(); err != nil {
		t.Fatal(err)
	}
	rejected, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Rollback()
	stale := rejected.captureMetadataCommitStampFrom(workspace, first)
	if err := writeMetadataRecordRow(t.Context(), rejected, workspace, key, metadataRecord{data: json.RawMessage(`"stale-overwrite"`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := rejected.Commit(); !errors.Is(err, ErrConcurrentWrite) {
		t.Fatal("peer-inclusive revision certified an incomplete logical source", err)
	}
	if value, ready := stale.result(); ready || value != (copyStamp{}) {
		t.Fatal("rejected logical publication exposed its candidate", value, ready)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.field, key.key).Scan(&raw); err != nil ||
		string(raw) != `"peer-write"` {
		t.Fatal("failed guard did not roll back its canonical write", string(raw), err)
	}
}

func TestScaleP21SW27MetadataCommitStampFailures(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	original, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"rollback", "preflush-rejection", "capture-error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			capture := tx.captureMetadataCommitStamp(workspace)
			tx.queueCacheRevision(workspace, cacheKindMetadata)
			injected := errors.New("sw27 rejected commit")
			switch mode {
			case "rollback":
				err = tx.Rollback()
			case "preflush-rejection":
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(current *sqlTx) {
					if current == tx {
						current.writeErr = injected
					}
				}})
				err = tx.Commit()
				f.db.commitHooks.Store(nil)
				if !errors.Is(err, injected) {
					t.Fatal("late write rejection changed", err)
				}
			case "capture-error":
				tx.captureMetadataCommitStamp(idgen.New("sw27-missing-"))
				err = tx.Commit()
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("missing captured workspace did not reject commit", err)
				}
			case "cancelled":
				cancel()
				err = tx.Commit()
				if err == nil {
					t.Fatal("cancelled native transaction unexpectedly committed")
				}
			}
			if mode == "rollback" && err != nil {
				t.Fatal(err)
			}
			if value, ready := capture.result(); ready || value != (copyStamp{}) {
				t.Fatal("failed transaction exposed its captured stamp", value, ready)
			}
			actual, err := readMetadataStamp(t.Context(), f.db, workspace)
			if err != nil || actual != original {
				t.Fatal("failed capture committed a queued metadata increment", actual, original, err)
			}
		})
	}
	t.Run("native-commit-error", func(t *testing.T) {
		if f.dialect != "sqlite" {
			t.Skip("deferred foreign-key native Commit error injection requires SQLite")
		}
		parent, child := idgen.New("sw27_stamp_parent_"), idgen.New("sw27_stamp_child_")
		if _, err := f.db.ExecContext(t.Context(), `CREATE TABLE `+parent+` (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `CREATE TABLE `+child+` (id INTEGER PRIMARY KEY,parent_id INTEGER REFERENCES `+parent+`(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := f.db.ExecContext(context.Background(), `DROP TABLE `+child); err != nil {
				t.Error(err)
			}
			if _, err := f.db.ExecContext(context.Background(), `DROP TABLE `+parent); err != nil {
				t.Error(err)
			}
		})
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		capture := tx.captureMetadataCommitStamp(workspace)
		tx.queueCacheRevision(workspace, cacheKindMetadata)
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO `+child+`(id,parent_id) VALUES(1,2)`); err != nil {
			t.Fatal("deferred constraint failed before Commit", err)
		}
		if err := tx.Commit(); err == nil {
			t.Fatal("invalid deferred reference unexpectedly committed")
		}
		if value, ready := capture.result(); ready || value != (copyStamp{}) {
			t.Fatal("native Commit failure exposed uncommitted capture", value, ready)
		}
	})
}
