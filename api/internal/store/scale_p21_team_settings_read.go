package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// teamSettingsReadFields are the fields team settings derive from (the team's
// settings entry, its inherited parent's, and the team's or the global
// workflow states for a team without settings) together with the inputs of
// the viewer projection that narrows them (teams, team visibility settings,
// workspace settings, the viewer).
// The template collections let the projection clear default templates the
// viewer may not see (BlankHiddenTemplateDefaults).
var teamSettingsReadFields = []string{"workspace", "viewer", "teams", "teamSettings", "teamParents", "states", "workspaceSettings", "issueTemplates", "projectTemplates"}

// TeamSettingsRead owns the viewer projection of teamSettingsReadFields.
type TeamSettingsRead struct {
	Data   domain.Bootstrap
	source *InstalledMetadataRead
	owner  *ResourceRead
	once   sync.Once
}

func (r *TeamSettingsRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
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

// BeginTeamSettingsRead answers the team settings inputs of the paged
// metadata projection (PagedWorkspaceMetadata) without its other fields: the
// installed source's teamSettingsReadFields, as they decode from the wire,
// projected by the same projectBootstrapForUser for userID. With no userID
// (development mode) the fields are unprojected with the admin role, as
// WorkspaceMetadata answers them. found reports the workspace's existence.
func (s *SQLiteStore) BeginTeamSettingsRead(ctx context.Context, workspace, userID string) (_ *TeamSettingsRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &TeamSettingsRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 256<<10); err != nil {
		return nil, true, err
	}
	data := domain.Bootstrap{}
	for _, name := range teamSettingsReadFields {
		index, _, ok := metadataLogicalField(name)
		if !ok {
			return nil, true, ErrMetadataRecordQuery
		}
		value, err := read.owner.node(ctx, source.source.fields[index], true)
		if err != nil {
			return nil, true, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	if userID == "" {
		data.ViewerRole = "admin"
		read.Data = data
		return read, true, ctx.Err()
	}
	if !metadataOutputOwnerPresent(ctx) {
		ctx = WithMetadataOutputOwner(ctx, nil)
	}
	projected, ok, err := s.projectBootstrapForUser(ctx, data, userID)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, true, ErrAuthForbidden
	}
	read.Data = projected
	return read, true, ctx.Err()
}
