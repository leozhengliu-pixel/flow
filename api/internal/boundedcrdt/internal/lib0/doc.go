// Package lib0 implements the binary encoding format used by Yjs.
//
// lib0 is the npm package by Kevin Jahns that defines Yjs's wire encoding.
// This package provides byte-for-byte compatible encode/decode primitives
// for varuint, varint (zigzag-encoded signed), variable-length strings,
// variable-length byte buffers, and the fixed-width number types used
// throughout the Yjs sync protocol.
//
// Wire format compatibility with the JS implementation is non-negotiable.
// See testdata/ for fixtures captured from the JS lib0 reference.
//
// Reference: https://github.com/dmonad/lib0
package lib0
