package store

import "flow/api/internal/boundedcrdt/internal/meter"

import ()

// ClientBlockList is the dense, clock-ordered list of every block
// produced by a single client. Invariants enforced:
//
//  1. Per-client clock density: the union of cell ranges equals
//     [0, Clock()) with no gaps and no overlaps.
//  2. Sorted by clock: list[i].ClockStart < list[i+1].ClockStart.
//  3. Inclusive ranges: ClockStart and ClockEnd are both inclusive;
//     Len = End - Start + 1.
//
// See docs/yrs-port-notes/store.md "Internal invariants".
type ClientBlockList struct {
	list []BlockCell
}

// NewClientBlockList returns an empty list. Capacity is reserved for
// the common case of a few-dozen blocks per client.
func NewClientBlockList(mc *meter.Meter) *ClientBlockList {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, ClientBlockList{list: meter.MakeSlice[[]BlockCell, BlockCell](mc, uint64(0), uint64(16))})
}

// Len returns the number of cells in the list.
func (l *ClientBlockList) Len(mc *meter.Meter) int {
	mc.Enter()
	defer mc.Leave()
	return len(l.list)
}

// Get returns the cell at index i. Returns the zero BlockCell and false
// if i is out of range.
func (l *ClientBlockList) Get(mc *meter.Meter, i int) (BlockCell, bool) {
	mc.Enter()
	defer mc.Leave()

	if i < 0 || i >= len(l.list) {
		return BlockCell{}, false
	}
	return l.list[i], true
}

// Clock returns the next free clock for this client. Equal to
// list[len-1].ClockEnd() + 1, or 0 for an empty list. Constant-time.
//
// Mirrors yrs ClientBlockList::clock (block_store.rs:24-34). Used for
// state vector materialization and append-precondition checks.
func (l *ClientBlockList) Clock(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()

	if len(l.list) == 0 {
		return 0
	}
	return l.list[len(l.list)-1].ClockEnd(mc) + 1
}

// FindPivot returns the index of the cell whose [ClockStart, ClockEnd]
// inclusive range contains the given clock. Returns (0, false) if the
// list is empty or the clock is past Clock().
//
// Implementation uses sort.Search rather than yrs's hand-rolled
// interpolation+binary search (block_store.rs:42-68). The interpolation
// is a micro-optimisation; sort.Search costs at most log2(N) extra
// comparisons and avoids the divide-by-zero edge case yrs has when the
// list contains a single block ending at clock 0.
func (l *ClientBlockList) FindPivot(mc *meter.Meter, clock uint64) (int, bool) {
	mc.Enter()
	defer mc.Leave()

	if len(l.list) == 0 {
		return 0, false
	}
	i := meter.Search(mc, len(l.list), func(i int) bool {
		return l.list[i].ClockEnd(mc) >= clock
	})
	if i == len(l.list) || l.list[i].ClockStart(mc) > clock {
		return 0, false
	}
	return i, true
}

// Push appends a cell to the list. Caller must guarantee
// cell.ClockStart() == l.Clock() to preserve invariant 1; in tests,
// follow with CheckInvariants.
//
// Mirrors yrs ClientBlockList::push (block_store.rs:84-87).
func (l *ClientBlockList) Push(mc *meter.Meter, cell BlockCell) {
	mc.Enter()
	defer mc.Leave()

	l.list = meter.Append(mc, l.list, cell)
}

// Insert places cell at the given index, shifting later cells right.
// Used by split_block and materialize for the new right-half cell;
// caller must compute index via FindPivot and validate that the insert
// keeps invariants 1-3.
//
// Mirrors yrs ClientBlockList::insert (block_store.rs:89-93).
func (l *ClientBlockList) Insert(mc *meter.Meter, index int, cell BlockCell) {
	mc.Enter()
	defer mc.Leave()

	l.list = meter.Append(mc, l.list, BlockCell{})
	copy(l.list[index+1:], l.list[index:])
	l.list[index] = cell
}

