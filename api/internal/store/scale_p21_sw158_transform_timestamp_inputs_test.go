package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW158ReplaceConnections(values []domain.IntegrationConnection) func(string, metadataLogicalNode) (metadataAggregateNode, bool, error) {
	return func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "integrationConnections" {
			return metadataAggregateNativeNode{reflect.ValueOf(values)}, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	}
}

func TestScaleP21SW158TransformTimestampInputsNativeIdentity(t *testing.T) {
	f, view, id := scaleP21SW31TimestampFixture(t)
	ctx, inputs := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	var transformed *metadataLogicalSource
	var expected []domain.IntegrationConnection
	var original, replacement time.Time
	err := view.MutateWorkspaceScoped(ctx, f.data.Workspace.URLKey, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
		var err error
		original, err = inputs.ParseRFC3339("2026-11-13T01:02:03.000000004+05:45")
		if err != nil {
			return err
		}
		inputs.mu.Lock()
		activeCtx := inputs.operation.ctx
		inputs.mu.Unlock()
		before := domain.Bootstrap{
			IntegrationConnections: []domain.IntegrationConnection{
				{ID: id, LastWebhookAt: &original},
				{ID: id + "-retained", LastWebhookAt: &original},
			},
			Settings: map[string]any{"retained": original},
		}
		source, err := captureMetadataLogicalSource(activeCtx, before, copyStamp{})
		if err != nil {
			return err
		}
		defer source.Release()
		replacement, err = inputs.ParseRFC3339("2026-11-14T04:05:06.000000007+05:45")
		if err != nil {
			return err
		}
		if original.Location() == replacement.Location() || replacement.Location() == metadataLogicalProcessLocal {
			t.Fatal("fixture did not create an independent, noncached parser location")
		}
		expected = append([]domain.IntegrationConnection(nil), before.IntegrationConnections...)
		expected[0].LastWebhookAt = &replacement
		transformed, err = source.Transform(activeCtx, scaleP21SW158ReplaceConnections(expected))
		if err != nil {
			return err
		}
		return ErrNoMutation
	})
	if transformed != nil {
		defer transformed.Release()
	}
	if err != nil || transformed == nil {
		t.Fatalf("live producer transform: %v", err)
	}
	if _, err := inputs.ParseRFC3339("2026-11-13T01:02:03+05:45"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("completed input scope remained active", err)
	}
	if err := transformed.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
		if name != "integrationConnections" && name != "settings" {
			return nil
		}
		value, err := node.Value(t.Context(), 2<<20)
		if err != nil {
			return err
		}
		var want any = expected
		if name == "settings" {
			want = map[string]any{"retained": original}
		}
		if !reflect.DeepEqual(value.Interface(), want) {
			t.Fatalf("%s changed the typed value", name)
		}
		if name == "integrationConnections" {
			got := value.Interface().([]domain.IntegrationConnection)
			if *got[0].LastWebhookAt != replacement || *got[1].LastWebhookAt != original {
				t.Fatal("transform changed time.Time identity or source receipt indices")
			}
		}
		gotJSON, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		wantJSON, err := json.Marshal(want)
		if err != nil {
			return err
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("%s JSON differs: got %s want %s", name, gotJSON, wantJSON)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("transform input scope leaked work admission")
	}
}

func TestScaleP21SW158TransformTimestampInputsScopeAndOpaqueGuards(t *testing.T) {
	f, view, id := scaleP21SW31TimestampFixture(t)
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		IntegrationConnections: []domain.IntegrationConnection{{ID: id}},
	}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	for _, mode := range []string{"inactive", "canceled", "opaque"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx, inputs := view.WithMetadataTimestampInputs(WithMetadataFields(ctx, "integrationConnections"))
			var activeCtx context.Context
			var values []domain.IntegrationConnection
			check := func(want error) {
				t.Helper()
				disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
				next, err := source.Transform(activeCtx, scaleP21SW158ReplaceConnections(values))
				if next != nil {
					index, _, _ := metadataLogicalField("integrationConnections")
					_, err = next.fields[index].Value(t.Context(), 2<<20)
					next.Release()
				}
				if !errors.Is(err, want) {
					t.Fatalf("%s accepted invalid producer input: got %v want %v", mode, err, want)
				}
				if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Fatal("rejected transform leaked logical storage")
				}
			}
			err := view.MutateWorkspaceScoped(ctx, f.data.Workspace.URLKey, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
				parsed, err := inputs.ParseRFC3339("2026-11-13T01:02:03+05:45")
				if err != nil {
					return err
				}
				inputs.mu.Lock()
				activeCtx = inputs.operation.ctx
				inputs.mu.Unlock()
				values = []domain.IntegrationConnection{{ID: id, LastWebhookAt: &parsed}}
				switch mode {
				case "canceled":
					cancel()
					check(context.Canceled)
					return context.Canceled
				case "opaque":
					fat := scaleP21SW31TimestampFatLocation()
					values[0].LastWebhookAt = &fat
					check(ErrBoundedMetadataCoverage)
				}
				return ErrNoMutation
			})
			if mode == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled operation lost cancellation", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "inactive" {
				check(ErrMetadataRecordQuery)
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("producer guard leaked work admission")
			}
		})
	}
}

