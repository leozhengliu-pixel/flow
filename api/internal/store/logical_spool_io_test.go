package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// flushForTesting writes the buffered appends out, creating the file.
func (s *metadataLogicalStorage) flushForTesting() error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.flushLocked()
}

// settleForTesting writes the buffered appends out and drops the read cache
// and the frame memo, so a test that damages the spool file itself (truncates
// or closes it) sees the damage on its next read.
func (s *metadataLogicalStorage) settleForTesting() error {
	if err := s.flushForTesting(); err != nil {
		return err
	}
	s.pendingMu.Lock()
	s.dropFrameMemo(0, metadataLogicalMemoEnd)
	s.pendingMu.Unlock()
	s.dropCachedBlocks()
	return nil
}

// The buffered, cached spool reads and writes exactly what the plain file
// would: random appends, header patches (buffered, flushed, straddling and
// past the end) and reads of every size, compared with a model after every
// operation and with the file once flushed. A spool that stays within its
// buffer creates no file.
func TestScaleLogicalSpoolIOMatchesFile(t *testing.T) {
	for _, seed := range []uint64{7, 19, 23} {
		directory := t.TempDir()
		storage := &metadataLogicalStorage{directory: directory, refs: 1}
		random := rand.New(rand.NewPCG(seed, 11))
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
					t.Fatalf("seed %d step %d: read %d+%d: count %d err %v", seed, step, offset, size, count, err)
				}
			}
			past := make([]byte, 16)
			start := max(0, len(model)-8)
			count, err := storage.ReadAt(past, int64(start))
			if !errors.Is(err, io.EOF) || count != len(model)-start || !bytes.Equal(past[:count], model[start:]) {
				t.Fatalf("seed %d step %d: read past the end: count %d err %v", seed, step, count, err)
			}
		}
		randomBytes := func(size int) []byte {
			data := make([]byte, size)
			for index := range data {
				data[index] = byte(random.Uint32())
			}
			return data
		}
		// A small spool, patched and settled, stays in memory.
		for range 40 {
			data := randomBytes(1 + random.IntN(200))
			if err := storage.append(data); err != nil {
				t.Fatal(err)
			}
			model = append(model, data...)
			patch := randomBytes(1 + random.IntN(8))
			offset := random.IntN(len(model) - len(patch) + 1)
			if err := storage.writeAt(patch, int64(offset)); err != nil {
				t.Fatal(err)
			}
			copy(model[offset:], patch)
			check(-1)
		}
		if err := storage.settle(); err != nil {
			t.Fatal(err)
		}
		check(-1)
		if storage.file != nil {
			t.Fatalf("seed %d: a %d-byte spool created a file", seed, len(model))
		}
		for step := range 4000 {
			switch choice := random.IntN(20); {
			case choice < 12 || len(model) < 32:
				data := randomBytes([]int{1, 8, 16, 200, 5000, 70000}[random.IntN(6)])
				if err := storage.append(data); err != nil {
					t.Fatal(err)
				}
				model = append(model, data...)
			case choice < 18:
				offset := random.IntN(len(model) - 16)
				data := randomBytes(1 + random.IntN(16))
				if err := storage.writeAt(data, int64(offset)); err != nil {
					t.Fatal(err)
				}
				copy(model[offset:], data)
			case choice == 18:
				// Past the end: extends the spool like os.File.WriteAt.
				offset := len(model) - random.IntN(8)
				data := randomBytes(8 + random.IntN(16))
				if err := storage.writeAt(data, int64(offset)); err != nil {
					t.Fatal(err)
				}
				model = append(model[:offset], data...)
			default:
				if err := storage.flushForTesting(); err != nil {
					t.Fatal(err)
				}
			}
			check(step)
			if len(model) > 4<<20 {
				break
			}
		}
		if err := storage.settle(); err != nil {
			t.Fatal(err)
		}
		check(-2)
		name := storage.Name()
		onDisk, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(onDisk, model) {
			t.Fatal("flushed spool file differs from the written bytes", err)
		}
		if err := storage.discard(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a discarded spool kept its file", err)
		}
		if _, err := storage.ReadAt(make([]byte, 4), 0); err == nil {
			t.Fatal("a discarded spool still reads")
		}
		if storage.Name() != "" || storage.append([]byte{1}) == nil {
			t.Fatal("a discarded spool created a file or accepted an append")
		}
	}
}

