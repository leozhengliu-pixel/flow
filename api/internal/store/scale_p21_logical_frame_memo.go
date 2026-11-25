package store

import (
	"math"
	"reflect"
	"slices"
	"sort"
	"sync/atomic"
)

// The frame memo of a logical storage: facts about its frames that save
// reading them again. Both are dropped where a write overlaps them
// (writeAt) and when the storage is discarded. memoMu guards them; a writer
// takes it inside pendingMu.

const metadataLogicalMemoEnd = math.MaxInt64

// metadataLogicalCopy is one verbatim copy of size bytes from source at
// from (itself resolved to the first origin when it was recorded).
type metadataLogicalCopy struct {
	offset, size, from int64
	source             *metadataLogicalStorage
}

// At most this many copies are remembered across all spools; beyond it a
// comparison of two copies reads their bytes, as it always did.
const metadataLogicalCopyLimit = 1 << 18

var metadataLogicalCopies atomic.Int64

// logicalSizeLocked is the spool length with every append written.
func (s *metadataLogicalStorage) logicalSizeLocked() int64 {
	return s.flushed.Load() + int64(len(s.pending))
}

// recordCopy remembers that the frame at offset holds the same bytes as
// origin, which the caller resolved with metadataLogicalFrameOrigin. Copies
// are kept in increasing, disjoint offset order.
func (s *metadataLogicalStorage) recordCopy(offset int64, origin metadataLogicalNode) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.closed || origin.source == nil || origin.size <= 0 || offset+origin.size > s.logicalSizeLocked() {
		return
	}
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	if len(s.copies) != 0 && s.copies[len(s.copies)-1].offset+s.copies[len(s.copies)-1].size > offset {
		return
	}
	if metadataLogicalCopies.Add(1) > metadataLogicalCopyLimit {
		metadataLogicalCopies.Add(-1)
		return
	}
	s.copies = append(s.copies, metadataLogicalCopy{offset: offset, size: origin.size, from: origin.offset, source: origin.source})
}

// originStep maps a frame inside a recorded copy to the same frame in the
// copy's origin.
func (s *metadataLogicalStorage) originStep(node metadataLogicalNode) (metadataLogicalNode, bool) {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	index := sort.Search(len(s.copies), func(i int) bool { return s.copies[i].offset > node.offset }) - 1
	if index < 0 {
		return node, false
	}
	copied := s.copies[index]
	if node.offset+node.size > copied.offset+copied.size {
		return node, false
	}
	node.offset = copied.from + node.offset - copied.offset
	node.source = copied.source
	return node, true
}

// metadataLogicalFrameOrigin returns the frame a node was (transitively) copied
// from verbatim, or the node itself. Two nodes with the same origin hold the
// same bytes and decode identically.
func metadataLogicalFrameOrigin(node metadataLogicalNode) metadataLogicalNode {
	for range metadataLogicalDepth {
		if node.source == nil {
			return node
		}
		next, ok := node.source.originStep(node)
		if !ok {
			return node
		}
		node = next
	}
	return node
}

// dropFrameMemo forgets every copy and collection index overlapping
// [offset, end). The caller holds pendingMu.
func (s *metadataLogicalStorage) dropFrameMemo(offset, end int64) {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	if len(s.copies) != 0 {
		first := sort.Search(len(s.copies), func(i int) bool { return s.copies[i].offset+s.copies[i].size > offset })
		last := first
		for last < len(s.copies) && s.copies[last].offset < end {
			last++
		}
		if last > first {
			s.copies = slices.Delete(s.copies, first, last)
			metadataLogicalCopies.Add(-int64(last - first))
		}
	}
	for key, index := range s.indexes {
		if key < end && offset < key+index.size {
			metadataLogicalIndexBytes.Add(-int64(len(index.headers)) * metadataLogicalIndexEntry)
			delete(s.indexes, key)
		}
	}
	for key, check := range s.clones {
		if key < end && offset < key+check.size {
			delete(s.clones, key)
		}
	}
	for key, index := range s.userIndexes {
		if key < end && offset < key+index.size {
			delete(s.userIndexes, key)
		}
	}
}

