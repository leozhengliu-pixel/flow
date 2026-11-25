package main

import (
	"context"

	"flow/api/internal/domain"
)

// This is the literal envelope algorithm with separate decoded-graph and wire
// lifetimes. Preparation is pure and admitted inside the claim transaction;
// no admission failure can leave a newly claimed delivery stuck delivering.
func (s *server) prepareBoundedIntegrationEnvelope(ctx context.Context, delivery domain.IntegrationDelivery) (_ []byte, close func(), err error) {
	decode, err := s.store.BeginMetadataJSONRead(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer decode.Close()
	output, err := s.store.BeginMetadataJSONRead(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			output.Close()
		}
	}()
	copyPayload := func() ([]byte, func(), error) {
		if err := output.ReserveEncoded(ctx, len(delivery.Payload)); err != nil {
			return nil, nil, err
		}
		return append([]byte(nil), delivery.Payload...), output.Close, nil
	}
	if len(delivery.PreviousValues) == 0 {
		return copyPayload()
	}
	value, valid, err := decode.DecodeGeneric(ctx, delivery.Payload)
	if err != nil {
		return nil, nil, err
	}
	envelope, object := value.(map[string]any)
	if !valid || !object || envelope == nil {
		value = nil
		return copyPayload()
	}
	previous, valid, err := decode.DecodeGeneric(ctx, delivery.PreviousValues)
	if err != nil {
		return nil, nil, err
	}
	if !valid {
		envelope, value = nil, nil
		return copyPayload()
	}
	envelope["previousValues"] = previous
	payload, valid, err := output.EncodeGeneric(ctx, envelope)
	envelope, value, previous = nil, nil, nil
	if err != nil {
		return nil, nil, err
	}
	if !valid {
		return copyPayload()
	}
	// The exact encoder owns payload itself. This independent allowance
	// retains string/request/HMAC copies until the network operation ends.
	if err := output.ReserveEncoded(ctx, len(payload)); err != nil {
		return nil, nil, err
	}
	return payload, output.Close, nil
}
