package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// auditJSONLocations is only for a fresh standard JSON decode. That provenance
// proves empty fixed-zone names do not retain arbitrary caller substrings.
// The private standard-library shape check bounds every retained allocation.
func (b *metadataLogicalBuilder) auditJSONLocations(value reflect.Value) error {
	var visit func(reflect.Value, int) error
	visit = func(value reflect.Value, depth int) error {
		if depth >= metadataLogicalDepth {
			return ErrMetadataWorkBudget
		}
		if value.Type() == reflect.TypeFor[time.Time]() {
			return b.auditJSONLocation(value.Interface().(time.Time).Location())
		}
		if value.Type() == reflect.TypeFor[*time.Time]() {
			if value.IsNil() {
				return nil
			}
			return visit(value.Elem(), depth+1)
		}
		unmarshaler := reflect.TypeFor[json.Unmarshaler]()
		if value.Type().Implements(unmarshaler) ||
			value.Kind() != reflect.Pointer && reflect.PointerTo(value.Type()).Implements(unmarshaler) {
			// A custom decoder can synthesize caller-owned Location objects.
			// Its time descendants require their own audited producer.
			return nil
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				return visit(value.Elem(), depth+1)
			}
		case reflect.Slice, reflect.Array:
			if value.Type().Elem().Kind() != reflect.Uint8 {
				for i := 0; i < value.Len(); i++ {
					if err := visit(value.Index(i), depth+1); err != nil {
						return err
					}
				}
			}
		case reflect.Map:
			iter := value.MapRange()
			for iter.Next() {
				if err := visit(iter.Value(), depth+1); err != nil {
					return err
				}
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				field := value.Type().Field(i)
				if field.PkgPath == "" && field.Tag.Get("json") != "-" {
					if err := visit(value.Field(i), depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if !value.IsValid() {
		return nil
	}
	return visit(value, 0)
}

func (b *metadataLogicalBuilder) auditJSONLocation(location *time.Location) error {
	if location == time.UTC || location == metadataLogicalProcessLocal || slices.Contains(b.storage.locations, location) {
		return nil
	}
	value := reflect.ValueOf(location).Elem()
	name, extend := value.FieldByName("name"), value.FieldByName("extend")
	zones, transitions, cached := value.FieldByName("zone"), value.FieldByName("tx"), value.FieldByName("cacheZone")
	if !name.IsValid() || !extend.IsValid() || !zones.IsValid() || !transitions.IsValid() || !cached.IsValid() ||
		name.Kind() != reflect.String || extend.Kind() != reflect.String ||
		zones.Kind() != reflect.Slice || transitions.Kind() != reflect.Slice || cached.Kind() != reflect.Pointer ||
		name.Len() != 0 || extend.Len() != 0 || zones.Len() != 1 || zones.Cap() != 1 ||
		transitions.Len() != 1 || transitions.Cap() != 1 || cached.IsNil() ||
		cached.Pointer() != zones.Index(0).Addr().Pointer() {
		return fmt.Errorf("%w: JSON timestamp has unaudited location shape", ErrBoundedMetadataCoverage)
	}
	zoneName := zones.Index(0).FieldByName("name")
	if !zoneName.IsValid() || zoneName.Kind() != reflect.String || zoneName.Len() != 0 {
		return ErrBoundedMetadataCoverage
	}
	return b.retainAuditedLocation(location)
}

// Existing source receipts carry the same audited provenance. This merges
// pointer identities; it does not intern zones with equal numeric offsets.
func (b *metadataLogicalBuilder) inheritLocations(source *metadataLogicalStorage) error {
	for _, location := range source.locations {
		if !slices.Contains(b.storage.locations, location) {
			if err := b.retainAuditedLocation(location); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *metadataLogicalBuilder) retainAuditedLocation(location *time.Location) error {
	if len(b.storage.locations) >= metadataLogicalDescriptorLimit/512 {
		return ErrMetadataWorkBudget
	}
	if len(b.storage.locations) >= 32 {
		if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, 512, metadataLogicalDescriptorLimit) {
			return ErrMetadataWorkBudget
		}
		b.storage.locationBytes += 512
	}
	b.storage.locations = append(b.storage.locations, location)
	return nil
}
