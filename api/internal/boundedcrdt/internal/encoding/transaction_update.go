package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/doc"
)

func init() {
	doc.UpdateEncoder = func(mc *meter.Meter, t *doc.TransactionMut, v2 bool) []byte {
		if v2 {
			return EncodeTransactionUpdateV2(mc, t)
		}
		return EncodeTransactionUpdate(mc, t)
	}
}

// EncodeTransactionUpdate returns the V1 update for one committed
// transaction, or nil when the transaction created no structs and
// deleted nothing. Mirrors yjs writeUpdateMessageFromTransaction, the
// bytes of doc.on('update'): every struct from the transaction's start
// state on (the first of each client cut at that clock, as in
// EncodeDiff) and the delete set of what this transaction deleted, not
// the whole store's.
//
// Call it after Commit's squash and garbage collection, as Doc's
// update handlers do, so the bytes describe the final layout.
func EncodeTransactionUpdate(mc *meter.Meter, t *doc.TransactionMut) []byte {
	mc.Enter()
	defer mc.Leave()

	if !transactionChanged(mc, t) {
		return nil
	}
	buf := writeClientsStructs(mc, nil, t.Store(mc), t.BeforeState(mc))
	return transactionDeleteSet(mc, t).Encode(mc, buf)
}

// EncodeTransactionUpdateV2 is EncodeTransactionUpdate in V2, the
// bytes of doc.on('updateV2').
func EncodeTransactionUpdateV2(mc *meter.Meter, t *doc.TransactionMut) []byte {
	mc.Enter()
	defer mc.Leave()

	if !transactionChanged(mc, t) {
		return nil
	}
	enc := NewEncoderV2(mc)
	writeClientsStructsV2(mc, enc, t.Store(mc), t.BeforeState(mc))
	writeDeleteSetV2(mc, enc, transactionDeleteSet(mc, t))
	return enc.Bytes(mc)
}

// transactionChanged is yjs's emit condition: the transaction deleted
// something or some client's clock moved.
func transactionChanged(mc *meter.Meter, t *doc.TransactionMut) bool {
	mc.Enter()
	defer mc.Leave()

	if len(t.DeletedRanges(mc)) > 0 {
		return true
	}
	before := t.BeforeState(mc)
	for c, clock := range t.Store(mc).GetStateVector(mc) {
		mc.Step()

		if clock != before[c] {
			return true
		}
	}
	return false
}

// transactionDeleteSet is the transaction's own delete set, sorted and
// merged as yjs sortAndMergeDeleteSet leaves it (IdSet.Insert merges
// overlapping and adjacent ranges).
func transactionDeleteSet(mc *meter.Meter, t *doc.TransactionMut) *IdSet {
	mc.Enter()
	defer mc.Leave()

	ds := NewIdSet(mc)
	for _, r := range t.DeletedRanges(mc) {
		mc.Step()

		ds.Insert(mc, r.ID.Client, r.ID.Clock, r.Len)
	}
	return ds
}
