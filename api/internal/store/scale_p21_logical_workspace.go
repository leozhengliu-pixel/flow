package store

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

func metadataLogicalPolicyFields() []string {
	return []string{"teams", "teamSettings", "workspaceSettings", "teamParents"}
}

func (s *SQLiteStore) metadataLogicalPolicy(ctx context.Context, source *metadataLogicalSource) (domain.Bootstrap, int, error) {
	cost := uint64(4096)
	fields := metadataLogicalPolicyFields()
	if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "workspace" || name == "viewer" || name == "viewerRole" || slices.Contains(fields, name) {
			cost += node.cost
		}
		if cost > uint64(s.metadataResidency.policyLimit) {
			return ErrMetadataWorkBudget
		}
		return nil
	}); err != nil {
		return domain.Bootstrap{}, 0, err
	}
	data, err := source.Select(ctx, MetadataRecordScope{Fields: fields}, int(cost))
	if err != nil {
		return domain.Bootstrap{}, 0, err
	}
	domain.RebuildTeamDirectory(&data)
	return data, int(cost), nil
}

func (s *SQLiteStore) readMetadataLogicalWorkspace(ctx context.Context, tx *sqlTx, workspace string) (domain.Bootstrap, copyStamp, int, *metadataLogicalSource, error) {
	ctx = s.metadataLogicalScratchContext(ctx, workspace)
	// Preserve journal, manifest and canonical-root validation while the
	// logical producer is installed independently of the policy projection.
	raw, err := s.readRawMetadataLogicalSourceValidated(ctx, tx, workspace)
	if err != nil {
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	defer raw.Release()
	if err := validateMetadataLogicalRootIdentity(ctx, raw); err != nil {
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	normalized, err := s.normalizeMetadataLogicalSource(ctx, raw)
	if err != nil {
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	data, cost, err := s.metadataLogicalPolicy(ctx, normalized)
	if err != nil {
		normalized.Release()
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	return data, normalized.Stamp(), cost, normalized, nil
}
