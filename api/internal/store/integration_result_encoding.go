package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// ReserveIntegrationDeliveryEncoding preflights a typed delivery response.
// Keep the six-width allowance for compact, escaped inputs. Encoder has no
// returned copy, but Grow uses the original raw width, and compaction that
// introduces HTML escapes can allocate another growth sequence.
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
	limit := uint64(metadataWorkBytes())
	if width > (limit-page)/6 {
		return r.fail(ErrMetadataWorkBudget)
	}
	rawFields := [...][]byte{value.Payload, value.PreviousValues}
	value.Payload, value.PreviousValues = nil, nil
	header, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return r.fail(err)
	}
	cost := uint64(header) + page
	for _, payload := range rawFields {
		n, expansion, scanner, _, err := metadataRawJSONSize(ctx, payload)
		if err != nil {
			return r.fail(err)
		}
		work := n*5 + scanner
		if expansion != 0 {
			work += (n + expansion) * 8
		}
		if work > limit || cost > limit-work {
			return r.fail(ErrMetadataWorkBudget)
		}
		cost += work
	}
	return r.reserve(ctx, max(width*6+page, cost))
}
