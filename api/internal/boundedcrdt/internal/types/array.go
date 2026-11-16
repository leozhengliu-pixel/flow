package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/anyval"
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
)

// Array is the user-facing wrapper around a positional Branch. The
// branch's Start linked list holds the document-order sequence; we
// count live (non-deleted, countable) items to translate user-facing
// positions to *Item references.
//
// Construct via NewArray; usually obtained as
// types.NewArray(d.Branch("name")).
//
// Array shares its underlying Branch with Map's possible usage of
// the same name — Map uses Branch.Map (string-keyed), Array uses
// Branch.Start (positional). The two surfaces operate on disjoint
// fields of the same Branch and may technically coexist, though in
// practice each root branch is used as one type.
type Array struct {
	branch *block.Branch
}

// NewArray wraps the given branch as an Array.
func NewArray(mc *meter.Meter, branch *block.Branch) *Array {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, Array{branch: branch})
}

// Branch returns the underlying *block.Branch for low-level access.
func (a *Array) Branch(mc *meter.Meter) *block.Branch {
	mc.Enter()
	defer mc.Leave()
	return a.branch
}

// Len returns the number of live elements. O(1) — reads
// branch.BlockLen, which Item.Integrate (+countable.Len) and
// TransactionMut.Delete (-countable.Len) maintain.
//
// Per types-array.md finding 5: Map keys are excluded from
// BlockLen; the counter only tracks positional, countable, live
// items.
func (a *Array) Len(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return a.branch.BlockLen
}

// Insert inserts a single value at idx. Convenience for
// InsertRange with a one-element slice.
//
// idx must be in [0, Len]; out-of-range inserts append at the end
// (matching JS Y.Array.insert tolerant behaviour).
func (a *Array) Insert(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, value any) {
	mc.Enter()
	defer mc.Leave()

	a.InsertRange(mc, txn, idx, meter.Literal[[]any](mc, 144, func() []any {
		return []any{value}
	}))
}

// Push appends one or more values to the end of the array: after the
// last item of the list, deleted or not, as yjs typeListPushGenerics
// does. InsertRange at Len() puts them right after the last live
// element instead, before any trailing tombstones, as yjs insert(length)
// does; the difference shows when a tombstone at the end comes back
// through undo or meets a concurrent insert.
func (a *Array) Push(mc *meter.Meter, txn *doc.TransactionMut, values ...any) {
	mc.Enter()
	defer mc.Leave()

	if len(values) == 0 {
		return
	}
	last, _ := startFromMarker(mc, a.branch, a.Len(mc))
	if last == nil {
		last = a.branch.Start
	}
	for last != nil && last.Right != nil {
		mc.Step()

		last = last.Right
	}
	a.insertBetween(mc, txn, a.Len(mc), last, nil, values)
}

// InsertRange inserts every value at idx in order, packed into a
// single ContentAny Item with Len == len(values). Per types-array.md
// finding 3, this matches yrs's RangePrelim squash-on-insert.
//
// Mid-block inserts trigger Store.SplitBlock to materialize a clean
// boundary at idx.
func (a *Array) InsertRange(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, values []any) {
	mc.Enter()
	defer mc.Leave()

	if len(values) == 0 {
		return
	}

	left, right := findInsertPosition(mc, a.branch, txn, idx)
	a.insertBetween(mc, txn, idx, left, right, values)
}

// validValues returns values with anyval.Valid applied to each, copying the
// slice only when one of them changes, so the caller's slice is not modified.
func validValues(mc *meter.Meter, values []any) []any {
	mc.Enter()
	defer mc.Leave()

	var out []any
	for i, v := range values {
		mc.Step()

		if anyval.NeedsFix(mc, v) {
			if out == nil {
				out = meter.Append(mc, []any(nil), values...)
			}
			out[i] = anyval.Valid(mc, v)
		}
	}
	if out == nil {
		return values
	}
	return out
}

