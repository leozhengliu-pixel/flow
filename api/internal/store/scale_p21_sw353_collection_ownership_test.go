package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW353CollectionOwnership(t *testing.T) {
	body := strings.Repeat("Complete native body <>&\x00 ", 16384)
	at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.UTC)
	notices := []domain.Notification{
		{ID: "z", Text: body, LatestActorIDs: []string{"first", "first"}, CreatedAt: at},
		{ID: "a", Text: body + "last", LatestActorIDs: []string{}, CreatedAt: at.Add(-time.Hour)},
	}
	activities := map[string][]domain.ActivityEvent{
		"z\x00": {
			{ID: "z", Metadata: domain.ActivityMetadata{"body": body}, CreatedAt: at},
			{ID: "a", Metadata: domain.ActivityMetadata{"body": body + "last"}, CreatedAt: at.Add(-time.Hour)},
		},
		"nil": nil, "empty": {},
	}
	cases := []struct {
		name, field string
		value       any
	}{
		{"slice", "notifications", notices}, {"map", "activities", activities},
		{"nil-slice", "notifications", []domain.Notification(nil)},
		{"empty-slice", "notifications", []domain.Notification{}},
		{"nil-map", "activities", map[string][]domain.ActivityEvent(nil)},
		{"empty-map", "activities", map[string][]domain.ActivityEvent{}},
	}
	values := map[string]domain.Bootstrap{}
	for _, row := range cases {
		var data domain.Bootstrap
		index, _, _ := metadataLogicalField(row.field)
		reflect.ValueOf(&data).Elem().Field(index).Set(reflect.ValueOf(row.value))
		values[row.name] = data
	}
	repo := scaleP21HotLogoStore(t, values)
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			source, err := repo.retainMetadataLogicalSource(t.Context(), row.name)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			index, _, _ := metadataLogicalField(row.field)
			node := source.fields[index]
			var native, retained, peak uint64
			measure := func(child metadataLogicalNode) error {
				value, err := child.Value(t.Context(), int(child.cost))
				if err != nil {
					return err
				}
				cost, err := metadataAggregateNativeCost(t.Context(), value, 0)
				if err != nil {
					return err
				}
				// Relocation copies the entry slot, not a second deep body.
				entry := cost + 2*uint64(value.Type().Size()) + 512
				peak = max(peak, retained+child.cost*8+4096+entry)
				native, retained = native+cost, retained+entry
				return nil
			}
			if node.typ.Kind() == reflect.Slice {
				err = node.Children(t.Context(), func(_ reflect.Value, child metadataLogicalNode) error { return measure(child) })
			} else {
				err = node.MapEntries(t.Context(), func(key, child metadataLogicalNode) error {
					if err := measure(key); err != nil {
						return err
					}
					return measure(child)
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			var firstData, secondData domain.Bootstrap
			firstOwner := &boundedDiscussionAttempt{owner: &ResourceRead{store: repo}, data: &firstData}
			secondOwner := &boundedDiscussionAttempt{owner: &ResourceRead{store: repo}, data: &secondData}
			defer firstOwner.Close()
			defer secondOwner.Close()
			first, err := firstOwner.collectionValue(t.Context(), node)
			if err != nil || !reflect.DeepEqual(first.Interface(), row.value) || repo.MetadataWorkUsedForTesting() != int(retained) {
				t.Fatal("first collection lost native/order/nil shape or exact retention", err, repo.MetadataWorkUsedForTesting(), retained)
			}
			reflect.ValueOf(&firstData).Elem().Field(index).Set(first)
			second, err := secondOwner.collectionValue(t.Context(), node)
			if err != nil || !reflect.DeepEqual(second.Interface(), row.value) || repo.MetadataWorkUsedForTesting() != 2*int(retained) {
				t.Fatal("independent collection lost its values or owner", err)
			}
			reflect.ValueOf(&secondData).Elem().Field(index).Set(second)
			if native != 0 && (retained <= native || retained >= 2*native) {
				t.Fatal("large-body fixture does not distinguish slot relocation from another body copy", native, retained)
			}
			if row.name == "slice" {
				first.Index(0).FieldByName("LatestActorIDs").Index(0).SetString("caller edit")
			} else if row.name == "map" {
				first.MapIndex(reflect.ValueOf("z\x00")).Interface().([]domain.ActivityEvent)[0].Metadata["body"] = "caller edit"
			}
			if !reflect.DeepEqual(second.Interface(), row.value) {
				t.Fatal("independent collection reads alias mutable native values")
			}
			firstOwner.Close()
			firstOwner.Close()
			if !reflect.ValueOf(firstData).IsZero() || repo.MetadataWorkUsedForTesting() != int(retained) || !reflect.DeepEqual(second.Interface(), row.value) {
				t.Fatal("closing one attempt changed another attempt or retained its work")
			}
			secondOwner.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("collection owners retained work")
			}
			if peak == 0 {
				return
			}
			budget := repo.metadataResidency.work
			budget.mu.Lock()
			original := budget.limit
			budget.limit = int(peak)
			budget.mu.Unlock()
			defer func() { budget.mu.Lock(); budget.limit = original; budget.mu.Unlock() }()
			owned := &boundedDiscussionAttempt{owner: &ResourceRead{store: repo}}
			defer owned.Close()
			value, err := owned.collectionValue(t.Context(), node)
			if err != nil || !reflect.DeepEqual(value.Interface(), row.value) || repo.MetadataWorkUsedForTesting() != int(retained) {
				t.Fatal("native-plus-slot envelope did not admit the complete large collection", err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := owned.collectionValue(cancelled, node); !errors.Is(err, context.Canceled) || repo.MetadataWorkUsedForTesting() != int(retained) || !reflect.DeepEqual(value.Interface(), row.value) {
				t.Fatal("cancellation released or changed previously owned values", err)
			}
			owned.Close()
			owned.Close()
			budget.mu.Lock()
			budget.limit = int(peak) - 1
			budget.mu.Unlock()
			refused := &boundedDiscussionAttempt{owner: &ResourceRead{store: repo}}
			defer refused.Close()
			if _, err := refused.collectionValue(t.Context(), node); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("under-admitted entry escaped the precise collection envelope", err)
			}
			refused.Close()
			refused.Close()
			if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("pressure/cancellation/Close retained work or hydrated metadata")
			}
		})
	}
}
