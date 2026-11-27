package store

import (
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"
)

// saveMetadataLogicalMemoCounters restores the process-wide memo counters
// a test moved.
func saveMetadataLogicalMemoCounters(t *testing.T) {
	t.Helper()
	copies, indexBytes, rebuilds := metadataLogicalCopies.Load(), metadataLogicalIndexBytes.Load(), metadataLogicalMemoRebuilds.Load()
	t.Cleanup(func() {
		if got := metadataLogicalMemoRebuilds.Load(); got != rebuilds {
			t.Errorf("memo ranges were rebuilt %d times: a memo write skipped its ranges", got-rebuilds)
		}
		metadataLogicalCopies.Store(copies)
		metadataLogicalIndexBytes.Store(indexBytes)
	})
}

// checkMetadataLogicalMemoRanges verifies the treap invariants of ranges
// against the frames memo holds.
func checkMetadataLogicalMemoRanges[V any](t *testing.T, name string, ranges *metadataLogicalMemoRanges, memo map[int64]V, size func(V) int64) {
	t.Helper()
	if ranges.count != len(memo) {
		t.Fatalf("%s: ranges count %d, memo %d", name, ranges.count, len(memo))
	}
	seen := 0
	var walk func(node *metadataLogicalMemoRange, low, high int64, depth int) int64
	walk = func(node *metadataLogicalMemoRange, low, high int64, depth int) int64 {
		if node == nil {
			return 0
		}
		if depth > 200 {
			t.Fatalf("%s: tree deeper than 200", name)
		}
		seen++
		if node.key < low || node.key > high {
			t.Fatalf("%s: key %d outside [%d,%d]", name, node.key, low, high)
		}
		if node.priority != metadataLogicalMemoPriority(node.key) {
			t.Fatalf("%s: key %d has a foreign priority", name, node.key)
		}
		for _, child := range []*metadataLogicalMemoRange{node.left, node.right} {
			if child != nil && child.priority > node.priority {
				t.Fatalf("%s: key %d breaks heap order", name, node.key)
			}
		}
		value, found := memo[node.key]
		if !found || node.end != node.key+size(value) {
			t.Fatalf("%s: node [%d,%d) is not a memo frame (found=%t)", name, node.key, node.end, found)
		}
		maxEnd := max(node.end, walk(node.left, low, node.key-1, depth+1), walk(node.right, node.key+1, high, depth+1))
		if node.maxEnd != maxEnd {
			t.Fatalf("%s: key %d maxEnd %d want %d", name, node.key, node.maxEnd, maxEnd)
		}
		return maxEnd
	}
	walk(ranges.root, -1<<62, 1<<62, 0)
	if seen != len(memo) {
		t.Fatalf("%s: tree holds %d nodes, memo %d", name, seen, len(memo))
	}
}

func checkMetadataLogicalMemo(t *testing.T, s *metadataLogicalStorage) {
	t.Helper()
	checkMetadataLogicalMemoRanges(t, "indexes", &s.indexRanges, s.indexes, func(index metadataLogicalChildIndex) int64 { return index.size })
	checkMetadataLogicalMemoRanges(t, "clones", &s.cloneRanges, s.clones, func(check metadataLogicalCloneCheck) int64 { return check.size })
	checkMetadataLogicalMemoRanges(t, "user indexes", &s.userRanges, s.userIndexes, func(index metadataLogicalUserIndex) int64 { return index.size })
}

