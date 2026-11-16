package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/lib0"
	"flow/api/internal/boundedcrdt/internal/store"
)

// This file exists for one job: encoding a document together with whatever is
// still queued in its pending buffer, so that merging updates cannot quietly
// lose the half that has not integrated yet.
//
// EncodeStateAsUpdate deliberately does NOT do this. It answers "what is in
// this document", and a queued block is not in it. Merging answers a different
// question - "what did these updates carry" - and dropping the queued half
// loses data that was handed to us. The reference implementation draws the
// same line: yjs `mergeUpdates` preserves unintegrated structs and writes Skip
// runs for the gaps between them, so merging a lone delta whose ancestor is
// absent returns that delta rather than an empty update.
//
// Wire shape, checked against yjs 13.6.32 on the same scenario: one client run
// whose declared start clock is the first block emitted, then blocks in
// clock-ascending order, with `Skip` (ref 10) covering any clocks in between
// that nobody has. yjs emits `item, Skip(5), item`; so does this.

// mergeRecord is one emittable block, normalised so the V1 and V2 emitters can
// walk the same plan.
type mergeRecord struct {
	clock  uint64
	length uint64
	kind   BlockKind
	item   *block.Item // WireBlockItem only
}

// buildMergePlan orders every block the document can speak for - integrated
// and queued alike - by client and clock. Returns nil when nothing is queued,
// which tells the caller to use the ordinary encoder and keep the common path
// byte-identical to what the fixtures pin.
func buildMergePlan(mc *meter.Meter, bs *store.BlockStore, pending *Pending) ([]uint64, map[uint64][]mergeRecord) {
	mc.Enter()
	defer mc.Leave()

	if pending.IsEmpty(mc) {
		return nil, nil
	}
	byClient := meter.Literal[map[uint64][]mergeRecord](mc, 128, func() map[uint64][]mergeRecord {
		return map[uint64][]mergeRecord{}
	})

	for client := range bs.GetStateVector(mc) {
		mc.Step()

		list := bs.GetClient(mc, client)
		for i := 0; i < list.Len(mc); i++ {
			mc.Step()

			cell, ok := list.Get(mc, i)
			if !ok {
				continue
			}
			rec := mergeRecord{clock: cell.ClockStart(mc), length: cell.ClockEnd(mc) - cell.ClockStart(mc) + 1}
			switch cell.Kind {
			case store.CellKindItem:
				rec.kind, rec.item = WireBlockItem, cell.Item
			case store.CellKindGC:
				rec.kind = WireBlockGC
			default:
				continue
			}
			meter.Set(mc, byClient, client, meter.Append(mc, byClient[client], rec))
		}
	}

	for client, blocks := range pending.Blocks {
		mc.Step()

		known := bs.GetClock(mc, client)
		for _, b := range blocks {
			mc.Step()

			rec := mergeRecord{kind: b.Kind, item: b.Item}
			switch b.Kind {
			case WireBlockItem:
				if b.Item == nil {
					continue
				}
				rec.clock, rec.length = b.Item.ID.Clock, b.Item.Len
			case WireBlockGC, WireBlockSkip:
				rec.clock, rec.length = b.ID.Clock, b.Len
			default:
				continue
			}
			// Below the store clock the block is already represented by the
			// integrated records above, or superseded by them. Emitting it as
			// well would overlap the run and corrupt the receiver's list.
			if rec.length == 0 || rec.clock < known {
				continue
			}
			meter.Set(mc, byClient, client, meter.Append(mc, byClient[client], rec))
		}
	}

	clients := meter.MakeSlice[[]uint64, uint64](mc, uint64(0), uint64(len(byClient)))
	for c := range byClient {
		mc.Step()

		clients = meter.Append(mc, clients, c)
	}
	// Descending clientID, matching EncodeDiff and yrs gotcha 1.
	meter.Slice(mc, clients, func(i, j int) bool { return clients[i] > clients[j] })
	for _, c := range clients {
		mc.Step()

		recs := byClient[c]
		meter.Slice(mc, recs, func(i, j int) bool { return recs[i].clock < recs[j].clock })
		meter.Set(mc, byClient, c, reconcile(mc, recs))
	}
	return clients, byClient
}

// reconcile removes overlap from one client's clock-sorted records.
//
// This is not tidiness, it is the difference between a correct update and a
// silently corrupt one. Neither wire format carries a per-block clock: a
// receiver derives each block's clock by accumulating lengths from the run's
// declared start. So two records covering overlapping clocks cannot both be
// emitted - the second would be RELABELLED at the end of the first, landing on
// clocks its client never minted, duplicating content and shifting every later
// block in the run. Both ygo and yjs accept the result without complaint,
// which is what makes it dangerous.
//
// Overlap is ordinary in a merge set rather than exotic: one peer publishes a
// client's run whole while another, having spliced into the middle of it,
// publishes the same run split in two. Above a causal hole neither integrates,
// so both reach the plan. Measured before this existed: merging those two
// diffs produced "cccccXccc" where every other path gives "ccXccc".
//
// Fully covered records are dropped; a record straddling what is already
// covered is sliced, on a COPY, because Content.Split mutates its receiver and
// the records point into the document's live pending buffer.
func reconcile(mc *meter.Meter, recs []mergeRecord) []mergeRecord {
	mc.Enter()
	defer mc.Leave()

	if len(recs) < 2 {
		return recs
	}
	out := meter.MakeSlice[[]mergeRecord, mergeRecord](mc, uint64(0), uint64(len(recs)))
	covered := recs[0].clock
	for _, r := range recs {
		mc.Step()

		if r.clock+r.length <= covered {
			continue // wholly inside what is already emitted
		}
		if r.clock < covered {
			off := covered - r.clock
			if r.kind == WireBlockItem {
				cp := *r.item
				if err := sliceWireItemRight(mc, &cp, off); err != nil {
					// Non-splittable content at a partial boundary. Dropping
					// the record would lose data, so keep the wider one that
					// already covers this span and skip only the overlap.
					continue
				}
				r.item = &cp
			}
			r.clock += off
			r.length -= off
		}
		out = meter.Append(mc, out, r)
		covered = r.clock + r.length
	}
	return out
}

