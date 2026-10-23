//go:build unix

package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
)

// fileStartupLock serializes SQLite startups with flock(2) on a file beside
// the database. Two processes opening the same SQLite file therefore run
// their schema work one after the other instead of failing on SQLITE_BUSY
// halfway through; the kernel drops the lock if the holder dies.
type fileStartupLock struct {
	path string
	file *os.File
}

func newFileStartupLock(path string) startupLockBackend { return &fileStartupLock{path: path} }

func (l *fileStartupLock) tryLock(context.Context) (bool, error) {
	if l.file == nil {
		file, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return false, err
		}
		l.file = file
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return false, nil
		}
		return false, err
	}
	// Record the holder for processes that wait behind it.
	if err := l.file.Truncate(0); err == nil {
		_, _ = l.file.WriteAt([]byte(startupProcessName()+"\n"), 0)
	}
	return true, nil
}

func (l *fileStartupLock) holder(context.Context) string {
	raw, err := os.ReadFile(l.path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return "another process"
	}
	return strings.TrimSpace(string(raw))
}

func (l *fileStartupLock) unlock(context.Context) (bool, error) {
	if l.file == nil {
		return false, nil
	}
	return true, syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
}

func (l *fileStartupLock) ping(context.Context) error { return nil }

func (l *fileStartupLock) close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
