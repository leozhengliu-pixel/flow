package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type frameReadContext struct {
	context.Context
	checks, cancelAt int
	ready            chan struct{}
}

func (c *frameReadContext) Err() error {
	c.checks++
	if c.checks == c.cancelAt {
		close(c.ready)
		<-c.Done()
	}
	return c.Context.Err()
}

func TestMetadataFrameReadPreservesCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    any
		cancelAt int
	}{
		{"string length", "fixture", 2},
		{"slice length", []string{"fixture"}, 3},
		{"child header", struct{ Title string }{"fixture"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer builder.Close()
			node, err := builder.writeValue(reflect.ValueOf(tc.value), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			read := &frameReadContext{Context: ctx, cancelAt: tc.cancelAt, ready: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				_, err := node.Value(read, 1024)
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("frame read returned before cancellation: %v", err)
			case <-read.ready:
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("frame read returned %v, want context.Canceled", err)
			}
		})
	}
}
