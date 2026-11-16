package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/lib0"
)

// Range is a half-open clock interval [Start, Start+Length). The wire
// form encodes (Start, Length), not (Start, End). yrs's in-memory
// Range<u32> is half-open; the wire form is unambiguous.
//
// Per docs/yrs-port-notes/update-v1.md gotcha 2.
type Range struct {
	Start  uint64
	Length uint64
}

// End returns the (exclusive) upper bound of the range.
func (r Range) End(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return r.Start + r.Length
}

// IdSet is a per-client set of half-open clock ranges. Used as the V1
// wire DeleteSet. Insert auto-merges overlapping or adjacent ranges so
// the per-client list stays sorted and non-overlapping at all times.
//
// Mirrors yrs IdSet (id_set.rs:121-135), simplified — yrs has a
// generic IdMapInner<V> with a () value type for the wire DeleteSet
// case; we just store []Range directly.
type IdSet struct {
	clients map[uint64][]Range
	// order lists the clients in the order they were first added: for a
	// decoded set, the order they were written in, which is the order
	// yjs readAndApplyDeleteSet applies an update's delete set in.
	order []uint64
}

// NewIdSet returns an empty IdSet.
func NewIdSet(mc *meter.Meter) *IdSet {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, IdSet{clients: meter.Literal[map[uint64][]Range](mc, 128, func() map[uint64][]Range {
		return map[uint64][]Range{}
	})})
}

// Insert adds the half-open range [start, start+length) for client.
// Overlapping or adjacent existing ranges are merged.
//
// Mirrors yrs IdRanges::insert (id_set.rs:34-90 area).
func (s *IdSet) Insert(mc *meter.Meter, client, start, length uint64) {
	mc.Enter()
	defer mc.Leave()

	if length == 0 {
		return
	}
	end := start + length
	existing, known := s.clients[client]
	if !known {
		s.order = meter.Append(mc, s.order, client)
	}

	// Find the first existing range that ends at or after start
	// (i.e. its End() >= start). That's the leftmost candidate for
	// a merge. Use sort.Search on the predicate so we can locate
	// the merge window in one binary search.
	i := meter.Search(mc, len(existing), func(i int) bool {
		return existing[i].End(mc) >= start
	})

	// Sweep right collecting every range that starts at or before
	// our (possibly extended) end. Each collected range extends our
	// merged span to max(end, existing.End()) and drops our merged
	// start to min(start, existing.Start).
	j := i
	for j < len(existing) && existing[j].Start <= end {
		mc.Step()

		if existing[j].End(mc) > end {
			end = existing[j].End(mc)
		}
		if existing[j].Start < start {
			start = existing[j].Start
		}
		j++
	}

	merged := Range{Start: start, Length: end - start}
	switch {
	case i == j:
		// No overlap; insert at i.
		existing = meter.Append(mc, existing, Range{})
		copy(existing[i+1:], existing[i:])
		existing[i] = merged
	case j == i+1:
		// Single existing range absorbed; replace in place.
		existing[i] = merged
	default:
		// Multiple existing ranges absorbed; collapse [i, j) into one.
		existing[i] = merged
		copy(existing[i+1:], existing[j:])
		existing = existing[:len(existing)-(j-i-1)]
	}
	meter.Set(mc, s.clients, client, existing)
}

// Contains reports whether the (client, clock) pair is covered by
// some range in the set.
func (s *IdSet) Contains(mc *meter.Meter, client, clock uint64) bool {
	mc.Enter()
	defer mc.Leave()

	ranges, ok := s.clients[client]
	if !ok {
		return false
	}
	i := meter.Search(mc, len(ranges), func(i int) bool {
		return ranges[i].End(mc) > clock
	})
	return i < len(ranges) && ranges[i].Start <= clock
}

// Iterate calls fn once per client, in ascending client ID order.
// The ranges slice handed to fn must NOT be retained or mutated; it
// aliases internal state.
func (s *IdSet) Iterate(mc *meter.Meter, fn func(client uint64, ranges []Range)) {
	mc.Enter()
	defer mc.Leave()

	clients := meter.MakeSlice[[]uint64, uint64](mc, uint64(0), uint64(len(s.clients)))
	for c := range s.clients {
		mc.Step()

		clients = meter.Append(mc, clients, c)
	}
	meter.Slice(mc, clients, func(i, j int) bool { return clients[i] < clients[j] })
	for _, c := range clients {
		mc.Step()

		fn(c, s.clients[c])
	}
}

// ClientsInOrder returns the clients in the order they were first added
// (for a decoded set, the order they were written in). The slice is new.
func (s *IdSet) ClientsInOrder(mc *meter.Meter) []uint64 {
	mc.Enter()
	defer mc.Leave()

	return meter.Append(mc, []uint64(nil), s.order...)
}

