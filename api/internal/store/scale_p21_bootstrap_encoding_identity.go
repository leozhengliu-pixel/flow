package store

import (
	"reflect"
	"sync/atomic"
)

// IssueBootstrapGeneration identifies a decoded bootstrap collection within
// this process. Images that reuse the same frame keep its generation.
type IssueBootstrapGeneration uint64

var bootstrapEncodingSequence atomic.Uint64

var bootstrapEncodingFields = [...]string{"projects", "documents", "initiatives"}

func (r *IssueBootstrapRead) setEncodingGenerations(image *coldDecodedImage) {
	for i, name := range bootstrapEncodingFields {
		index, _, _ := metadataLogicalField(name)
		r.encodingGenerations[i] = image.fields[index].encodingID
	}
}

func (r *IssueBootstrapRead) EncodingGeneration(field string) (IssueBootstrapGeneration, bool) {
	if r == nil || !r.resident || r.borrowed || r.owner == nil || r.owner.source == nil || r.owner.source.behind.Load() {
		return 0, false
	}
	for i, name := range bootstrapEncodingFields {
		if field == name {
			id := r.encodingGenerations[i]
			return id, id != 0
		}
	}
	return 0, false
}

// BoundedBootstrapEncodingBytes is the combined allowance for cached JSON
// and a cache producer's scratch.
func BoundedBootstrapEncodingBytes() int64 { return metadataMemoryShare(1, 16) }

// BootstrapEncodingWitnessBytes accounts for the projected field retained
// alongside its cached encoding, including its share of the decoded image.
func BootstrapEncodingWitnessBytes(value any) int64 {
	return decodedValueBytes(reflect.ValueOf(value))
}
