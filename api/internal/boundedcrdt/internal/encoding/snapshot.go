package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/lib0"
	"flow/api/internal/boundedcrdt/internal/store"
)

// Snapshot is a point-in-time marker of a document's history: the set
// of deleted ID ranges (DS) plus the per-client clock heads (SV) as of
// the snapshot moment. Replaying the document up to SV, with DS applied
// as the deletions-so-far, reconstructs exactly the state the document
// had when the snapshot was taken.
//
// Mirrors yjs Snapshot (src/utils/Snapshot.js): a {ds, sv} pair. The
// wire form is writeDeleteSet(ds) followed by writeStateVector(sv).
type Snapshot struct {
	DS *IdSet
	SV store.StateVector
}

// CreateSnapshot captures the current delete set and state vector of
// the store. Mirrors yjs's `snapshot(doc)`.
//
// For snapshots to be useful for time-travel the doc should be created
// with GC disabled, otherwise deleted content the snapshot references
// may have been collected. The capture itself does not require it.
func CreateSnapshot(mc *meter.Meter, bs *store.BlockStore) Snapshot {
	mc.Enter()
	defer mc.Leave()

	sv := bs.GetStateVector(mc)
	ds := buildDeleteSetFromStore(mc, bs, sv)
	return Snapshot{DS: ds, SV: sv}
}

// EqualSnapshots reports whether two snapshots carry the same delete
// set and state vector. Mirrors yjs equalSnapshots.
func EqualSnapshots(mc *meter.Meter, a, b Snapshot) bool {
	mc.Enter()
	defer mc.Leave()

	if len(a.SV) != len(b.SV) {
		return false
	}
	for c, clk := range a.SV {
		mc.Step()

		if b.SV[c] != clk {
			return false
		}
	}
	ac, bc := a.DS.clients, b.DS.clients
	if len(ac) != len(bc) {
		return false
	}
	for c, ar := range ac {
		mc.Step()

		br, ok := bc[c]
		if !ok || len(ar) != len(br) {
			return false
		}
		for i := range ar {
			mc.Step()

			if ar[i] != br[i] {
				return false
			}
		}
	}
	return true
}

// EncodeSnapshot returns the V1 wire encoding of s: the delete set
// followed by the state vector. Byte-compatible with yjs
// `encodeSnapshot` (DSEncoderV1).
func EncodeSnapshot(mc *meter.Meter, s Snapshot) []byte {
	mc.Enter()
	defer mc.Leave()

	var buf []byte
	buf = s.DS.Encode(mc, buf) // V1 delete set, descending client order
	buf = EncodeStateVector(mc, s.SV, buf)
	return buf
}

// DecodeSnapshot parses a V1 snapshot produced by EncodeSnapshot or
// yjs `encodeSnapshot`.
func DecodeSnapshot(mc *meter.Meter, buf []byte) (Snapshot, error) {
	mc.Enter()
	defer mc.Leave()

	ds, rest, err := DecodeIdSet(mc, buf)
	if err != nil {
		return Snapshot{}, err
	}
	sv, _, err := DecodeStateVector(mc, rest)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{DS: ds, SV: sv}, nil
}

// SplitAtSnapshotBoundaries forces a clean cell boundary at each
// client's snapshot clock, so EncodeSnapshotAsUpdateV1 can emit exactly
// the clocks [0, snapshotClock) without including content created after
// the snapshot. Mirrors yjs's getItemCleanStart pass in
// createDocFromSnapshot. Mutates the store (splitting is semantically
// transparent), so the caller must hold a write transaction.
func SplitAtSnapshotBoundaries(mc *meter.Meter, bs *store.BlockStore, snap Snapshot) {
	mc.Enter()
	defer mc.Leave()

	for c, clock := range snap.SV {
		mc.Step()

		if clock == 0 {
			continue
		}
		cell, ok := bs.GetBlock(mc, block.ID{Client: c, Clock: clock})
		if ok && cell.Kind == store.CellKindItem && cell.Item.ID.Clock < clock {
			bs.SplitBlock(mc, cell.Item, clock-cell.Item.ID.Clock)
		}
	}
}

// EncodeSnapshotAsUpdateV1 builds a V1 update that contains, for each
// client, only the cells with clock strictly below the snapshot's clock
// for that client, plus the snapshot's delete set. Applying this update
// to a fresh document reconstructs the exact state captured by the
// snapshot.
//
// Call SplitAtSnapshotBoundaries first so no cell straddles a snapshot
// boundary. The wire shape matches EncodeDiff (per-client runs in
// descending client order, then the delete set).
func EncodeSnapshotAsUpdateV1(mc *meter.Meter, bs *store.BlockStore, snap Snapshot) []byte {
	mc.Enter()
	defer mc.Leave()

	type run struct {
		client uint64
		cells  []store.BlockCell
	}
	clients := meter.MakeSlice[[]uint64, uint64](mc, uint64(0), uint64(len(snap.SV)))
	for c, clock := range snap.SV {
		mc.Step()

		if clock > 0 {
			clients = meter.Append(mc, clients, c)
		}
	}
	meter.Slice(mc, clients, func(i, j int) bool { return clients[i] > clients[j] })

	var runs []run
	for _, c := range clients {
		mc.Step()

		clock := snap.SV[c]
		list := bs.GetClient(mc, c)
		if list == nil {
			continue
		}
		var cells []store.BlockCell
		for i := 0; i < list.Len(mc); i++ {
			mc.Step()

			cell, _ := list.Get(mc, i)
			if cell.ClockEnd(mc) < clock {
				cells = meter.Append(mc, cells, cell)
			}
		}
		if len(cells) > 0 {
			runs = meter.Append(mc, runs, run{client: c, cells: cells})
		}
	}

	buf := lib0.WriteVarUint(mc, nil, uint64(len(runs)))
	for _, r := range runs {
		mc.Step()

		buf = lib0.WriteVarUint(mc, buf, uint64(len(r.cells)))
		buf = lib0.WriteVarUint(mc, buf, r.client)
		buf = lib0.WriteVarUint(mc, buf, r.cells[0].ClockStart(mc))
		for _, cell := range r.cells {
			mc.Step()

			buf = encodeCell(mc, buf, cell)
		}
	}
	buf = snap.DS.Encode(mc, buf) // V1 delete set, descending client order
	return buf
}
