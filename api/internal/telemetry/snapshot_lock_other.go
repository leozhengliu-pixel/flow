//go:build !unix && !windows

package telemetry

import "os"

const snapshotLockSupported = false

func lockSnapshot(*os.File) bool { return false }