func TestScaleP21SW158TransformTimestampInputsClearedRegistries(t *testing.T) {
	f, view, id := scaleP21SW31TimestampFixture(t)
	known := time.Now()
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		Issues: []domain.Issue{{ID: id, CreatedAt: known}},
	}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ctx, inputs := view.WithMetadataTimestampInputs(WithMetadataFields(t.Context(), "integrationConnections"))
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "integrationConnections", ID: id}}}
	err = view.MutateWorkspaceScoped(ctx, f.data.Workspace.URLKey, "integration.adapter_configured", id, nil, scope, func(*domain.Bootstrap) error {
		current := source
		defer func() {
			if current != source {
				current.Release()
			}
		}()
		for i := range 3 {
			parsed, err := inputs.ParseRFC3339("2026-11-13T01:02:03+05:45")
			if err != nil {
				return err
			}
			inputs.mu.Lock()
			activeCtx := inputs.operation.ctx
			inputs.mu.Unlock()
			if i == 0 {
				scalar, err := source.Transform(activeCtx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
					if name == "resourceDetailsOmitted" {
						return metadataAggregateNativeNode{reflect.ValueOf(true)}, true, nil
					}
					return metadataAggregateStoredNode{node}, false, nil
				})
				if err != nil {
					return err
				}
				index, _, _ := metadataLogicalField("resourceDetailsOmitted")
				locations := len(scalar.fields[index].source.locations)
				scalar.Release()
				if locations != 0 {
					t.Fatal("unrelated scalar retained unused parser receipts", locations)
				}
			}
			next, err := current.Transform(activeCtx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
				if name == "issues" {
					return metadataAggregateNativeNode{reflect.ValueOf([]domain.Issue{{ID: id, CreatedAt: known, UpdatedAt: parsed}})}, true, nil
				}
				return metadataAggregateStoredNode{node}, false, nil
			})
			if err != nil {
				return err
			}
			if current != source {
				current.Release()
			}
			current = next
			index, _, _ := metadataLogicalField("issues")
			node := current.fields[index]
			got, err := node.Value(activeCtx, 2<<20)
			if err != nil {
				return err
			}
			issue := got.Interface().([]domain.Issue)[0]
			if issue.CreatedAt != known || issue.UpdatedAt != parsed || len(node.source.locations) != i+1 || len(node.source.times) == 0 {
				t.Fatal("repeated replacement changed native timestamps or lost test registry history")
			}
			if i != 2 {
				continue
			}
			cleared, err := current.Transform(activeCtx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
				if name == "issues" {
					return metadataAggregateNativeNode{reflect.Zero(node.typ)}, true, nil
				}
				return metadataAggregateStoredNode{node}, false, nil
			})
			if err != nil {
				return err
			}
			defer cleared.Release()
			clearedNode := cleared.fields[index]
			value, err := clearedNode.Value(activeCtx, 2<<20)
			if err != nil {
				return err
			}
			if !value.IsNil() || len(clearedNode.source.locations) != 0 || len(clearedNode.source.times) != 0 || len(clearedNode.source.types) != 0 {
				t.Fatal("native zero retained old logical registries")
			}
		}
		return ErrNoMutation
	})
	if err != nil || view.metadataResidency.work.used != 0 {
		t.Fatal("registry guard failed or leaked admission", err)
	}
}
