// Package block defines the building blocks of a Yjs document.
//
// The central type is Item — a single insertion in the CRDT linked list,
// identified by an ID (clientID + clock). Items carry origin pointers
// captured at insertion time (used by YATA for conflict resolution) plus
// current left/right pointers in the post-merge structure.
//
// Wire format compatibility with the JS Yjs implementation is the prime
// directive of this package; field semantics mirror y-crdt/yrs (see
// yrs/src/block.rs as the executable reference).
//
// This package is internal: callers should use the public ygo, doc, and
// types packages instead.
package block
