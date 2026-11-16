package store

import (
	"context"

	"flow/api/internal/domain"
)

type legacyResidentPublication struct {
	publication *metadataLogicalPublication
	policy      domain.Bootstrap
	cost        int
	owner       *ResourceRead
}

// The caller holds writeMu through preparation, SQL commit and Install.
// Candidate ownership transfers only on success.
func (s *SQLiteStore) prepareLegacyResidentPublication(ctx context.Context, tx *sqlTx, workspace string, candidate *metadataLogicalSource, baseStamp copyStamp, exact, last bool) (_ *legacyResidentPublication, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db || tx.dialect != s.dialect || candidate == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	owner := &ResourceRead{store: s}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	if candidate.Stamp().workspaceID == "" || candidate.Stamp().workspaceID != baseStamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	if err := validateMetadataLogicalRootIdentity(ctx, candidate); err != nil {
		return nil, err
	}
	// Keep the existing scoped writer's canonical policy preflight separate
	// from the actual native policy installed after the legacy merge.
	probe := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency()}
	probe.metadataResidency.requireCoverage = false
	// The probe is temporary. Its independent cache must not retain another
	// copy of every visited policy page outside the caller's work budget.
	probe.metadataResidency.cacheLimit = 0
	canonical, err := func() (copyStamp, error) {
		scratch := &ResourceRead{store: s}
		defer scratch.Close()
		if err := scratch.reserve(ctx, 2<<20); err != nil {
			return copyStamp{}, err
		}
		_, stamp, _, err := probe.readResidentWorkspacePolicy(ctx, tx, workspace, func(cost uint64) error {
			return scratch.reserve(ctx, cost)
		}, true)
		return stamp, err
	}()
	if err != nil {
		return nil, err
	}
	if canonical.workspaceID != baseStamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	policy, cost, err := s.metadataLogicalPolicyAdmitted(ctx, candidate, func(cost uint64) error {
		return owner.reserve(ctx, cost)
	})
	if err != nil {
		return nil, err
	}
	s.metadataResidency.mu.Lock()
	total := cost
	for key, bytes := range s.metadataResidency.policy {
		if key != workspace {
			total += bytes
		}
	}
	limit := s.metadataResidency.policyLimit
	s.metadataResidency.mu.Unlock()
	if total > limit {
		return nil, ErrMetadataWorkBudget
	}
	publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, baseStamp, exact, last)
	if err != nil {
		return nil, err
	}
	return &legacyResidentPublication{publication: publication, policy: policy, cost: cost, owner: owner}, nil
}

func (p *legacyResidentPublication) Close() {
	if p == nil {
		return
	}
	if p.publication != nil {
		p.publication.Close()
		p.publication = nil
	}
	p.policy = domain.Bootstrap{}
	if p.owner != nil {
		p.owner.Close()
		p.owner = nil
	}
}

func (p *legacyResidentPublication) Install() error {
	if p == nil || p.publication == nil || p.publication.candidate == nil {
		return ErrBoundedMetadataCoverage
	}
	base := p.publication
	final, ready := base.capture.result()
	if !ready {
		return ErrBoundedMetadataCoverage
	}
	sourceStamp := copyStamp{workspaceID: final.workspaceID, revision: -1}
	if base.exact {
		sourceStamp = final
	}
	if err := base.candidate.setStamp(sourceStamp); err != nil {
		return err
	}
	if err := base.store.installResidentPolicySource(base.workspace, p.policy, final, 0, p.cost, base.last, base.candidate); err != nil {
		return err
	}
	base.candidate = nil
	p.policy = domain.Bootstrap{}
	return nil
}
