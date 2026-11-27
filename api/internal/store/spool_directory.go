package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Per-process spool directory.
//
// Installed metadata, record spools and migration stages keep their spool
// files under one directory per process:
//
//	$TMPDIR/flow-bounded metadata-<pid>-<random>/
//	    .lock                      held with flock(2) for the process's life
//	    flow-bounded metadata-logical-*, flow-bounded metadata-root-*, flow-bounded metadata-triage-*, ...
//
// A process that stops through Close removes its files one by one and then its
// directory (RemoveMetadataSpoolDirectory). A process that is killed leaves
// the directory behind; the next process that creates its own directory under
// the same parent removes every sibling whose .lock it can take. The kernel
// drops a flock when its holder dies, so a live process, whatever its PID,
// always keeps its lock and its directory, and a reused PID cannot keep a
// dead process's directory alive. Directories whose lock file does not exist
// yet (a process between mkdir and creating the lock) are left alone unless
// they are old.
const (
	metadataSpoolDirectoryPrefix = "flow-p21-"
	metadataSpoolLockName        = ".lock"
	// A directory without a lock file this old belongs to a process that died
	// between mkdir and the lock.
	metadataSpoolUnlockedAge = 10 * time.Minute
)

var metadataSpool struct {
	mu   sync.Mutex
	dir  string
	lock *os.File
}

// metadataSpoolDirectory is the directory a spool file is created in when the
// caller has no directory of its own. It is created once per process, under the
// TMPDIR in force at that first use, and is returned unchanged afterwards so a
// later TMPDIR change cannot move or break installed spools. When it cannot be
// created the result is "" (os.CreateTemp then uses the current temp
// directory, as before).
func metadataSpoolDirectory(configured string) string {
	if configured != "" {
		return configured
	}
	metadataSpool.mu.Lock()
	defer metadataSpool.mu.Unlock()
	if metadataSpool.dir != "" {
		return metadataSpool.dir
	}
	parent := os.TempDir()
	removeStaleMetadataSpoolDirectories(parent)
	dir, err := os.MkdirTemp(parent, fmt.Sprintf("%s%d-", metadataSpoolDirectoryPrefix, os.Getpid()))
	if err != nil {
		return ""
	}
	lock, err := os.OpenFile(filepath.Join(dir, metadataSpoolLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err == nil {
		if locked, lockErr := lockMetadataSpoolFile(lock); lockErr != nil || !locked {
			_ = lock.Close()
			lock = nil
		}
	}
	metadataSpool.dir, metadataSpool.lock = dir, lock
	return dir
}

// RemoveMetadataSpoolDirectory removes the process's spool directory with
// whatever it still holds and releases its lock. Call it after every store has
// been closed; later spools create a fresh directory.
func RemoveMetadataSpoolDirectory() error {
	metadataSpool.mu.Lock()
	defer metadataSpool.mu.Unlock()
	if metadataSpool.dir == "" {
		return nil
	}
	err := os.RemoveAll(metadataSpool.dir)
	if metadataSpool.lock != nil {
		_ = metadataSpool.lock.Close()
	}
	metadataSpool.dir, metadataSpool.lock = "", nil
	return err
}

// removeStaleMetadataSpoolDirectories removes the spool directories under
// parent whose owner is gone. It reads the parent once; a directory costs one
// open and one flock.
func removeStaleMetadataSpoolDirectories(parent string) {
	if !metadataSpoolLockSupported {
		return
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !isMetadataSpoolDirectoryName(name) {
			continue
		}
		dir := filepath.Join(parent, name)
		lock, err := os.OpenFile(filepath.Join(dir, metadataSpoolLockName), os.O_RDWR, 0)
		if err != nil {
			if os.IsNotExist(err) {
				if info, statErr := entry.Info(); statErr == nil && time.Since(info.ModTime()) > metadataSpoolUnlockedAge {
					_ = os.RemoveAll(dir)
				}
			}
			continue
		}
		// Holding the lock while deleting keeps a second sweeper out; the
		// directory's own process, if alive, holds the lock and we skip.
		if locked, err := lockMetadataSpoolFile(lock); err == nil && locked {
			_ = os.RemoveAll(dir)
		}
		_ = lock.Close()
	}
}

// isMetadataSpoolDirectoryName matches flow-bounded metadata-<pid>-<random> and nothing
// else: legacy flow-bounded metadata-* spool files are not directories, and a directory of
// another name is never touched.
func isMetadataSpoolDirectoryName(name string) bool {
	rest, ok := strings.CutPrefix(name, metadataSpoolDirectoryPrefix)
	if !ok {
		return false
	}
	pid, random, ok := strings.Cut(rest, "-")
	if !ok || random == "" {
		return false
	}
	_, err := strconv.ParseUint(pid, 10, 32)
	return err == nil
}
