package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestActivityMetadataWireCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"id":"old","metadata":{"priority":"High","labels":"bug"}}`,
		`{"id":"new","metadata":{"addedLabelIds":["bug"],"removedLabelIds":[],"labels":"bug","priorityBefore":"Low","priority":"High"}}`,
		`{"id":"empty","metadata":null}`,
	} {
		var event ActivityEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var original, roundtrip map[string]any
		_ = json.Unmarshal([]byte(raw), &original)
		_ = json.Unmarshal(encoded, &roundtrip)
		if !reflect.DeepEqual(original["metadata"], roundtrip["metadata"]) {
			t.Fatalf("metadata changed on roundtrip: %s -> %s", raw, encoded)
		}
	}
}
