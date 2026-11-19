package store

import "context"

type integrationDeliveryPagesKey struct{}

const integrationDeliveryPageWorkBytes = 8 << 20

type integrationDeliveryPageOwner struct {
	work     *ResourceRead
	admitted bool
}

// The transaction's journal projector reads the canonical row too. Its larger
// physical page and SQL append buffers share this private owner. Four copies
// of a 1 MiB append, four copies of a 256 KiB read, and fixed parser/driver
// scratch fit independently of the row encoder's existing 2 MiB allowance.
func (s *SQLiteStore) integrationDeliveryPageContext(ctx context.Context) (context.Context, func(), error) {
	owner := &ResourceRead{store: s}
	if integrationDeliveryStreamEvent(ctx) == "" || !s.BoundedMetadataEnabled() {
		return nil, nil, ErrBoundedMetadataCoverage
	}
	if err := owner.reserve(ctx, integrationDeliveryPageWorkBytes); err != nil {
		owner.Close()
		return nil, nil, err
	}
	pages := &integrationDeliveryPageOwner{work: owner, admitted: true}
	return context.WithValue(ctx, integrationDeliveryPagesKey{}, pages), owner.Close, nil
}

func integrationDeliveryCanonicalPageBytes(ctx context.Context, tx *sqlTx) (int, error) {
	owned, err := integrationDeliveryPagesOwned(ctx, tx)
	if err != nil || !owned {
		return 0, err
	}
	return MetadataRecordPageMaxBytes, nil
}

func integrationDeliverySQLChunkBytes(ctx context.Context, tx *sqlTx) (int, error) {
	owned, err := integrationDeliveryPagesOwned(ctx, tx)
	if err != nil || !owned {
		return 0, err
	}
	return metadataSQLIntegrationOwnedChunkBytes, nil
}

func integrationDeliveryPagesOwned(ctx context.Context, tx *sqlTx) (bool, error) {
	pages, _ := ctx.Value(integrationDeliveryPagesKey{}).(*integrationDeliveryPageOwner)
	if pages == nil {
		if ctx.Value(integrationDeliveryPagesKey{}) != nil {
			return false, ErrBoundedMetadataCoverage
		}
		return false, nil
	}
	if !pages.admitted || pages.work == nil {
		return false, ErrBoundedMetadataCoverage
	}
	owner := pages.work
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed || owner.store == nil || len(owner.release) != 1 || tx == nil ||
		owner.store.db != tx.database || integrationDeliveryStreamEvent(ctx) == "" {
		return false, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}
