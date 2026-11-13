package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// MetadataAppendScope permits new records at the end of declared collections.
// Existing records remain governed by MetadataRecordScope. Callers must declare
// uniqueness, position and authorization inputs separately.
type MetadataAppendScope struct {
	MetadataRecordScope
	AppendKinds []string
}

type metadataAppendKindsKey struct{}
type metadataAppendOrdersKey struct{}

func (s *SQLiteStore) MutateWorkspaceAppending(ctx context.Context, workspace, event, aggregate string, payload any, scope MetadataAppendScope, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(ctx, workspace, event, aggregate, payload, mutate)
	}
	return s.MutateWorkspaceAppendingWithAggregate(ctx, workspace, event, payload, scope, func(data *domain.Bootstrap) (string, error) {
		return aggregate, mutate(data)
	})
}

func (s *SQLiteStore) MutateWorkspaceAppendingWithAggregate(ctx context.Context, workspace, event string, payload any, scope MetadataAppendScope, mutate func(*domain.Bootstrap) (string, error)) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspaceWithAggregate(ctx, workspace, event, payload, mutate)
	}
	kinds := slices.Clone(scope.AppendKinds)
	for i, kind := range kinds {
		field, _, ok := metadataRecordType(kind)
		if !ok || reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() != reflect.Slice ||
			kind == "auditLog" || kind == "trash" || kind == "favorites" ||
			slices.Contains(kinds[:i], kind) || slices.Contains(scope.Fields, kind) {
			return fmt.Errorf("%w: invalid append collection %s", ErrMetadataRecordQuery, kind)
		}
	}
	ctx = context.WithValue(ctx, metadataAppendKindsKey{}, kinds)
	return s.MutateWorkspaceScopedWithAggregate(ctx, workspace, event, payload, scope.MetadataRecordScope, mutate)
}

func metadataAppendKinds(ctx context.Context) []string {
	kinds, _ := ctx.Value(metadataAppendKindsKey{}).([]string)
	return kinds
}

func metadataAppendOrdinal(ctx context.Context, key metadataRecordKey) (int, bool) {
	orders, _ := ctx.Value(metadataAppendOrdersKey{}).(map[metadataRecordKey]int)
	order, ok := orders[key]
	return order, ok
}

// Root membership is locked by the scoped writer. Only created IDs and indexed
// ordinals are queried here; existing collection bodies never enter the callback.
func (s *SQLiteStore) prepareMetadataAppends(ctx context.Context, tx *sqlTx, workspace string, before, after domain.Bootstrap, scope MetadataRecordScope) (MetadataRecordScope, map[metadataRecordKey]int, error) {
	kinds := slices.Clone(metadataAppendKinds(ctx))
	if len(kinds) == 0 {
		return scope, nil, nil
	}
	slices.Sort(kinds)
	result := scope
	result.Read, result.Write = slices.Clone(scope.Read), slices.Clone(scope.Write)
	orders := map[metadataRecordKey]int{}
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	for _, kind := range kinds {
		field, _, _ := metadataRecordType(kind)
		old := newMetadataCollection(field, left.Field(field.index))
		next := right.Field(field.index)
		seen := map[string]bool{}
		created := []string{}
		for i := 0; i < next.Len(); i++ {
			id, valid := metadataRecordValueKey(next.Index(i))
			if !valid || id == "" || seen[id] {
				return scope, nil, fmt.Errorf("%w: duplicate or missing append ID in %s", ErrMetadataRecordQuery, kind)
			}
			seen[id] = true
			if _, exists := old.record(id); exists {
				if len(created) > 0 {
					return scope, nil, fmt.Errorf("%w: new %s records must follow existing selected records", ErrMetadataRecordQuery, kind)
				}
				continue
			}
			created = append(created, id)
			key := RecordKey{Kind: kind, ID: id}
			result.Read = slices.DeleteFunc(result.Read, func(read RecordKey) bool { return read == key })
			if !metadataRecordSelected(result, kind, id, true) {
				result.Write = append(result.Write, key)
			}
			if err := validateMetadataRecordScope(result); err != nil {
				return scope, nil, err
			}
		}
		if len(created) == 0 {
			continue
		}
		clause, values := bindList("record_key", created)
		args := append([]any{workspace, kind}, values...)
		lock := ""
		if tx.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		rows, err := tx.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+lock, args...)
		if err != nil {
			return scope, nil, err
		}
		exists := rows.Next()
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return scope, nil, rowErr
		}
		if exists {
			return scope, nil, fmt.Errorf("%w: append ID already exists in %s", ErrMetadataRecordQuery, kind)
		}
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, kind).Scan(&last); err != nil {
			return scope, nil, err
		}
		order := 0
		if last.Valid {
			order = int(last.Int64) + 1
		}
		for _, id := range created {
			orders[metadataRecordKey{field: kind, key: id}] = order
			order++
		}
	}
	return result, orders, nil
}
