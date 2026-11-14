package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalJSONOffsetLocations(t *testing.T) {
	ctx := t.Context()
	var connections []domain.IntegrationConnection
	if err := json.Unmarshal([]byte(`[{"id":"offset-a","createdAt":"2026-11-13T01:02:03.000000004+05:30","updatedAt":"2026-11-13T01:02:03.000000004+05:30"},{"id":"offset-b","createdAt":"2026-11-13T01:02:03.000000004-03:30","updatedAt":"2026-11-13T01:02:03.000000004+00:45"}]`), &connections); err != nil {
		t.Fatal(err)
	}
	var user domain.User
	if err := json.Unmarshal([]byte(`{"id":"usr_offset","outOfOfficeUntil":"2026-11-13T01:02:03+05:45"}`), &user); err != nil {
		t.Fatal(err)
	}
	data := domain.Bootstrap{IntegrationConnections: connections, Users: []domain.User{user}}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	value := reflect.ValueOf(data)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.Tag.Get("json") == "-" {
			continue
		}
		if err := builder.auditJSONLocations(value.Field(i)); err != nil {
			t.Fatal(err)
		}
		if err := builder.Field(jsonFieldName(field), value.Field(i)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := builder.Finish(copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	got, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"integrationConnections", "users"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(got.IntegrationConnections, connections) || !reflect.DeepEqual(got.Users, data.Users) {
		t.Fatalf("native JSON offset locations changed: %v", err)
	}
	for i := range connections {
		if got.IntegrationConnections[i].CreatedAt != connections[i].CreatedAt ||
			got.IntegrationConnections[i].UpdatedAt != connections[i].UpdatedAt {
			t.Fatalf("timestamp pointer/representation changed at %d", i)
		}
	}
	// Re-encoding a value read from a source transfers the audited ownership;
	// it never invokes the JSON parser again or interns distinct zone pointers.
	fork, err := source.Fork(ctx, got, got, MetadataRecordScope{Fields: []string{"integrationConnections"}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Release()
	again, err := fork.Select(ctx, MetadataRecordScope{Fields: []string{"integrationConnections"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(again.IntegrationConnections, connections) {
		t.Fatalf("audited locations lost during fork: %v", err)
	}
}

type scaleP21SW29CustomTimestamp struct{ At time.Time }

func (value *scaleP21SW29CustomTimestamp) UnmarshalJSON([]byte) error {
	value.At = time.Date(2026, 11, 13, 1, 2, 3, 0, time.FixedZone("private producer", 19800))
	return nil
}

func TestScaleP21SW29LogicalCustomDecoderNotAudited(t *testing.T) {
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	var custom scaleP21SW29CustomTimestamp
	if err := json.Unmarshal([]byte(`{}`), &custom); err != nil {
		t.Fatal(err)
	}
	if err := builder.auditJSONLocations(reflect.ValueOf(custom)); err != nil {
		t.Fatal(err)
	}
	if len(builder.storage.locations) != 0 {
		t.Fatal("custom JSON decoder gained standard-library location provenance")
	}
}

func TestScaleP21SW29LogicalJSONLocationsUseProcessBudget(t *testing.T) {
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	values := make([]time.Time, 70)
	for i := range values {
		if err := json.Unmarshal([]byte(`"2026-11-13T01:02:03.000000004+05:30"`), &values[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.auditJSONLocations(reflect.ValueOf(values)); err != nil {
		builder.Close()
		t.Fatal(err)
	}
	if len(builder.storage.locations) != len(values) {
		builder.Close()
		t.Fatal("independent JSON locations were interned")
	}
	if got := metadataLogicalDescriptorsUsed.Load() - descriptors; got != metadataLogicalDescriptorBytes+int64(len(values)-32)*512 {
		builder.Close()
		t.Fatalf("location process charge: %d", got)
	}
	node, err := builder.writeValue(reflect.ValueOf(values), 0, false)
	if err != nil {
		builder.Close()
		t.Fatal(err)
	}
	got, err := node.Value(t.Context(), 2<<20)
	if err != nil || !reflect.DeepEqual(got.Interface(), values) {
		builder.Close()
		t.Fatalf("many native JSON offset timestamps: %v", err)
	}
	builder.Close()
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("location receipts leaked process accounting")
	}
}
