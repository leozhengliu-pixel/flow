package store

import (
	"errors"
	"io"
)

// A logical spool is read as many small frame fields (8-byte sizes, costs,
// counts and scalars) and written as many small appends. Each was one pread or
// write system call: a COLD decode of a 5,000-issue workspace spent seconds in
// system calls alone. Reads of up to a quarter block are now served from a
// few cached blocks of the spool, and appends are buffered until the buffer
// fills or the builder finishes, so a walk issues one call per block.
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
type metadataLogicalBlock struct {
	index int64
	data  []byte
}

// append adds data at the end of the spool.
func (s *metadataLogicalStorage) append(data []byte) error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if len(s.pending)+len(data) > metadataLogicalWriteBytes {
		if err := s.flushLocked(); err != nil {
			return err
		}
		if len(data) >= metadataLogicalWriteBytes {
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
	if s.pending == nil {
		s.pending = make([]byte, 0, metadataLogicalWriteBytes)
	}
	s.pending = append(s.pending, data...)
	return nil
}

// flush writes the buffered appends out and drops the buffer.
func (s *metadataLogicalStorage) flush() error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
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
	at := s.flushed.Load()
	count, err := s.file.WriteAt(s.pending, at)
	if err == nil && count != len(s.pending) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// The buffer still holds every byte; reads keep serving it.
		return err
	}
	// Cached blocks never extend past the flushed end, so appends leave
	// them valid.
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
	end := offset + int64(len(data))
	flushed := s.flushed.Load()
	if end > flushed+int64(len(s.pending)) {
		// Past the end: write through, as the file itself would.
		if err := s.flushLocked(); err != nil {
			return err
		}
		_, err := s.file.WriteAt(data, offset)
		s.invalidate(offset, end)
		if err == nil && end > flushed {
			s.flushed.Store(end)
		}
		return err
	}
	if end > flushed {
		start := max(offset, flushed)
		copy(s.pending[start-flushed:], data[start-offset:])
		data = data[:start-offset]
		if len(data) == 0 {
			return nil
		}
	}
	_, err := s.file.WriteAt(data, offset)
	s.invalidate(offset, offset+int64(len(data)))
	return err
}

// ReadAt reads the spool as the file would hold it with every append written.
func (s *metadataLogicalStorage) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errMetadataLogicalNegativeOffset
	}
	flushed := s.flushed.Load()
	if offset+int64(len(p)) > flushed {
		return s.readPendingAt(p, offset)
	}
	return s.readFlushedAt(p, offset)
}

func (s *metadataLogicalStorage) readPendingAt(p []byte, offset int64) (int, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
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

// block returns the cached block index holding at least need bytes, reading
// it when the slot holds another block or a shorter one.
func (s *metadataLogicalStorage) block(index, need int64) (*metadataLogicalBlock, error) {
	slot := &s.blocks[index%metadataLogicalReadSlots]
	if cached := slot.Load(); cached != nil && cached.index == index && int64(len(cached.data)) >= need {
		return cached, nil
	}
	base := index * metadataLogicalReadBlock
	size := min(int64(metadataLogicalReadBlock), s.flushed.Load()-base)
	if size < need {
		size = need
	}
	data := make([]byte, size)
	count, err := s.file.ReadAt(data, base)
	block := &metadataLogicalBlock{index: index, data: data[:count]}
	if err != nil {
		// A failed or short read is never cached; it still serves the bytes
		// it holds, as the file read would.
		if int64(count) >= need {
			return block, nil
		}
		return block, err
	}
	slot.Store(block)
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

// dropBuffers releases the cache and the append buffer of a released storage.
func (s *metadataLogicalStorage) dropBuffers() {
	for index := range s.blocks {
		s.blocks[index].Store(nil)
	}
	s.pendingMu.Lock()
	s.pending = nil
	s.pendingMu.Unlock()
}
