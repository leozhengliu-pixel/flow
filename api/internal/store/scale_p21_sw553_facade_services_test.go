package store

import (
	"context"
	"testing"
	"time"
)

// SW553: a COLD facade shares the live store's agent-run wakers and API key
// use queue. A run a COLD legacy write enqueues (or cancels) wakes the claims
// waiting on the live store, as the same write wakes them flag off; an API key
// use recorded through a facade goes through the live store's throttle and
// worker, which a facade alone would not start or shut down.
func TestScaleP21SW553FacadeSharesWakersAndAPIKeyUses(t *testing.T) {
	_, view, workspace := scaleP21ColdReadFixture(t)
	view.metadataResidency.work.limit = metadataWorkBytes
	runs, cancels := view.AgentRunWake(), view.AgentRunCancelWake()
	if err := view.WithColdMetadataRead(t.Context(), scaleP21SW191Read, func(ctx context.Context, facade *SQLiteStore) error {
		if facade == view || facade.coldParent != view {
			t.Fatal("SW553 no COLD facade")
		}
		facade.agentRunWake.wake()
		facade.agentRunCancel.wake()
		if err := facade.RecordAPIKeyUse(ctx, workspace, "sw553-key", ""); err != nil {
			t.Fatal(err)
		}
		if facade.apiKeyUseQueue != nil {
			t.Fatal("SW553 the facade started its own API key use queue")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for name, channel := range map[string]<-chan struct{}{"run": runs, "cancel": cancels} {
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("SW553 a %s wake on the facade did not reach the live store", name)
		}
	}
	view.apiKeyUseMu.Lock()
	_, recorded := view.apiKeyUseTimes[workspace+":sw553-key"]
	started := view.apiKeyUseQueue != nil
	view.apiKeyUseMu.Unlock()
	if !started || !recorded {
		t.Fatal("SW553 the facade's API key use did not go through the live store's queue", started, recorded)
	}
	// The live store's queue is closed with the live store; a second use
	// within the minute is throttled there, not queued again.
	if err := view.RecordAPIKeyUse(t.Context(), workspace, "sw553-key", ""); err != nil {
		t.Fatal(err)
	}
}
