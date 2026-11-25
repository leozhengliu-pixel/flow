package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW326IntegrationNormalization(t *testing.T) {
	for _, shape := range []string{"nil", "empty", "defaults", "large"} {
		t.Run(shape, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			data := domain.Bootstrap{}
			switch shape {
			case "empty":
				data.IntegrationConnections = []domain.IntegrationConnection{}
				data.IntegrationDeliveries = []domain.IntegrationDelivery{}
			case "defaults", "large":
				data.IntegrationConnections = []domain.IntegrationConnection{
					{ID: "duplicate", Config: map[string]string{"body": "original"}},
					{ID: "duplicate", Scopes: []string{}, Channels: []string{"kept"}},
					{ID: "", Scopes: []string{"kept"}},
				}
				data.IntegrationDeliveries = []domain.IntegrationDelivery{
					{ID: "", Payload: json.RawMessage{}, PreviousValues: nil},
					{ID: "", Payload: json.RawMessage(`{"n":9007199254740993}`), PreviousValues: json.RawMessage{}},
				}
				if shape == "large" {
					data.IntegrationConnections[0].Config["body"] = strings.Repeat("x", 17<<20)
					data.IntegrationDeliveries[1].Payload = json.RawMessage(`"` + strings.Repeat("x", 17<<20) + `"`)
				}
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			normalizeStoredMetadata(&data)
			normalized, err := repo.normalizeMetadataLogicalSource(t.Context(), source)
			if err != nil {
				t.Fatal("supported integration normalization refused", err)
			}
			defer normalized.Release()
			for _, field := range []struct {
				name string
				want any
			}{
				{"integrationConnections", data.IntegrationConnections},
				{"integrationDeliveries", data.IntegrationDeliveries},
			} {
				index, _, _ := metadataLogicalField(field.name)
				node := normalized.fields[index]
				value, err := node.Value(t.Context(), int(node.cost))
				if err != nil || !reflect.DeepEqual(value.Interface(), field.want) {
					t.Fatal("normalization changed exact integration values", field.name, err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if result, err := repo.normalizeMetadataLogicalSource(ctx, source); result != nil || !errors.Is(err, context.Canceled) {
				if result != nil {
					result.Release()
				}
				t.Fatal("normalization ignored cancellation", err)
			}
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("normalization retained metadata work")
			}
		})
	}
}
