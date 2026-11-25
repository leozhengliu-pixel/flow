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
//     checked under the cache's mu, which the invalidation also takes. Every
//     read that starts after a patch returned sees the patched bytes (a block
//     published before the patch began is dropped by its invalidation; one
//     read after it began fails the check).
//  4. Cached blocks are immutable while published and never cover bytes past
//     the flushed end they were read under, so appends and flushes leave them
//     valid. Readers copy out of a block only under the cache's mu, and the
//     pread filling a block runs before it is published, so a block's buffer
//     is reused (spare) once it has left its slot without any reader seeing
//     it change: a header scan of a large collection reads block after block
//     without allocating one each time.
//  5. Patches of bytes still in pending are copied into pending; the bytes
//     reach the file with the next flush. A patch past the logical end writes
//     through, as os.File.WriteAt would.
//  6. A failed flush leaves every byte in pending, and reads keep serving it.
//  7. discard (the last Release) marks the storage closed and drops the
//     buffer, its cached blocks and the frame memo; no file is created
//     afterwards, and a never-created file has nothing to remove. A block
//     read that finishes after the discard is never published (dropped is
//     set before the cached blocks are dropped and checked under mu).
//
// The cache is shared by the storages of one spool group (one workspace's
// installed lineage, scale_p21_logical_spool_group.go); each block names the
// storage it was read from. Cached blocks and spare buffers together never
// exceed the slots, and the group's last release drops both. The cache
// (96 KiB) and the group's resident allowance (32 KiB) make up its descriptor
// charge (metadataLogicalDescriptorBytes, 128 KiB). Twelve slots: an installed
// source is traversed by several readers at once (COLD decodes, a reload's
// normalization, pulse, schedule and resource reads), each walking the child
// headers of a large collection; with four slots they evicted each other's
// blocks and nearly every header read was a pread (the load rig at scale 1).
// Shared by every storage of a workspace, twelve still suffice: a header scan
// keeps one block live (large collections are walked in cursor chunks and
// nodes read in one piece, scale_p21_logical_read_window.go), and with a miss
// reserving its slot, 16 concurrent scans of different storages read 1.04x
// the blocks they would with a cache each (1.14x for 16 scans that also
// compare against a second storage; 32 scans: 1.14x and 1.39x). Before the
// reservation, two concurrent misses could take one empty slot and the first
// read's block was never cached: 1.5x to 1.8x from two readers on.
const (
	metadataLogicalReadBlock  = 8 << 10
	metadataLogicalReadSlots  = 12
	metadataLogicalWriteBytes = 32 << 10
)

var errMetadataLogicalNegativeOffset = errors.New("installed logical metadata source: negative offset")

// metadataLogicalBlock is one cached block: the spool bytes of owner from
// index*metadataLogicalReadBlock (shorter than a block at the spool's end).
// Its buffer has the capacity of a whole block and is reused once the block
// leaves its slot (invariant 4).
type metadataLogicalBlock struct {
	owner *metadataLogicalStorage
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
	metadataLogicalSpoolReadCalls atomic.Int64
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
	if len(data) > metadataLogicalWriteBytes-len(s.pending) {
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
	need, err := boundedMetadataSize(metadataLogicalWriteBytes, len(s.pending), len(data))
	if err != nil {
		return err
	}
	if need > cap(s.pending) {
		capacity := max(need, 2*min(cap(s.pending), metadataLogicalWriteBytes/2), 512)
		grown := make([]byte, len(s.pending), capacity)
		copy(grown, s.pending)
		s.pending = grown
	}
	s.pending = append(s.pending, data...)
	return nil
}

// settle ends a builder's appends: a spool with a file writes its buffer out
// and returns its memory; a spool that never outgrew its buffer stays in
// memory, trimmed to its bytes, while its group's resident allowance admits
// it, and is written to its file otherwise (scale_p21_logical_spool_group.go).
// A settled storage returns the write buffer it joined its group with.
// Settling again does nothing.
func (s *metadataLogicalStorage) settle() error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed {
		return errMetadataLogicalClosed
	}
	if s.settled {
		return nil
	}
	if s.file == nil {
		if cap(s.pending) > len(s.pending) {
			s.pending = append(make([]byte, 0, len(s.pending)), s.pending...)
		}
		size := int64(len(s.pending))
		if s.group == nil || s.group.admitResident(size) {
			if s.group != nil {
				s.resident = size
			}
			s.settledLocked()
			return nil
		}
	}
	if err := s.flushLocked(); err != nil {
		return err
	}
	s.pending = nil
	s.settledLocked()
	return nil
}

func (s *metadataLogicalStorage) settledLocked() {
	s.settled = true
	metadataLogicalDescriptorsUsed.Add(-s.writeCharge)
	s.writeCharge = 0
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
	if s.borrowedOverlap(offset, end) {
		// Borrowed bytes belong to another storage and never change.
		return ErrMetadataRecordQuery
	}
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
		metadataLogicalSpoolReadCalls.Add(1)
	}
	if offset < 0 {
		return 0, errMetadataLogicalNegativeOffset
	}
	if borrows := s.borrows.Load(); borrows != nil {
		// Ranges another storage holds (scale_p21_logical_borrowed_range.go).
		return s.readBorrowedAt(p, offset, *borrows)
	}
	return s.readOwnAt(p, offset)
}

