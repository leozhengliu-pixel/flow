package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestScaleP21MetadataOutputOwner(t *testing.T) {
	var closes []func()
	ctx, cancel := context.WithCancel(t.Context())
	ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
		closes = append(closes, close)
		return true
	})
	var released atomic.Int32
	if !registerMetadataOutputClose(ctx, func() { released.Add(1) }) {
		t.Fatal("output owner was not transferred")
	}
	cancel()
	ctx = context.WithoutCancel(ctx)
	if released.Load() != 0 || ctx.Err() != nil {
		t.Fatal("cancellation released a still-used output")
	}
	legacy := errors.New("legacy SQL or JSON error")
	recordMetadataOutputError(ctx, legacy)
	if metadataOutputError(ctx) != nil {
		t.Fatal("legacy ignored error changed projection behavior")
	}
	first := fmt.Errorf("selected output: %w", ErrMetadataWorkBudget)
	recordMetadataOutputError(ctx, first)
	recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	if metadataOutputError(ctx) != first {
		t.Fatal("first bounded failure was not retained")
	}
	nested := WithMetadataOutputOwner(ctx, nil)
	if registerMetadataOutputClose(nested, func() {}) || metadataOutputError(nested) != nil {
		t.Fatal("nested operation did not own its boundary")
	}
	if registerMetadataOutputClose(context.Background(), func() {}) ||
		registerMetadataOutputClose(ctx, nil) {
		t.Fatal("missing output owner accepted registration")
	}
	for _, close := range closes {
		close()
	}
	if released.Load() != 1 {
		t.Fatal("output was not retained until its containing operation ended")
	}
}

func TestScaleP21MetadataOutputOwnerConcurrentClose(t *testing.T) {
	read := &ResourceRead{}
	ctx := read.metadataOutputContext(t.Context())
	var released atomic.Int32
	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			close := func() { released.Add(1) }
			if !registerMetadataOutputClose(ctx, close) {
				close()
			}
			recordMetadataOutputError(ctx, ErrMetadataWorkBudget)
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		read.Close()
	}()
	workers.Wait()
	read.Close()
	if released.Load() != 100 || !errors.Is(metadataOutputError(ctx), ErrMetadataWorkBudget) {
		t.Fatal("concurrent output release or first error was lost", released.Load())
	}
	if registerMetadataOutputClose(ctx, func() { t.Error("closed owner retained work") }) {
		t.Fatal("closed owner accepted output")
	}
}
