package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// A parent with NUL is stored without it on PostgreSQL. The literal delta
// matches known and wanted rows under that stored key, so the streamed
// parent-map writer replaces the row too, as on SQLite and MySQL.
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
				if !updates[0].Missing {
					t.Fatal("SQL dialect parent identity changed literal persistence", view.dialect, updates[0].Missing)
				}
			}
		})
	}
}
