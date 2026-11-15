package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/store"
)

func TestScaleP12SocketAccessSweepLifetime(t *testing.T) {
	for _, test := range []struct {
		name           string
		zeroStore      bool
		closeStore     bool
		noCoordination bool
	}{
		{name: "store-close", closeStore: true},
		{name: "store-close-without-coordination", closeStore: true, noCoordination: true},
		{name: "coordination-cancel"},
		{name: "zero-store-coordination-cancel", zeroStore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &store.SQLiteStore{}
			closeRepository := func() {}
			if !test.zeroStore {
				var err error
				repository, err = store.OpenSQLite(filepath.Join(t.TempDir(), "sweep.db"))
				if err != nil {
					t.Fatal(err)
				}
				closed := false
				closeRepository = func() {
					if closed {
						return
					}
					closed = true
					if err := repository.Close(); err != nil {
						t.Errorf("close sweep store: %v", err)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			service := &server{store: repository, coordinationContext: ctx}
			if test.noCoordination {
				service.coordinationContext = nil
			}
			var coordinationDone <-chan struct{}
			if service.coordinationContext != nil {
				coordinationDone = service.coordinationContext.Done()
			}
			storeDone := repository.WorkerContext().Done()
			done := make(chan struct{})
			go func() {
				service.runSocketAccessSweep(coordinationDone, storeDone)
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				closeRepository()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("socket access sweep did not stop during cleanup")
				}
			})
			select {
			case <-done:
				t.Fatal("socket access sweep exited with both lifetimes active")
			case <-time.After(10 * time.Millisecond):
			}
			if test.closeStore {
				closeRepository()
				if ctx.Err() != nil {
					t.Fatal("closing the store canceled the independent coordination context")
				}
			} else {
				cancel()
				if repository.WorkerContext().Err() != nil {
					t.Fatal("canceling coordination canceled the independent store context")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("socket access sweep did not stop when its lifetime ended")
			}
		})
	}
}
