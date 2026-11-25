package store

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

// SW602: every generation derived from a source shares its spool group, so a
// workspace holds one descriptor charge however many generations and
// storages are alive (forks, transforms, abandoned forks); settled in-memory
// spools stay within the group's allowance and the rest go to files; the
// charge returns when the last generation is released.
func TestScaleP21SW602SpoolGroupHoldsOneCharge(t *testing.T) {
	ctx := t.Context()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	labels := func(round int) []domain.IssueLabel {
		var out []domain.IssueLabel
		for i := range 8 {
			out = append(out, domain.IssueLabel{ID: fmt.Sprintf("label-%d", i), Name: fmt.Sprintf("%d %s", round, strings.Repeat("n", 1<<10)), Color: "#4cb782"})
		}
		return out
	}
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{Labels: labels(0), Settings: map[string]any{"large": strings.Repeat("x", 256<<10)}}, copyStamp{workspaceID: "sw602-group"})
	if err != nil {
		t.Fatal(err)
	}
	group := source.spoolGroup()
	charged := func(step string) {
		t.Helper()
		if got := metadataLogicalDescriptorsUsed.Load() - descriptors; got != metadataLogicalDescriptorBytes {
			t.Fatalf("%s: the lineage holds %d bytes of FLOW_METADATA_SOURCE_BYTES, want one charge of %d", step, got, metadataLogicalDescriptorBytes)
		}
		group.mu.Lock()
		resident := group.resident
		group.mu.Unlock()
		if resident > metadataLogicalWriteBytes {
			t.Fatalf("%s: %d bytes of settled spools in memory, over the group's allowance", step, resident)
		}
	}
	charged("captured")
	scope := MetadataRecordScope{Fields: []string{"labels"}}
	generations := []*metadataLogicalSource{source}
	defer func() {
		for _, generation := range generations {
			generation.Release()
		}
	}()
	files := 0
	for round := 1; round <= 8; round++ {
		previous := generations[len(generations)-1]
		before, err := previous.Select(ctx, scope, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		next := cloneBootstrap(before)
		next.Labels = labels(round)
		fork, err := previous.Fork(ctx, before, next, scope, previous.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, fork)
		if fork.spoolGroup() != group {
			t.Fatalf("round %d: the fork wrote a spool group of its own", round)
		}
		for _, storage := range fork.stores {
			if storage.group != group {
				t.Fatalf("round %d: the fork holds a storage of another group", round)
			}
		}
		charged(fmt.Sprintf("fork %d", round))
		// An abandoned fork (cancelled while it writes) returns what it took.
		_, err = previous.Fork(&scaleP21SW27CancelAfterChecks{Context: ctx, limit: 6}, before, next, scope, previous.Stamp())
		if err == nil {
			t.Fatalf("round %d: the cancelled fork succeeded", round)
		}
		charged(fmt.Sprintf("cancelled fork %d", round))
		unchanged, err := fork.Transform(ctx, func(string, metadataLogicalNode) (metadataAggregateNode, bool, error) { return nil, false, nil })
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, unchanged)
		charged(fmt.Sprintf("transform %d", round))
	}
	for _, generation := range generations {
		for _, storage := range generation.stores {
			storage.pendingMu.Lock()
			if storage.file != nil && storage.bytes < metadataLogicalWriteBytes {
				files++
			}
			storage.pendingMu.Unlock()
		}
	}
	if files == 0 {
		t.Fatal("no small spool went to a file once the group's allowance was full")
	}
	// Reads of every generation still see their own labels.
	for i, generation := range generations[1:] {
		data, err := generation.Select(ctx, scope, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%d ", i/2+1); len(data.Labels) != 8 || !strings.HasPrefix(data.Labels[0].Name, want) {
			t.Fatalf("generation %d reads labels %q, want round %q", i+1, data.Labels[0].Name[:8], want)
		}
	}
	for _, generation := range generations[:len(generations)-1] {
		generation.Release()
	}
	generations = generations[len(generations)-1:]
	charged("older generations released")
	generations[0].Release()
	generations = nil
	if metadataLogicalDescriptorsUsed.Load() != descriptors || metadataLogicalDiskUsed.Load() != disk {
		t.Fatalf("releasing every generation left %d descriptor and %d disk bytes", metadataLogicalDescriptorsUsed.Load()-descriptors, metadataLogicalDiskUsed.Load()-disk)
	}
}

