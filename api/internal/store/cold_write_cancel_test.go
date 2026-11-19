package store

import (
	"context"
	"errors"
	"testing"
)

// A COLD legacy write whose request was cancelled while the facade refreshed
// reports the cancellation, not "workspace not found".
func TestColdLegacyWriteCancelledRefreshKeepsContextError(t *testing.T) {
	_, view, workspace := scaleP21ColdWriteFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := view.coldLegacyWrite(ctx, workspace, func(ctx context.Context, facade *SQLiteStore) error {
		// The facade's pinned generation differs from the installed one, so
		// taking the write lock must refresh it.
		read := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
		read.mu.Lock()
		pinned := read.sources[workspace]
		read.sources[workspace] = nil
		read.mu.Unlock()
		defer func() {
			read.mu.Lock()
			read.sources[workspace] = pinned
			read.mu.Unlock()
		}()
		cancel() // the client timed out before the facade took the write lock
		facade.writeMu.Lock()
		defer facade.writeMu.Unlock()
		if _, ok := facade.workspaces[workspace]; ok {
			return nil
		}
		return errors.New("workspace not found")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled COLD write = %v, want context.Canceled", err)
	}
}