// ClientRanges returns client's ranges, sorted and merged. The slice
// aliases internal state and must not be retained or mutated.
func (s *IdSet) ClientRanges(mc *meter.Meter, client uint64) []Range {
	mc.Enter()
	defer mc.Leave()
	return s.clients[client]
}

// ClientCount returns the number of distinct clients with at least
// one range in the set.
func (s *IdSet) ClientCount(mc *meter.Meter) int {
	mc.Enter()
	defer mc.Leave()
	return len(s.clients)
}

// Encode appends the V1 wire encoding of s to buf and returns the
// extended slice. Wire layout:
//
//	varuint clientCount
//	clientCount × (
//	    varuint clientID
//	    varuint rangeCount
//	    rangeCount × (varuint start, varuint length)
//	)
//
// Clients are emitted in DESCENDING order, matching the V1 delete-set
// wire form yjs produces (writeDeleteSet sorts "(a, b) => b[0] -
// a[0]"). Ranges within a client are already sorted by Insert. Note
// this differs from Iterate, which is ascending for general-purpose
// deterministic traversal; the wire form specifically tracks yjs, our
// byte-compatibility reference, not yrs (whose BTreeMap emits
// ascending). Single-client sets encode identically either way, which
// is why the difference stayed latent until multi-client snapshots.
func (s *IdSet) Encode(mc *meter.Meter, buf []byte) []byte {
	mc.Enter()
	defer mc.Leave()

	clients := meter.MakeSlice[[]uint64, uint64](mc, uint64(0), uint64(len(s.clients)))
	for c := range s.clients {
		mc.Step()

		clients = meter.Append(mc, clients, c)
	}
	meter.Slice(mc, clients, func(i, j int) bool { return clients[i] > clients[j] })

	buf = lib0.WriteVarUint(mc, buf, uint64(len(clients)))
	for _, c := range clients {
		mc.Step()

		ranges := s.clients[c]
		buf = lib0.WriteVarUint(mc, buf, c)
		buf = lib0.WriteVarUint(mc, buf, uint64(len(ranges)))
		for _, r := range ranges {
			mc.Step()

			buf = lib0.WriteVarUint(mc, buf, r.Start)
			buf = lib0.WriteVarUint(mc, buf, r.Length)
		}
	}
	return buf
}

// DecodeIdSet parses a V1 wire-encoded IdSet from buf and returns
// the IdSet plus the unconsumed tail.
func DecodeIdSet(mc *meter.Meter, buf []byte) (*IdSet, []byte, error) {
	mc.Enter()
	defer mc.Leave()

	clientCount, n, err := lib0.ReadVarUint(mc, buf)
	if err != nil {
		return nil, buf, err
	}
	buf = buf[n:]
	if err := checkDecodeCount(mc, clientCount, len(buf)); err != nil {
		return nil, buf, err
	}

	s := NewIdSet(mc)
	for i := uint64(0); i < clientCount; i++ {
		mc.Step()

		clientID, n, err := lib0.ReadVarUint(mc, buf)
		if err != nil {
			return nil, buf, err
		}
		buf = buf[n:]

		rangeCount, n, err := lib0.ReadVarUint(mc, buf)
		if err != nil {
			return nil, buf, err
		}
		buf = buf[n:]

		if err := checkDecodeCount(mc, rangeCount, len(buf)); err != nil {
			return nil, buf, err
		}
		ranges := meter.MakeSlice[[]Range, Range](mc, uint64(rangeCount), uint64(rangeCount))
		for j := uint64(0); j < rangeCount; j++ {
			mc.Step()

			start, n, err := lib0.ReadVarUint(mc, buf)
			if err != nil {
				return nil, buf, err
			}
			buf = buf[n:]
			length, n, err := lib0.ReadVarUint(mc, buf)
			if err != nil {
				return nil, buf, err
			}
			buf = buf[n:]
			if err := checkClockRange(mc, start, length); err != nil {
				return nil, buf, err
			}
			ranges[j] = Range{Start: start, Length: length}
		}
		if len(ranges) == 0 {
			continue // yjs readDeleteSet keeps no entry for a client with no ranges
		}
		if _, known := s.clients[clientID]; known {
			// A client written twice keeps both entries' ranges, as yjs
			// readDeleteSet and readAndApplyDeleteSet read every entry.
			for _, r := range ranges {
				mc.Step()

				s.Insert(mc, clientID, r.Start, r.Length)
			}
			continue
		}
		s.order = meter.Append(mc, s.order, clientID)
		meter.Set(mc, s.clients, clientID, ranges)
	}
	return s, buf, nil
}
