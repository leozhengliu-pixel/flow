package main

import "testing"

// Reuse the wave-one oracle so changing its epoch cannot silently weaken
// any of the permission/field-redaction cases it proved equivalent.
func TestScalePermissionDifferentialPermission(t *testing.T) {
	for _, mode := range []string{"shadow", "on", "off"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			TestScalePermissionDifferentialResourceAccess(t)
		})
	}
}

func TestScaleQueuedAndReplayRevocationPermission(t *testing.T) {
	TestScaleRequestAuthorizationMatchesLegacyDuringRevocation(t)
}

func TestScalePeerCompatibilityPermission(t *testing.T) {
	TestScalePeerCompatibilityResourceAccess(t)
}

func TestScaleSharedEpochKeepsPrincipalScopes(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", mode)
			TestScalePermissionDifferentialEventSource(t)
			TestScaleAPIDocumentProjectionMatchesLegacy(t)
		})
	}
}
