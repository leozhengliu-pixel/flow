package store

// Reviewed snapshot access and mutation entry points. Each full-workspace
// fallback has an explicit removal owner. Activation waits for its HOT
// entries to be migrated (validateBoundedMetadataCoverage); COLD entries run
// on the COLD facade. The integration coverage test rejects new or stale
// entries.
type metadataFullWorkspaceConsumer struct{ owner string }

func metadataFullWorkspaceConsumers() map[string]metadataFullWorkspaceConsumer {
	return registeredMetadataConsumers()
}
