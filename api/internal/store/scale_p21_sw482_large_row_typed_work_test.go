package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// SW482: the loaded exit of the large-row rebase charges long string tokens
// at their decode copies, so a legal document whose resolved current body and
// current-version body total 8 MiB reaches its exact typed comparison.
func TestScaleP21SW482LargeRowTypedWork(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	kind := reflect.TypeFor[domain.Document]()
	small := domain.Document{ID: "small", Title: "Small", Content: "body", ContentData: map[string]any{"type": "doc"}, CreatedAt: at}
	raw, err := json.Marshal(small)
	if err != nil {
		t.Fatal(err)
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{})
	owner := &ResourceRead{store: view}
	defer owner.Close()
	generic, err := resourceWindowTypedJSONWork(t.Context(), raw, kind)
	if err != nil {
		t.Fatal(err)
	}
	if work, err := largeRowTypedJSONWork(t.Context(), owner, raw, kind); err != nil || work != generic {
		t.Fatal("a record without long strings changed its typed work", work, generic, err)
	}
	body := strings.Repeat("x", 4<<20)
	escaped := strings.Repeat("<\"\\>\u00e9", 8<<10)
	large := domain.Document{ID: "large", Title: "Large", Content: body, ContentState: escaped, CreatedAt: at,
		Revisions: []domain.DocumentRevision{{ID: "latest", Content: body, CreatedAt: at}, {ID: "short", Content: "short", CreatedAt: at}}}
	raw, err = json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resourceWindowTypedJSONWork(t.Context(), raw, kind); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("generic typed work no longer refuses the 8 MiB fixture; the regression is not exercised", err)
	}
	work, err := largeRowTypedJSONWork(t.Context(), owner, raw, kind)
	if err != nil {
		t.Fatal(err)
	}
	if work < uint64(len(body))*2*3 || work > metadataWorkBytes/4 {
		t.Fatal("long string decode work is not charged at its copies", work)
	}
	if _, err := largeRowTypedJSONWork(t.Context(), owner, []byte(`{"content":"`+strings.Repeat("y", 64<<10)), kind); err == nil {
		t.Fatal("invalid JSON was admitted")
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("typed work skeleton was not released")
	}
}
