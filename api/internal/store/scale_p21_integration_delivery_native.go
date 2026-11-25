package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

// The ordinary scalar fields keep the existing typed decoder and reservation.
// Opaque RawMessage leaves instead own one exact-sized native byte allocation,
// admitted before allocation and filled directly from their borrowed frame.
func (r *ResourceRead) integrationDeliveryNative(ctx context.Context, node metadataLogicalNode) (domain.IntegrationDelivery, error) {
	if node.typ != reflect.TypeFor[domain.IntegrationDelivery]() {
		return domain.IntegrationDelivery{}, ErrBoundedMetadataCoverage
	}
	header, err := r.projectNode(ctx, node,
		"ID", "IdempotencyKey", "ConnectionID", "EventType", "ResourceID", "Channel",
		"Status", "Attempts", "NextAttemptAt", "LastError", "CreatedAt", "UpdatedAt")
	if err != nil {
		return domain.IntegrationDelivery{}, err
	}
	delivery := header.Interface().(domain.IntegrationDelivery)
	for _, field := range []struct {
		name   string
		target *json.RawMessage
	}{
		{"Payload", &delivery.Payload},
		{"PreviousValues", &delivery.PreviousValues},
	} {
		leaf, err := issueHotField(ctx, node, field.name)
		if err != nil {
			return domain.IntegrationDelivery{}, err
		}
		*field.target, err = r.integrationDeliveryRawMessage(ctx, leaf)
		if err != nil {
			return domain.IntegrationDelivery{}, err
		}
	}
	return delivery, nil
}

func (r *ResourceRead) integrationDeliveryRawMessage(ctx context.Context, node metadataLogicalNode) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node.typ != reflect.TypeFor[json.RawMessage]() || node.source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if node.unavailable {
		return nil, fmt.Errorf("%w: installed logical subtree %s is unavailable", ErrBoundedMetadataCoverage, node.typ)
	}
	if err := node.source.Retain(); err != nil {
		return nil, err
	}
	defer node.source.Release()
	cursor := metadataLogicalCursor{node: node, offset: node.offset + 16, ctx: ctx}
	present, err := cursor.byte()
	if err != nil {
		return nil, err
	}
	if present == 0 {
		if node.size != 17 {
			return nil, ErrMetadataRecordQuery
		}
		return nil, nil
	}
	if present != 1 {
		return nil, ErrMetadataRecordQuery
	}
	size, err := cursor.uint()
	if err != nil {
		return nil, err
	}
	if node.size < 25 || size != uint64(node.size-25) {
		return nil, ErrMetadataRecordQuery
	}
	if size > metadataWorkBytes-8192 {
		return nil, ErrMetadataWorkBudget
	}
	// Cover Go's allocation classes, byte-slice header and owner bookkeeping.
	// This is one explicitly allocated byte slice, not an assumed decode factor.
	allocation := ((size + 8191) &^ uint64(8191)) + 64
	if err := r.reserve(ctx, allocation); err != nil {
		return nil, err
	}
	raw := make(json.RawMessage, int(size))
	if err := node.StreamBytes(ctx, func(input io.Reader) error {
		_, err := io.ReadFull(input, raw)
		return err
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
