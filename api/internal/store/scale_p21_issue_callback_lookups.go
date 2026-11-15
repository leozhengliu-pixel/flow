package store

import "context"

type mutationCallbackLookupsKey struct{}

type mutationCallbackLookups struct {
	owner   *SQLiteStore
	tx      *sqlTx
	former  func() map[string]bool
	updates func(string) ([][]byte, error)
}

// Owned issue callbacks retain these read-only services through verification
// and commit. Other callbacks keep their literal lookup preparation.
func withMutationCallbackLookups(ctx context.Context, owner *SQLiteStore, tx *sqlTx, former func() map[string]bool, updates func(string) ([][]byte, error)) context.Context {
	return context.WithValue(ctx, mutationCallbackLookupsKey{}, &mutationCallbackLookups{owner, tx, former, updates})
}

func mutationCallbackLookupsFor(ctx context.Context, owner *SQLiteStore, tx *sqlTx, kind string) (*mutationCallbackLookups, error) {
	if kind != "issue" {
		return nil, nil
	}
	value, present := ctx.Value(mutationCallbackLookupsKey{}).(*mutationCallbackLookups)
	if !present {
		return nil, nil
	}
	if value == nil || owner == nil || value.owner != owner || value.tx != tx || tx == nil ||
		tx.database != owner.db || value.former == nil || value.updates == nil {
		return nil, ErrMetadataRecordQuery
	}
	return value, nil
}
