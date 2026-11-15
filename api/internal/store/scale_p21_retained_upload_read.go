package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"sync"
)

// RetainedUploadRead owns candidate names after excluding references in the
// current installed trash. Keep it open through the upload deletion checks.
type RetainedUploadRead struct {
	Names []string
	owner *ResourceRead
	once  sync.Once
}

func (r *RetainedUploadRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Names = nil
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// BeginRetainedUploadRead preserves the literal WorkspaceMetadataFields trash
// lookup, including default workspace selection and installed native payloads.
// It is a trusted lookup; ReleasableUploads still checks live/foreign ownership.
func (s *SQLiteStore) BeginRetainedUploadRead(ctx context.Context, workspace string, names []string) (_ *RetainedUploadRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &RetainedUploadRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	cost := uint64(128 << 10)
	for _, name := range names {
		part := uint64(len(name))*8 + 256
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return nil, ErrMetadataWorkBudget
		}
		cost += part
	}
	if err = read.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	read.Names = slices.Clone(names)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return read, nil
	}
	defer source.Close()
	// Keep only candidate keys: a decoded URL name can share a large query
	// string's backing storage, so matches update bits rather than map keys.
	positions := make(map[string]int, len(names))
	retained := make([]bool, len(names))
	for index, name := range names {
		positions[name] = index
	}
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	err = source.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != "trash" {
			return nil
		}
		return scratch.nativeArray(ctx, node, func(_ int, entry metadataLogicalNode) error {
			return entry.Children(ctx, func(key reflect.Value, payload metadataLogicalNode) error {
				if key.String() != "Payload" {
					return nil
				}
				row := &ResourceRead{store: s}
				defer row.Close()
				value, err := row.node(ctx, payload, false)
				if err != nil {
					return err
				}
				raw := value.Interface().(json.RawMessage)
				if err := row.reserve(ctx, 128<<10); err != nil {
					return err
				}
				work, err := retainedUploadJSONWork(ctx, raw)
				if err != nil {
					return err
				}
				if work != 0 {
					if err := row.reserve(ctx, work); err != nil {
						return err
					}
				}
				for _, name := range AttachmentUploads(raw) {
					if index, candidate := positions[name]; candidate {
						retained[index] = true
					}
				}
				return ctx.Err()
			})
		})
	})
	if err != nil {
		return nil, err
	}
	read.Names = slices.DeleteFunc(read.Names, func(name string) bool { return retained[positions[name]] })
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// Gzip is traversed once with fixed scratch space before AttachmentUploads
// materializes it. The same JSON work rule also admits the decompression
// buffers, decoded containers and collector maps. Invalid gzip remains an
// empty reference set, as in AttachmentUploads.
func retainedUploadJSONWork(ctx context.Context, raw []byte) (uint64, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return metadataAggregatePayloadJSONWork(ctx, raw)
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return 0, ctx.Err()
	}
	defer reader.Close()
	var buffer [32 << 10]byte
	cost := uint64(0)
	quoted, escaped := false, false
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := reader.Read(buffer[:])
		for _, c := range buffer[:n] {
			cost += 16
			if quoted {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					quoted = false
				}
			} else {
				switch c {
				case '"':
					quoted = true
					cost += 128
				case '{', '[':
					cost += 1024
				case ':', ',':
					cost += 128
				}
			}
			if cost > metadataWorkBytes {
				return 0, ErrMetadataWorkBudget
			}
		}
		if err == io.EOF {
			return cost, ctx.Err()
		}
		if err != nil {
			return cost, ctx.Err()
		}
	}
}
