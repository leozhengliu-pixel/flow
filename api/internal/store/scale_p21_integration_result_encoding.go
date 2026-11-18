package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// ReserveIntegrationDeliveryEncoding preflights the ordinary typed delivery
// result encoder. Six rounded output widths retain the existing SW205 bound
// for Marshal's string growth, encoder growth, returned bytes and capacity
// slack. This domain record has no map-sort work outside its raw JSON bytes.
func (r *MetadataJSONRead) ReserveIntegrationDeliveryEncoding(ctx context.Context, value domain.IntegrationDelivery) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	raw, release, err := r.store.metadataAggregateEncodeOwned(ctx,
		metadataAggregateNativeNode{value: reflect.ValueOf(value)})
	if err != nil {
		return r.fail(err)
	}
	size := uint64(len(raw))
	raw = nil
	release()
	const page = uint64(64 << 10)
	width := (size + page - 1) / page * page
	if width > (metadataWorkBytes-(64<<10))/6 {
		return r.fail(ErrMetadataWorkBudget)
	}
	return r.reserve(ctx, width*6+(64<<10))
}