// metadataLogicalCloneCheck remembers whether the frame of typ at an offset
// (size bytes) needs cloneMetadataLogicalSnapshot's rewrite: a dropped
// (json:"-") field holds a value somewhere in it. A snapshot clone otherwise
// walks every record of every such collection on each HOT write, although
// installed frames do not change.
type metadataLogicalCloneCheck struct {
	size    int64
	typ     reflect.Type
	changed bool
}

// metadataLogicalCloneMemoBytes: smaller frames are walked again (a memo
// entry would cost more than the walk).
const metadataLogicalCloneMemoBytes = 4 << 10

func (s *metadataLogicalStorage) cloneCheck(node metadataLogicalNode) (changed, found bool) {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	check, found := s.clones[node.offset]
	if !found || check.size != node.size || check.typ != node.typ {
		return false, false
	}
	return check.changed, true
}

func (s *metadataLogicalStorage) storeCloneCheck(node metadataLogicalNode, changed bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	if s.closed || node.offset+node.size > s.logicalSizeLocked() {
		return
	}
	if s.clones == nil {
		s.clones = map[int64]metadataLogicalCloneCheck{}
	}
	s.clones[node.offset] = metadataLogicalCloneCheck{size: node.size, typ: node.typ, changed: changed}
}

// A large collection is scanned header by header: one read per record, and a
// write walks the same collection several times. The storage keeps the child
// headers of a scanned collection (32 bytes per record, within a global
// budget) until a write overlaps it.
const (
	metadataLogicalIndexMinimum = 32
	metadataLogicalIndexLimit   = 64 << 20
	metadataLogicalIndexEntry   = 32
)

var metadataLogicalIndexBytes atomic.Int64

type metadataLogicalChildHeader struct {
	offset, size int64
	cost         uint64
	flags        uint8
}

type metadataLogicalChildIndex struct {
	size, start int64
	headers     []metadataLogicalChildHeader
}

func (h metadataLogicalChildHeader) node(source *metadataLogicalStorage, t reflect.Type) metadataLogicalNode {
	return metadataLogicalNode{source: source, offset: h.offset, size: h.size, cost: h.cost, typ: t,
		unavailable: h.flags&1 != 0, blocked: h.flags&2 != 0, generic: h.flags&4 != 0}
}

func metadataLogicalHeaderOf(node metadataLogicalNode) metadataLogicalChildHeader {
	header := metadataLogicalChildHeader{offset: node.offset, size: node.size, cost: node.cost}
	if node.unavailable {
		header.flags |= 1
	}
	if node.blocked {
		header.flags |= 2
	}
	if node.generic {
		header.flags |= 4
	}
	return header
}

func (s *metadataLogicalStorage) childIndex(offset, size, start int64, count int) ([]metadataLogicalChildHeader, bool) {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	index, found := s.indexes[offset]
	if !found || index.size != size || index.start != start || len(index.headers) != count {
		return nil, false
	}
	return index.headers, true
}

// reserveMetadataLogicalChildIndex admits the headers of a count-record
// collection.
func reserveMetadataLogicalChildIndex(count int) bool {
	return reserveMetadataLogicalBytes(&metadataLogicalIndexBytes, int64(count)*metadataLogicalIndexEntry, metadataLogicalIndexLimit)
}

// storeChildIndex keeps admitted headers; it releases them when the storage
// is closed or already holds an index for that collection.
func (s *metadataLogicalStorage) storeChildIndex(offset, size, start int64, headers []metadataLogicalChildHeader) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	if _, found := s.indexes[offset]; found || s.closed || offset+size > s.logicalSizeLocked() {
		metadataLogicalIndexBytes.Add(-int64(len(headers)) * metadataLogicalIndexEntry)
		return
	}
	if s.indexes == nil {
		s.indexes = map[int64]metadataLogicalChildIndex{}
	}
	s.indexes[offset] = metadataLogicalChildIndex{size: size, start: start, headers: headers}
}
