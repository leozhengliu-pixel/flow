package store

import (
	"context"
	"reflect"
	"sync"
)

// Record ordinals from the resident image (bounded metadata on).
//
// Selecting records of a large collection by ID (Select) and forking a
// generation that replaces some (Fork) read every record's ID from the
// spool: one read per record, most of a project edit's time on the load
// rig. The resident image of the generation (resident_generation.go)
// has every record decoded, so it knows at which ordinals each ID is stored.
// A writer that holds the image passes it in its context
// (withResidentRecordOrdinals); a walk over a collection whose frame is
// exactly the frame the image decoded (same origin, same patch count) then
// reads the ID only of the records at those ordinals and skips the others,
// which cannot match. A collection the image did not decode (another
// generation, a transformed copy) is walked as before.

type residentRecordOrdinalsKey struct{}

// An unchanged collection shares its lazy index with the next generation.
type recordOrdinalIndex struct {
	once    sync.Once
	records map[string][]int
}

func withResidentRecordOrdinals(ctx context.Context, image *coldDecodedImage) context.Context {
	if image == nil {
		return ctx
	}
	return context.WithValue(ctx, residentRecordOrdinalsKey{}, image)
}

// residentRecordOrdinals returns the ordinals of each record ID of field
// name's collection node when node is the frame the context's image decoded.
func residentRecordOrdinals(ctx context.Context, name string, node metadataLogicalNode) (map[string][]int, bool) {
	image, _ := ctx.Value(residentRecordOrdinalsKey{}).(*coldDecodedImage)
	return image.matchingRecordOrdinals(name, node)
}

func (image *coldDecodedImage) matchingRecordOrdinals(name string, node metadataLogicalNode) (map[string][]int, bool) {
	if node.typ == nil || node.typ.Kind() != reflect.Slice {
		return nil, false
	}
	field := image.matchingField(name, node)
	if field == nil {
		return nil, false
	}
	return field.recordOrdinals()
}

func (field *coldDecodedField) recordOrdinals() (map[string][]int, bool) {
	value := field.value
	id, ok := recordOrdinalField(value.Type().Elem())
	if !ok {
		return nil, false
	}
	if field.ordinals == nil {
		return nil, value.Len() == 0
	}
	field.ordinals.once.Do(func() {
		ordinals := make(map[string][]int, value.Len())
		for ordinal := 0; ordinal < value.Len(); ordinal++ {
			key := value.Index(ordinal).Field(id.Index[0]).String()
			ordinals[key] = append(ordinals[key], ordinal)
		}
		field.ordinals.records = ordinals
	})
	return field.ordinals.records, true
}

func recordOrdinalField(element reflect.Type) (reflect.StructField, bool) {
	if element.Kind() != reflect.Struct {
		return reflect.StructField{}, false
	}
	id, ok := element.FieldByName("ID")
	return id, ok && id.Type.Kind() == reflect.String && len(id.Index) == 1
}

// residentSelectedOrdinals is the set of ordinals of node's records whose ID
// is one of ids (ok false when the image does not describe node).
func residentSelectedOrdinals(ctx context.Context, name string, node metadataLogicalNode, ids []string) (map[int]bool, bool) {
	ordinals, ok := residentRecordOrdinals(ctx, name, node)
	if !ok {
		return nil, false
	}
	selected := map[int]bool{}
	for _, id := range ids {
		for _, ordinal := range ordinals[id] {
			selected[ordinal] = true
		}
	}
	residentOrdinalWalks.Add(1)
	return selected, true
}

// withInstalledRecordOrdinals adds the resident image of workspace's
// installed generation to ctx when one is (or can be) resident. The image
// applies only to frames it decoded, so a later generation simply walks.
func (s *SQLiteStore) withInstalledRecordOrdinals(ctx context.Context, workspace string) context.Context {
	if s.metadataResidency == nil || coldDecodeCacheBudget() <= 0 {
		return ctx
	}
	source, err := s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return ctx
	}
	defer source.Release()
	image, ok, err := s.metadataResidency.residentImage(ctx, workspace, source)
	if err != nil || !ok {
		return ctx
	}
	return withResidentRecordOrdinals(ctx, image)
}
