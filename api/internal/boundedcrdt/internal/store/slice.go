package store

import "flow/api/internal/boundedcrdt/internal/meter"

import "flow/api/internal/boundedcrdt/internal/block"

// SliceKind discriminates BlockSlice variants. Mirrors yrs slice.rs.
type SliceKind uint8

const (
	SliceKindItem SliceKind = 0
	SliceKindGC   SliceKind = 1
)

// ItemSlice is a non-destructive sub-range view over an Item. Start and
// End are inclusive offsets within the underlying block (0..=Item.Len-1).
//
// A read-only computation; no mutation. To actually realize the slice
// boundaries as separate Items, callers must run Materialize on it,
// which splices the underlying block. See tech-debt.md and store.md
// "Edge cases".
type ItemSlice struct {
	Ptr   *block.Item
	Start uint64 // inclusive offset into [Ptr.ID.Clock, Ptr.ID.Clock+Ptr.Len)
	End   uint64 // inclusive offset; Start <= End <= Ptr.Len-1
}

// GCSlice is the GC-cell counterpart to ItemSlice. Fields are absolute
// clocks, not offsets, because GC cells have no notion of an
// underlying block.
type GCSlice struct {
	Start uint64 // inclusive
	End   uint64 // inclusive
}

// BlockSlice is a tagged union over ItemSlice and GCSlice.
type BlockSlice struct {
	Kind SliceKind
	Item ItemSlice
	GC   GCSlice
}

// Len returns the number of clocks the slice covers.
func (s ItemSlice) Len(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return s.End - s.Start + 1
}

// ClockStart returns the absolute clock of the first element in the slice.
func (s ItemSlice) ClockStart(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return s.Ptr.ID.Clock + s.Start
}

// ClockEnd returns the absolute clock of the last element in the slice.
func (s ItemSlice) ClockEnd(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return s.Ptr.ID.Clock + s.End
}

// AdjacentLeft reports whether the slice covers the leftmost element of
// the underlying block. Materialize uses this to skip a no-op left split.
func (s ItemSlice) AdjacentLeft(mc *meter.Meter) bool {
	mc.Enter()
	defer mc.Leave()
	return s.Start == 0
}

// AdjacentRight reports whether the slice covers the rightmost element
// of the underlying block. Materialize uses this to skip a no-op right
// split.
func (s ItemSlice) AdjacentRight(mc *meter.Meter) bool {
	mc.Enter()
	defer mc.Leave()
	return s.End == s.Ptr.Len-1
}

// Len returns the number of clocks the GC slice covers.
func (s GCSlice) Len(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return s.End - s.Start + 1
}
