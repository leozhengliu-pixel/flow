package main

import (
	"testing"

	"flow/api/internal/store"
)

// Exercise the production callbacks, authorization and HTTP errors on both
// dispatch paths. Store tests separately compare persisted rows and envelopes.
func TestMetadataEventHandlerFullPathParity(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "metadata"
		if full {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			restore := store.ForceFullMutationsForTesting(full)
			defer restore()
			t.Run("feedback", TestAgentMessageAndSessionFeedback)
			t.Run("approval", TestAgentChatSavedApprovalStillResolves)
			t.Run("changes", TestAgentChangeSetRecordsWritesAndUndoesThem)
			t.Run("release_keys", TestReleaseAccessKeyRotationGraceAndRevocation)
		})
	}
}
