package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestScaleP21SW324DeliveryRawFrameComparison(t *testing.T) {
	values := []json.RawMessage{nil, {}, {'x'}, {'x', 0, 'y'}, {'x', 0, 'z'}, {'x', 0, 'y', 0}}
	large := bytes.Repeat([]byte{'x'}, 3*4096)
	changed := append([]byte(nil), large...)
	changed[2*4096+1] = 'z'
	values = append(values, large, changed, large[:2*4096+1])
	for index, before := range values {
		builder, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { builder.Close() })
		node, err := builder.writeValue(reflect.ValueOf(before), 0, false)
		if err != nil {
			t.Fatal(err)
		}
		for other, after := range values {
			equal, err := integrationDeliveryRawFrameEqual(t.Context(), node, after)
			if err != nil || equal != reflect.DeepEqual(before, after) {
				t.Fatalf("pair %d/%d: equal=%t err=%v", index, other, equal, err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if equal, err := integrationDeliveryRawFrameEqual(ctx, node, before); equal || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled comparison succeeded", equal, err)
		}
	}
}

type scaleP21SW324CancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *scaleP21SW324CancelContext) Err() error {
	c.checks++
	if c.checks == 10 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScaleP21SW324DeliveryRawFrameReadCancellation(t *testing.T) {
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	value := json.RawMessage(bytes.Repeat([]byte{'x'}, 64<<10))
	node, err := builder.writeValue(reflect.ValueOf(value), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scaleP21SW324CancelContext{Context: base, cancel: cancel}
	if equal, err := integrationDeliveryRawFrameEqual(ctx, node, value); equal ||
		!errors.Is(err, context.Canceled) || ctx.checks < 10 {
		t.Fatal("streaming comparison ignored mid-read cancellation", equal, err, ctx.checks)
	}
	if equal, err := integrationDeliveryRawFrameEqual(t.Context(), node, value); !equal || err != nil {
		t.Fatal("canceled comparison damaged its independently retained source", equal, err)
	}
}
