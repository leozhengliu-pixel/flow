package store

import (
	"reflect"
	"sync"
	"time"
)

// metadataLogicalOffsetTime encodes a timestamp whose location is an unnamed
// numeric RFC 3339 offset: the location the standard JSON decoder (the legacy
// decode of every stored record) builds for "+08:00" or "+00:00". Such a
// value often reaches a builder without a location receipt: a point read or a
// candidate built from records PostgreSQL, MySQL or older data stored with a
// non-UTC offset. A value without a location receipt is encoded by value
// (instant and offset) instead of by location identity, and decodes to an
// interned time.FixedZone("", offset), the location the legacy decode gives
// the same text; no caller-owned location is retained. A named or sub-minute
// zone stays unavailable, as before.
const metadataLogicalOffsetTime byte = 3

// metadataLogicalOffsetLocations interns one location per offset (at most
// 2,879), so every decode of an offset time returns the same *time.Location,
// as a source receipt does. For whole hours it is the standard library's own
// shared zone, the one the legacy decode returns.
var metadataLogicalOffsetLocations sync.Map // int -> *time.Location

func metadataLogicalOffsetLocation(offset int) *time.Location {
	if location, ok := metadataLogicalOffsetLocations.Load(offset); ok {
		return location.(*time.Location)
	}
	location, _ := metadataLogicalOffsetLocations.LoadOrStore(offset, time.FixedZone("", offset))
	return location.(*time.Location)
}

// metadataLogicalFixedZoneShape reports whether location has the private
// shape of time.FixedZone("", offset): no name, one unnamed zone and one
// transition, its cache on that zone.
func metadataLogicalFixedZoneShape(location *time.Location) bool {
	value := reflect.ValueOf(location).Elem()
	name, extend := value.FieldByName("name"), value.FieldByName("extend")
	zones, transitions, cached := value.FieldByName("zone"), value.FieldByName("tx"), value.FieldByName("cacheZone")
	if !name.IsValid() || !extend.IsValid() || !zones.IsValid() || !transitions.IsValid() || !cached.IsValid() ||
		name.Kind() != reflect.String || extend.Kind() != reflect.String ||
		zones.Kind() != reflect.Slice || transitions.Kind() != reflect.Slice || cached.Kind() != reflect.Pointer ||
		name.Len() != 0 || extend.Len() != 0 || zones.Len() != 1 || zones.Cap() != 1 ||
		transitions.Len() != 1 || transitions.Cap() != 1 || cached.IsNil() ||
		cached.Pointer() != zones.Index(0).Addr().Pointer() {
		return false
	}
	zoneName := zones.Index(0).FieldByName("name")
	return zoneName.IsValid() && zoneName.Kind() == reflect.String && zoneName.Len() == 0
}

// metadataLogicalOffsetZone returns value's offset when value is exactly the
// offset time its encoding decodes to: a valid RFC 3339 offset (whole minutes,
// under 24 hours), an unnamed fixed zone and no monotonic reading.
func metadataLogicalOffsetZone(value time.Time) (int, bool) {
	location := value.Location()
	if location == time.UTC || location == time.Local || location == metadataLogicalProcessLocal || !metadataLogicalFixedZoneShape(location) {
		return 0, false
	}
	_, offset := value.Zone()
	if offset%60 != 0 || offset <= -24*60*60 || offset >= 24*60*60 {
		return 0, false
	}
	decoded, err := metadataLogicalOffsetValue(uint64(value.Unix()), uint64(value.Nanosecond()), uint64(int64(offset)))
	if err != nil || !reflect.DeepEqual(decoded, value) {
		return 0, false
	}
	return offset, true
}

func (b *metadataLogicalBuilder) writeOffsetTime(node metadataLogicalNode, value time.Time, offset int) (metadataLogicalNode, error) {
	if err := b.write([]byte{metadataLogicalOffsetTime}); err != nil {
		return node, err
	}
	for _, part := range []uint64{uint64(value.Unix()), uint64(value.Nanosecond()), uint64(int64(offset))} {
		if err := b.writeUint(part); err != nil {
			return node, err
		}
	}
	return node, nil
}

func metadataLogicalOffsetValue(seconds, nanoseconds, offset uint64) (time.Time, error) {
	zone := int64(offset)
	if nanoseconds >= uint64(time.Second) || zone%60 != 0 || zone <= -24*60*60 || zone >= 24*60*60 {
		return time.Time{}, ErrMetadataRecordQuery
	}
	return time.Unix(int64(seconds), int64(nanoseconds)).In(metadataLogicalOffsetLocation(int(zone))), nil
}
