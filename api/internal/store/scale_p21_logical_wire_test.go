package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21WireTimes struct {
	ID      string               `json:"id"`
	Time    time.Time            `json:"time"`
	Pointer *time.Time           `json:"pointer,omitempty"`
	Values  []any                `json:"values"`
	Map     map[string]time.Time `json:"map"`
	Ignored time.Time            `json:"-"`
}

type scaleP21WireCustomTime struct{ Time time.Time }

func (value scaleP21WireCustomTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.Time.Location().String())
}

type scaleP21WireCustomText struct{ Time time.Time }

func (value *scaleP21WireCustomText) MarshalText() ([]byte, error) {
	return []byte(value.Time.Location().String()), nil
}

func scaleP21CaptureWire(t *testing.T, input any) (*metadataLogicalSource, metadataLogicalNode) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		Settings: map[string]any{"target": input},
	}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	index, _, _ := metadataLogicalField("settings")
	return source, source.fields[index]
}

func scaleP21ReadWire(t *testing.T, node metadataLogicalNode) any {
	t.Helper()
	value, err := node.WireValue(t.Context(), int(node.cost))
	if err != nil {
		t.Fatal(err)
	}
	return value.Interface().(map[string]any)["target"]
}

func TestScaleP21LogicalWireTimeJSON(t *testing.T) {
	for _, offset := range []int{-13*3600 - 123, -123, 0, 7*3600 + 123, 23*3600 + 59*60} {
		t.Run(time.FixedZone("", offset).String(), func(t *testing.T) {
			zone := time.FixedZone(strings.Repeat("caller-owned-zone-name", 32768), offset)
			stamp := time.Date(2026, 11, 14, 1, 2, 3, 456789123, zone)
			input := scaleP21WireTimes{
				ID: "native\x00\xff", Time: stamp, Pointer: &stamp,
				Values:  []any{stamp, &stamp, [2]time.Time{stamp, stamp.Add(time.Second)}, nil},
				Map:     map[string]time.Time{"first": stamp, "second": stamp.Add(time.Minute)},
				Ignored: time.Date(10000, 1, 1, 0, 0, 0, 0, zone),
			}
			want, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			source, node := scaleP21CaptureWire(t, input)
			defer source.Release()
			if _, err := node.Value(t.Context(), int(node.cost)); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("wire fallback changed native availability", err)
			}
			value := scaleP21ReadWire(t, node)
			got, err := json.Marshal(value)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("time wire differs: %s/%s, %v", want, got, err)
			}
			if value.(scaleP21WireTimes).Time.Location() == zone {
				t.Fatal("wire value retained unaudited location")
			}
			for _, storage := range source.stores {
				if len(storage.locations) != 0 || len(storage.times) != 0 {
					t.Fatal("wire fallback retained producer location or monotonic anchors")
				}
			}
		})
	}
}

func TestScaleP21LogicalWireEncodingErrors(t *testing.T) {
	zone := time.FixedZone("unaudited", 7331)
	year := time.Date(10000, 1, 1, 0, 0, 0, 0, zone)
	offset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("outside JSON range", 25*3600))
	for _, input := range []any{
		year, &year, offset, &offset,
		time.Date(-1, 1, 1, 0, 0, 0, 0, zone),
		struct {
			Time *time.Time `json:"time,omitempty"`
		}{&year},
		map[string]any{"a": year, "z": offset},
	} {
		want, oldErr := json.Marshal(input)
		if oldErr == nil {
			t.Fatal("invalid time fixture encoded")
		}
		source, node := scaleP21CaptureWire(t, input)
		value := scaleP21ReadWire(t, node)
		got, newErr := json.Marshal(value)
		source.Release()
		if !bytes.Equal(got, want) || newErr == nil || newErr.Error() != oldErr.Error() {
			t.Fatalf("encoding error changed: %v/%v", oldErr, newErr)
		}
	}
	// An ignored invalid field must not fail while materializing the record.
	input := struct {
		Name string    `json:"name"`
		Time time.Time `json:"-"`
	}{"visible", year}
	source, node := scaleP21CaptureWire(t, input)
	defer source.Release()
	raw, err := json.Marshal(scaleP21ReadWire(t, node))
	if err != nil || string(raw) != `{"name":"visible"}` {
		t.Fatal("wire decoder introduced an ignored-field encoding error", err, string(raw))
	}
}

func TestScaleP21LogicalWireProjectOwnershipAndQuota(t *testing.T) {
	zone := time.FixedZone("private backing", 7*3600+123)
	stamp := time.Date(2026, 11, 14, 1, 2, 3, 456, zone)
	input := &domain.Project{
		ID: "wire-project", Name: "warm\x00name",
		Description: strings.Repeat("legal\x00<>\u2028", 300000),
		CreatedAt:   stamp, UpdatedAt: stamp,
		Creator: &domain.User{ID: "creator", OutOfOfficeUntil: &stamp},
	}
	want, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	source, node := scaleP21CaptureWire(t, input)
	if _, err := node.WireValue(t.Context(), int(node.cost)-1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("wire quota did not preflight complete materialization", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := node.WireValue(ctx, int(node.cost)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled wire read materialized data", err)
	}
	input.Description, input.Name = "later", "later"
	input.CreatedAt = stamp.Add(24 * time.Hour)
	value := scaleP21ReadWire(t, node)
	source.Release()
	got, err := json.Marshal(value)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("wire output lost payload or retained changed producer data", err, len(got), len(want))
	}
	if _, err := node.WireValue(t.Context(), int(node.cost)); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("wire read used a released source", err)
	}
}

func TestScaleP21LogicalWireNativeAndOpaqueBoundaries(t *testing.T) {
	instant := time.Now()
	source, node := scaleP21CaptureWire(t, []time.Time{instant, instant.Add(time.Second), time.Time{}})
	native, err := node.Value(t.Context(), int(node.cost))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := node.WireValue(t.Context(), int(node.cost))
	source.Release()
	if err != nil || !reflect.DeepEqual(native.Interface(), wire.Interface()) {
		t.Fatal("wire path changed supported native timestamps", err)
	}
	zone := time.FixedZone("unaudited", 123)
	for _, input := range []any{
		struct{ hidden string }{"opaque"},
		map[string]any{"time": time.Now().In(zone), "opaque": make(chan int)},
		map[time.Time]string{time.Now().In(zone): "native-key"},
		scaleP21WireCustomTime{Time: time.Now().In(zone)},
		&scaleP21WireCustomText{Time: time.Now().In(zone)},
	} {
		source, node := scaleP21CaptureWire(t, input)
		_, err := node.WireValue(t.Context(), int(node.cost))
		source.Release()
		if !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("wire path accepted non-time opaque data or changed native map keys", err)
		}
	}
}
