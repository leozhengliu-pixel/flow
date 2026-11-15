package store

import (
	"context"
	"encoding/json"
	"reflect"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) readBoundedEventPolicySource(ctx context.Context, workspace string, version EventSourceVersion) (*EventPolicySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	s.mu.RLock()
	s.metadataResidency.mu.Lock()
	_, found := s.workspaces[workspace]
	source := s.metadataResidency.logical[workspace]
	stamp := copyStamp{revision: -1}
	if source != nil {
		stamp = source.Stamp()
	}
	current := found && stamp.revision >= 0 &&
		stamp.workspaceID == version.WorkspaceID && stamp.revision == version.Metadata
	var err error
	if current {
		err = source.Retain()
		if err == nil {
			owner.source = source
		}
	}
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	var data domain.Bootstrap
	if current {
		data, err = owner.nativeEventPolicy(ctx)
	} else {
		data, err = s.readLegacyEventPolicyRecords(ctx, workspace, version.WorkspaceID)
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(raw) > 256<<10 {
		return nil, ErrEventSourceBudget
	}
	// The existing event-source cache charges Bytes for retained projections.
	return &EventPolicySource{store: s, workspace: workspace, version: version, metadata: data, bytes: len(raw)*3 + 1024}, nil
}

// Project the certified logical generation itself. A separately refreshed
// resident policy must not replace this generation's native values.
func (r *ResourceRead) nativeEventPolicy(ctx context.Context) (domain.Bootstrap, error) {
	data := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
	if err := r.reserve(ctx, 2<<20); err != nil {
		return data, err
	}
	err := r.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		switch name {
		case "workspace":
			value, err := r.projectNode(ctx, node, "ID", "URLKey")
			if err == nil {
				data.Workspace = value.Interface().(domain.Workspace)
			}
			return err
		case "teams":
			return node.Children(ctx, func(_ reflect.Value, team metadataLogicalNode) error {
				value, err := r.projectNode(ctx, team, "ID", "Key", "ExternalSource", "Private", "ArchivedAt", "RetiredAt")
				if err == nil {
					data.Teams = append(data.Teams, value.Interface().(domain.Team))
				}
				return err
			})
		case "teamSettings":
			return node.MapEntries(ctx, func(key, settings metadataLogicalNode) error {
				if key.cost > (metadataWorkBytes-512)/8 {
					return ErrMetadataWorkBudget
				}
				cost := key.cost*8 + 512
				if err := r.reserve(ctx, cost); err != nil {
					return err
				}
				id, err := key.Value(ctx, int(cost))
				if err != nil {
					return err
				}
				value, err := r.projectNode(ctx, settings, "ParentTeamID", "Access", "MembershipRestriction",
					"SettingsPermission", "LabelPermission", "MemberPermission")
				if err == nil {
					data.TeamSettings[id.String()] = value.Interface().(domain.TeamSettings)
				}
				return err
			})
		}
		return nil
	})
	return data, err
}
