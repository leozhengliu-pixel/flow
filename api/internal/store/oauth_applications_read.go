package store

import (
	"context"
	"sync"

	"flow/api/internal/domain"
)

// OAuthApplicationsRead owns the installed oauthApplications field value
// exactly (source order, nil versus empty). It is a trusted lookup, not
// authorization.
type OAuthApplicationsRead struct {
	Applications []domain.OAuthApplication
	source       *InstalledMetadataRead
	owner        *ResourceRead
	once         sync.Once
}

func (r *OAuthApplicationsRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Applications = nil
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

// BeginOAuthApplicationsRead decodes only the workspace's oauthApplications
// field from its installed logical source, as BootstrapFor's projection holds
// it (the viewer projection does not narrow the field). The bool reports the
// workspace's existence.
func (s *SQLiteStore) BeginOAuthApplicationsRead(ctx context.Context, workspace string) (_ *OAuthApplicationsRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &OAuthApplicationsRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, field metadataLogicalNode) error {
		if name != "oauthApplications" {
			return nil
		}
		value, err := read.owner.node(ctx, field, false)
		if err == nil {
			read.Applications = value.Interface().([]domain.OAuthApplication)
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
