//go:build !linux

package store

func cgroupMemoryLimit() int64 { return 0 }