// readOwnAt reads bytes this storage holds itself.
func (s *metadataLogicalStorage) readOwnAt(p []byte, offset int64) (int, error) {
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
		cache := s.blockCache()
		if copied, ok := cache.cachedRead(s, p[count:], index, start, need); ok {
			count += copied
			continue
		}
		copied, complete, err := cache.blockRead(s, p[count:], index, start, need)
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

// cachedRead copies p from the storage's cached block index when it holds at
// least need bytes. The slots of the group's cache are searched in full: a
// comparison of two frames far apart in one spool keeps both cached.
func (c *metadataLogicalBlockCache) cachedRead(s *metadataLogicalStorage, p []byte, index, start, need int64) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, cached := range c.blocks {
		if cached != nil && cached.owner == s && cached.index == index && int64(len(cached.data)) >= need {
			return copy(p, cached.data[start:]), true
		}
	}
	return 0, false
}

// metadataLogicalBlockReading marks a slot a block read has taken: other
// misses never pick it (a concurrent miss took an empty slot and its block
// was lost when the first read found the slot filled), lookups never match it
// and its block is published there or the slot is emptied again.
var metadataLogicalBlockReading = &metadataLogicalBlock{index: -1}

// blockRead reads block index of s into an evicted or spare buffer, copies p
// from it and publishes it when the read overlapped no patch (invariant 3) and
// s was not discarded meanwhile (invariant 7). A miss takes an empty slot or
// an outdated one of the same block, else the next slot in turn that no other
// read has taken; when every slot is taken the block is read uncached.
// complete reports that the block held need bytes.
func (c *metadataLogicalBlockCache) blockRead(s *metadataLogicalStorage, p []byte, index, start, need int64) (int, bool, error) {
	c.mu.Lock()
	victim := -1
	for i, cached := range c.blocks {
		if cached == nil {
			victim = i
			continue
		}
		if cached.owner == s && cached.index == index {
			if int64(len(cached.data)) >= need {
				// Another reader published it meanwhile.
				copied := copy(p, cached.data[start:])
				c.mu.Unlock()
				return copied, true, nil
			}
			victim = i
		}
	}
	for try := 0; victim < 0 && try < metadataLogicalReadSlots; try++ {
		c.victim++
		if next := int(c.victim % metadataLogicalReadSlots); c.blocks[next] != metadataLogicalBlockReading {
			victim = next
		}
	}
	var block *metadataLogicalBlock
	if victim >= 0 {
		block = c.blocks[victim]
		c.blocks[victim] = metadataLogicalBlockReading
	}
	if block == nil && len(c.spare) > 0 {
		block = c.spare[len(c.spare)-1]
		c.spare[len(c.spare)-1] = nil
		c.spare = c.spare[:len(c.spare)-1]
	}
	c.mu.Unlock()
	if block == nil {
		block = &metadataLogicalBlock{data: make([]byte, metadataLogicalReadBlock)}
	}
	base := index * metadataLogicalReadBlock
	size := max(min(int64(metadataLogicalReadBlock), s.flushed.Load()-base), need)
	patches := s.patches.Load()
	count, err := s.file.ReadAt(block.data[:size], base)
	metadataLogicalSpoolTestPoint(s, metadataLogicalSpoolBlockRead)
	block.owner, block.index, block.data = s, index, block.data[:count]
	copied := 0
	if start < int64(count) {
		copied = copy(p, block.data[start:])
	}
	complete := int64(count) >= need
	c.mu.Lock()
	reserved := victim >= 0 && c.blocks[victim] == metadataLogicalBlockReading
	if reserved {
		c.blocks[victim] = nil
	}
	// A failed or short read is never cached (it still serves the bytes it
	// holds, as the file read would), nor one a patch was in flight for or
	// began during: these bytes may predate it. Nor is a block of a storage
	// discarded meanwhile (its blocks are gone from the shared cache), one
	// read while every slot was taken or after the cache was released, or a
	// block another reader published first.
	if reserved && err == nil && patches&1 == 0 && s.patches.Load() == patches && !s.dropped.Load() &&
		!c.holdsLocked(s, index, int64(count)) {
		c.blocks[victim] = block
	} else {
		c.recycleLocked(block)
	}
	c.mu.Unlock()
	if err != nil && complete {
		err = nil
	}
	return copied, complete, err
}

// holdsLocked reports whether a slot holds block index of s with at least
// need bytes. The caller holds mu.
func (c *metadataLogicalBlockCache) holdsLocked(s *metadataLogicalStorage, index, need int64) bool {
	for _, cached := range c.blocks {
		if cached != nil && cached.owner == s && cached.index == index && int64(len(cached.data)) >= need {
			return true
		}
	}
	return false
}

// recycleLocked keeps the buffer of a block that left (or never reached) its
// slot for the next block read, while the cache holds fewer buffers than it
// has slots and is not released. The caller holds mu.
func (c *metadataLogicalBlockCache) recycleLocked(block *metadataLogicalBlock) {
	block.owner = nil
	if c.released || cap(block.data) != metadataLogicalReadBlock {
		return
	}
	held := len(c.spare)
	for _, cached := range c.blocks {
		if cached != nil {
			held++
		}
	}
	if held < metadataLogicalReadSlots {
		c.spare = append(c.spare, block)
	}
}

// invalidate drops the storage's cached blocks overlapping [start, end).
func (s *metadataLogicalStorage) invalidate(start, end int64) {
	cache := s.blockCache()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for index, cached := range cache.blocks {
		if cached == nil || cached.owner != s {
			continue
		}
		base := cached.index * metadataLogicalReadBlock
		if base < end && start < base+metadataLogicalReadBlock {
			cache.blocks[index] = nil
			cache.recycleLocked(cached)
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
	s.dropped.Store(true)
	s.pendingMu.Unlock()
	s.dropCachedBlocks()
	if own := s.ownCache.Load(); own != nil {
		own.release()
	}
	s.releaseBorrowed()
	if file == nil {
		return nil
	}
	name := file.Name()
	_ = file.Close()
	return os.Remove(name)
}
