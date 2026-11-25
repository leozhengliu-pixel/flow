package block

import "flow/api/internal/boundedcrdt/internal/meter"

import "strconv"

// ID uniquely identifies an Item in a Yjs document. Client is the random uint64
// assigned to a Doc at creation; Clock is a per-client monotonic counter that
// increments on every operation produced by that client. The pair is the
// Lamport timestamp Yjs uses for ordering and conflict resolution.
type ID struct {
	Client uint64
	Clock  uint64
}

// IsZero reports whether the ID is the zero value. Yjs treats Client=0,Clock=0
// as a valid ID for the synthetic root entries, so callers should compare
// against an explicit "no parent" sentinel rather than relying on IsZero in
// origin/parent positions.
func (i ID) IsZero(mc *meter.Meter) bool {
	mc.Enter()
	defer mc.Leave()

	return i.Client == 0 && i.Clock == 0
}

// Equal reports whether two IDs name the same Item.
func (i ID) Equal(mc *meter.Meter, other ID) bool {
	mc.Enter()
	defer mc.Leave()

	return i.Client == other.Client && i.Clock == other.Clock
}

// Less orders IDs lexicographically: Client first, then Clock. This matches
// yrs's Ord impl for ID and is the order used by the y-sync state-vector
// encoding (which iterates clients in ascending order).
//
// Note: this is *not* the YATA ordering used for conflict resolution at
// insertion time. YATA compares origins and falls back on Client only when
// origins tie. Use this Less only for storage/iteration ordering.
func (i ID) Less(mc *meter.Meter, other ID) bool {
	mc.Enter()
	defer mc.Leave()

	if i.Client != other.Client {
		return i.Client < other.Client
	}
	return i.Clock < other.Clock
}

// String returns a debug representation in the form "client:clock".
func (i ID) String(mc *meter.Meter) string {
	mc.Enter()
	defer mc.Leave()

	return meter.Concat(mc, meter.Concat(mc, strconv.FormatUint(i.Client, 10), ":"), strconv.FormatUint(i.Clock, 10))
}

// IDOrNil is a pointer-or-value helper for fields that semantically permit
// "absent" (origin-left/right of an Item inserted at a boundary). nil means
// "no neighbor at insertion time"; a non-nil *ID names the neighbor.
//
// Yjs distinguishes "no origin" (insertion at boundary) from "origin happens
// to be the synthetic root ID" — encoding them differently on the wire. Using
// *ID rather than a sentinel ID value keeps that distinction explicit.
type IDOrNil = *ID