// newMetadataLogicalMemoStorage is a storage holding size bytes, so memo
// writes up to that length are admitted.
func newMetadataLogicalMemoStorage(t *testing.T, size int) *metadataLogicalStorage {
	t.Helper()
	s, err := newMetadataLogicalStorage(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Release)
	if err := s.append(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	return s
}

func storeMetadataLogicalMemoIndex(t *testing.T, s *metadataLogicalStorage, offset, size int64, count int) {
	t.Helper()
	if !reserveMetadataLogicalChildIndex(count) {
		t.Fatal("child index budget is full")
	}
	s.storeChildIndex(offset, size, offset, make([]metadataLogicalChildHeader, count))
}

func TestMetadataLogicalDropFrameMemoOverlapKinds(t *testing.T) {
	type kind struct {
		name  string
		add   func(*metadataLogicalStorage, int64, int64)
		check func(*metadataLogicalStorage, int64) bool
	}
	frameType := reflect.TypeFor[[]string]()
	kinds := []kind{
		{
			name: "copies",
			add: func(s *metadataLogicalStorage, offset, size int64) {
				s.recordCopy(offset, metadataLogicalNode{source: s, offset: 0, size: size})
			},
			check: func(s *metadataLogicalStorage, offset int64) bool {
				return slices.ContainsFunc(s.copies, func(copy metadataLogicalCopy) bool { return copy.offset == offset })
			},
		},
		{
			name: "indexes",
			add: func(s *metadataLogicalStorage, offset, size int64) {
				storeMetadataLogicalMemoIndex(t, s, offset, size, 2)
			},
			check: func(s *metadataLogicalStorage, offset int64) bool {
				_, found := s.indexes[offset]
				return found
			},
		},
		{
			name: "clones",
			add: func(s *metadataLogicalStorage, offset, size int64) {
				s.storeCloneCheck(metadataLogicalNode{source: s, offset: offset, size: size, typ: frameType}, true)
			},
			check: func(s *metadataLogicalStorage, offset int64) bool {
				_, found := s.clones[offset]
				return found
			},
		},
		{
			name: "user indexes",
			add: func(s *metadataLogicalStorage, offset, size int64) {
				s.storeUserIndex(metadataLogicalNode{source: s, offset: offset, size: size, typ: frameType}, metadataLogicalUserIndex{size: size, typ: frameType})
			},
			check: func(s *metadataLogicalStorage, offset int64) bool {
				_, found := s.userIndexes[offset]
				return found
			},
		},
	}
	// The memo frame is [10, 20); each write is [from, to).
	writes := []struct {
		name     string
		from, to int64
		kept     bool
	}{
		{"before", 2, 8, true},
		{"adjacent before", 4, 10, true},
		{"overlapping start", 6, 12, false},
		{"contained", 12, 14, false},
		{"same range", 10, 20, false},
		{"containing", 5, 25, false},
		{"overlapping end", 18, 24, false},
		{"adjacent after", 20, 26, true},
		{"after", 22, 30, true},
		{"one byte at start", 10, 11, false},
		{"one byte at end", 19, 20, false},
		{"discard", 0, metadataLogicalMemoEnd, false},
	}
	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			saveMetadataLogicalMemoCounters(t)
			for _, write := range writes {
				s := newMetadataLogicalMemoStorage(t, 64)
				copies, indexBytes := metadataLogicalCopies.Load(), metadataLogicalIndexBytes.Load()
				// Neighbours on both sides that no write above reaches.
				kind.add(s, 0, 2)
				kind.add(s, 10, 10)
				kind.add(s, 40, 8)
				if !kind.check(s, 10) {
					t.Fatalf("%s: the frame was not stored", write.name)
				}
				s.dropFrameMemo(write.from, write.to)
				if got := kind.check(s, 10); got != write.kept {
					t.Fatalf("%s: kept=%t want %t", write.name, got, write.kept)
				}
				if write.to != metadataLogicalMemoEnd && (!kind.check(s, 0) || !kind.check(s, 40)) {
					t.Fatalf("%s: a neighbour was dropped", write.name)
				}
				checkMetadataLogicalMemo(t, s)
				s.dropFrameMemo(0, metadataLogicalMemoEnd)
				checkMetadataLogicalMemo(t, s)
				if got := metadataLogicalCopies.Load(); got != copies {
					t.Fatalf("%s: copies counter %d want %d", write.name, got, copies)
				}
				if got := metadataLogicalIndexBytes.Load(); got != indexBytes {
					t.Fatalf("%s: index bytes %d want %d", write.name, got, indexBytes)
				}
			}
		})
	}
}

func TestMetadataLogicalDropFrameMemoCounters(t *testing.T) {
	saveMetadataLogicalMemoCounters(t)
	s := newMetadataLogicalMemoStorage(t, 64)
	copies, indexBytes := metadataLogicalCopies.Load(), metadataLogicalIndexBytes.Load()
	for i, count := range []int{3, 5, 7} {
		offset := int64(10 * i)
		s.recordCopy(offset, metadataLogicalNode{source: s, size: 4})
		storeMetadataLogicalMemoIndex(t, s, offset, 4, count)
	}
	// A second index for a stored collection is released, not kept.
	storeMetadataLogicalMemoIndex(t, s, 10, 4, 9)
	if got, want := metadataLogicalCopies.Load(), copies+3; got != want {
		t.Fatalf("copies=%d want %d", got, want)
	}
	if got, want := metadataLogicalIndexBytes.Load(), indexBytes+metadataLogicalChildIndexBytes(3)+metadataLogicalChildIndexBytes(5)+metadataLogicalChildIndexBytes(7); got != want {
		t.Fatalf("index bytes=%d want %d", got, want)
	}
	s.dropFrameMemo(11, 19)
	if got, want := metadataLogicalCopies.Load(), copies+2; got != want {
		t.Fatalf("copies=%d want %d", got, want)
	}
	if got, want := metadataLogicalIndexBytes.Load(), indexBytes+metadataLogicalChildIndexBytes(3)+metadataLogicalChildIndexBytes(7); got != want {
		t.Fatalf("index bytes=%d want %d", got, want)
	}
	if len(s.indexes) != 2 || len(s.copies) != 2 {
		t.Fatalf("remaining memo entries indexes=%d copies=%d", len(s.indexes), len(s.copies))
	}
	checkMetadataLogicalMemo(t, s)
	// The last release discards the rest and returns both counters.
	s.Release()
	checkMetadataLogicalMemo(t, s)
	if got := metadataLogicalCopies.Load(); got != copies {
		t.Fatalf("copies=%d want %d after discard", got, copies)
	}
	if got := metadataLogicalIndexBytes.Load(); got != indexBytes {
		t.Fatalf("index bytes=%d want %d after discard", got, indexBytes)
	}
}

