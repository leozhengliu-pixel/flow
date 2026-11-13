package store

import (
	"context"
	"testing"
)

func TestScaleP12ActivateB1UnreadyEpochsFallBack(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			for _, repo := range []*SQLiteStore{nil, {}} {
				if repo.RealtimeEpochsReady() {
					t.Fatal("uninitialized store enabled epoch reuse")
				}
				ctx := context.WithValue(t.Context(), struct{ name string }{"b1-readiness"}, "retained")
				got, err := repo.LiveEventContext(ctx)
				if err != nil || got != ctx {
					t.Fatalf("unready store changed the legacy context: same=%v err=%v", got == ctx, err)
				}
			}
		})
	}
}
