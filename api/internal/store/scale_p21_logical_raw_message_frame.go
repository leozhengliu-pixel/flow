package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

// A nil reader with zero size denotes a nil RawMessage. A nonnil reader
// denotes a present slice, including an empty one. The caller owns JSON
// validation and limits the borrowed range; bytes beyond size are not read.
func (b *metadataLogicalBuilder) writeRawMessageFrame(ctx context.Context, reader io.Reader, size int64) (metadataLogicalNode, error) {
	if b == nil || ctx == nil || size < 0 || reader == nil && size != 0 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	if b.finished || b.closed {
		return metadataLogicalNode{}, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := b.ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	frameSize := int64(17)
	if reader != nil {
		if size > metadataLogicalDiskBytes-25 {
			return metadataLogicalNode{}, ErrMetadataWorkBudget
		}
		frameSize = 25 + size
	}
	if b.offset < 0 || frameSize > metadataLogicalDiskBytes-b.offset {
		return metadataLogicalNode{}, ErrMetadataWorkBudget
	}
	typ := reflect.TypeFor[json.RawMessage]()
	node := metadataLogicalNode{
		source: b.storage, offset: b.offset, size: frameSize,
		cost: uint64(typ.Size())*2 + 128 + uint64(size)*2, typ: typ,
	}
	var header [16]byte
	if err := b.write(header[:]); err != nil {
		return metadataLogicalNode{}, err
	}
	present := byte(0)
	if reader != nil {
		present = 1
	}
	if err := b.write([]byte{present}); err != nil {
		return metadataLogicalNode{}, err
	}
	if reader != nil {
		if err := b.writeUint(uint64(size)); err != nil {
			return metadataLogicalNode{}, err
		}
		remaining, emptyReads := size, 0
		for remaining != 0 {
			if err := ctx.Err(); err != nil {
				return metadataLogicalNode{}, err
			}
			if err := b.ctx.Err(); err != nil {
				return metadataLogicalNode{}, err
			}
			chunk := b.buffer[:int(min(remaining, int64(len(b.buffer))))]
			count, readErr := reader.Read(chunk)
			if count < 0 || count > len(chunk) {
				return metadataLogicalNode{}, ErrMetadataRecordQuery
			}
			if err := ctx.Err(); err != nil {
				return metadataLogicalNode{}, err
			}
			if err := b.ctx.Err(); err != nil {
				return metadataLogicalNode{}, err
			}
			if count != 0 {
				if err := b.write(chunk[:count]); err != nil {
					return metadataLogicalNode{}, err
				}
				remaining -= int64(count)
				emptyReads = 0
			}
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return metadataLogicalNode{}, readErr
			}
			if remaining == 0 {
				break
			}
			if errors.Is(readErr, io.EOF) {
				return metadataLogicalNode{}, io.ErrUnexpectedEOF
			}
			if count == 0 {
				emptyReads++
				if emptyReads >= 100 {
					return metadataLogicalNode{}, io.ErrNoProgress
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := b.ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := b.patch(node); err != nil {
		return metadataLogicalNode{}, err
	}
	return node, nil
}