// Readers of one storage run concurrently with header patches of flushed and
// buffered bytes and with appends. A read that starts after a patch returned
// sees the patched byte (never a cached older block), and appended frames
// read back exactly once their header patch returned. Run under -race.
func TestScaleLogicalSpoolIOConcurrentReadPatch(t *testing.T) {
	storage := &metadataLogicalStorage{directory: t.TempDir(), refs: 1}
	defer storage.discard()
	// The flushed region holds one-byte counters every 61 bytes; each is
	// patched to 1, 2, ... up to 250 times, so a counter never goes back.
	const (
		initial = 256 << 10
		spacing = 61
		slots   = initial / spacing
		frame   = 1000
		frames  = 600
		rounds  = 250
	)
	if err := storage.append(make([]byte, initial)); err != nil {
		t.Fatal(err)
	}
	if err := storage.flushForTesting(); err != nil {
		t.Fatal(err)
	}
	var committed [slots]atomic.Uint32
	expected := func(position int64) byte {
		index, within := (position-initial)/frame, (position-initial)%frame
		if within < 8 {
			var header [8]byte
			binary.LittleEndian.PutUint64(header[:], uint64(index+1))
			return header[within]
		}
		return byte(position * 31)
	}
	var complete atomic.Int64
	complete.Store(initial)
	var stop atomic.Bool
	var failures atomic.Int64
	fail := func(format string, args ...any) {
		if failures.Add(1) <= 5 {
			t.Errorf(format, args...)
		}
	}
	var writers, readers sync.WaitGroup
	// Patchers: two goroutines on disjoint counters (patches serialize on
	// the storage; their reads and invalidations race with readers).
	for patcher := range 2 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			random := rand.New(rand.NewPCG(uint64(patcher), 3))
			for range slots / 2 * 12 {
				slot := random.IntN(slots/2)*2 + patcher
				next := committed[slot].Load() + 1
				if next > rounds {
					continue
				}
				if err := storage.writeAt([]byte{byte(next)}, int64(slot*spacing)); err != nil {
					fail("patch: %v", err)
					return
				}
				committed[slot].Store(next)
			}
		}()
	}
	// Appender: frames written like a builder writes them, a zero header,
	// the body, then the header patched (often still in the buffer).
	writers.Add(1)
	go func() {
		defer writers.Done()
		for index := range frames {
			start := int64(initial + index*frame)
			body := make([]byte, frame)
			for at := 8; at < frame; at++ {
				body[at] = byte((start + int64(at)) * 31)
			}
			if err := storage.append(body); err != nil {
				fail("append: %v", err)
				return
			}
			var header [8]byte
			binary.LittleEndian.PutUint64(header[:], uint64(index+1))
			if err := storage.writeAt(header[:], start); err != nil {
				fail("header patch: %v", err)
				return
			}
			complete.Store(start + frame)
		}
	}()
	for reader := range 6 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			random := rand.New(rand.NewPCG(uint64(reader), 5))
			for !stop.Load() {
				if random.IntN(2) == 0 {
					// Counters: one byte, a frame field or a large read.
					size := []int{1, 8, 200, 3000}[random.IntN(4)]
					offset := random.IntN(initial - size)
					var before []uint32
					for at := offset; at < offset+size; at++ {
						if at%spacing == 0 && at/spacing < slots {
							before = append(before, committed[at/spacing].Load())
						}
					}
					got := make([]byte, size)
					if count, err := storage.ReadAt(got, int64(offset)); err != nil || count != size {
						fail("read %d+%d: %d %v", offset, size, count, err)
						return
					}
					seen := 0
					for at := offset; at < offset+size; at++ {
						if at%spacing == 0 && at/spacing < slots {
							if uint32(got[at-offset]) < before[seen] {
								fail("counter %d read %d after patch %d returned", at/spacing, got[at-offset], before[seen])
							}
							seen++
						} else if got[at-offset] != 0 {
							fail("byte %d is %d, never written", at, got[at-offset])
						}
					}
					continue
				}
				end := complete.Load()
				if end == initial {
					continue
				}
				size := int64([]int{8, 16, 500, 2500}[random.IntN(4)])
				offset := initial + random.Int64N(end-initial)
				size = min(size, end-offset)
				got := make([]byte, size)
				if count, err := storage.ReadAt(got, offset); err != nil || int64(count) != size {
					fail("read appended %d+%d: %d %v", offset, size, count, err)
					return
				}
				for at := range got {
					if want := expected(offset + int64(at)); got[at] != want {
						fail("appended byte %d is %d, want %d", offset+int64(at), got[at], want)
						break
					}
				}
			}
		}()
	}
	writers.Wait()
	stop.Store(true)
	readers.Wait()
	// Everything settles to the same bytes on disk.
	if err := storage.settle(); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(storage.Name())
	if err != nil || len(onDisk) != initial+frames*frame {
		t.Fatal("settled spool size", len(onDisk), err)
	}
	for slot := range slots {
		if onDisk[slot*spacing] != byte(committed[slot].Load()) {
			t.Fatalf("counter %d on disk %d, committed %d", slot, onDisk[slot*spacing], committed[slot].Load())
		}
	}
	for at := int64(initial); at < int64(len(onDisk)); at++ {
		if onDisk[at] != expected(at) {
			t.Fatalf("appended byte %d on disk %d", at, onDisk[at])
		}
	}
}

