package store

import (
	"context"
	"reflect"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func metadataPolicyRowWork(row metadataCacheRow, cost int) uint64 {
	work := uint64(cost) + uint64(len(row.Key))*2 + 1024
	for _, field := range persistedMetadataFields() {
		if field.name != row.Field {
			continue
		}
		typ := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			// Reflect's temporary plus old/new collection backing storage.
			work += uint64(typ.Elem().Size()) * 4
		}
		break
	}
	return work
}

// Use the native codec's container/key accounting for the three team maps,
// child slices and empty label index, including duplicate/empty team keys.
func reserveMetadataPolicyDirectory(ctx context.Context, data domain.Bootstrap, reserve func(uint64) error) error {
	stringSize := uint64(reflect.TypeFor[string]().Size())
	intSize := uint64(reflect.TypeFor[int]().Size())
	sliceSize := uint64(reflect.TypeFor[[]string]().Size())
	work := uint64(4) * (uint64(reflect.TypeFor[map[string]int]().Size())*2 + 128)
	stringNode := stringSize*2 + 192
	intNode := intSize*2 + 128
	sliceNode := sliceSize*2 + 128
	byID := intSize*2 + 64 + stringNode + intNode
	byKey := stringSize*2 + 64 + stringNode*2
	children := sliceSize*2 + 64 + stringNode + sliceNode + stringSize*2 + 64 + stringNode
	for _, team := range data.Teams {
		if err := ctx.Err(); err != nil {
			return err
		}
		keyBytes := uint64(0)
		for _, r := range team.Key {
			keyBytes += uint64(utf8.RuneLen(unicode.ToLower(r)))
		}
		// ToLower can expand invalid native UTF-8. Include the mapped key's
		// builder growth as well as the directory's owned string.
		part := byID + byKey + children + uint64(len(team.ID))*6 +
			keyBytes*4 + uint64(utf8.UTFMax) + 128 +
			uint64(len(data.TeamSettings[team.ID].ParentTeamID))*2
		if part > metadataWorkBytes || work > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		work += part
	}
	return reserve(work)
}
