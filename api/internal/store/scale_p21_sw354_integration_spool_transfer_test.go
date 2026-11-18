package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW354IntegrationSpoolTransfer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	repo := scaleP21ResidentProbe(f.SQLiteStore)
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	defer func() {
		if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools || repo.MetadataWorkUsedForTesting() != 0 {
			t.Error("receipt retained temporary storage or work")
		}
	}()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer frames.Close()
	value := domain.IntegrationDelivery{
		ID: "sw354", ConnectionID: "connection", EventType: "issue.updated", ResourceID: "issue",
		Payload: json.RawMessage(`{"body":"` + strings.Repeat("x", 100<<10) + `"}`),
		Status:  "retry", Attempts: 3, CreatedAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
	}
	node, err := frames.writeValue(reflect.ValueOf(value), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	value.Status, value.Attempts = "stale", 1
	stale, err := frames.writeValue(reflect.ValueOf(value), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	field, _ := metadataFieldByName("integrationDeliveries")
	selected := &legacyMetadataWriteField{field: field}
	row := &legacyMetadataWriteRow{id: "sw354", before: node, next: node, changed: true}
	type result struct {
		raw   []byte
		order int
		bill  int
	}
	run := func(t *testing.T, mode string) result {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		plan := &legacyMetadataWritePlan{store: repo, tx: tx, workspace: f.data.Workspace.URLKey, frames: frames}
		ctx, cancel := context.WithCancel(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		defer cancel()
		var receipt *integrationDeliveryRowSpool
		var spool *RecordSpool
		if mode != "original" {
			source := node
			if mode == "stale-node" {
				source = stale
			}
			owner := &ResourceRead{store: repo}
			spool, err = legacySessionNodeSpool(ctx, owner, source)
			owner.Close()
			if err != nil {
				t.Fatal(err)
			}
			defer spool.Close()
			receipt = newIntegrationDeliveryRowSpool(plan, field.name, row.id, source, spool)
			switch mode {
			case "other-plan":
				copy := *plan
				receipt.plan = &copy
			case "other-tx":
				receipt.tx = nil
			case "other-workspace":
				receipt.workspace += "-other"
			case "other-field":
				receipt.field = "integrationConnections"
			case "other-id":
				receipt.id += "-other"
			case "changed-node":
				receipt.node.offset++
			case "changed-size":
				receipt.size++
			case "nil-spool":
				receipt.spool = nil
			case "closed-spool":
				if err := spool.Close(); err != nil {
					t.Fatal(err)
				}
			}
			wantBorrow := mode == "exact" || strings.HasPrefix(mode, "canceled")
			if got := receipt.borrow(plan, selected, row); (got == spool) != wantBorrow {
				t.Fatal("receipt identity validation", mode, got == spool, wantBorrow)
			}
			if mode == "canceled" {
				cancel()
			}
			if mode == "canceled-during-stage" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, "UPDATE bounded_metadata_write_stage SET data=") {
						cancel()
					}
				})
			}
		}
		var handled bool
		if mode == "original" {
			handled, err = plan.writeIntegrationDeliveryNativeRow(ctx, selected, row, 7, true)
		} else {
			handled, err = plan.writeIntegrationDeliveryNativeRowWithSpool(ctx, selected, row, 7, true, receipt)
		}
		if !handled && !strings.HasPrefix(mode, "canceled") {
			t.Fatal("native writer declined the fixture")
		}
		if strings.HasPrefix(mode, "canceled") {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled write", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if spool != nil && mode != "closed-spool" {
			var first [1]byte
			if _, err := spool.Stat(); err != nil {
				t.Fatal("writer closed caller spool", err)
			}
			if _, err := spool.ReadAt(first[:], 0); err != nil || first[0] != '{' {
				t.Fatal("writer invalidated caller spool", first, err)
			}
			path := spool.file.Name()
			if err := spool.Close(); err != nil {
				t.Fatal(err)
			}
			if err := spool.Close(); err != nil {
				t.Fatal("second close", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("caller close left spool file", err)
			}
		}
		var got result
		if strings.HasPrefix(mode, "canceled") {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var rows int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
				plan.workspace, field.name, row.id).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("canceled write escaped rollback", rows, err)
			}
			return got
		}
		if err := tx.QueryRowContext(ctx, `SELECT data,collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
			plan.workspace, field.name, row.id).Scan(&got.raw, &got.order); err != nil {
			t.Fatal(err)
		}
		got.bill = tx.writtenBytes
		return got
	}
	want := run(t, "original")
	for _, mode := range []string{"exact", "other-plan", "other-tx", "other-workspace", "other-field", "other-id",
		"changed-node", "changed-size", "stale-node", "nil-spool", "closed-spool", "canceled", "canceled-during-stage"} {
		t.Run(mode, func(t *testing.T) {
			got := run(t, mode)
			if !strings.HasPrefix(mode, "canceled") && (!bytes.Equal(got.raw, want.raw) || got.order != want.order || got.bill != want.bill) {
				t.Fatal("receipt changed SQL bytes, order, or logical billing", got.order, want.order, got.bill, want.bill)
			}
		})
	}
}