// Clone checks and user indexes are stored again at an offset with a new
// size; the ranges follow the latest size.
func TestMetadataLogicalMemoRangesResize(t *testing.T) {
	saveMetadataLogicalMemoCounters(t)
	s := newMetadataLogicalMemoStorage(t, 64)
	frameType := reflect.TypeFor[[]string]()
	s.storeCloneCheck(metadataLogicalNode{source: s, offset: 10, size: 30, typ: frameType}, false)
	s.storeCloneCheck(metadataLogicalNode{source: s, offset: 10, size: 4, typ: frameType}, true)
	s.storeUserIndex(metadataLogicalNode{source: s, offset: 10, size: 4, typ: frameType}, metadataLogicalUserIndex{size: 4, typ: frameType})
	s.storeUserIndex(metadataLogicalNode{source: s, offset: 10, size: 30, typ: frameType}, metadataLogicalUserIndex{size: 30, typ: frameType})
	checkMetadataLogicalMemo(t, s)
	// [20, 22) misses the 4-byte clone frame and hits the 30-byte user frame.
	s.dropFrameMemo(20, 22)
	if _, found := s.clones[10]; !found {
		t.Fatal("the resized clone check was dropped by a write past its end")
	}
	if _, found := s.userIndexes[10]; found {
		t.Fatal("the resized user index survived a write inside it")
	}
	checkMetadataLogicalMemo(t, s)
}

// Hash priorities keep the tree shallow when frames are stored in offset
// order, as a builder writes them.
func TestMetadataLogicalMemoRangesDepth(t *testing.T) {
	var ranges metadataLogicalMemoRanges
	const frames = 1 << 17
	for i := range int64(frames) {
		ranges.set(i*64, 64)
	}
	var depth func(*metadataLogicalMemoRange) int
	depth = func(node *metadataLogicalMemoRange) int {
		if node == nil {
			return 0
		}
		return 1 + max(depth(node.left), depth(node.right))
	}
	if got := depth(ranges.root); got > 64 {
		t.Fatalf("depth %d for %d sequential frames", got, frames)
	}
	if keys := ranges.drop(64*1000+10, 64*1001+1, nil); !slices.Equal(keys, []int64{64 * 1000, 64 * 1001}) {
		t.Fatalf("dropped %v", keys)
	}
	if ranges.count != frames-2 {
		t.Fatalf("count %d", ranges.count)
	}
}