// countWithSkips returns how many blocks a client's run will emit once the
// holes are filled with Skip records. The count is written before the blocks,
// so it has to be known up front.
func countWithSkips(mc *meter.Meter, recs []mergeRecord) uint64 {
	mc.Enter()
	defer mc.Leave()

	n := uint64(len(recs))
	next := recs[0].clock
	for _, r := range recs {
		mc.Step()

		if r.clock > next {
			n++
		}
		next = r.clock + r.length
	}
	return n
}

// EncodeStateAsUpdateWithPending is EncodeStateAsUpdate plus the pending
// buffer, in V1 wire format. See the file comment for why the two differ.
func EncodeStateAsUpdateWithPending(mc *meter.Meter, d *doc.Doc) []byte {
	mc.Enter()
	defer mc.Leave()

	txn := d.ReadTxn(mc)
	defer txn.Close(mc)
	bs := txn.Store(mc)

	clients, byClient := buildMergePlan(mc, bs, GetPending(mc, txn))
	if clients == nil {
		return EncodeDiff(mc, d, txn, nil)
	}

	buf := lib0.WriteVarUint(mc, nil, uint64(len(clients)))
	for _, client := range clients {
		mc.Step()

		recs := byClient[client]
		buf = lib0.WriteVarUint(mc, buf, countWithSkips(mc, recs))
		buf = lib0.WriteVarUint(mc, buf, client)
		buf = lib0.WriteVarUint(mc, buf, recs[0].clock)

		next := recs[0].clock
		for _, r := range recs {
			mc.Step()

			if r.clock > next {
				buf = lib0.WriteVarUint(mc, buf, 10) // BLOCK_SKIP_REF_NUMBER
				buf = lib0.WriteVarUint(mc, buf, r.clock-next)
			}
			switch r.kind {
			case WireBlockItem:
				buf = encodeItem(mc, buf, r.item)
			default: // GC and Skip share the shape: ref number, then length.
				ref := uint64(0)
				if r.kind == WireBlockSkip {
					ref = 10
				}
				buf = lib0.WriteVarUint(mc, buf, ref)
				buf = lib0.WriteVarUint(mc, buf, r.length)
			}
			next = r.clock + r.length
		}
	}
	return mergedDeleteSet(mc, bs, GetPending(mc, txn)).Encode(mc, buf)
}

// EncodeStateAsUpdateV2WithPending is the V2 twin. The formats differ in how a
// Skip carries its length - V2 puts it in the rest stream rather than the len
// column, an anomaly inherited from yjs and already honoured by the decoder -
// so the two emitters cannot share a writer, only the plan.
func EncodeStateAsUpdateV2WithPending(mc *meter.Meter, d *doc.Doc) []byte {
	mc.Enter()
	defer mc.Leave()

	txn := d.ReadTxn(mc)
	defer txn.Close(mc)
	bs := txn.Store(mc)

	clients, byClient := buildMergePlan(mc, bs, GetPending(mc, txn))
	if clients == nil {
		return EncodeDiffV2(mc, d, txn, nil)
	}

	enc := NewEncoderV2(mc)
	enc.WriteVarUint(mc, uint64(len(clients)))
	for _, client := range clients {
		mc.Step()

		recs := byClient[client]
		enc.WriteVarUint(mc, countWithSkips(mc, recs))
		enc.WriteClient(mc, client)
		enc.WriteVarUint(mc, recs[0].clock)

		next := recs[0].clock
		for _, r := range recs {
			mc.Step()

			if r.clock > next {
				enc.WriteInfo(mc, 10)
				enc.WriteVarUint(mc, r.clock-next)
			}
			switch r.kind {
			case WireBlockItem:
				encodeItemV2(mc, enc, r.item)
			case WireBlockSkip:
				enc.WriteInfo(mc, 10)
				enc.WriteVarUint(mc, r.length)
			default:
				enc.WriteInfo(mc, 0)
				enc.WriteLen(mc, r.length)
			}
			next = r.clock + r.length
		}
	}
	writeDeleteSetV2(mc, enc, mergedDeleteSet(mc, bs, GetPending(mc, txn)))
	return enc.Bytes(mc)
}

// mergedDeleteSet combines the store's delete set with the ranges still queued
// against IDs nobody has yet. A queued delete is as much a part of what the
// updates carried as a queued block.
func mergedDeleteSet(mc *meter.Meter, bs *store.BlockStore, pending *Pending) *IdSet {
	mc.Enter()
	defer mc.Leave()

	ds := buildDeleteSetFromStore(mc, bs, bs.GetStateVector(mc))
	if pending != nil && pending.DeleteSet != nil {
		pending.DeleteSet.Iterate(mc, func(client uint64, ranges []Range) {
			for _, r := range ranges {
				mc.Step()

				ds.Insert(mc, client, r.Start, r.Length)
			}
		})
	}
	return ds
}
