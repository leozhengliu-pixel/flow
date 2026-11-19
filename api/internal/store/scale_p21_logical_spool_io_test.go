package store

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"testing"
)

// settleForTesting writes the buffered appends out and drops the read cache,
// so a test that damages the spool file itself (truncates or closes it) sees
// the damage on its next read.
func (s *metadataLogicalStorage) settleForTesting() error {
	if err := s.flush(); err != nil {
		return err
	}
	for index := range s.blocks {
		s.blocks[index].Store(nil)
	}
	return nil
}

// The buffered, cached spool reads and writes exactly what the plain file
// would: random appends, header patches (buffered, flushed and straddling)
// and reads of every size, compared with a model after every operation and
// with the file once flushed.
func TestScaleP21LogicalSpoolIOMatchesFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "spool-io-*")
	if err != nil {
		t.Fatal(err)
	}
	storage := &metadataLogicalStorage{file: file, refs: 1}
	defer file.Close()
	random := rand.New(rand.NewPCG(7, 11))
	var model []byte
	check := func(step int) {
		t.Helper()
		for range 8 {
			if len(model) == 0 {
				return
			}
			offset := random.IntN(len(model))
			size := 1 + random.IntN(min(len(model)-offset, []int{8, 64, 3000, 40000}[random.IntN(4)]))
			got := make([]byte, size)
			count, err := storage.ReadAt(got, int64(offset))
			if err != nil || count != size || !bytes.Equal(got, model[offset:offset+size]) {
				t.Fatalf("step %d: read %d+%d: count %d err %v", step, offset, size, count, err)
			}
		}
		past := make([]byte, 16)
		start := max(0, len(model)-8)
		count, err := storage.ReadAt(past, int64(start))
		if !errors.Is(err, io.EOF) || count != len(model)-start || !bytes.Equal(past[:count], model[start:]) {
			t.Fatalf("step %d: read past the end: count %d err %v", step, count, err)
		}
	}
	for step := range 4000 {
		switch choice := random.IntN(10); {
		case choice < 6 || len(model) < 32:
			data := make([]byte, []int{1, 8, 16, 200, 5000, 70000}[random.IntN(6)])
			for index := range data {
				data[index] = byte(random.Uint32())
			}
			if err := storage.append(data); err != nil {
				t.Fatal(err)
			}
			model = append(model, data...)
		case choice < 9:
			offset := random.IntN(len(model) - 16)
			data := make([]byte, 1+random.IntN(16))
			for index := range data {
				data[index] = byte(random.Uint32())
			}
			if err := storage.writeAt(data, int64(offset)); err != nil {
				t.Fatal(err)
			}
			copy(model[offset:], data)
		default:
			if err := storage.flush(); err != nil {
				t.Fatal(err)
			}
		}
		check(step)
		if len(model) > 4<<20 {
			break
		}
	}
	if err := storage.flush(); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(onDisk, model) {
		t.Fatal("flushed spool file differs from the written bytes", err)
	}
}
