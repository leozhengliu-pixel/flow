//go:build !darwin && !linux

package store

// physicalMemoryBytes is unknown on this platform; the ceiling falls back
// to its floor unless GOMEMLIMIT, a cgroup limit or
// FLOW_METADATA_MEMORY_BYTES states it.
func physicalMemoryBytes() int64 { return 0 }
