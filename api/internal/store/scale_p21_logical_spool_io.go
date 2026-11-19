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
//     different count when it publishes, is never published: the count is
//     checked under cacheMu, which the invalidation also takes. Every read
//     that starts after a patch returned sees the patched bytes (a block
//     published before the patch began is dropped by its invalidation; one
//     read after it began fails the check).
//  4. Cached blocks are immutable while published and never cover bytes past
//     the flushed end they were read under, so appends and flushes leave them
//     valid. Readers copy out of a block only under cacheMu, and the pread
//     filling a block runs before it is published, so a block's buffer is
//     reused (spare) once it has left its slot without any reader seeing it
//     change: a header scan of a large collection reads block after block
//     without allocating one each time.
//  5. Patches of bytes still in pending are copied into pending; the bytes
//     reach the file with the next flush. A patch past the logical end writes
//     through, as os.File.WriteAt would.
//  6. A failed flush leaves every byte in pending, and reads keep serving it.
//  7. discard (the last Release) marks the storage closed and drops the
//     buffer, the cache and the frame memo; no file is created afterwards,
//     and a never-created file has nothing to remove.
//
// The cache and the buffer (at most 128 KiB per storage) are covered by the
// storage's descriptor charge (metadataLogicalDescriptorBytes). Twelve slots:
// an installed storage is traversed by several readers at once (COLD
// decodes, a reload's normalization, pulse, schedule and resource reads),
// each walking the child headers of a large collection; with four slots
// they evicted each other's blocks and nearly every header read was a pread
// (the load rig at scale 1).
const (
	metadataLogicalReadBlock  = 8 << 10
	metadataLogicalReadSlots  = 12
	metadataLogicalWriteBytes = 32 << 10
)

var errMetadataLogicalNegativeOffset = errors.New("installed logical metadata source: negative offset")

// metadataLogicalBlock is one cached block: the spool bytes from
// index*metadataLogicalReadBlock (shorter than a block at the spool's end).
// Its buffer has the capacity of a whole block and is reused once the block
// leaves its slot (invariant 4).
type metadataLogicalBlock struct {
	index int64
	data  []byte
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
	if dir := metadataSpoolDirectory(s.directory); dir != "" {
		return dir
	}
	return os.TempDir()
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
	file, err := os.CreateTemp(metadataSpoolDirectory(s.directory), "flow-p21-logical-*")
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
		if copied, ok := s.cachedRead(p[count:], index, start, need); ok {
			count += copied
			continue
		}
		copied, complete, err := s.blockRead(p[count:], index, start, need)
		count += copied
		if err != nil {
			return count, err
		}
		if !complete {
			return count, io.EOF
		}
	}
	return count, nil
}

// cachedRead copies p from the cached block index when it holds at least
// need bytes. The slots are searched in full: a comparison of two frames far
// apart in one spool keeps both cached.
func (s *metadataLogicalStorage) cachedRead(p []byte, index, start, need int64) (int, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	for _, cached := range s.blocks {
		if cached != nil && cached.index == index && int64(len(cached.data)) >= need {
			return copy(p, cached.data[start:]), true
		}
	}
	return 0, false
}

// blockRead reads block index into an evicted or spare buffer, copies p from
// it and publishes it when the read overlapped no patch (invariant 3). A miss
// replaces an empty or outdated slot of the same block, else the next slot in
// turn. complete reports that the block held need bytes.
func (s *metadataLogicalStorage) blockRead(p []byte, index, start, need int64) (int, bool, error) {
	s.cacheMu.Lock()
	victim := -1
	for i, cached := range s.blocks {
		if cached == nil {
			victim = i
			continue
		}
		if cached.index == index {
			if int64(len(cached.data)) >= need {
				// Another reader published it meanwhile.
				copied := copy(p, cached.data[start:])
				s.cacheMu.Unlock()
				return copied, true, nil
			}
			victim = i
		}
	}
	if victim < 0 {
		s.victim++
		victim = int(s.victim % metadataLogicalReadSlots)
	}
	block := s.blocks[victim]
	s.blocks[victim] = nil
	if block == nil && len(s.spare) > 0 {
		block = s.spare[len(s.spare)-1]
		s.spare[len(s.spare)-1] = nil
		s.spare = s.spare[:len(s.spare)-1]
	}
	s.cacheMu.Unlock()
	if block == nil {
		block = &metadataLogicalBlock{data: make([]byte, metadataLogicalReadBlock)}
	}
	base := index * metadataLogicalReadBlock
	size := max(min(int64(metadataLogicalReadBlock), s.flushed.Load()-base), need)
	patches := s.patches.Load()
	count, err := s.file.ReadAt(block.data[:size], base)
	metadataLogicalSpoolTestPoint(s, metadataLogicalSpoolBlockRead)
	block.index, block.data = index, block.data[:count]
	copied := 0
	if start < int64(count) {
		copied = copy(p, block.data[start:])
	}
	complete := int64(count) >= need
	s.cacheMu.Lock()
	// A failed or short read is never cached (it still serves the bytes it
	// holds, as the file read would), nor one a patch was in flight for or
	// began during: these bytes may predate it.
	if err == nil && patches&1 == 0 && s.patches.Load() == patches && s.blocks[victim] == nil {
		s.blocks[victim] = block
	} else {
		s.recycleLocked(block)
	}
	s.cacheMu.Unlock()
	if err != nil && complete {
		err = nil
	}
	return copied, complete, err
}

// recycleLocked keeps the buffer of a block that left (or never reached) its
// slot for the next block read, while the cache holds fewer buffers than it
// has slots. The caller holds cacheMu.
func (s *metadataLogicalStorage) recycleLocked(block *metadataLogicalBlock) {
	held := len(s.spare)
	for _, cached := range s.blocks {
		if cached != nil {
			held++
		}
	}
	if held < metadataLogicalReadSlots && cap(block.data) == metadataLogicalReadBlock {
		s.spare = append(s.spare, block)
	}
}

// invalidate drops cached blocks overlapping [start, end).
func (s *metadataLogicalStorage) invalidate(start, end int64) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for index, cached := range s.blocks {
		if cached == nil {
			continue
		}
		base := cached.index * metadataLogicalReadBlock
		if base < end && start < base+metadataLogicalReadBlock {
			s.blocks[index] = nil
			s.recycleLocked(cached)
		}
	}
}

// dropReadCache releases the cached blocks and their spare buffers.
func (s *metadataLogicalStorage) dropReadCache() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.blocks = [metadataLogicalReadSlots]*metadataLogicalBlock{}
	s.spare = nil
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
	s.dropReadCache()
	if file == nil {
		return nil
	}
	name := file.Name()
	_ = file.Close()
	return os.Remove(name)
}
