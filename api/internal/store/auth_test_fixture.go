package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// authTestFixtureUserWork bounds one decoded user of the test fixture.
const authTestFixtureUserWork = 1 << 20

// visitAuthTestFixtureUsers is the bounded input of ensureAuthTestFixture, a
// startup-only test-profile seed. It visits the workspace's users in
// Bootstrap.Users order from the installed logical generation (record-backed
// and inline collections alike), decoding one user at a time under a work
// lease, and never touches the caller's SQL transaction.
func (s *SQLiteStore) visitAuthTestFixtureUsers(ctx context.Context, workspace string, visit func(domain.User) error) error {
	ctx, release, err := s.admitMetadataWork(ctx, authTestFixtureUserWork)
	if err != nil {
		return err
	}
	defer release()
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	index, _, ok := metadataLogicalField("users")
	if !ok {
		return ErrBoundedMetadataCoverage
	}
	return source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		value, err := record.Value(ctx, authTestFixtureUserWork)
		if err != nil {
			return err
		}
		user, ok := value.Interface().(domain.User)
		if !ok {
			return ErrBoundedMetadataCoverage
		}
		return visit(user)
	})
}
