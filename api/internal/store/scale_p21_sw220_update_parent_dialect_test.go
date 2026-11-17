package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW220ParentPostgresNativeNULDoesNotQuery(t *testing.T) {
	for _, parent := range []string{"ordinary\x00parent", "\x00"} {
		// A native parent is a known/wanted map key. textArgs strips NUL
		// only at SQL execution, while scanned canonical keys are stripped.
		// Literal applyMetadataRecords skips this known-but-absent native key;
		// a point lookup must not turn it into a canonical alias.
		got := textArgs("postgres", []any{parent})
		if got[0] == parent {
			t.Fatal("fixture parent did not differ from its physical SQL key")
		}
		// No underlying SQL transaction or store is present: any query,
		// reservation or stage attempt is a regression.
		plan := &legacyMetadataWritePlan{tx: &sqlTx{dialect: "postgres"}}
		if err := plan.writeProjectUpdateScheduleParent(t.Context(), &legacyMetadataWriteField{}, parent, legacySessionJSONPart{}); err != nil {
			t.Fatal("native NUL parent did not preserve the literal skipped write", err)
		}
	}
}

func TestScaleP21SW220ParentNativeNULDifferential(t *testing.T) {
	f, view, seed := scaleP21SW77Fixture(t)
	for _, test := range []struct {
		name string
		size int
	}{{"small", 32}, {"large", 5 << 20}} {
		t.Run(test.name, func(t *testing.T) {
			for _, parent := range []string{"ordinary\x00parent", "\x00"} {
				before := cloneBootstrap(seed)
				item := domain.ProjectUpdate{ID: "duplicate", ProjectID: parent, Body: strings.Repeat("x", test.size)}
				before.ProjectUpdates = map[string][]domain.ProjectUpdate{parent: {item, item}}
				target := cloneBootstrap(before)
				target.ProjectUpdates[parent][0].Missing = true
				want := scaleP21SW220Run(t, f, view, before, target, parent, "", false)
				got := scaleP21SW220Run(t, f, view, before, target, parent, "", true)
				if !reflect.DeepEqual(want.image, got.image) || !reflect.DeepEqual(want.native, got.native) || want.bill != got.bill {
					t.Fatal("native NUL parent differs from literal known/wanted key behavior", test.name, want.bill, got.bill)
				}
				physical := parent
				if view.dialect == "postgres" {
					physical = StripNUL(parent)
				}
				row, found := got.image.Rows[metadataRecordKey{"projectUpdates", physical}]
				var updates []domain.ProjectUpdate
				if !found || json.Unmarshal(row.data, &updates) != nil || len(updates) != 2 {
					t.Fatal("native NUL parent lost its canonical row")
				}
				if updates[0].Missing != (view.dialect != "postgres") {
					t.Fatal("SQL dialect parent identity changed literal persistence", view.dialect, updates[0].Missing)
				}
			}
		})
	}
}
