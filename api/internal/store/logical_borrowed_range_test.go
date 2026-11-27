package store

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
)

// A storage that borrows ranges of settled storages (and ranges those
// storages borrowed themselves) reads exactly the bytes a copy would hold,
// across borrowed and own bytes, buffered and flushed, and past the end. A
// patch of a borrowed range is refused, the lender limit makes the caller
// copy, and every lender stays open until its last borrower is discarded.
func TestScaleLogicalBorrowedRangesMatchCopies(t *testing.T) {
	for _, seed := range []uint64{3, 41} {
		random := rand.New(rand.NewPCG(seed, 5))
		directory := t.TempDir()
		randomBytes := func(size int) []byte {
			data := make([]byte, size)
			for index := range data {
				data[index] = byte(random.Uint32())
			}
			return data
		}
		type modeled struct {
			storage *metadataLogicalStorage
			model   []byte
		}
		check := func(step int, s modeled) {
			t.Helper()
			for range 24 {
				offset := random.IntN(len(s.model))
				size := 1 + random.IntN(min(len(s.model)-offset, []int{8, 3000, 70000, 400000}[random.IntN(4)]))
				got := make([]byte, size)
				count, err := s.storage.ReadAt(got, int64(offset))
				if err != nil || count != size || !bytes.Equal(got, s.model[offset:offset+size]) {
					t.Fatalf("seed %d step %d: read %d+%d of %d: count %d err %v", seed, step, offset, size, len(s.model), count, err)
				}
			}
			past := make([]byte, 64)
			start := len(s.model) - 16
			if count, err := s.storage.ReadAt(past, int64(start)); !errors.Is(err, io.EOF) || count != 16 || !bytes.Equal(past[:count], s.model[start:]) {
				t.Fatalf("seed %d step %d: read past the end: count %d err %v", seed, step, count, err)
			}
		}
		base := modeled{storage: &metadataLogicalStorage{directory: directory, refs: 1}}
		base.model = randomBytes(3 << 20)
		if err := base.storage.append(base.model); err != nil {
			t.Fatal(err)
		}
		if err := base.storage.settle(); err != nil {
			t.Fatal(err)
		}
		generations := []modeled{base}
		for step := range 12 {
			lender := generations[random.IntN(len(generations))]
			next := modeled{storage: &metadataLogicalStorage{directory: directory, refs: 1}}
			if borrowed, err := next.storage.borrow(lender.storage, 0, metadataLogicalBorrowMinimum); err != nil || !borrowed {
				t.Fatalf("seed %d step %d: borrow from a settled storage = %v %v", seed, step, borrowed, err)
			}
			next.model = append(next.model, lender.model[:metadataLogicalBorrowMinimum]...)
			for range 6 {
				own := randomBytes([]int{1, 300, 50000}[random.IntN(3)])
				if err := next.storage.append(own); err != nil {
					t.Fatal(err)
				}
				next.model = append(next.model, own...)
				from := random.IntN(len(lender.model) - metadataLogicalBorrowMinimum)
				size := metadataLogicalBorrowMinimum + random.IntN(min(len(lender.model)-from-metadataLogicalBorrowMinimum, 600000)+1)
				borrowed, err := next.storage.borrow(lender.storage, int64(from), int64(size))
				if err != nil {
					t.Fatal(err)
				}
				if borrowed {
					next.model = append(next.model, lender.model[from:from+size]...)
				}
				check(step, next)
			}
			if err := next.storage.writeAt([]byte{1}, int64(metadataLogicalBorrowMinimum/2)); !errors.Is(err, ErrMetadataRecordQuery) {
				t.Fatalf("seed %d step %d: patch of a borrowed range = %v", seed, step, err)
			}
			if lent, ok := next.storage.lent(0, 1); ok || lent != nil {
				t.Fatalf("seed %d step %d: an unsettled storage lent its bytes", seed, step)
			}
			if err := next.storage.settle(); err != nil {
				t.Fatal(err)
			}
			check(step, next)
			if len(next.storage.lenders) > metadataLogicalLenderLimit {
				t.Fatalf("seed %d step %d: %d lenders", seed, step, len(next.storage.lenders))
			}
			// Borrowed ranges resolve to the storage holding the bytes.
			for _, borrow := range *next.storage.borrows.Load() {
				if borrow.lender.borrowedOverlap(borrow.from, borrow.from+borrow.size) {
					t.Fatalf("seed %d step %d: a range was borrowed from a range its lender borrowed", seed, step)
				}
			}
			generations = append(generations, next)
		}
		// Discarding every generation but the last keeps its lenders
		// readable.
		last := generations[len(generations)-1]
		for _, generation := range generations[:len(generations)-1] {
			generation.storage.Release()
		}
		check(-1, last)
		last.storage.Release()
		for _, generation := range generations {
			if generation.storage.refs != 0 {
				t.Fatalf("seed %d: a storage still has %d references", seed, generation.storage.refs)
			}
		}
	}
}

// A storage never borrows from more than metadataLogicalLenderLimit
// storages: the next borrow is refused and the caller copies.
func TestScaleLogicalBorrowedRangesLenderLimit(t *testing.T) {
	directory := t.TempDir()
	borrower := &metadataLogicalStorage{directory: directory, refs: 1}
	defer borrower.Release()
	for index := range metadataLogicalLenderLimit + 1 {
		lender := &metadataLogicalStorage{directory: directory, refs: 1}
		if err := lender.append(make([]byte, metadataLogicalBorrowMinimum)); err != nil {
			t.Fatal(err)
		}
		if err := lender.settle(); err != nil {
			t.Fatal(err)
		}
		borrowed, err := borrower.borrow(lender, 0, metadataLogicalBorrowMinimum)
		lender.Release()
		if err != nil || borrowed != (index < metadataLogicalLenderLimit) {
			t.Fatalf("borrow %d = %v %v", index, borrowed, err)
		}
	}
}
