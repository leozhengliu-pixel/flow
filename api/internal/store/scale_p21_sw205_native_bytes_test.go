package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestScaleP21SW205NativeByteWork(t *testing.T) {
	backing := make([]byte, 8<<20)
	raw := json.RawMessage(`{"body":"` + strings.Repeat("x", 4<<20) + `"}`)
	type bytesAlias []byte
	for _, value := range []any{
		[]byte(nil), []byte{}, []byte{0, 255}, backing[:1],
		backing[:4<<20], raw, bytesAlias(backing[:4<<20]), [7]byte{1, 255},
	} {
		node := reflect.ValueOf(value)
		width := node.Len()
		if node.Kind() == reflect.Slice {
			width = node.Cap()
		}
		want := uint64(node.Type().Size())*2 + 128 + uint64(width)*2
		cost, err := metadataAggregateNativeCost(t.Context(), node, 0)
		if err != nil || cost != want {
			t.Fatalf("native byte owner type=%s len=%d cap=%d work=%d want=%d err=%v",
				node.Type(), node.Len(), node.Cap(), cost, want, err)
		}
	}
	view := scaleP21HotLogoStore(t, nil)
	work := &ResourceRead{store: view}
	cost, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf(raw), 0)
	if err != nil || cost >= 16<<20 {
		t.Fatal("legal raw payload still charged as element objects", cost, err)
	}
	if err := work.reserve(t.Context(), cost); err != nil {
		t.Fatal("legal raw payload could not acquire its native owner", err)
	}
	work.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("raw payload native owner leaked")
	}
	view.metadataResidency.work.limit = int(cost) - 1
	work = &ResourceRead{store: view}
	defer work.Close()
	if err := work.reserve(t.Context(), cost); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("byte ownership bypassed exhausted admission", err)
	}
}
