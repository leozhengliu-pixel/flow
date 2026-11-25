package store

import (
	"context"
	"reflect"
	"sync/atomic"
)

// Child indexes of the collections a builder writes.
//
// A large collection's walk reads its child headers one by one from the
// spool unless the storage keeps the collection's child index
// (scale_p21_logical_frame_memo.go), which a first walk stores. Every write
// derives several short-lived images (a snapshot clone, the event source, the
// display and actor images, the publication candidate) whose large
// collections are fresh copies in fresh storages, each walked once or twice
// before it is released: nearly every walk was a first walk, one spool read
// per record (most of a HOT write's and a COLD decode's time on the load rig
// went to those reads).
//
// A builder knows every header it writes, so it stores the index of a slice
// or array collection it writes child by child (writeAggregateNode) as it
// finishes the collection, and a verbatim copy of a collection (copyNode)
// takes the source's index shifted to the copy. The headers are exactly the
// ones a walk reads back (metadataLogicalHeaderOf of the written child
// nodes, which carry the size and cost their frames hold); the same budget
// and invalidation (a later write overlapping the collection drops it) apply.

// metadataLogicalWrittenIndexes counts indexes stored by builders (tests and
// load measurement).
var metadataLogicalWrittenIndexes atomic.Int64

// writtenChildIndex collects the child headers of one collection a builder
// writes; start is the offset of its first child frame.
type writtenChildIndex struct {
	start   int64
	headers []metadataLogicalChildHeader
}

func newWrittenChildIndex(t reflect.Type, start int64) *writtenChildIndex {
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array || t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		// (A byte slice is read whole, never walked.)
		return nil
	}
	return &writtenChildIndex{start: start}
}

func (w *writtenChildIndex) add(node metadataLogicalNode) {
	if w != nil {
		w.headers = append(w.headers, metadataLogicalHeaderOf(node))
	}
}

// keep stores the collected headers as node's child index when the
// collection is large enough to be indexed and the budget admits them. The
// caller has written the collection completely, its header included.
func (w *writtenChildIndex) keep(storage *metadataLogicalStorage, node metadataLogicalNode) {
	if w == nil || len(w.headers) < metadataLogicalIndexMinimum || !reserveMetadataLogicalChildIndex(len(w.headers)) {
		return
	}
	headers := make([]metadataLogicalChildHeader, len(w.headers))
	copy(headers, w.headers)
	storage.storeChildIndex(node.offset, node.size, w.start, headers)
	metadataLogicalWrittenIndexes.Add(1)
}

// copyChildIndex gives a verbatim copy of the frame at from in source (size
// bytes, now at to in s) the source's child index of that frame, shifted.
func (s *metadataLogicalStorage) copyChildIndex(source *metadataLogicalStorage, from, to, size int64) {
	if source == nil {
		return
	}
	source.memoMu.Lock()
	index, found := source.indexes[from]
	source.memoMu.Unlock()
	if !found || index.size != size || !reserveMetadataLogicalChildIndex(len(index.headers)) {
		return
	}
	shift := to - from
	headers := make([]metadataLogicalChildHeader, len(index.headers))
	for i, header := range index.headers {
		header.offset += shift
		headers[i] = header
	}
	s.storeChildIndex(to, size, index.start+shift, headers)
	metadataLogicalWrittenIndexes.Add(1)
}

// verifyChildIndexesForTesting compares every child index storage keeps with
// the headers a walk reads from its bytes.
func (s *metadataLogicalStorage) verifyChildIndexesForTesting(ctx context.Context) (int, error) {
	s.memoMu.Lock()
	indexes := make(map[int64]metadataLogicalChildIndex, len(s.indexes))
	for offset, index := range s.indexes {
		indexes[offset] = index
	}
	s.memoMu.Unlock()
	t := reflect.TypeFor[struct{}]()
	for offset, index := range indexes {
		frame := metadataLogicalNode{source: s, offset: offset, size: index.size, typ: t}
		c := metadataLogicalCursor{frame, index.start, ctx}
		for i, want := range index.headers {
			child, err := c.child(t)
			if err != nil {
				return 0, err
			}
			if got := metadataLogicalHeaderOf(child); got != want {
				return 0, errChildIndexMismatch{offset: offset, child: i, got: got, want: want}
			}
		}
	}
	return len(indexes), nil
}

type errChildIndexMismatch struct {
	offset    int64
	child     int
	got, want metadataLogicalChildHeader
}

func (e errChildIndexMismatch) Error() string {
	return "child index differs from the spool"
}
