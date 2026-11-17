package store

import (
	"context"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func metadataLogicalPolicyFields() []string {
	return []string{"teams", "teamSettings", "workspaceSettings", "teamParents"}
}

func (s *SQLiteStore) metadataLogicalPolicy(ctx context.Context, source *metadataLogicalSource) (domain.Bootstrap, int, error) {
	return s.metadataLogicalPolicyAdmitted(ctx, source, nil)
}

func (s *SQLiteStore) metadataLogicalPolicyAdmitted(ctx context.Context, source *metadataLogicalSource, reserve func(uint64) error) (domain.Bootstrap, int, error) {
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
	if reserve != nil {
		if err := reserve(cost + uint64(reflect.TypeFor[domain.Bootstrap]().Size())*2 + 128); err != nil {
			return domain.Bootstrap{}, 0, err
		}
	}
	data, err := source.Select(ctx, MetadataRecordScope{Fields: fields}, int(cost))
	if err != nil {
		return domain.Bootstrap{}, 0, err
	}
	if reserve != nil {
		if err := reserveMetadataPolicyDirectory(ctx, data, reserve); err != nil {
			return domain.Bootstrap{}, 0, err
		}
	}
	domain.RebuildTeamDirectory(&data)
	return data, int(cost), nil
}

func (s *SQLiteStore) readMetadataLogicalWorkspace(ctx context.Context, tx *sqlTx, workspace string) (domain.Bootstrap, copyStamp, int, *metadataLogicalSource, error) {
	return s.readMetadataLogicalWorkspaceAdmitted(ctx, tx, workspace, nil)
}

func (s *SQLiteStore) readMetadataLogicalWorkspaceAdmitted(ctx context.Context, tx *sqlTx, workspace string, reserve func(uint64) error) (domain.Bootstrap, copyStamp, int, *metadataLogicalSource, error) {
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
	normalizeCtx := ctx
	if reserve != nil {
		// The loader's parser lease is not the normalizer's retained base.
		// Let normalization admit its actual base independently.
		normalizeCtx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	}
	normalized, err := s.normalizeMetadataLogicalSource(normalizeCtx, raw)
	if err != nil {
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	data, cost, err := s.metadataLogicalPolicyAdmitted(ctx, normalized, reserve)
	if err != nil {
		normalized.Release()
		return domain.Bootstrap{}, copyStamp{}, 0, nil, err
	}
	return data, normalized.Stamp(), cost, normalized, nil
}
