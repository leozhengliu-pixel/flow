package store

// Reviewed snapshot access and mutation entry points. Each full-workspace
// fallback has an explicit removal owner; activation waits for this map to
// become empty. The integration coverage test rejects new or stale entries.
type metadataFullWorkspaceConsumer struct{ owner string }

func metadataFullWorkspaceConsumers() map[string]metadataFullWorkspaceConsumer {
	return registeredMetadataConsumers()
}
