package store

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// PresenceDocumentRead owns only literal documentRole inputs. It is a trusted
// installed lookup, not authorization or a complete document.
type PresenceDocumentRead struct {
	Found    bool
	Document domain.Document
	source   *InstalledMetadataRead
	owner    *ResourceRead
	once     sync.Once
}

func (r *PresenceDocumentRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Found, r.Document = false, domain.Document{}
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

// BeginPresenceDocumentRead preserves DocumentByID's first installed ID-or-
// current-slug match. Previous slugs are ignored. The returned bool describes
// workspace existence; Found separately describes the document match.
func (s *SQLiteStore) BeginPresenceDocumentRead(ctx context.Context, workspace, ref string) (_ *PresenceDocumentRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	// The literal accessor has no context and does not borrow a policy read's
	// installed generation. Each lookup observes the current registry source.
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &PresenceDocumentRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, documents metadataLogicalNode) error {
		if name != "documents" {
			return nil
		}
		err := documents.Children(ctx, func(_ reflect.Value, document metadataLogicalNode) error {
			matched := false
			if err := document.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
				if key.String() != "ID" && key.String() != "SlugID" {
					return nil
				}
				equal, err := value.StringEqual(ctx, ref)
				matched = matched || equal
				return err
			}); err != nil || !matched {
				return err
			}
			value, err := read.owner.projectNode(ctx, document, "ID", "SlugID", "TeamIDs", "InheritedTeamIDs")
			if err != nil {
				return err
			}
			read.Document = value.Interface().(domain.Document)
			if err := document.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				switch key.String() {
				case "Creator":
					value, err := read.owner.projectNode(ctx, node, "ID")
					if err == nil {
						read.Document.Creator = value.Interface().(domain.User)
					}
					return err
				case "Permissions":
					absent, err := node.isNil(ctx)
					if err != nil {
						return err
					}
					if !absent {
						read.Document.Permissions = []domain.DocumentPermission{}
					}
					return node.Children(ctx, func(_ reflect.Value, permission metadataLogicalNode) error {
						value, err := read.owner.projectNode(ctx, permission, "SubjectType", "SubjectID", "Role")
						if err == nil {
							read.Document.Permissions = append(read.Document.Permissions, value.Interface().(domain.DocumentPermission))
						}
						return err
					})
				}
				return nil
			}); err != nil {
				return err
			}
			read.Found = true
			return ErrMetadataIterationDone
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
		return err
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}
