package store

import (
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

const defaultRecordSpoolMaxBytes int64 = 256 << 20

// ErrRecordSpoolBudget means the caller must continue directly from its
// current encoding position, after draining any bytes already in the spool.
var ErrRecordSpoolBudget = errors.New("record spool byte budget exhausted")

var recordSpoolBytes atomic.Int64

func recordSpoolMaxBytes() int64 {
	value, err := strconv.ParseInt(os.Getenv("FLOW_RECORD_SPOOL_MAX_BYTES"), 10, 64)
	if err != nil || value < 0 {
		return defaultRecordSpoolMaxBytes
	}
	return value
}

// RecordSpool shares one process-wide byte budget with HTTP compatibility
// downloads. The file is append-only; readers may seek after encoding ends.
// Close removes the private TMPDIR file and returns its written-byte
// reservation to the budget.
type RecordSpool struct {
	file  *os.File
	bytes int64
	once  sync.Once
	err   error
}

func NewRecordSpool() (*RecordSpool, error) {
	if recordSpoolBytes.Load() >= recordSpoolMaxBytes() {
		return nil, ErrRecordSpoolBudget
	}
	file, err := os.CreateTemp("", "flow-p15-spool-*")
	if err != nil {
		return nil, err
	}
	return &RecordSpool{file: file}, nil
}

func (s *RecordSpool) Write(p []byte) (int, error) {
	length := int64(len(p))
	limit := recordSpoolMaxBytes()
	for {
		used := recordSpoolBytes.Load()
		if length > limit-used {
			return 0, ErrRecordSpoolBudget
		}
		if recordSpoolBytes.CompareAndSwap(used, used+length) {
			break
		}
	}
	n, err := s.file.Write(p)
	s.bytes += int64(n)
	recordSpoolBytes.Add(int64(n) - length)
	return n, err
}

func (s *RecordSpool) Read(p []byte) (int, error) { return s.file.Read(p) }
func (s *RecordSpool) ReadAt(p []byte, offset int64) (int, error) {
	return s.file.ReadAt(p, offset)
}
func (s *RecordSpool) Seek(offset int64, whence int) (int64, error) {
	return s.file.Seek(offset, whence)
}
func (s *RecordSpool) Stat() (os.FileInfo, error) { return s.file.Stat() }

func (s *RecordSpool) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		closeErr := s.file.Close()
		removeErr := os.Remove(s.file.Name())
		// Keep charging an orphan if removal fails, so later requests cannot
		// reuse space that is still occupied on the temporary volume.
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			recordSpoolBytes.Add(-s.bytes)
			removeErr = nil
		}
		s.err = errors.Join(closeErr, removeErr)
	})
	return s.err
}

var _ io.ReadSeekCloser = (*RecordSpool)(nil)
