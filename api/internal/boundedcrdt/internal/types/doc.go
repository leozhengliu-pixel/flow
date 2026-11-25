// Package types holds the user-facing shared CRDT collection types:
// Map, Array, Text, XmlElement / XmlFragment / XmlText.
//
// Each type is a thin wrapper around a *block.Branch — the branch
// owns the actual CRDT state (linked list of Items + map of map-key
// tails); the type wrapper exposes idiomatic Go APIs that hide the
// block-layer mechanics.
//
// Construction pattern: a Doc registers root branches by name via
// Doc.Branch(name); the types-layer constructor (NewMap, NewArray,
// NewText) wraps the returned branch.
//
// Mutating methods take a *doc.TransactionMut. Read methods do not
// require a transaction parameter for ergonomic reasons but the
// caller is responsible for holding at least a read lock on the Doc
// (typically via a ReadTxn).
//
// See docs/yrs-port-notes/types-map.md for the per-method contract
// of the Map type, and DESIGN.md for the API style decisions.
package types
