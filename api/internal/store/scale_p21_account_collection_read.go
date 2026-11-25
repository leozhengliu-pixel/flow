package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// AccountCollectionRead owns the exact installed settings map value and,
// optionally, the user's passkeys. It is a trusted lookup, not authorization.
type AccountCollectionRead struct {
	Passkeys []domain.Passkey
	Settings domain.UserSettings
	source   *InstalledMetadataRead
	owner    *ResourceRead
	once     sync.Once
}

func (r *AccountCollectionRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Passkeys, r.Settings = nil, domain.UserSettings{}
			if r.source != nil {
				r.source.Close()
				r.source = nil
			}
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// BeginAccountCollectionRead preserves AccountCollections' physical/default
// workspace selection, exact user map key and passkey source order. The bool
// reports workspace existence, including when no user settings are present.
// Settings-only callers omit passkeys without inspecting credential bodies.
func (s *SQLiteStore) BeginAccountCollectionRead(ctx context.Context, workspace, userID string, includePasskeys bool) (_ *AccountCollectionRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	// The literal lookup has no context and observes the current registry,
	// independently of a request's earlier installed-source selection.
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &AccountCollectionRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, field metadataLogicalNode) error {
		switch name {
		case "passkeys":
			if !includePasskeys {
				return nil
			}
			return field.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
				matched := false
				if err := item.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					if key.String() != "UserID" {
						return nil
					}
					var err error
					matched, err = value.StringEqual(ctx, userID)
					return err
				}); err != nil || !matched {
					return err
				}
				value, err := read.owner.node(ctx, item, false)
				if err == nil {
					read.Passkeys = append(read.Passkeys, value.Interface().(domain.Passkey))
				}
				return err
			})
		case "userSettings":
			return field.MapEntries(ctx, func(key, item metadataLogicalNode) error {
				equal, err := key.StringEqual(ctx, userID)
				if err != nil || !equal {
					return err
				}
				value, err := read.owner.node(ctx, item, false)
				if err == nil {
					read.Settings = value.Interface().(domain.UserSettings)
				}
				return err
			})
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}