// insertBetween integrates values as one item between left and right,
// at user index idx (for the search markers).
func (a *Array) insertBetween(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, left, right *block.Item, values []any) {
	mc.Enter()
	defer mc.Leave()

	values = validValues(mc, values)
	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}
	if right != nil {
		// Per types-array.md finding 4: array Items carry
		// right_origin = right.ID (Map sets it nil).
		rid := right.ID
		rightOrigin = meter.Pointer(mc, rid)
	}

	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)

	anys := meter.MakeSlice[[]block.Any, block.Any](mc, uint64(len(values)), uint64(len(values)))
	copy(anys, values)

	item := meter.Pointer(mc, block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         uint64(len(values)),
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     block.Content{Kind: block.KindAny, Anys: anys},
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: a.branch},
		// ParentSub: nil — positional, not map-key.
		Flags: block.FlagCountable,
	})

	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
		return
	}
	// Shift markers past the insertion point so they keep pointing
	// at the right user-facing index.
	a.branch.Markers.ShiftAfter(mc, idx, item.Len)
	// Remember the just-inserted item so the next call near idx
	// short-circuits the walk.
	rememberMarker(mc, a.branch, item, idx)
}

// Get returns the value at idx, or nil if idx is out of range or
// the underlying content kind is not unpacked by extractValueAt.
//
// O(N) over the linked list. Search markers help only when callers
// alternate Get with Insert/Delete near the same area; pure Get
// loops still walk from start (no marker write occurs here).
func (a *Array) Get(mc *meter.Meter, idx uint64) any {
	mc.Enter()
	defer mc.Leave()

	var counted uint64 = 0
	for cur := a.branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			continue
		}
		if counted+cur.Len > idx {
			offset := idx - counted
			return extractValueAt(mc, cur.Content, offset)
		}
		counted += cur.Len
	}
	return nil
}

// Delete removes length elements starting at idx. Items that
// straddle the [idx, idx+length) range are split via SplitBlock so
// the deletion lands on Item boundaries.
//
// length == 0 is a no-op. Out-of-range deletions clip to Len().
func (a *Array) Delete(mc *meter.Meter, txn *doc.TransactionMut, idx, length uint64) {
	mc.Enter()
	defer mc.Leave()

	if length == 0 {
		return
	}
	end := idx + length
	if end > a.Len(mc) {
		end = a.Len(mc)
	}
	if idx >= end {
		return
	}
	defer a.branch.Markers.ShrinkAfter(mc, idx, end-idx)

	var counted uint64 = 0
	cur := a.branch.Start
	for cur != nil && counted < end {
		mc.Step()

		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			cur = cur.Right
			continue
		}

		blockStart := counted
		blockEnd := counted + cur.Len

		if blockEnd <= idx {
			counted = blockEnd
			cur = cur.Right
			continue
		}
		if blockStart >= end {
			break
		}

		// Block overlaps the delete range. Split at start boundary
		// if needed.
		if blockStart < idx {
			right := txn.Store(mc).SplitBlock(mc, cur, idx-blockStart)
			if right == nil {
				// Should not happen for valid offset; guard anyway.
				cur = cur.Right
				continue
			}
			counted = idx
			cur = right
			blockStart = counted
			blockEnd = counted + cur.Len
		}

		// Split at end boundary if the block extends past `end`.
		if blockEnd > end {
			if right := txn.Store(mc).SplitBlock(mc, cur, end-blockStart); right == nil {
				// Defensive.
				return
			}
			blockEnd = end
		}

		// Now [blockStart, blockEnd) ⊆ [idx, end). Delete cur and
		// advance.
		next := cur.Right
		txn.Delete(mc, cur)
		counted = blockEnd
		cur = next
	}
}

// Range visits live elements in document order. The callback returns
// false to stop early. For ContentAny items with Len > 1 (squashed
// runs from InsertRange / Push) each element is yielded individually.
func (a *Array) Range(mc *meter.Meter, fn func(idx uint64, value any) bool) {
	mc.Enter()
	defer mc.Leave()

	var idx uint64
	for cur := a.branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			continue
		}
		for offset := uint64(0); offset < cur.Len; offset++ {
			mc.Step()

			v := extractValueAt(mc, cur.Content, offset)
			if !fn(idx, v) {
				return
			}
			idx++
		}
	}
}

