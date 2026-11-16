package store

import "flow/api/internal/boundedcrdt/internal/meter"

import "flow/api/internal/boundedcrdt/internal/block"

// CellKind discriminates BlockCell variants. Mirrors yrs BlockCell enum.
type CellKind uint8

const (
	// CellKindGC is a tombstone range whose content has been collected.
	// The ID range stays valid so that other items pointing at it via
	// Origin/RightOrigin continue to resolve.
	CellKindGC CellKind = 0
	// CellKindItem is a live or tombstoned-but-content-preserved item.
	CellKindItem CellKind = 1
)

// GC is a garbage-collected ID range. Both Start and End are inclusive
// (matching yrs BlockCell::clock_range invariant 3 in store.md).
type GC struct {
	Start uint64
	End   uint64 // inclusive
}

// Len returns the number of clocks this GC range covers.
func (g GC) Len(mc *meter.Meter) uint64 { mc.Enter(); defer mc.Leave(); return g.End - g.Start + 1 }

// BlockCell is the unit a ClientBlockList holds. It carries either a
// pinned *Item (for live or soft-deleted items) or an inline GC range.
//
// Single struct + discriminator instead of an interface: keeps the
// ClientBlockList slice cheap to shift on insert and avoids interface
// dispatch on the FindPivot binary search hot path.
type BlockCell struct {
	Kind CellKind
	GC   GC          // valid when Kind == CellKindGC
	Item *block.Item // valid when Kind == CellKindItem; nil otherwise
}

// CellOfItem builds a BlockCell wrapping the given item. Convenience
// constructor for callers that produce items elsewhere.
func CellOfItem(mc *meter.Meter, it *block.Item) BlockCell {
	mc.Enter()
	defer mc.Leave()

	return BlockCell{Kind: CellKindItem, Item: it}
}

// CellOfGC builds a BlockCell wrapping the given GC range.
func CellOfGC(mc *meter.Meter, start, end uint64) BlockCell {
	mc.Enter()
	defer mc.Leave()

	return BlockCell{Kind: CellKindGC, GC: GC{Start: start, End: end}}
}

// ClockStart returns the inclusive lower bound of the cell's ID range.
func (c BlockCell) ClockStart(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == CellKindGC {
		return c.GC.Start
	}
	return c.Item.ID.Clock
}

// ClockEnd returns the inclusive upper bound of the cell's ID range.
func (c BlockCell) ClockEnd(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == CellKindGC {
		return c.GC.End
	}
	return c.Item.ID.Clock + c.Item.Len - 1
}

// Len returns the number of clocks this cell covers.
// Equivalent to ClockEnd - ClockStart + 1.
func (c BlockCell) Len(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == CellKindGC {
		return c.GC.Len(mc)
	}
	return c.Item.Len
}

// IsDeleted reports whether the cell represents a tombstoned range. GC
// cells are always considered deleted (they exist only because something
// was deleted and then collected); Item cells consult Item.Flags.
func (c BlockCell) IsDeleted(mc *meter.Meter) bool {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == CellKindGC {
		return true
	}
	return c.Item.IsDeleted(mc)
}

// AsItem returns the underlying *Item or nil if this is a GC cell.
// Provided so callers don't read c.Item directly without checking Kind.
func (c BlockCell) AsItem(mc *meter.Meter) *block.Item {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == CellKindItem {
		return c.Item
	}
	return nil
}
