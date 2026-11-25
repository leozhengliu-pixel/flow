package store

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
)

type coldMetadataGenericDecodeKey struct{}

// Only the synchronous decoder receives this token. The public cold request
// context never lends a HOT-quota bypass to later readers.
type coldMetadataGenericDecode struct {
	stores map[*metadataLogicalStorage]bool
}

func coldMetadataGenericDecodeContext(ctx context.Context, sources map[string]*metadataLogicalSource) context.Context {
	token := &coldMetadataGenericDecode{stores: map[*metadataLogicalStorage]bool{}}
	for _, source := range sources {
		for _, storage := range source.stores {
			token.stores[storage] = true
		}
	}
	return context.WithValue(ctx, coldMetadataGenericDecodeKey{}, token)
}

type coldMetadataGenericReader struct {
	ctx   context.Context
	input io.Reader
}

func (r coldMetadataGenericReader) Read(output []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.input.Read(output)
}

func (n metadataLogicalNode) coldGenericValue(ctx context.Context) (reflect.Value, bool, error) {
	token, _ := ctx.Value(coldMetadataGenericDecodeKey{}).(*coldMetadataGenericDecode)
	if token == nil || !token.stores[n.source] {
		return reflect.Value{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, true, err
	}
	if !metadataGenericJSONType(n.typ) {
		return reflect.Value{}, true, ErrBoundedMetadataCoverage
	}
	value := reflect.New(n.typ)
	input := json.NewDecoder(coldMetadataGenericReader{ctx: ctx, input: n.genericPart().reader()})
	if err := input.Decode(value.Interface()); err != nil {
		return reflect.Value{}, true, err
	}
	var trailing any
	if err := input.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = ErrMetadataRecordQuery
		}
		return reflect.Value{}, true, err
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, true, err
	}
	return value.Elem(), true, nil
}
