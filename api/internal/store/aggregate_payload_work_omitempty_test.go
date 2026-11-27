package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Empty omitempty fields are neither encoded nor decoded, so the encoding
// work of a long, mostly empty progress history stays proportional to its
// encoding instead of exceeding the work budget for one project.
func TestPayloadEncodingWorkSkipsOmittedFields(t *testing.T) {
	start := time.Date(2006, 1, 2, 0, 0, 0, 0, time.UTC)
	history := make([]domain.ProjectProgressHistoryPoint, 1100)
	for i := range history {
		history[i] = domain.ProjectProgressHistoryPoint{Date: start.AddDate(0, 0, 7*i)}
	}
	project := domain.Project{ID: "project", Name: "Long project", IssueCountHistory: history, ScopeHistory: history,
		CompletedScopeHistory: history, InProgressScopeHistory: history, ProgressHistory: history}
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	work, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(project), 0)
	if err != nil {
		t.Fatal(err)
	}
	if work < len(raw)*3 || work > len(raw)*64 {
		t.Fatalf("encoding work %d for %d encoded bytes", work, len(raw))
	}
	// Rebasing the project row admits four times each side's work.
	if uint64(work)*4*2 > uint64(metadataWorkBytes())/4 {
		t.Fatalf("a rebase of this project admits %d bytes", uint64(work)*8)
	}

	full := domain.ProjectProgressHistoryPoint{Date: start, Value: 1, BacklogEstimate: 1, UnstartedEstimate: 1, StartedEstimate: 1,
		CompletedEstimate: 1, ScopeEstimate: 1, ScopeCount: 1, CompletedIssueCount: 1, StartedIssueCount: 1,
		CompletedEstimateToday: 1, CompletedIssueCountToday: 1, AddedEstimateToday: 1, AddedIssueCountToday: 1}
	empty := domain.ProjectProgressHistoryPoint{Date: start}
	fullWork, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(full), 0)
	if err != nil {
		t.Fatal(err)
	}
	emptyWork, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(empty), 0)
	if err != nil {
		t.Fatal(err)
	}
	if emptyWork*4 > fullWork {
		t.Fatalf("an empty point costs %d, a full one %d", emptyWork, fullWork)
	}
	if emptyWork < int(reflect.TypeFor[domain.ProjectProgressHistoryPoint]().Size()) {
		t.Fatalf("an empty point costs %d, less than its inline size", emptyWork)
	}
}

func TestPayloadEncodingWorkOmittedFieldRules(t *testing.T) {
	type sample struct {
		Text    string          `json:"text,omitempty"`
		Items   []string        `json:"items,omitempty"`
		Values  map[string]int  `json:"values,omitempty"`
		Pointer *int            `json:"pointer,omitempty"`
		Flag    bool            `json:"flag,omitempty"`
		Any     any             `json:"any,omitempty"`
		Inner   struct{ A int } `json:"inner,omitempty"`
		Count   int             `json:"count"`
		Named   int             `json:",omitempty"`
	}
	value := reflect.ValueOf(sample{})
	omitted := map[string]bool{"Text": true, "Items": true, "Values": true, "Pointer": true, "Flag": true, "Any": true, "Named": true}
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if got := metadataAggregateOmittedField(field.Tag.Get("json"), value.Field(i)); got != omitted[field.Name] {
			t.Fatalf("%s omitted=%t", field.Name, got)
		}
	}
	one := 1
	filled := reflect.ValueOf(sample{Text: "x", Items: []string{}, Pointer: &one, Flag: true, Named: 2})
	for _, name := range []string{"Text", "Pointer", "Flag", "Named"} {
		field, _ := filled.Type().FieldByName(name)
		if metadataAggregateOmittedField(field.Tag.Get("json"), filled.FieldByName(name)) {
			t.Fatalf("%s holds a value and is encoded", name)
		}
	}
	// An empty, non-nil slice is omitted like a nil one.
	field, _ := filled.Type().FieldByName("Items")
	if !metadataAggregateOmittedField(field.Tag.Get("json"), filled.FieldByName("Items")) {
		t.Fatal("an empty slice is omitted")
	}
}
