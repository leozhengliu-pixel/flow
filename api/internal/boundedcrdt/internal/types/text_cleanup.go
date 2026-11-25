package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
)

func init() {
	doc.FormatCleanupHook = formatCleanupFor
}

// formatCleanupFor is doc.FormatCleanupHook. yjs asks for a cleanup when a
// transaction that is not local changed a YText or YXmlText with
// formatting (YText._callObserver, for each changed type whose item is
// not deleted); the cleanup itself is cleanupTextAfterTransaction.
func formatCleanupFor(mc *meter.Meter, remote *doc.TransactionMut) func(*doc.TransactionMut) {
	mc.Enter()
	defer mc.Leave()

	for _, br := range remote.ChangedTypes(mc) {
		mc.Step()

		if br.FormattedText && (br.Item == nil || !br.Item.IsDeleted(mc)) {
			return func(cleanup *doc.TransactionMut) {
				cleanupTextAfterTransaction(mc, remote, cleanup)
			}
		}
	}
	return nil
}

// cleanupTextAfterTransaction is yjs cleanupYTextAfterTransaction, run in
// the cleanup transaction while remote's deleted items still hold their
// content. A text that got a live format marker in remote, or lost one,
// gets the full cleanup; around other deleted content of a formatted
// text, the contextless one. The order matters and is yjs's: the set of
// texts for the full cleanup grows while the delete set is walked, and a
// text in it takes no more contextless cleanups.
func cleanupTextAfterTransaction(mc *meter.Meter, remote, cleanup *doc.TransactionMut) {
	mc.Enter()
	defer mc.Leave()

	var full []*block.Branch
	inFull := meter.Literal[map[*block.Branch]bool](mc, 128, func() map[*block.Branch]bool {
		return map[*block.Branch]bool{}
	})
	addFull := func(br *block.Branch) {
		if !inFull[br] {
			meter.Set(mc, inFull, br, true)
			full = meter.Append(mc, full, br)
		}
	}
	bs := remote.Store(mc)
	before, after := remote.BeforeState(mc), remote.AfterState(mc)
	// The structs remote added, client by client in the order yjs
	// iterates afterState (the store's), each from its start state on.
	for _, client := range bs.ClientOrder(mc) {
		mc.Step()

		from, to := before[client], after[client]
		if to == from {
			continue
		}
		eachBlock(mc, remote, client, from, to, func(it *block.Item) {
			if !it.IsDeleted(mc) && it.Content.Kind == block.KindFormat && it.Parent.IsResolved(mc) {
				addFull(it.Parent.Branch)
			}
		})
	}
	// The structs remote deleted, as yjs walks transaction.deleteSet:
	// clients in the order of their first deletion, ranges sorted.
	for _, rg := range deletedByClientInOrder(mc, remote) {
		mc.Step()

		for _, r := range rg.ranges {
			mc.Step()

			eachBlock(mc, remote, rg.client, r[0], r[1], func(it *block.Item) {
				if !it.Parent.IsResolved(mc) {
					return
				}
				parent := it.Parent.Branch
				if !hasFormatting(mc, parent) || inFull[parent] {
					return
				}
				if it.Content.Kind == block.KindFormat {
					addFull(parent)
				} else {
					cleanupContextlessFormattingGap(mc, cleanup, it)
				}
			})
		}
	}
	for _, br := range full {
		mc.Step()

		cleanupTextFormatting(mc, cleanup, br)
	}
}

// hasFormatting is yjs's _hasFormatting of the object that stands for br,
// which the delete-set walk reads. A Text or XmlText is the YText that
// Doc.get built, flag false from the moment the root was typed
// (FormattedText); an untyped root is the placeholder that collected the
// format markers integrated into it, flag set by each (HasFormatting).
// Only the former asks for a cleanup, but once one runs, both take part.
func hasFormatting(mc *meter.Meter, br *block.Branch) bool {
	mc.Enter()
	defer mc.Leave()

	if br.TypeRef == block.TypeRefText || br.TypeRef == block.TypeRefXmlText {
		return br.FormattedText
	}
	return br.HasFormatting
}

