package store

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
)

// Spool I/O of a logical storage: the one layer between the typed frames and
// the temporary file.
//
// A logical spool is read as many small frame fields (8-byte sizes, costs,
// counts and scalars) and written as many small appends and header patches.
// Each was one pread or write system call: a COLD decode of a 5,000-issue
// workspace and a HOT document edit spent nearly all their time in system
// calls. Small reads are now served from a few cached blocks, appends are
// buffered, and a spool that never outgrows its buffer (most per-record
// spools of a write) never creates a file at all.
//
// Layout. Bytes [0, flushed) are in the file; bytes [flushed, flushed +
// len(pending)) are only in pending. The file is created (under pendingMu)
// the first time bytes must leave the buffer, a patch extends past the end or
// its name is asked for; until then flushed is 0 and every read goes through
// pendingMu.
//
// Invariants:
//
//  1. Flushed bytes are written before flushed is advanced (an atomic store),
//     so a reader that loads flushed may read [0, flushed) from the file
//     without a lock: those bytes change only through patches (3).
//  2. All mutations (append, flush, patch, settle, discard) hold pendingMu,
//     so there is one writer at a time. Readers never take pendingMu for
//     flushed bytes: concurrent readers of one installed storage do not
//     serialize on a lock held across a pread.
//  3. A patch of flushed bytes runs as a sequence: patches becomes odd, the
//     file is written, every cached block overlapping the range is dropped,
//     patches becomes even again. A block read that saw an odd count, or a
//     different count after it published the block, is never made servable:
//     a block is published not ready, the count is checked again, and only
//     then marked ready. Every read that starts after a patch returned sees
//     the patched bytes (a block published before the patch began is dropped
//     by its invalidation; one published after it began fails the check).
//  4. Cached blocks are immutable once published and never cover bytes past
//     the flushed end they were read under, so appends and flushes leave them
//     valid; a reader holding a dropped block may finish with it.
//  5. Patches of bytes still in pending are copied into pending; the bytes
//     reach the file with the next flush. A patch past the logical end writes
//     through, as os.File.WriteAt would.
//  6. A failed flush leaves every byte in pending, and reads keep serving it.
//  7. discard (the last Release) marks the storage closed and drops the
//     buffer, the cache and the frame memo; no file is created afterwards,
//     and a never-created file has nothing to remove.
//
// The cache and the buffer (at most 64 KiB per storage) are covered by the
// storage's descriptor charge (metadataLogicalDescriptorBytes).
const (
	metadataLogicalReadBlock  = 8 << 10
	metadataLogicalReadSlots  = 4
	metadataLogicalWriteBytes = 32 << 10
)

var errMetadataLogicalNegativeOffset = errors.New("installed logical metadata source: negative offset")

// metadataLogicalBlock is one immutable cached block: the spool bytes from
// index*metadataLogicalReadBlock (shorter than a block at the spool's end).
// Only a ready block is served from its slot.
type metadataLogicalBlock struct {
	index int64
	data  []byte
	ready atomic.Bool
}

// metadataLogicalSpoolReadBytes counts the spool bytes callers read, served
// from a cache or not, while metadataLogicalSpoolCounting is set (tests): the
// work a metadata operation does on its frames. Off, it costs no shared
// cache-line write per read.
var (
	metadataLogicalSpoolCounting  atomic.Bool
	metadataLogicalSpoolReadBytes atomic.Int64
)

// Tests pause a patch or a block read at these points to force the
// interleavings invariant 3 covers; production never sets the hook.
type metadataLogicalSpoolPoint int

const (
	metadataLogicalSpoolPatchBegun metadataLogicalSpoolPoint = iota + 1
	metadataLogicalSpoolBlockRead
)

var metadataLogicalSpoolHook atomic.Pointer[func(*metadataLogicalStorage, metadataLogicalSpoolPoint)]

func metadataLogicalSpoolTestPoint(s *metadataLogicalStorage, point metadataLogicalSpoolPoint) {
	if hook := metadataLogicalSpoolHook.Load(); hook != nil {
		(*hook)(s, point)
	}
}

// Dir is the directory the spool's file is (or will be) created in.
func (s *metadataLogicalStorage) Dir() string {
	if s.directory == "" {
		return os.TempDir()
	}
	return s.directory
}

