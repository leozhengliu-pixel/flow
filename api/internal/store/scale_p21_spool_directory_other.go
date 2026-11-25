//go:build !unix

package store

import "os"

// Without flock(2) liveness cannot be proved, so stale directories are never
// swept.
const metadataSpoolLockSupported = false

func lockMetadataSpoolFile(*os.File) (bool, error) { return false, nil }