// SquashFrom merges adjacent same-client item cells at and after
// startIdx into their left neighbour wherever Item.TrySquash succeeds,
// dropping the absorbed cell. Returns the number of cells removed.
// startIdx is clamped to >= 1 (index 0 has no left neighbour).
//
// This is the commit-time block-squash primitive: it collapses runs of
// same-client adjacent-clock items (the classic per-character
// Text.Insert pattern) into single items, matching yjs's on-commit
// merging and removing the V1 per-item wire overhead. Clock density is
// preserved because TrySquash extends the surviving cell to cover the
// absorbed cell's clocks.
func (l *ClientBlockList) SquashFrom(mc *meter.Meter, startIdx int) int {
	mc.Enter()
	defer mc.Leave()

	if startIdx < 1 {
		startIdx = 1
	}
	removed := 0
	for i := startIdx; i >= 1 && i < len(l.list); {
		mc.Step()

		prev := l.list[i-1]
		cur := l.list[i]
		switch {
		case prev.Kind == CellKindItem && cur.Kind == CellKindItem &&
			prev.Item != nil && cur.Item != nil &&
			prev.Item.TrySquash(mc, cur.Item):
			// cur merged into prev; its Item is now detached from the
			// list. Any search marker pointing at the absorbed item is
			// stale, so invalidate the branch's marker cache. This is
			// cheap and does not regress positional walks: after squash
			// the branch holds far fewer items, so a marker-less walk is
			// already O(items).
			if br := prev.Item.Parent.Branch; br != nil && br.Markers != nil {
				br.Markers.Invalidate(mc)
			}
			l.list = meter.Append(mc, l.list[:i], l.list[i+1:]...)
			removed++
		case prev.Kind == CellKindGC && cur.Kind == CellKindGC:
			// Collapse two adjacent garbage-collected runs into one,
			// matching yjs's merge of contiguous GC structs. This is the
			// run that forms when a deleted shared type's children are
			// each replaced with a GC cell (see ReplaceWithGC).
			l.list[i-1] = CellOfGC(mc, prev.GC.Start, cur.GC.End)
			l.list = meter.Append(mc, l.list[:i], l.list[i+1:]...)
			removed++
		default:
			i++
		}
	}
	return removed
}

// MergeWithLefts merges the cell at pos into its left neighbour, and the
// result into its own left neighbour, while that succeeds (yjs
// tryToMergeWithLefts). Returns the number of merges.
func (l *ClientBlockList) MergeWithLefts(mc *meter.Meter, pos int) int {
	mc.Enter()
	defer mc.Leave()

	merged := 0
	for i := pos; i >= 1 && i < len(l.list); i-- {
		mc.Step()

		prev, cur := l.list[i-1], l.list[i]
		switch {
		case prev.Kind == CellKindItem && cur.Kind == CellKindItem &&
			prev.Item != nil && cur.Item != nil && prev.Item.TrySquash(mc, cur.Item):
			if br := prev.Item.Parent.Branch; br != nil && br.Markers != nil {
				br.Markers.Invalidate(mc)
			}
		case prev.Kind == CellKindGC && cur.Kind == CellKindGC:
			l.list[i-1] = CellOfGC(mc, prev.GC.Start, cur.GC.End)
		default:
			return merged
		}
		l.list = meter.Append(mc, l.list[:i], l.list[i+1:]...)
		merged++
	}
	return merged
}

// ReplaceWithGC converts the cell at index i into a GC cell covering the
// same clock range, discarding the live Item. No-op if i is out of range
// or the cell is already a GC cell.
//
// Used by nested-type collection: when a shared type is deleted, each of
// its children collapses to a garbage-collected range (yjs's parentGCd
// path in ContentType.gc), encoded as a ref-0 GC struct. Adjacent GC
// cells produced this way merge into a single run via SquashFrom.
func (l *ClientBlockList) ReplaceWithGC(mc *meter.Meter, i int) {
	mc.Enter()
	defer mc.Leave()

	if i < 0 || i >= len(l.list) {
		return
	}
	c := l.list[i]
	if c.Kind == CellKindGC {
		return
	}
	l.list[i] = CellOfGC(mc, c.ClockStart(mc), c.ClockEnd(mc))
}

// CheckInvariants validates structural invariants 1-3 across the list.
// Returns the first error found, or nil if everything is consistent.
//
// Test-only utility; production code should not call this on the hot
// path. Recommended pattern: defer l.CheckInvariants() in any test that
// performs Push / Insert / SquashLeft to catch regressions immediately.
func (l *ClientBlockList) CheckInvariants(mc *meter.Meter) error {
	mc.Enter()
	defer mc.Leave()

	var nextClock uint64 = 0
	for i, c := range l.list {
		mc.Step()

		if c.ClockEnd(mc) < c.ClockStart(mc) {
			return meter.Errorf(mc, "cell %d: inverted range [%d, %d]", i, c.ClockStart(mc), c.ClockEnd(mc))
		}
		if c.ClockStart(mc) != nextClock {
			return meter.Errorf(mc, "cell %d: gap or overlap (expected start %d, got %d)", i, nextClock, c.ClockStart(mc))
		}
		nextClock = c.ClockEnd(mc) + 1
	}
	return nil
}
