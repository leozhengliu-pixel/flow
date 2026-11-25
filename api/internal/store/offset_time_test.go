package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A stored timestamp with any valid RFC 3339 offset decodes on the installed
// logical source exactly as the legacy decode reads it, whichever path built
// the source (here a capture of a legacy-decoded image, as point reads and
// candidates are). A named or sub-minute zone stays unavailable.
func TestScaleP21OffsetTimestampsDecodeAsLegacy(t *testing.T) {
	_, localOffset := time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local).Zone()
	local := time.Date(2026, 3, 4, 5, 6, 7, 8, time.FixedZone("", localOffset)).Format(time.RFC3339Nano)
	for _, text := range []string{
		"2026-03-04T05:06:07.000000008Z", "2026-03-04T05:06:07.000000008+00:00", "2026-03-04T05:06:07+08:00",
		"2026-03-04T05:06:07.123-03:30", "2026-03-04T05:06:07+05:45", "2026-03-04T05:06:07+14:00",
		"2026-03-04T05:06:07-12:00", "2026-03-04T05:06:07+23:59", "1969-07-20T20:17:40-23:59", local,
	} {
		t.Run(text, func(t *testing.T) {
			var legacy time.Time
			if err := json.Unmarshal([]byte(`"`+text+`"`), &legacy); err != nil {
				t.Fatal(err)
			}
			data := domain.Bootstrap{
				WorkflowRuns: []domain.WorkflowRun{{ID: "run", StartedAt: legacy, CompletedAt: &legacy, Output: map[string]string{}}},
				APIKeys:      []domain.APIKey{{ID: "key", CreatedAt: legacy, LastUsedAt: &legacy}},
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			for name, want := range map[string]any{"workflowRuns": data.WorkflowRuns, "apiKeys": data.APIKeys} {
				index, _, _ := metadataLogicalField(name)
				node := source.fields[index]
				if node.unavailable || node.blocked {
					t.Fatalf("%s is unavailable", name)
				}
				value, err := node.Value(t.Context(), 1<<20)
				if err != nil {
					t.Fatal(name, err)
				}
				if !reflect.DeepEqual(value.Interface(), want) {
					t.Fatalf("%s decoded %+v, legacy %+v", name, value.Interface(), want)
				}
				got, _ := json.Marshal(value.Interface())
				wire, _ := json.Marshal(want)
				if string(got) != string(wire) {
					t.Fatalf("%s encodes %s, legacy %s", name, got, wire)
				}
			}
			// The decoded value is itself a legal input: re-capturing it
			// keeps the same native value.
			index, _, _ := metadataLogicalField("workflowRuns")
			value, err := source.fields[index].Value(t.Context(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			again, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{WorkflowRuns: value.Interface().([]domain.WorkflowRun)}, copyStamp{revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer again.Release()
			second, err := again.fields[index].Value(t.Context(), 1<<20)
			if err != nil || !reflect.DeepEqual(second.Interface(), data.WorkflowRuns) {
				t.Fatal("re-captured offset time changed", err)
			}
		})
	}
	for name, value := range map[string]time.Time{
		"named":      time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("CST", 8*3600)),
		"sub-minute": time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("", 123)),
	} {
		t.Run(name, func(t *testing.T) {
			source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{WorkflowRuns: []domain.WorkflowRun{{ID: "run", StartedAt: value}}}, copyStamp{revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			index, _, _ := metadataLogicalField("workflowRuns")
			if !source.fields[index].unavailable {
				t.Fatal("an unaudited zone became available")
			}
		})
	}
}
