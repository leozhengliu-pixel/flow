package store

import (
	"io"
	"slices"
	"sort"
	"sync/atomic"
)

// Borrowed ranges (bounded metadata on).
//
// An edit's next generation rewrites the collection it changes: the frame of
// a collection holds its records' frames one after the other, so a project
// edit wrote the 2,321 unchanged project records (36 MB on the load rig) to a
// new spool file, and the writes stalled in the page cache for up to seconds
// (one request at a time, SQLite). The records the edit keeps are already in
// settled spool files of earlier generations, byte for byte.
//
// A storage can therefore borrow a range of a settled storage instead of
// copying it: the range takes its place in the storage's offsets (the file
// leaves a hole there) and reads of it are served by the lender. Borrowed
// ranges are always resolved to the storage that holds the bytes itself, so
// a read is redirected once, and every lender is retained until the borrower
// is discarded. Settled bytes never change (a patch of a borrowed range is
// refused), so the borrower reads exactly what a copy would hold. A storage
// that would borrow from more than metadataLogicalLenderLimit storages copies
// instead (it then holds the bytes and later generations borrow from it), so
// a long run of edits does not keep every earlier generation's files alive.

type metadataLogicalBorrow struct {
	at, size int64
	lender   *metadataLogicalStorage
	from     int64
}

const (
	// metadataLogicalLenderLimit bounds the storages one storage borrows from.
	metadataLogicalLenderLimit = 48
	// metadataLogicalBorrowMinimum is the smallest range worth borrowing.
	metadataLogicalBorrowMinimum = 64 << 10
)

// Borrowed bytes and borrows refused for the lender limit (tests and load
// measurement).
var metadataLogicalBorrowedBytes, metadataLogicalBorrowRefusals atomic.Int64

// lent resolves [from, from+size) of s to the storages that hold the bytes:
// pieces of s's own bytes and of the ranges s borrowed itself. ok is false
// when s is not settled (its bytes may still be patched).
func (s *metadataLogicalStorage) lent(from, size int64) (pieces []metadataLogicalBorrow, ok bool) {
	s.pendingMu.Lock()
	settled, closed := s.settled, s.closed
	s.pendingMu.Unlock()
	if !settled || closed {
		return nil, false
	}
	var borrows []metadataLogicalBorrow
	if loaded := s.borrows.Load(); loaded != nil {
		borrows = *loaded
	}
	at, end := from, from+size
	for at < end {
		index := sort.Search(len(borrows), func(i int) bool { return borrows[i].at+borrows[i].size > at })
		if index < len(borrows) && borrows[index].at <= at {
			borrow := borrows[index]
			n := min(end, borrow.at+borrow.size) - at
			pieces = append(pieces, metadataLogicalBorrow{size: n, lender: borrow.lender, from: borrow.from + at - borrow.at})
			at += n
			continue
		}
		next := end
		if index < len(borrows) {
			next = min(end, borrows[index].at)
		}
		pieces = append(pieces, metadataLogicalBorrow{size: next - at, lender: s, from: at})
		at = next
	}
	return pieces, true
}

// borrow appends size bytes of lender at from to s without copying them.
// borrowed is false when the range is left to the caller to copy.
func (s *metadataLogicalStorage) borrow(lender *metadataLogicalStorage, from, size int64) (borrowed bool, err error) {
	if lender == nil || lender == s || size < metadataLogicalBorrowMinimum {
		return false, nil
	}
	pieces, ok := lender.lent(from, size)
	if !ok {
		return false, nil
	}
	s.pendingMu.Lock()
	if s.closed {
		s.pendingMu.Unlock()
		return false, errMetadataLogicalClosed
	}
	lenders := slices.Clone(s.lenders)
	for _, piece := range pieces {
		if !slices.Contains(lenders, piece.lender) {
			lenders = append(lenders, piece.lender)
		}
	}
	if len(lenders) > metadataLogicalLenderLimit {
		s.pendingMu.Unlock()
		metadataLogicalBorrowRefusals.Add(1)
		return false, nil
	}
	// Each new lender is retained once; a lender being discarded cannot
	// lend.
	added := lenders[len(s.lenders):]
	for index, lender := range added {
		if err := lender.Retain(); err != nil {
			for _, retained := range added[:index] {
				retained.Release()
			}
			s.pendingMu.Unlock()
			return false, nil
		}
	}
	if err := s.flushLocked(); err != nil {
		s.pendingMu.Unlock()
		for _, retained := range added {
			retained.Release()
		}
		return false, err
	}
	at := s.flushed.Load()
	var borrows []metadataLogicalBorrow
	if loaded := s.borrows.Load(); loaded != nil {
		borrows = slices.Clone(*loaded)
	}
	for _, piece := range pieces {
		piece.at = at
		borrows = append(borrows, piece)
		at += piece.size
	}
	s.lenders = lenders
	s.borrows.Store(&borrows)
	// The file leaves a hole where the borrowed bytes would be.
	s.flushed.Store(at)
	s.pendingMu.Unlock()
	metadataLogicalBorrowedBytes.Add(size)
	return true, nil
}

// borrowedOverlap reports whether [from, end) overlaps a borrowed range. The
// caller holds pendingMu.
func (s *metadataLogicalStorage) borrowedOverlap(from, end int64) bool {
	loaded := s.borrows.Load()
	if loaded == nil {
		return false
	}
	for _, borrow := range *loaded {
		if borrow.at < end && from < borrow.at+borrow.size {
			return true
		}
	}
	return false
}

// readBorrowedAt reads p at offset, serving borrowed ranges from their
// lenders and the rest from s itself.
func (s *metadataLogicalStorage) readBorrowedAt(p []byte, offset int64, borrows []metadataLogicalBorrow) (int, error) {
	count := 0
	for count < len(p) {
		at := offset + int64(count)
		index := sort.Search(len(borrows), func(i int) bool { return borrows[i].at+borrows[i].size > at })
		var read int
		var err error
		if index < len(borrows) && borrows[index].at <= at {
			borrow := borrows[index]
			n := min(int64(len(p)-count), borrow.at+borrow.size-at)
			read, err = borrow.lender.ReadAt(p[count:count+int(n)], borrow.from+at-borrow.at)
		} else {
			n := int64(len(p) - count)
			if index < len(borrows) {
				n = min(n, borrows[index].at-at)
			}
			read, err = s.readOwnAt(p[count:count+int(n)], at)
		}
		count += read
		if err == nil && read == 0 {
			return count, io.ErrUnexpectedEOF
		}
		if err != nil {
			if err == io.EOF && count == len(p) {
				return count, nil
			}
			return count, err
		}
	}
	return count, nil
}

// releaseBorrowed releases s's lenders (s is discarded).
func (s *metadataLogicalStorage) releaseBorrowed() {
	s.pendingMu.Lock()
	lenders := s.lenders
	s.lenders = nil
	s.pendingMu.Unlock()
	for _, lender := range lenders {
		lender.Release()
	}
}

// borrowRange appends size bytes of source at from to the builder's storage
// without copying them when the storage can borrow them.
func (b *metadataLogicalBuilder) borrowRange(source *metadataLogicalStorage, from, size int64) (bool, error) {
	if b.finished || b.closed {
		return false, errMetadataLogicalClosed
	}
	borrowed, err := b.storage.borrow(source, from, size)
	if borrowed {
		b.offset += size
	}
	return borrowed, err
}
