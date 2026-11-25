package store

import (
	"context"
	"errors"
	"sync"
)

type metadataOutputOwnerKey struct{}

type metadataOutputOwner struct {
	register func(func()) bool
	mu       sync.Mutex
	err      error
}

// WithMetadataOutputOwner binds decoded output to its containing request or
// operation. The registrar must reject registration after that lifetime ends.
// Cancellation does not release values that are still being encoded or used.
func WithMetadataOutputOwner(ctx context.Context, register func(func()) bool) context.Context {
	return context.WithValue(ctx, metadataOutputOwnerKey{}, &metadataOutputOwner{register: register})
}

func registerMetadataOutputClose(ctx context.Context, close func()) bool {
	owner, ok := ctx.Value(metadataOutputOwnerKey{}).(*metadataOutputOwner)
	return ok && owner != nil && owner.register != nil && close != nil && owner.register(close)
}

func metadataOutputOwnerPresent(ctx context.Context) bool {
	owner, ok := ctx.Value(metadataOutputOwnerKey{}).(*metadataOutputOwner)
	return ok && owner != nil
}

// Old projection code deliberately ignores several SQL/decoding errors.
// Newly introduced admission and coverage failures must not become an empty,
// apparently successful projection through those legacy error boundaries.
func recordMetadataOutputError(ctx context.Context, err error) error {
	if !errors.Is(err, ErrMetadataWorkBudget) && !errors.Is(err, ErrBoundedMetadataCoverage) {
		return err
	}
	if owner, ok := ctx.Value(metadataOutputOwnerKey{}).(*metadataOutputOwner); ok && owner != nil {
		owner.mu.Lock()
		if owner.err == nil {
			owner.err = err
		}
		owner.mu.Unlock()
	}
	return err
}

func metadataOutputError(ctx context.Context) error {
	if owner, ok := ctx.Value(metadataOutputOwnerKey{}).(*metadataOutputOwner); ok && owner != nil {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		return owner.err
	}
	return nil
}

func (r *ResourceRead) metadataOutputContext(ctx context.Context) context.Context {
	return WithMetadataOutputOwner(ctx, func(close func()) bool {
		if r == nil || close == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			return false
		}
		r.release = append(r.release, close)
		return true
	})
}
