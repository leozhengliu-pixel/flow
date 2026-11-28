//go:build unix

package telemetry

import (
	"os"
	"syscall"
)

const snapshotLockSupported = true

func lockSnapshot(file *os.File) bool {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
