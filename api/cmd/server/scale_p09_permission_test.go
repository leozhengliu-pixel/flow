package main

import "testing"

// Reuse the wave-one oracle so changing its epoch cannot silently weaken
// any of the permission/field-redaction cases it proved equivalent.
func TestScaleP09PermissionDifferential(t *testing.T) {
	for _, mode := range []string{"shadow", "on", "off"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			TestScaleP03PermissionDifferential(t)
		})
	}
}

func TestScaleP09QueuedAndReplayRevocation(t *testing.T) {
	TestScaleP03RequestAuthorizationMatchesLegacyDuringRevocation(t)
}

func TestScaleP09PeerCompatibility(t *testing.T) {
	TestScaleP03PeerCompatibility(t)
}

func TestScaleP09SharedEpochKeepsPrincipalScopes(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			TestScaleP12PermissionDifferential(t)
			TestScaleP12APIDocumentProjectionMatchesLegacy(t)
		})
	}
}