// Name creates the spool file if needed and returns its path ("" once the
// storage is closed or when the file cannot be created).
func (s *metadataLogicalStorage) Name() string {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed || s.createLocked() != nil {
		return ""
	}
	return s.file.Name()
}

func (s *metadataLogicalStorage) createLocked() error {
	if s.file != nil {
		return nil
	}
	if s.closed {
		return errMetadataLogicalClosed
	}
	file, err := os.CreateTemp(s.directory, "flow-p21-logical-*")
	if err != nil {
		return err
	}
	s.file = file
	return nil
}

// append adds data at the end of the spool.
func (s *metadataLogicalStorage) append(data []byte) error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed {
		return errMetadataLogicalClosed
	}
	if len(s.pending)+len(data) > metadataLogicalWriteBytes {
		if err := s.flushLocked(); err != nil {
			return err
		}
		if len(data) >= metadataLogicalWriteBytes {
			if err := s.createLocked(); err != nil {
				return err
			}
			at := s.flushed.Load()
			count, err := s.file.WriteAt(data, at)
			if count > 0 {
				s.flushed.Store(at + int64(count))
			}
			if err == nil && count != len(data) {
				err = io.ErrShortWrite
			}
			return err
		}
	}
	// The buffer grows with the spool up to its bound: most spools hold one
	// small record.
	if need := len(s.pending) + len(data); need > cap(s.pending) {
		grown := make([]byte, len(s.pending), min(max(need, 2*cap(s.pending), 512), metadataLogicalWriteBytes))
		copy(grown, s.pending)
		s.pending = grown
	}
	s.pending = append(s.pending, data...)
	return nil
}

// settle ends a builder's appends: a spool with a file writes its buffer out
// and returns its memory; a spool that never outgrew its buffer stays in
// memory.
func (s *metadataLogicalStorage) settle() error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed {
		return errMetadataLogicalClosed
	}
	if s.file == nil {
		return nil
	}
	err := s.flushLocked()
	if err == nil {
		s.pending = nil
	}
	return err
}

func (s *metadataLogicalStorage) flushLocked() error {
	if len(s.pending) == 0 {
		return nil
	}
	if err := s.createLocked(); err != nil {
		return err
	}
	at := s.flushed.Load()
	count, err := s.file.WriteAt(s.pending, at)
	if err == nil && count != len(s.pending) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// The buffer still holds every byte; reads keep serving it.
		return err
	}
	s.flushed.Store(at + int64(count))
	s.pending = s.pending[:0]
	return nil
}

