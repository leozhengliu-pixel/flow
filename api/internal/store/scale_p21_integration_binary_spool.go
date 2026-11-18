package store

import (
	"context"
	"io"
	"unicode/utf8"
)

// RawMessage preserves invalid UTF-8 accepted by encoding/json. Such bytes
// cannot pass through the TEXT stage; the literal BLOB statement keeps them.
func integrationDeliveryBinarySpool(ctx context.Context, owner *ResourceRead, spool *RecordSpool) ([]byte, bool, error) {
	if integrationDeliveryStreamEvent(ctx) == "" || spool == nil || spool.bytes < 0 {
		return nil, false, ErrBoundedMetadataCoverage
	}
	if err := legacySessionCanonicalOwnerOpen(ctx, owner); err != nil {
		return nil, false, err
	}
	var buffer [metadataRecordChunkBytes + utf8.UTFMax - 1]byte
	retained := 0
	for offset := int64(0); offset < spool.bytes; {
		if err := legacySessionCanonicalOwnerOpen(ctx, owner); err != nil {
			return nil, false, err
		}
		size := int(min(int64(metadataRecordChunkBytes), spool.bytes-offset))
		n, err := spool.ReadAt(buffer[retained:retained+size], offset)
		if err != nil {
			return nil, false, err
		}
		if n != size {
			return nil, false, io.ErrUnexpectedEOF
		}
		total, end := retained+size, retained+size
		if offset+int64(size) < spool.bytes {
			start := total - 1
			for start > 0 && !utf8.RuneStart(buffer[start]) && total-start < utf8.UTFMax {
				start--
			}
			if utf8.RuneStart(buffer[start]) && !utf8.FullRune(buffer[start:total]) {
				end = start
			}
		}
		if !utf8.Valid(buffer[:end]) {
			if spool.bytes < 0 || uint64(spool.bytes) > (metadataWorkBytes-(64<<10))/4 {
				return nil, false, ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, uint64(spool.bytes)*4+64<<10); err != nil {
				return nil, false, err
			}
			raw := make([]byte, int(spool.bytes))
			n, err := spool.ReadAt(raw, 0)
			if err != nil {
				return nil, false, err
			}
			if n != len(raw) {
				return nil, false, io.ErrUnexpectedEOF
			}
			if err := legacySessionCanonicalOwnerOpen(ctx, owner); err != nil {
				return nil, false, err
			}
			return raw, true, nil
		}
		retained = total - end
		copy(buffer[:retained], buffer[end:total])
		offset += int64(size)
	}
	return nil, false, legacySessionCanonicalOwnerOpen(ctx, owner)
}
