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

// ErrRecordSpoolContended is a temporary refusal for scratch space the
// operation could reserve without concurrent use of the process quota.
var ErrRecordSpoolContended error = recordSpoolContendedError{}

type recordSpoolContendedError struct{}

func (recordSpoolContendedError) Error() string { return ErrRecordSpoolBudget.Error() }
func (recordSpoolContendedError) Unwrap() error { return ErrRecordSpoolBudget }

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
	file    *os.File
	bytes   int64
	once    sync.Once
	err     error
	counter *atomic.Int64
	limit   int64
	maxSize int64
	demand  *atomic.Int64
	owned   bool
}

func NewRecordSpool() (*RecordSpool, error) {
	return newRecordSpool(false)
}

// An owned spool is the operation's only use of the compatibility spool
// budget. A group of spools requires a separate aggregate size check.
func newRecordSpool(owned bool) (*RecordSpool, error) {
	limit := recordSpoolMaxBytes()
	if recordSpoolBytes.Load() >= limit {
		return nil, recordSpoolRefusal(owned, 0, 1, limit)
	}
	file, err := os.CreateTemp("", "flow-p15-spool-*")
	if err != nil {
		return nil, err
	}
	return &RecordSpool{file: file, owned: owned}, nil
}

func recordSpoolRefusal(owned bool, size, additional, limit int64) error {
	if owned && additional <= limit-size {
		return ErrRecordSpoolContended
	}
	return ErrRecordSpoolBudget
}

func (s *RecordSpool) appendBudgetError(size, used, limit int64) error {
	if size < 0 || (s.maxSize > 0 && size > s.maxSize-s.bytes) {
		return ErrRecordSpoolBudget
	}
	if size <= limit-used {
		return nil
	}
	if metadataLogicalDiskContended(s.demand, size, limit) {
		return ErrRecordSpoolContended
	}
	return recordSpoolRefusal(s.owned, s.bytes, size, limit)
}

func (s *RecordSpool) Write(p []byte) (int, error) {
	length := int64(len(p))
	counter, limit := s.byteBudget()
	for {
		used := counter.Load()
		if err := s.appendBudgetError(length, used, limit); err != nil {
			return 0, err
		}
		if counter.CompareAndSwap(used, used+length) {
			break
		}
	}
	n, err := s.file.Write(p)
	s.bytes += int64(n)
	counter.Add(int64(n) - length)
	if s.demand != nil {
		s.demand.Add(int64(n))
	}
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

// CanAppend is an advisory size check before encoding a known large response.
// Write still reserves bytes atomically against concurrent spool writers.
func (s *RecordSpool) CanAppend(size int64) bool {
	return s.checkAppend(size) == nil
}

func (s *RecordSpool) checkAppend(size int64) error {
	counter, limit := s.byteBudget()
	return s.appendBudgetError(size, counter.Load(), limit)
}

func (s *RecordSpool) byteBudget() (*atomic.Int64, int64) {
	if s.counter != nil {
		return s.counter, s.limit
	}
	return &recordSpoolBytes, recordSpoolMaxBytes()
}

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
			counter, _ := s.byteBudget()
			counter.Add(-s.bytes)
			if s.demand != nil {
				s.demand.Add(-s.bytes)
			}
			removeErr = nil
		}
		s.err = errors.Join(closeErr, removeErr)
	})
	return s.err
}

var _ io.ReadSeekCloser = (*RecordSpool)(nil)
