package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func scaleP21SW333Target(workspace, workspaceID, field string, size int) metadataSQLStageTarget {
	if field == "" {
		return metadataSQLStageTarget{
			workspaceState: true, workspace: workspace, workspaceID: workspaceID,
			updatedAt: "2026-11-17T12:00:00Z", dataBytes: size, searchBytes: -1,
		}
	}
	target := metadataSQLStageTarget{workspace: workspace, field: field, key: "sw333", dataBytes: size, searchBytes: -1}
	if field == "projects" {
		target.searchBytes = 0
	}
	return target
}

func TestScaleP21SW333IntegrationSQLChunks(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	const large = 256 << 10
	const ordinary = 16 << 10
	if metadataRecordChunkBytes != ordinary {
		t.Fatal("SQL coalescing changed the metadata reader chunk")
	}
	t.Run("selection-and-small-buffers", func(t *testing.T) {
		for _, test := range []struct {
			event, field string
			size, chunk  int
		}{
			{"integration_delivery.claimed", "integrationDeliveries", large + 1, large},
			{"integration_delivery.retry", "integrationConnections", large + 1, large},
			{"integration_delivery.retry", "", large + 1, large},
			{"integration_delivery.claimed", "integrationDeliveries", 19, large},
			{"integration_delivery.claimed", "integrationDeliveries", 0, large},
			{"", "integrationDeliveries", large + 1, ordinary},
			{"integration_delivery.other", "integrationDeliveries", large + 1, ordinary},
			{"integration_delivery.claimed", "agentSessions", large + 1, ordinary},
			{"integration_delivery.retry", "projectUpdates", large + 1, ordinary},
			{"integration_delivery.retry", "projects", large + 1, ordinary},
			{"", "", large + 1, ordinary},
		} {
			t.Run(fmt.Sprintf("%s/%s/%d", test.event, test.field, test.size), func(t *testing.T) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				ctx := withIntegrationDeliveryStream(t.Context(), test.event)
				stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW333Target(workspace, workspaceID, test.field, test.size))
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close(context.WithoutCancel(ctx))
				writer, err := stage.writer(ctx, "data")
				if err != nil || stage.appendChunkBytes != test.chunk {
					t.Fatal("scoped chunk selection", stage.appendChunkBytes, test.chunk, err)
				}
				if cap(writer.buffer) != min(test.size, test.chunk) {
					t.Fatal("small SQL target allocated a full chunk", cap(writer.buffer), test.size, test.chunk)
				}
				if _, err := writer.Write(nil); err != nil || len(writer.buffer) != 0 {
					t.Fatal("empty write changed staging", err)
				}
			})
		}
	})
	t.Run("utf8-and-partial-final-chunk", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		raw := []byte(`{"body":"` + strings.Repeat("x", large-2-len(`{"body":"`)) + "\U0001f600" + strings.Repeat("y", large+17) + `"}`)
		appends := 0
		ctx := WithSQLStatementObserver(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"), func(query string) {
			if strings.HasPrefix(query, "UPDATE bounded_metadata_write_stage SET data=") {
				appends++
			}
		})
		stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW333Target(workspace, workspaceID, "integrationDeliveries", len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close(context.WithoutCancel(ctx))
		writer, err := stage.writer(ctx, "data")
		if err != nil {
			t.Fatal(err)
		}
		for start := 0; start < len(raw); start += ordinary {
			if _, err := writer.Write(raw[start:min(start+ordinary, len(raw))]); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.flush(true); err != nil {
			t.Fatal(err)
		}
		if appends != 3 {
			t.Fatal("reader-sized writes did not coalesce across the UTF-8 boundary", appends)
		}
		value, err := stage.valueSQL("data", true)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		if err := tx.QueryRowContext(t.Context(), "SELECT "+value, stage.id).Scan(&got); err != nil || !bytes.Equal(got, raw) {
			t.Fatal("coalescing changed UTF-8 bytes", err)
		}
	})
	t.Run("cancel-on-actual-second-append", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx, cancel := context.WithCancel(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		defer cancel()
		appends := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "UPDATE bounded_metadata_write_stage SET data=") {
				appends++
				if appends == 2 {
					cancel()
				}
			}
		})
		stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW333Target(workspace, workspaceID, "integrationDeliveries", 3*large))
		if err != nil {
			t.Fatal(err)
		}
		writer, err := stage.writer(ctx, "data")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(bytes.Repeat([]byte{'x'}, 3*large)); !errors.Is(err, context.Canceled) || appends != 2 {
			t.Fatal("coalesced append lost cancellation", appends, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var rows int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage WHERE operation_id=?`, stage.id).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("canceled coalesced stage survived rollback", rows, err)
		}
	})
	t.Run("immutable-chunk-and-argument-guards", func(t *testing.T) {
		for _, event := range []string{"", "integration_delivery.retry"} {
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				ctx := withIntegrationDeliveryStream(t.Context(), event)
				stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW333Target(workspace, workspaceID, "integrationDeliveries", 2*large))
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close(context.WithoutCancel(ctx))
				chunk, billed := stage.appendChunkBytes, tx.writtenBytes
				stage.appendChunkBytes = ordinary
				if chunk == ordinary {
					stage.appendChunkBytes = large
				}
				if _, err := stage.writer(ctx, "data"); !errors.Is(err, ErrMetadataRecordQuery) {
					t.Fatal("admitted chunk declaration was mutable", event, err)
				}
				stage.appendChunkBytes = chunk
				if _, err := stage.exec(ctx, metadataSQLStageAppendData, strings.Repeat("x", chunk+1), stage.id); !errors.Is(err, ErrBoundedMetadataCoverage) {
					t.Fatal("stage accepted an oversized append", event, err)
				}
				if tx.writtenBytes != billed || stage.written[0] != 0 {
					t.Fatal("rejected chunk claim changed the logical bill or consumed bytes")
				}
			}()
		}
	})
	t.Run("literal-row-and-root-bills", func(t *testing.T) {
		raw := []byte(`{"body":"` + strings.Repeat("x", large+37) + `"}`)
		for _, field := range []string{"integrationDeliveries", ""} {
			target := scaleP21SW333Target(workspace, workspaceID, field, len(raw))
			bill := len(metadataSQLStageLegacyRowSQL) + len(workspace) + len(field) + len(target.key) + 16 + len(raw)
			if target.workspaceState {
				bill = len(metadataSQLStageLegacyRootSQL) + len(workspaceID) + len(raw) + len(target.updatedAt) + len(workspace)
			}
			write := func(staged bool, limit int) (int, bool) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				tx.maxTransactionBytes = limit
				ctx := withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry")
				if !staged {
					if target.workspaceState {
						_, err = tx.ExecContext(ctx, metadataSQLStageLegacyRootSQL, workspaceID, raw, target.updatedAt, workspace)
					} else {
						_, err = tx.ExecContext(ctx, metadataSQLStageLegacyRowSQL, workspace, field, target.key, target.order, raw)
					}
				} else {
					var stage *metadataSQLWriteStage
					stage, err = beginMetadataSQLWriteStage(ctx, tx, target)
					if err == nil {
						defer stage.Close(context.WithoutCancel(ctx))
						var writer *metadataSQLStageWriter
						writer, err = stage.writer(ctx, "data")
						if err == nil {
							_, err = writer.Write(raw)
						}
						if err == nil {
							err = writer.flush(true)
						}
						if err == nil && target.workspaceState {
							err = stage.writeWorkspaceState(ctx)
						} else if err == nil {
							err = stage.writeMetadataRow(ctx)
						}
						if err == nil {
							err = stage.Close(ctx)
						}
					}
				}
				if err != nil && !errors.Is(err, ErrTransactionWriteBudget) {
					t.Fatal(err)
				}
				return tx.writtenBytes, errors.Is(err, ErrTransactionWriteBudget)
			}
			for _, delta := range []int{-1, 0, 1} {
				wantBill, wantRefused := write(false, bill+delta)
				gotBill, gotRefused := write(true, bill+delta)
				if gotBill != wantBill || gotRefused != wantRefused || gotRefused != (delta < 0) {
					t.Fatal("coalescing changed the literal transaction boundary", field, delta, gotBill, wantBill, gotRefused, wantRefused)
				}
			}
		}
	})
}
