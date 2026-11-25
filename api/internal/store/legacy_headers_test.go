package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func scaleP21SW196HeaderPlan(t *testing.T, before, next domain.Bootstrap, name string) (*legacyMetadataWritePlan, *legacyMetadataWriteField) {
	t.Helper()
	left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{workspaceID: "sw196"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(left.Release)
	right, err := captureMetadataLogicalSource(t.Context(), next, copyStamp{workspaceID: "sw196"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(right.Release)
	index, _, _ := metadataLogicalField(name)
	field, ok := metadataFieldByName(name)
	if !ok {
		t.Fatal("missing metadata field", name)
	}
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	work := &ResourceRead{store: view}
	t.Cleanup(work.Close)
	plan := &legacyMetadataWritePlan{store: view, work: work}
	return plan, &legacyMetadataWriteField{field: field, before: left.fields[index], next: right.fields[index]}
}

func TestScaleP21SW196LegacyHeadersDuplicateOccurrences(t *testing.T) {
	const count = 20000
	before := domain.Bootstrap{Cycles: make([]domain.Cycle, count)}
	next := domain.Bootstrap{Cycles: make([]domain.Cycle, count)}
	for index := 0; index < count; index++ {
		before.Cycles[index] = domain.Cycle{ID: "same-cycle", Number: index, Status: "current"}
		next.Cycles[index] = domain.Cycle{ID: "same-cycle", Number: index, Status: "previous"}
	}
	plan, field := scaleP21SW196HeaderPlan(t, before, next, "cycles")
	if err := plan.headers(t.Context(), field); err != nil {
		t.Fatal("legal duplicate fallback exceeded its topology budget", err)
	}
	if !field.full || !field.changed || len(field.rows) != count ||
		len(field.oldOrder) != count || len(field.order) != count {
		t.Fatal("header admission changed duplicate fallback shape", field.full, field.changed, len(field.rows))
	}
	for index := 0; index < count; index++ {
		if field.oldOrder[index] != index || field.order[index] != count-1 ||
			field.rows[index].oldOrdinal != index || field.rows[index].id != "same-cycle" {
			t.Fatal("header admission lost duplicate occurrence order", index)
		}
		if index != count-1 && field.rows[index].next.typ != nil {
			t.Fatal("header admission changed last-next-duplicate precedence", index)
		}
	}
	if field.rows[count-1].ordinal != count-1 {
		t.Fatal("last next occurrence lost its ordinal")
	}
	held := plan.store.MetadataWorkUsedForTesting()
	if held <= 0 || held > 32<<20 {
		t.Fatal("header work did not track retained row/order topology", held)
	}
	t.Logf("occurrences=%d retained_topology_work=%d", count, held)
	plan.work.Close()
	if plan.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("header topology retained a work lease")
	}
}

func TestScaleP21SW196LegacyHeadersKeepOriginalIdentity(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("x", 192), "invalid-\xff", "<tag>&\u2028"} {
		t.Run(strings.ReplaceAll(id, "/", "_"), func(t *testing.T) {
			before := domain.Bootstrap{Cycles: []domain.Cycle{{ID: id, Number: 1}}}
			next := domain.Bootstrap{Cycles: []domain.Cycle{{ID: id, Number: 2}}}
			plan, field := scaleP21SW196HeaderPlan(t, before, next, "cycles")
			if err := plan.headers(t.Context(), field); err != nil {
				t.Fatal(err)
			}
			wantFull := id == "" || len(id) > 191 || !utf8.ValidString(id)
			if field.full != wantFull || !field.changed ||
				len(field.rows) != 1 || field.rows[0].id != id ||
				!reflect.DeepEqual(field.oldOrder, []int{0}) || !reflect.DeepEqual(field.order, []int{0}) {
				t.Fatal("normalization changed stored ID or fallback behavior", field.full, wantFull, field.rows)
			}
		})
	}
}

func TestScaleP21SW196LegacyHeadersRefusalReleases(t *testing.T) {
	before := domain.Bootstrap{Cycles: []domain.Cycle{{ID: "cycle", Number: 1}}}
	next := domain.Bootstrap{Cycles: []domain.Cycle{{ID: "cycle", Number: 2}}}
	for _, cancelled := range []bool{false, true} {
		plan, field := scaleP21SW196HeaderPlan(t, before, next, "cycles")
		ctx := t.Context()
		want := ErrMetadataWorkBudget
		if cancelled {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
			want = context.Canceled
		} else {
			plan.store.metadataResidency.work.limit = 1
		}
		if err := plan.headers(ctx, field); !errors.Is(err, want) {
			t.Fatal("header admission lost refusal", cancelled, err)
		}
		plan.work.Close()
		if plan.store.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("refused header retained partial work", cancelled)
		}
	}
}