// eachBlock calls f for every item among client's blocks covering clocks
// [from, to), each once; GC blocks are skipped, as yjs skips GC structs.
func eachBlock(mc *meter.Meter, txn *doc.TransactionMut, client, from, to uint64, f func(*block.Item)) {
	mc.Enter()
	defer mc.Leave()

	bs := txn.Store(mc)
	for clock := from; clock < to; {
		mc.Step()

		cell, ok := bs.GetBlock(mc, block.ID{Client: client, Clock: clock})
		if !ok {
			return
		}
		if it := cell.AsItem(mc); it != nil {
			f(it)
		}
		next := cell.ClockEnd(mc) + 1
		if next <= clock {
			return
		}
		clock = next
	}
}

type clientRanges struct {
	client uint64
	ranges [][2]uint64 // half-open, sorted, merged
}

// deletedByClientInOrder returns txn's deleted ranges grouped by client,
// clients in the order of their first deletion, each client's ranges
// sorted and merged: yjs transaction.deleteSet after sortAndMergeDeleteSet.
func deletedByClientInOrder(mc *meter.Meter, txn *doc.TransactionMut) []clientRanges {
	mc.Enter()
	defer mc.Leave()

	var out []clientRanges
	at := meter.Literal[map[uint64]int](mc, 128, func() map[uint64]int {
		return map[uint64]int{}
	})
	for _, d := range txn.DeletedRanges(mc) {
		mc.Step()

		i, ok := at[d.ID.Client]
		if !ok {
			i = len(out)
			meter.Set(mc, at, d.ID.Client, i)
			out = meter.Append(mc, out, clientRanges{client: d.ID.Client})
		}
		out[i].ranges = meter.Append(mc, out[i].ranges, [2]uint64{d.ID.Clock, d.ID.Clock + d.Len})
	}
	for i := range out {
		mc.Step()

		rs := out[i].ranges
		meter.Slice(mc, rs, func(a, b int) bool { return rs[a][0] < rs[b][0] })
		merged := rs[:0]
		for _, r := range rs {
			mc.Step()

			if n := len(merged); n > 0 && r[0] <= merged[n-1][1] {
				if r[1] > merged[n-1][1] {
					merged[n-1][1] = r[1]
				}
				continue
			}
			merged = meter.Append(mc, merged, r)
		}
		out[i].ranges = merged
	}
	return out
}

// cleanupContextlessFormattingGap is yjs cleanupContextlessFormattingGap:
// from item, go right to the last of the deleted or uncountable items
// that follow it, then left over that run, deleting every live format
// marker whose key a marker to its right in the run already set.
func cleanupContextlessFormattingGap(mc *meter.Meter, txn *doc.TransactionMut, item *block.Item) {
	mc.Enter()
	defer mc.Leave()

	for item != nil && item.Right != nil && (item.Right.IsDeleted(mc) || !item.Right.IsCountable(mc)) {
		mc.Step()

		item = item.Right
	}
	seen := meter.Literal[map[string]bool](mc, 128, func() map[string]bool {
		return map[string]bool{}
	})
	for item != nil && (item.IsDeleted(mc) || !item.IsCountable(mc)) {
		mc.Step()

		if !item.IsDeleted(mc) && item.Content.Kind == block.KindFormat {
			key := item.Content.FormatKey
			if seen[key] {
				txn.Delete(mc, item)
			} else {
				meter.Set(mc, seen, key, true)
			}
		}
		item = item.Left
	}
}

// cleanupTextFormatting is yjs cleanupYTextFormatting, ported as it is:
// at each live content item it runs cleanupFormattingGap from the
// previous one. Once start is a live content item, the gap's first loop
// stops on it at once, so in effect only the markers before the first
// live content are cleaned, and the ones after the last never are; yjs
// behaves so, and ygo with it.
func cleanupTextFormatting(mc *meter.Meter, txn *doc.TransactionMut, br *block.Branch) int {
	mc.Enter()
	defer mc.Leave()

	res := 0
	start := br.Start
	startAttrs := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})
	cur := meter.Pointer(mc, textPos{attrs: meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})})
	for end := br.Start; end != nil; end = end.Right {
		mc.Step()

		if end.IsDeleted(mc) {
			continue
		}
		if end.Content.Kind == block.KindFormat {
			cur.updateAttr(mc, end.Content.FormatKey, formatValue(mc, end))
			continue
		}
		res += cleanupFormattingGapFrom(mc, txn, start, end, startAttrs, cur)
		startAttrs, _ = cur.currentAttrs(mc)
		start = end
	}
	return res
}