// ToSlice materializes the array into a fresh []any. Convenience
// for tests; production callers should prefer Range to avoid the
// allocation.
func (a *Array) ToSlice(mc *meter.Meter) []any {
	mc.Enter()
	defer mc.Leave()

	out := meter.MakeSlice[[]any, any](mc, uint64(0), uint64(a.Len(mc)))
	a.Range(mc, func(_ uint64, v any) bool {
		out = meter.Append(mc, out, v)
		return true
	})
	return out
}

// findInsertPosition resolves the user-facing index to the (left,
// right) neighbour pair that the YATA integrate algorithm needs.
//
// Walks branch.Start counting live, countable items. If idx lands
// mid-block, calls Store.SplitBlock to materialize a clean boundary
// and returns the (truncated-left, new-right) halves.
//
// Edge cases:
//   - idx == 0: left = nil, right = branch.Start (which may itself
//     be a deleted item; YATA preserves links through tombstones).
//   - idx == Len(): left = last item in linked list (live or
//     deleted, since YATA needs the immediate-list neighbour, not
//     the last live one), right = nil.
//   - idx > Len(): clipped to Len() (treated as append).
//
// Per types-array.md finding 2: this is the eager-split flavour —
// we split immediately rather than deferring via a rel cursor.
//
// Search-marker fast path: if branch.Markers has a marker whose
// Index is closer to idx than the linear-from-start walk would
// be, start the walk from that marker instead. Successful walks
// add a fresh marker at the landing position to amortize the
// cost across subsequent calls near the same area (the B4 trace
// motivator).
func findInsertPosition(mc *meter.Meter, branch *block.Branch, txn *doc.TransactionMut, idx uint64) (*block.Item, *block.Item) {
	mc.Enter()
	defer mc.Leave()

	if idx == 0 {
		return nil, branch.Start
	}

	startItem, startIdx := startFromMarker(mc, branch, idx)

	counted := startIdx
	lastSeen := startItem
	cur := startItem
	if cur == nil {
		cur = branch.Start
	}
	// Marker boundary case: marker said "cur starts at counted" and
	// caller wants idx == counted. Without this guard the walk falls into
	// the `counted+cur.Len > idx` branch and tries to split cur at offset
	// 0, which corrupts the linked list. The new item goes right after the
	// last live element before cur, ahead of any tombstones, where a walk
	// from the start puts it and where yjs typeListInsertGenerics puts it
	// (n = n.prev, the previous undeleted item).
	if cur != nil && counted == idx {
		p := cur.Left
		for p != nil && p.IsDeleted(mc) {
			mc.Step()

			p = p.Left
		}
		if p == nil {
			return nil, branch.Start
		}
		return p, p.Right
	}
	for ; cur != nil; cur = cur.Right {
		mc.Step()

		lastSeen = cur
		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			continue
		}
		if counted+cur.Len == idx {
			rememberMarker(mc, branch, cur.Right, idx)
			return cur, cur.Right
		}
		if counted+cur.Len > idx {
			splitOffset := idx - counted
			right := txn.Store(mc).SplitBlock(mc, cur, splitOffset)
			if right == nil {
				return cur, cur.Right
			}
			rememberMarker(mc, branch, right, idx)
			return cur, right
		}
		counted += cur.Len
	}

	return lastSeen, nil
}

// startFromMarker picks the best starting point for a position
// walk: either the nearest marker (if it's closer to idx than
// branch.Start) or branch.Start itself. Returns the starting Item
// and its known starting index.
//
// Backward walks (target idx < marker.Index) are skipped — the
// shared types' walk loops are forward-only (cur = cur.Right).
// Reverse-direction walking would need a separate Left-traversal
// path that the current callers don't have; an opportunistic
// future enhancement.
func startFromMarker(mc *meter.Meter, branch *block.Branch, idx uint64) (*block.Item, uint64) {
	mc.Enter()
	defer mc.Leave()

	if branch.Markers == nil {
		return nil, 0
	}
	mk := branch.Markers.Nearest(mc, idx)
	if mk == nil || mk.Item == nil {
		return nil, 0
	}
	// Forward-only: marker.Index must be <= idx and closer than 0.
	if mk.Index > idx || mk.Index == 0 {
		return nil, 0
	}
	branch.Markers.Touch(mc, mk)
	return mk.Item, mk.Index
}

