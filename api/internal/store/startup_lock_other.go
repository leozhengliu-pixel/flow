//go:build !unix

package store

// Without flock(2), SQLite startups rely on busy_timeout alone.
func newFileStartupLock(string) startupLockBackend { return nil }
