package store

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
)

// MetadataJSONSource is a borrowed, seekable encoded JSON value. A rewriter
// may revisit its bytes only during the callback.
type MetadataJSONSource interface {
	io.ReaderAt
	Size() int64
	ContainsUploads() bool
}

type MetadataJSONRewriter func(context.Context, MetadataJSONSource, io.Writer) error

func (s *logicalJSONStream) rewriteNode(node metadataLogicalNode) (bool, error) {
	mode := metadataJSONStringQuoted
	switch {
	case node.typ == reflect.TypeFor[json.RawMessage]():
		mode = metadataJSONStringCompactRaw
	case node.typ.Kind() == reflect.String:
	case node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() == reflect.Uint8:
		mode = metadataJSONStringBase64
	default:
		return false, nil
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, s.ctx}
	if node.typ.Kind() == reflect.Slice {
		present, err := cursor.byte()
		if err != nil {
			return false, err
		}
		if present == 0 {
			return false, nil
		}
	}
	length, err := cursor.uint()
	if err != nil {
		return false, err
	}
	if cursor.offset > node.offset+node.size || length > uint64(node.offset+node.size-cursor.offset) {
		return false, ErrMetadataRecordQuery
	}
	input := io.NewSectionReader(node.source.file, cursor.offset, int64(length))
	source, err := newMetadataJSONStringSource(s.ctx, s.owner, input, int64(length), mode)
	if err != nil {
		return false, err
	}
	defer source.Close()
	if !source.ContainsUploads() {
		return false, nil
	}
	return true, s.rewrite(s.ctx, source, s.out)
}