// rememberMarker records the (item, idx) pair so the next call
// with a target near idx can short-circuit the walk. Allocates
// branch.Markers lazily.
func rememberMarker(mc *meter.Meter, branch *block.Branch, item *block.Item, idx uint64) {
	mc.Enter()
	defer mc.Leave()

	if item == nil || idx == 0 {
		return
	}
	if branch.Markers == nil {
		branch.Markers = block.NewMarkerList(mc)
	}
	branch.Markers.Add(mc, item, idx)
}

// extractValueAt returns the offset-th element of c. For
// ContentAny (the variant we emit from Insert/InsertRange) this is
// Anys[offset]. ContentString uses byte indexing as a placeholder
// pending the Text type's UTF-16 storage decision; ContentBinary
// returns the whole []byte at offset 0 (it's a single-element
// variant). ContentType returns the wrapped nested *Map/*Array/*Text
// at offset 0 (Len == 1 always for KindType items). Other kinds
// return nil.
func extractValueAt(mc *meter.Meter, c block.Content, offset uint64) any {
	mc.Enter()
	defer mc.Leave()

	switch c.Kind {
	case block.KindAny:
		if offset < uint64(len(c.Anys)) {
			return c.Anys[offset]
		}
	case block.KindString:
		if offset < uint64(len(c.Str)) {
			return meter.ToString(mc, c.Str[offset])
		}
	case block.KindBinary:
		if offset == 0 {
			return c.Bytes
		}
	case block.KindType:
		if offset != 0 || c.Branch == nil {
			return nil
		}
		switch c.Branch.TypeRef {
		case block.TypeRefMap:
			return NewMap(mc, c.Branch)
		case block.TypeRefArray:
			return NewArray(mc, c.Branch)
		case block.TypeRefText:
			return NewText(mc, c.Branch)
		}
	}
	return nil
}

// InsertMap inserts a freshly-constructed nested Map at idx and
// returns the wrapper. Per docs/yrs-port-notes/nested-types.md §6.
//
// Like InsertRange, idx is clamped to [0, Len]; the new Map element
// occupies one slot (Len contribution = 1).
func (a *Array) InsertMap(mc *meter.Meter, txn *doc.TransactionMut, idx uint64) *Map {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{
		TypeRef: block.TypeRefMap,
		Map: meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		}),
	})
	a.insertNested(mc, txn, idx, inner)
	return meter.Pointer(mc, Map{branch: inner})
}

// InsertArray inserts a freshly-constructed nested Array at idx.
func (a *Array) InsertArray(mc *meter.Meter, txn *doc.TransactionMut, idx uint64) *Array {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefArray})
	a.insertNested(mc, txn, idx, inner)
	return meter.Pointer(mc, Array{branch: inner})
}

// InsertText inserts a freshly-constructed nested Text at idx
// (plain-text only).
func (a *Array) InsertText(mc *meter.Meter, txn *doc.TransactionMut, idx uint64) *Text {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefText})
	a.insertNested(mc, txn, idx, inner)
	return meter.Pointer(mc, Text{branch: inner})
}

// insertNested is the shared scaffolding for InsertMap/InsertArray/
// InsertText: build an Item with KindType content at position idx
// and integrate. Item.Integrate KindType arm wires Branch.Item back
// to the Item.
func (a *Array) insertNested(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, inner *block.Branch) {
	mc.Enter()
	defer mc.Leave()

	left, right := findInsertPosition(mc, a.branch, txn, idx)

	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}
	if right != nil {
		rid := right.ID
		rightOrigin = meter.Pointer(mc, rid)
	}

	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)

	item := meter.Pointer(mc, block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         1,
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     block.Content{Kind: block.KindType, Branch: inner},
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: a.branch},
		Flags:       block.FlagCountable,
	})

	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
	}
}