func TestMetadataLogicalDropFrameMemoMatchesFullScan(t *testing.T) {
	saveMetadataLogicalMemoCounters(t)
	const spool = 6000
	frameType := reflect.TypeFor[[]string]()
	fixture := func(seed int64) *metadataLogicalStorage {
		rng := rand.New(rand.NewSource(seed))
		s := newMetadataLogicalMemoStorage(t, spool)
		offset := int64(0)
		for range 160 {
			offset += int64(rng.Intn(12))
			size := int64(rng.Intn(20) + 1)
			s.recordCopy(offset, metadataLogicalNode{source: s, size: size})
			offset += size
		}
		for range 300 {
			// Nested and overlapping frames of every size class, some
			// stored twice at one offset.
			size := int64(rng.Intn(30) + 1)
			if rng.Intn(20) == 0 {
				size = int64(rng.Intn(spool / 2))
			}
			key := int64(rng.Intn(spool - int(size)))
			storeMetadataLogicalMemoIndex(t, s, key, size, rng.Intn(8)+1)
			key, size = int64(rng.Intn(5000)), int64(rng.Intn(30)+1)
			s.storeCloneCheck(metadataLogicalNode{source: s, offset: key, size: size, typ: frameType}, rng.Intn(2) == 0)
			key, size = int64(rng.Intn(5000)), int64(rng.Intn(30)+1)
			s.storeUserIndex(metadataLogicalNode{source: s, offset: key, size: size, typ: frameType}, metadataLogicalUserIndex{size: size, typ: frameType})
		}
		return s
	}
	optimized, oracle := fixture(255), fixture(255)
	if !reflect.DeepEqual(memoStorageSnapshot(optimized), memoStorageSnapshot(oracle)) {
		t.Fatal("random memo fixtures differ")
	}
	rng := rand.New(rand.NewSource(256))
	for step := range 2000 {
		offset := int64(rng.Intn(spool))
		end := offset + int64(rng.Intn(40)+1)
		if rng.Intn(50) == 0 {
			end = offset + int64(rng.Intn(spool))
		}
		if step%5 == 0 {
			// Keep both memos populated: store the same new frames.
			key, size := int64(rng.Intn(spool-32)), int64(rng.Intn(32)+1)
			count := rng.Intn(8) + 1
			changed := rng.Intn(2) == 0
			for _, s := range []*metadataLogicalStorage{optimized, oracle} {
				storeMetadataLogicalMemoIndex(t, s, key, size, count)
				s.storeCloneCheck(metadataLogicalNode{source: s, offset: key, size: size, typ: frameType}, changed)
				s.storeUserIndex(metadataLogicalNode{source: s, offset: key, size: size, typ: frameType}, metadataLogicalUserIndex{size: size, typ: frameType})
			}
		}
		indexBytes, copies := metadataLogicalIndexBytes.Load(), metadataLogicalCopies.Load()
		optimized.dropFrameMemo(offset, end)
		optimizedBytes, optimizedCopies := indexBytes-metadataLogicalIndexBytes.Load(), copies-metadataLogicalCopies.Load()
		oracleBytes, oracleCopies := metadataLogicalIndexBytes.Load(), metadataLogicalCopies.Load()
		dropFrameMemoFullScanForTest(oracle, offset, end)
		if got, want := memoStorageSnapshot(optimized), memoStorageSnapshot(oracle); !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d range [%d,%d) diverged:\noptimized=%+v\noracle=%+v", step, offset, end, got, want)
		}
		if released := oracleBytes - metadataLogicalIndexBytes.Load(); released != optimizedBytes {
			t.Fatalf("step %d: released %d index bytes, full scan %d", step, optimizedBytes, released)
		}
		if released := oracleCopies - metadataLogicalCopies.Load(); released != optimizedCopies {
			t.Fatalf("step %d: released %d copies, full scan %d", step, optimizedCopies, released)
		}
		checkMetadataLogicalMemo(t, optimized)
	}
	// The full scan leaves the oracle's ranges stale; set them from its maps
	// so its release does not count as a rebuild.
	oracle.memoMu.Lock()
	oracle.indexRanges.reset()
	oracle.cloneRanges.reset()
	oracle.userRanges.reset()
	for key, index := range oracle.indexes {
		oracle.indexRanges.set(key, index.size)
	}
	for key, check := range oracle.clones {
		oracle.cloneRanges.set(key, check.size)
	}
	for key, index := range oracle.userIndexes {
		oracle.userRanges.set(key, index.size)
	}
	oracle.memoMu.Unlock()
	checkMetadataLogicalMemo(t, oracle)
}

// dropFrameMemoFullScanForTest is dropFrameMemo before it indexed the memo
// maps by range: it walks every entry of every map.
func dropFrameMemoFullScanForTest(s *metadataLogicalStorage, offset, end int64) {
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
			metadataLogicalIndexBytes.Add(-metadataLogicalChildIndexBytes(len(index.headers)))
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

type metadataLogicalMemoSnapshot struct {
	copies, indexes, clones, userIndexes []metadataLogicalMemoEntry
}

type metadataLogicalMemoEntry struct {
	key, size, detail int64
}

func memoStorageSnapshot(s *metadataLogicalStorage) metadataLogicalMemoSnapshot {
	s.memoMu.Lock()
	defer s.memoMu.Unlock()
	var snapshot metadataLogicalMemoSnapshot
	for _, copy := range s.copies {
		snapshot.copies = append(snapshot.copies, metadataLogicalMemoEntry{key: copy.offset, size: copy.size})
	}
	for key, index := range s.indexes {
		snapshot.indexes = append(snapshot.indexes, metadataLogicalMemoEntry{key: key, size: index.size, detail: int64(len(index.headers))})
	}
	for key, check := range s.clones {
		detail := int64(0)
		if check.changed {
			detail = 1
		}
		snapshot.clones = append(snapshot.clones, metadataLogicalMemoEntry{key: key, size: check.size, detail: detail})
	}
	for key, index := range s.userIndexes {
		snapshot.userIndexes = append(snapshot.userIndexes, metadataLogicalMemoEntry{key: key, size: index.size})
	}
	for _, entries := range [][]metadataLogicalMemoEntry{snapshot.indexes, snapshot.clones, snapshot.userIndexes} {
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	}
	return snapshot
}