// SW602: the group's storages share one block cache. Blocks of different
// storages at the same offsets stay apart; two concurrent misses never take
// one slot (the first read's block was lost and read again); the cache keeps
// at most one buffer per slot, reusing evicted buffers instead of allocating
// per miss; the group's last release drops every buffer.
func TestScaleP21SW602SharedBlockCache(t *testing.T) {
	ctx := t.Context()
	descriptors := metadataLogicalDescriptorsUsed.Load()
	first, err := newMetadataLogicalStorage(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	group := first.group
	second, err := newMetadataLogicalStorage(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	const size = 40 * metadataLogicalReadBlock
	content := func(storage int, at int64) byte { return byte(at*int64(2*storage+3) + int64(storage)) }
	storages := []*metadataLogicalStorage{first, second}
	for i, storage := range storages {
		data := make([]byte, size)
		for at := range data {
			data[at] = content(i, int64(at))
		}
		if err := storage.append(data); err != nil {
			t.Fatal(err)
		}
		if err := storage.flushForTesting(); err != nil {
			t.Fatal(err)
		}
	}
	check := func(p []byte, storage int, at int64) {
		t.Helper()
		if count, err := storages[storage].ReadAt(p, at); err != nil || count != len(p) {
			t.Errorf("read %d of storage %d: %d %v", at, storage, count, err)
			return
		}
		for i, got := range p {
			if want := content(storage, at+int64(i)); got != want {
				t.Errorf("storage %d byte %d reads %d, want %d: a block of the other storage was served", storage, at+int64(i), got, want)
				return
			}
		}
	}
	p := make([]byte, 16)
	var blockReads atomic.Int64
	var pause atomic.Bool
	paused, resume := make(chan struct{}), make(chan struct{})
	hook := func(s *metadataLogicalStorage, point metadataLogicalSpoolPoint) {
		if point != metadataLogicalSpoolBlockRead || (s != first && s != second) {
			return
		}
		blockReads.Add(1)
		if s == first && pause.CompareAndSwap(true, false) {
			close(paused)
			<-resume
		}
	}
	metadataLogicalSpoolHook.Store(&hook)
	defer metadataLogicalSpoolHook.Store(nil)
	// The first storage's miss is in flight while the second misses the same
	// offset of its own spool: both blocks are cached.
	pause.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); check(make([]byte, 16), 0, 100) }()
	<-paused
	check(p, 1, 100)
	close(resume)
	<-done
	before := blockReads.Load()
	check(p, 0, 100)
	check(p, 1, 100)
	if reads := blockReads.Load() - before; reads != 0 {
		t.Fatalf("concurrent misses of two storages left %d of their blocks uncached", reads)
	}
	// Concurrent scans of both storages: right bytes, and the cache never
	// holds more buffers than slots.
	var wg sync.WaitGroup
	for reader := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := make([]byte, 16)
			for round := range 3 {
				for at := int64(0); at+16 <= size; at += 4100 + int64(reader+round) {
					check(p, reader%2, at)
				}
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	buffers := func() int {
		group.cache.mu.RLock()
		defer group.cache.mu.RUnlock()
		held := len(group.cache.spare)
		for _, cached := range group.cache.blocks {
			if cached != nil {
				held++
			}
		}
		return held
	}
	if held := buffers(); held > metadataLogicalReadSlots {
		t.Fatalf("the shared cache holds %d buffers for %d slots", held, metadataLogicalReadSlots)
	}
	// A scan of new blocks reads into evicted buffers.
	allocs := testing.AllocsPerRun(1, func() {
		for at := int64(0); at+16 <= size; at += metadataLogicalReadBlock {
			check(p, 1, at)
		}
	})
	if allocs > 2 {
		t.Fatalf("a scan of %d blocks allocated %.0f times", size/metadataLogicalReadBlock, allocs)
	}
	first.Release()
	if held := buffers(); held == 0 {
		t.Fatal("releasing one storage of a live group dropped the whole cache")
	}
	group.cache.mu.RLock()
	for _, cached := range group.cache.blocks {
		if cached != nil && cached.owner == first {
			t.Fatal("a released storage keeps blocks in the shared cache")
		}
	}
	group.cache.mu.RUnlock()
	second.Release()
	if held := buffers(); held != 0 || !group.cache.released {
		t.Fatalf("the group's last release left %d buffers", held)
	}
	if metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatalf("the group left %d descriptor bytes", metadataLogicalDescriptorsUsed.Load()-descriptors)
	}
}
