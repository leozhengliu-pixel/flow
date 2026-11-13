package main

import "testing"

func TestScaleP12ActivateB1CredentialTelemetryWithUnreadyJournal(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_SYNC_V2", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			TestCredentialTelemetryIsFilteredBeforeProjectionAndOldReplay(t)
		})
	}
}
