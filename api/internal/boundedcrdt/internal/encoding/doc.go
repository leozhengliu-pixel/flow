// Package encoding implements the V1 wire format yrs and JS Yjs use
// for state vectors, delete sets, and document updates.
//
// Per docs/yrs-port-notes/update-v1.md the V1 wire format has three
// load-bearing pieces:
//
//   - StateVector: varuint count followed by (clientID, clock) pairs.
//   - IdSet (the wire DeleteSet): per-client (id, range count, ranges
//     of (start, length)) — half-open ranges, written as length not end.
//   - Update: per-client block runs followed by an embedded IdSet.
//
// This package owns StateVector and IdSet end-to-end. Update encode
// and decode arrive in subsequent commits; they will compose StateVector
// and IdSet here.
//
// All primitive byte operations route through internal/lib0, which is
// already verified byte-equivalent with JS lib0 across 40 fixtures.
//
// Determinism: encoding sorts clients ascending by clientID before
// emission. yrs's StateVector and IdSet iterate HashMap / BTreeMap
// (BTreeMap = ascending; HashMap = arbitrary). JS Yjs iterates JS Map
// in insertion order. To produce reproducible bytes that round-trip
// across runs and runtimes we always sort here. Decoders accept any
// order — the wire is unambiguous.
package encoding
