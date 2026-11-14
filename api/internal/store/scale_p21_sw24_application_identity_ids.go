package store

import (
	"context"
	"errors"
	"slices"
	"strings"
)

const scaleP21SW24IdentityIDsMaxWork = 64 << 20

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
func (s *SQLiteStore) beginApplicationIdentityIDsRead(ctx context.Context) (*applicationIdentityIDsRead, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count, cost := 0, 4096
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			// Include exact-sized string storage, list slots, error-list
			// growth and errors.Join slots for the complete batch.
			if cost > scaleP21SW24IdentityIDsMaxWork-128 ||
				len(app.ClientID) > (scaleP21SW24IdentityIDsMaxWork-cost-128)/2 {
				return nil, ErrMetadataWorkBudget
			}
			cost += 128 + 2*len(app.ClientID)
			count++
		}
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, count)
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			ids = append(ids, strings.Clone(app.ClientID))
		}
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
