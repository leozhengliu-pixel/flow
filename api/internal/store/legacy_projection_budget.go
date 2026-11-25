package store

// LegacyProjectionBudgetBytes is the memory that legacy resource reads and
// issue bootstrap may hold in workspace copies at the same time: half of the
// metadata memory ceiling (FLOW_METADATA_MEMORY_BYTES, else GOMEMLIMIT, the
// cgroup limit or physical memory). The other half stays free for resident
// state, SQL and requests outside these gates.
func LegacyProjectionBudgetBytes() int64 {
	return metadataMemoryShare(1, 2)
}
