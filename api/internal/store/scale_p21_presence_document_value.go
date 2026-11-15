package store

import (
	"context"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

// FirstUserPermission owns the first literal user grant through this read's
// lifetime. Duplicate grants retain source order and native timestamp values.
func (r *PresenceDocumentRead) FirstUserPermission(ctx context.Context, userID string) (permission domain.DocumentPermission, found bool, err error) {
	if r == nil || r.owner == nil || r.source == nil || r.owner.closed {
		return permission, false, errMetadataLogicalClosed
	}
	if err = ctx.Err(); err != nil || !r.Found {
		return permission, false, err
	}
	err = r.documentNode.Children(ctx, func(key reflect.Value, permissions metadataLogicalNode) error {
		if key.String() != "Permissions" {
			return nil
		}
		return permissions.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			user, subject := false, false
			if err := node.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
				var err error
				switch key.String() {
				case "SubjectType":
					user, err = value.StringEqual(ctx, "user")
				case "SubjectID":
					subject, err = value.StringEqual(ctx, userID)
				}
				return err
			}); err != nil || !user || !subject {
				return err
			}
			value, err := r.owner.node(ctx, node, false)
			if err != nil {
				return err
			}
			permission, found = value.Interface().(domain.DocumentPermission), true
			return ErrMetadataIterationDone
		})
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return domain.DocumentPermission{}, false, err
	}
	return permission, found, nil
}

// LoadDocument owns the full native value of the original first ID-or-current-
// slug match through this read's lifetime, without performing another lookup.
func (r *PresenceDocumentRead) LoadDocument(ctx context.Context) (domain.Document, error) {
	if r == nil || r.owner == nil || r.source == nil || r.owner.closed {
		return domain.Document{}, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return domain.Document{}, err
	}
	if !r.Found {
		return domain.Document{}, nil
	}
	value, err := r.owner.node(ctx, r.documentNode, false)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return domain.Document{}, err
	}
	return value.Interface().(domain.Document), nil
}
