//go:build unix

package store

import (
	"errors"
	"os"
	"syscall"
)

const metadataSpoolLockSupported = true

// lockMetadataSpoolFile takes the exclusive lock without waiting; false means
// another open file description (a live process) holds it.
func lockMetadataSpoolFile(file *os.File) (bool, error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
