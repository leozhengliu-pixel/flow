package store

import (
	"context"
	"fmt"
	"reflect"
)

type formerMembersLogicalSourceKey struct{}

// Only suspended status and exact user IDs are observed by the literal
// no-membership fallback. Names and other potentially large member fields
// remain in the installed source.
func (s *SQLiteStore) readFormerMembersLogicalFallback(ctx context.Context, read *FormerMembersRead, add func(string) error) error {
	source, _ := ctx.Value(formerMembersLogicalSourceKey{}).(*metadataLogicalSource)
	if source == nil {
		return fmt.Errorf("%w: former-member fallback needs logical source provenance", ErrBoundedMetadataCoverage)
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, scaleP21SW16FormerWorkBytes)
	if err != nil {
		return err
	}
	read.releases = append(read.releases, release)
	index, _, _ := metadataLogicalField("members")
	return source.fields[index].Children(ctx, func(_ reflect.Value, member metadataLogicalNode) error {
		var id metadataLogicalNode
		suspended := false
		err := member.Children(ctx, func(key reflect.Value, field metadataLogicalNode) error {
			switch key.String() {
			case "Status":
				var err error
				suspended, err = field.StringEqual(ctx, "suspended")
				return err
			case "User":
				return field.Children(ctx, func(name reflect.Value, child metadataLogicalNode) error {
					if name.String() == "ID" {
						id = child
					}
					return nil
				})
			}
			return nil
		})
		if err != nil || !suspended {
			return err
		}
		if id.typ == nil || id.typ.Kind() != reflect.String {
			return ErrMetadataRecordQuery
		}
		if id.cost > scaleP21SW16FormerMapBytes {
			return ErrMetadataWorkBudget
		}
		value, err := id.Value(ctx, int(id.cost))
		if err != nil {
			return err
		}
		return add(value.String())
	})
}