// The interleavings of invariant 3, forced with the test hook: a block read
// overlapping a patch, whether the read began during the patch or the patch
// ran while the read was in flight, never leaves a block that serves the
// bytes from before the patch to a read that starts after it returned.
func TestScaleLogicalSpoolIOPatchDuringBlockRead(t *testing.T) {
	for _, readFirst := range []bool{false, true} {
		storage := &metadataLogicalStorage{directory: t.TempDir(), refs: 1}
		if err := storage.append(make([]byte, 64<<10)); err != nil {
			t.Fatal(err)
		}
		if err := storage.flushForTesting(); err != nil {
			t.Fatal(err)
		}
		patchBegun, patchGo := make(chan struct{}), make(chan struct{})
		blockRead, readGo := make(chan struct{}), make(chan struct{})
		hook := func(s *metadataLogicalStorage, point metadataLogicalSpoolPoint) {
			if s != storage {
				return
			}
			switch point {
			case metadataLogicalSpoolPatchBegun:
				patchBegun <- struct{}{}
				<-patchGo
			case metadataLogicalSpoolBlockRead:
				blockRead <- struct{}{}
				<-readGo
			}
		}
		metadataLogicalSpoolHook.Store(&hook)
		patched, read := make(chan error, 1), make(chan error, 1)
		patch := func() { patched <- storage.writeAt([]byte{7}, 100) }
		readOld := func() { _, err := storage.ReadAt(make([]byte, 8), 96); read <- err }
		if readFirst {
			// The read loads an even count and reads; the whole patch runs
			// before the read publishes its block.
			go readOld()
			<-blockRead
			go patch()
			<-patchBegun
			close(patchGo)
			if err := <-patched; err != nil {
				t.Fatal(err)
			}
			close(readGo)
		} else {
			// The patch has begun (odd count) when the read loads the count
			// and reads the bytes before the patch writes them.
			go patch()
			<-patchBegun
			go readOld()
			<-blockRead
			close(patchGo)
			if err := <-patched; err != nil {
				t.Fatal(err)
			}
			close(readGo)
		}
		if err := <-read; err != nil {
			t.Fatal(err)
		}
		metadataLogicalSpoolHook.Store(nil)
		got := make([]byte, 8)
		if _, err := storage.ReadAt(got, 96); err != nil || got[4] != 7 {
			t.Fatalf("read first %v: a read after the patch returned sees %v (%v)", readFirst, got, err)
		}
		if err := storage.discard(); err != nil {
			t.Fatal(err)
		}
	}
}