// writeAt overwrites already appended bytes (a frame header patched once its
// body is complete).
func (s *metadataLogicalStorage) writeAt(data []byte, offset int64) error {
	if offset < 0 {
		return errMetadataLogicalNegativeOffset
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed {
		return errMetadataLogicalClosed
	}
	end := offset + int64(len(data))
	s.dropFrameMemo(offset, end)
	flushed := s.flushed.Load()
	if end > flushed+int64(len(s.pending)) {
		// Past the end: write through, as the file itself would.
		if err := s.flushLocked(); err != nil {
			return err
		}
		if err := s.createLocked(); err != nil {
			return err
		}
		return s.patchFileLocked(data, offset)
	}
	if end > flushed {
		start := max(offset, flushed)
		copy(s.pending[start-flushed:], data[start-offset:])
		data = data[:start-offset]
		if len(data) == 0 {
			return nil
		}
	}
	return s.patchFileLocked(data, offset)
}

// patchFileLocked writes data at offset of the file (invariant 3).
func (s *metadataLogicalStorage) patchFileLocked(data []byte, offset int64) error {
	s.patches.Add(1)
	metadataLogicalSpoolTestPoint(s, metadataLogicalSpoolPatchBegun)
	count, err := s.file.WriteAt(data, offset)
	if written := offset + int64(count); written > s.flushed.Load() {
		s.flushed.Store(written)
	}
	s.invalidate(offset, offset+int64(len(data)))
	s.patches.Add(1)
	return err
}

// ReadAt reads the spool as the file would hold it with every append written.
func (s *metadataLogicalStorage) ReadAt(p []byte, offset int64) (int, error) {
	if metadataLogicalSpoolCounting.Load() {
		metadataLogicalSpoolReadBytes.Add(int64(len(p)))
	}
	if offset < 0 {
		return 0, errMetadataLogicalNegativeOffset
	}
	if offset+int64(len(p)) > s.flushed.Load() {
		return s.readPendingAt(p, offset)
	}
	return s.readFlushedAt(p, offset)
}

func (s *metadataLogicalStorage) readPendingAt(p []byte, offset int64) (int, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed {
		return 0, os.ErrClosed
	}
	flushed := s.flushed.Load()
	count := 0
	if offset < flushed {
		read, err := s.readFlushedAt(p[:min(int64(len(p)), flushed-offset)], offset)
		count += read
		if err != nil {
			return count, err
		}
	}
	if count < len(p) {
		start := offset + int64(count) - flushed
		if start < int64(len(s.pending)) {
			count += copy(p[count:], s.pending[start:])
		}
	}
	if count < len(p) {
		return count, io.EOF
	}
	return count, nil
}

// readFlushedAt reads bytes the file holds: a large read directly, a small
// one through the block cache.
func (s *metadataLogicalStorage) readFlushedAt(p []byte, offset int64) (int, error) {
	if len(p) > metadataLogicalReadBlock/4 {
		return s.file.ReadAt(p, offset)
	}
	count := 0
	for count < len(p) {
		at := offset + int64(count)
		index := at / metadataLogicalReadBlock
		start := at - index*metadataLogicalReadBlock
		need := min(int64(metadataLogicalReadBlock), start+int64(len(p)-count))
		block, err := s.block(index, need)
		if block != nil && start < int64(len(block.data)) {
			count += copy(p[count:], block.data[start:])
		}
		if err != nil {
			return count, err
		}
		if block == nil || int64(len(block.data)) < need {
			return count, io.EOF
		}
	}
	return count, nil
}

// block returns a ready cached block index holding at least need bytes, or
// reads one. The slots are searched in full (a comparison of two frames far
// apart in one spool keeps both cached); a miss replaces an empty or
// outdated slot of the same block, else the next slot in turn.
func (s *metadataLogicalStorage) block(index, need int64) (*metadataLogicalBlock, error) {
	victim := -1
	for i := range s.blocks {
		cached := s.blocks[i].Load()
		if cached == nil {
			victim = i
			continue
		}
		if cached.index == index {
			if int64(len(cached.data)) >= need && cached.ready.Load() {
				return cached, nil
			}
			victim = i
		}
	}
	base := index * metadataLogicalReadBlock
	size := max(min(int64(metadataLogicalReadBlock), s.flushed.Load()-base), need)
	data := make([]byte, size)
	patches := s.patches.Load()
	count, err := s.file.ReadAt(data, base)
	metadataLogicalSpoolTestPoint(s, metadataLogicalSpoolBlockRead)
	block := &metadataLogicalBlock{index: index, data: data[:count]}
	if err != nil {
		// A failed or short read is never cached; it still serves the bytes
		// it holds, as the file read would.
		if int64(count) >= need {
			return block, nil
		}
		return block, err
	}
	if patches&1 != 0 {
		// A patch is in flight: these bytes may predate it.
		return block, nil
	}
	if victim < 0 {
		victim = int(s.victim.Add(1) % metadataLogicalReadSlots)
	}
	slot := &s.blocks[victim]
	slot.Store(block)
	if s.patches.Load() == patches {
		block.ready.Store(true)
	} else {
		// A patch began after the read started; its invalidation may already
		// have run, so the block is withdrawn here and never served.
		slot.CompareAndSwap(block, nil)
	}
	return block, nil
}

// invalidate drops cached blocks overlapping [start, end).
func (s *metadataLogicalStorage) invalidate(start, end int64) {
	for index := range s.blocks {
		slot := &s.blocks[index]
		cached := slot.Load()
		if cached == nil {
			continue
		}
		base := cached.index * metadataLogicalReadBlock
		if base < end && start < base+metadataLogicalReadBlock {
			slot.CompareAndSwap(cached, nil)
		}
	}
}

// discard closes a released storage: it drops the buffer, the cache and the
// frame memo and removes the file, if one was ever created.
func (s *metadataLogicalStorage) discard() error {
	s.pendingMu.Lock()
	s.closed = true
	file := s.file
	s.pending = nil
	s.dropFrameMemo(0, metadataLogicalMemoEnd)
	s.pendingMu.Unlock()
	for index := range s.blocks {
		s.blocks[index].Store(nil)
	}
	if file == nil {
		return nil
	}
	name := file.Name()
	_ = file.Close()
	return os.Remove(name)
}
