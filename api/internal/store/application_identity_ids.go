package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

const identityIDsMaxWork = 64 << 20

type applicationIdentityIDsRead struct {
	ids     []string
	release func()
}

func (r *applicationIdentityIDsRead) close() {
	if r != nil {
		r.ids = nil
		if r.release != nil {
			r.release()
			r.release = nil
		}
	}
}

// The caller retains this independent allowance through the complete sync
// batch. Names, logos and other application fields are not enumeration inputs.
// The applications are read as the legacy store holds them
// (installedOAuthApplications): the bounded image does not hold the field.
func (s *SQLiteStore) beginApplicationIdentityIDsRead(ctx context.Context) (*applicationIdentityIDsRead, error) {
	cost := 4096
	ids := []string{}
	err := s.installedOAuthApplications(ctx, "SyncAllApplicationIdentities", func(_ string, _ domain.Bootstrap, apps []domain.OAuthApplication) error {
		for _, app := range apps {
			// Include exact-sized string storage, list slots, error-list
			// growth and errors.Join slots for the complete batch.
			if cost > identityIDsMaxWork-128 ||
				len(app.ClientID) > (identityIDsMaxWork-cost-128)/2 {
				return ErrMetadataWorkBudget
			}
			cost += 128 + 2*len(app.ClientID)
			ids = append(ids, strings.Clone(app.ClientID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		return nil, err
	}
	return &applicationIdentityIDsRead{ids: ids, release: release}, nil
}

func syncApplicationIdentityIDs(ctx context.Context, ids []string, syncOne func(context.Context, string) error) error {
	slices.Sort(ids)
	var errs []error
	for _, clientID := range slices.Compact(ids) {
		errs = append(errs, syncOne(ctx, clientID))
	}
	return errors.Join(errs...)
}
