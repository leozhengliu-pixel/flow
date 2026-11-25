package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"flow/api/internal/boundedcrdt"
)

// DecodeGeneric owns an independent encoding/json-compatible generic graph.
// A false valid result is a legacy JSON fallback, not an admission failure;
// it leaves this owner usable for the caller's original-payload copy.
func (r *MetadataJSONRead) DecodeGeneric(ctx context.Context, raw []byte) (value any, valid bool, err error) {
	if err := r.check(ctx); err != nil {
		return nil, false, err
	}
	value, release, err := boundedcrdt.DecodeJSON(ctx, func(size uint64) (func(), error) {
		if err := r.check(ctx); err != nil {
			return nil, err
		}
		if size > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(size))
		return release, err
	}, raw)
	if errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, r.fail(err)
	}
	if !r.addChild(release) {
		release()
		return nil, false, r.fail(ErrBoundedMetadataCoverage)
	}
	if err := r.check(ctx); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// EncodeGeneric accepts the normalized graph produced by DecodeGeneric.
// Its exact encoder and independently retained output owner are shared with
// metadata publication. Network/HMAC copies require ReserveEncoded too.
func (r *MetadataJSONRead) EncodeGeneric(ctx context.Context, value any) (raw []byte, valid bool, err error) {
	if err := r.check(ctx); err != nil {
		return nil, false, err
	}
	var release func()
	if value == nil {
		// A typed nil interface is a valid logical node encoding JSON null.
		raw, release, err = r.store.metadataAggregateEncodeOwned(ctx,
			metadataAggregateNativeNode{value: reflect.Zero(reflect.TypeFor[any]())})
	} else {
		raw, release, err = r.store.metadataAggregateEncodeNormalizedOwned(ctx, value)
	}
	if err != nil {
		var syntax *json.SyntaxError
		var unsupportedType *json.UnsupportedTypeError
		var unsupportedValue *json.UnsupportedValueError
		var marshaler *json.MarshalerError
		var codec *metadataAggregateCodecError
		if errors.As(err, &syntax) || errors.As(err, &unsupportedType) ||
			errors.As(err, &unsupportedValue) || errors.As(err, &marshaler) || errors.As(err, &codec) {
			return nil, false, nil
		}
		return nil, false, r.fail(err)
	}
	if !r.addChild(release) {
		release()
		return nil, false, r.fail(ErrBoundedMetadataCoverage)
	}
	if err := r.check(ctx); err != nil {
		return nil, false, err
	}
	return raw, true, nil
}
