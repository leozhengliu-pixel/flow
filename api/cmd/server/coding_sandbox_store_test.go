package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/codingstore"
	"flow/api/internal/config"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// TestCodingSandboxReconcilerEndToEnd runs the sandbox reconciler, warm pool
// and GC over the real SQLite store (through the adapter) and the fake
// provider, including a restart of the reconciler with a new owner.
func TestCodingSandboxReconcilerEndToEnd(t *testing.T) {
	db, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	adapter := codingstore.New(db)
	ctx := context.Background()
	provider := sandbox.NewFake(nil)
	c := config.CodingConfig{
		RuntimeURL: "http://flow:8090", InstanceID: "flow", Image: "img:1", ReconcileInterval: time.Second, OrphanGrace: time.Nanosecond,
		Sizes:    map[string]config.SandboxSize{"small": {CPU: "1", Memory: "8Gi"}},
		Timeouts: config.CodingTimeouts{Start: 10 * time.Minute, StopGrace: time.Second, MaxSession: time.Hour, TokenGrace: time.Minute},
		WarmPool: map[string]int{"small": 2}, WarmPoolMaxAge: time.Hour,
	}
	pool := &sandbox.WarmPool{Store: adapter, Config: c}
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	first := &sandbox.Reconciler{Store: adapter, Provider: provider, Config: c, Owner: "a"}
	if n, err := first.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("first pass: %d %v", n, err)
	}
	counts, _ := adapter.WarmSandboxes(ctx)
	if counts[sandbox.WarmKey("small", "")] != 2 {
		t.Fatalf("warm counts = %v", counts)
	}
	objects, _ := provider.ListManaged(ctx, sandbox.ListFilter{})
	if len(objects) != 2 {
		t.Fatalf("provider objects = %d", len(objects))
	}
	// Binding needs a session row; an unknown session maps to coding.ErrNotFound.
	if _, err := adapter.BindWarmSandbox(ctx, "small", "", "nope", ""); !errors.Is(err, coding.ErrNotFound) {
		t.Fatalf("bind unknown session = %v", err)
	}
	// An orphan (no row) is collected; rows' sandboxes survive.
	if _, err := provider.StartSandbox(ctx, sandbox.SandboxSpec{ID: "orphan", Kind: sandbox.KindSession, Token: "t"}); err != nil {
		t.Fatal(err)
	}
	gc := &sandbox.GC{Store: adapter, Provider: provider, Config: c}
	if n, err := gc.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("gc deleted %d %v", n, err)
	}
	// Recycle everything: a restarted reconciler (new owner) stops them once the
	// rows are due again.
	if n, err := adapter.RecycleWarmSandboxes(ctx, time.Now().Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("recycle %d %v", n, err)
	}
	second := &sandbox.Reconciler{Store: adapter, Provider: provider, Config: c, Owner: "b"}
	if n, err := second.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("second pass: %d %v", n, err)
	}
	if objects, _ := provider.ListManaged(ctx, sandbox.ListFilter{}); len(objects) != 0 {
		t.Fatalf("left = %+v", objects)
	}
	if n, _ := second.RunOnce(ctx); n != 0 {
		t.Fatalf("settled rows leased again: %d", n)
	}
}
