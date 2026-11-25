package store

import (
	"context"
	"slices"

	"flow/api/internal/domain"
)

// MetadataResolvedScope derives additional read keys from the initial selected
// records inside the mutation transaction. It neither authorizes nor permits
// additional writes. The resolver receives an owned snapshot.
type MetadataResolvedScope struct {
	MetadataRecordScope
	ResolveRead func(domain.Bootstrap) ([]RecordKey, error)
}

type metadataScopeResolverKey struct{}

func (s *SQLiteStore) MutateWorkspaceResolved(ctx context.Context, workspace, event, aggregate string, payload any, scope MetadataResolvedScope, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(ctx, workspace, event, aggregate, payload, mutate)
	}
	scope.Fields, scope.Read, scope.Write = slices.Clone(scope.Fields), slices.Clone(scope.Read), slices.Clone(scope.Write)
	ctx = context.WithValue(ctx, metadataScopeResolverKey{}, scope.ResolveRead)
	return s.MutateWorkspaceScoped(ctx, workspace, event, aggregate, payload, scope.MetadataRecordScope, mutate)
}

func (s *SQLiteStore) MutateWorkspaceResolvedWithAggregate(ctx context.Context, workspace, event string, payload any, scope MetadataResolvedScope, mutate func(*domain.Bootstrap) (string, error)) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspaceWithAggregate(ctx, workspace, event, payload, mutate)
	}
	scope.Fields, scope.Read, scope.Write = slices.Clone(scope.Fields), slices.Clone(scope.Read), slices.Clone(scope.Write)
	ctx = context.WithValue(ctx, metadataScopeResolverKey{}, scope.ResolveRead)
	return s.MutateWorkspaceScopedWithAggregate(ctx, workspace, event, payload, scope.MetadataRecordScope, mutate)
}

func metadataScopeResolver(ctx context.Context) func(domain.Bootstrap) ([]RecordKey, error) {
	resolve, _ := ctx.Value(metadataScopeResolverKey{}).(func(domain.Bootstrap) ([]RecordKey, error))
	return resolve
}

func extendMetadataReadScope(scope MetadataRecordScope, keys []RecordKey) (MetadataRecordScope, error) {
	scope.Read, scope.Write = slices.Clone(scope.Read), slices.Clone(scope.Write)
	for _, key := range keys {
		if !metadataRecordSelected(scope, key.Kind, key.ID, false) {
			scope.Read = append(scope.Read, key)
		}
	}
	return scope, validateMetadataRecordScope(scope)
}
