package store

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
)

const metadataLogicalRootNormalizeBuffer = 32 << 10

type metadataLogicalRawNormalizer struct {
	input  *bufio.Reader
	output *bufio.Writer
}

// Values have already passed the root structural scanner or json.Valid.
// Keep escape spelling, number spelling and duplicate object properties;
// RawMessage marshaling only removes insignificant space and escapes HTML.
func normalizeMetadataLogicalRaw(ctx context.Context, source io.Reader, destination io.Writer) (bool, error) {
	normalizer := metadataLogicalRawNormalizer{}
	return normalizer.normalize(ctx, source, destination)
}

func (n *metadataLogicalRawNormalizer) normalize(ctx context.Context, source io.Reader, destination io.Writer) (bool, error) {
	if n.input == nil {
		n.input = bufio.NewReaderSize(source, metadataLogicalRootNormalizeBuffer)
	} else {
		n.input.Reset(source)
	}
	input := n.input
	var output *bufio.Writer
	if destination != nil {
		if n.output == nil {
			n.output = bufio.NewWriterSize(destination, metadataLogicalRootNormalizeBuffer)
		} else {
			n.output.Reset(destination)
		}
		output = n.output
	}
	changed, quoted, escaped := false, false, false
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		value, err := input.ReadByte()
		if err == io.EOF {
			if output == nil {
				return changed, nil
			}
			return changed, output.Flush()
		}
		if err != nil {
			return false, err
		}
		if !quoted && (value == ' ' || value == '\t' || value == '\r' || value == '\n') {
			if output == nil {
				return true, nil
			}
			changed = true
			continue
		}
		if value == '<' || value == '>' || value == '&' {
			if output == nil {
				return true, nil
			}
			literal := `\u003c`
			if value == '>' {
				literal = `\u003e`
			} else if value == '&' {
				literal = `\u0026`
			}
			if _, err := output.WriteString(literal); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		if value == 0xe2 {
			next, err := input.Peek(2)
			if err != nil && err != io.EOF {
				return false, err
			}
			if len(next) == 2 && next[0] == 0x80 && next[1]&^1 == 0xa8 {
				if output == nil {
					return true, nil
				}
				separator := next[1]
				if _, err := input.Discard(2); err != nil {
					return false, err
				}
				literal := `\u2028`
				if separator == 0xa9 {
					literal = `\u2029`
				}
				if _, err := output.WriteString(literal); err != nil {
					return false, err
				}
				changed = true
				continue
			}
		}
		if output != nil {
			if err := output.WriteByte(value); err != nil {
				return false, err
			}
		}
		if quoted {
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if value == '"' {
				quoted = false
			}
		} else if value == '"' {
			quoted = true
		}
	}
}

type metadataLogicalRootNormalizedWriter struct {
	store *SQLiteStore
	spool *RecordSpool
}

func (w metadataLogicalRootNormalizedWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > int64(w.store.maxStateBytes)-w.spool.bytes {
		return 0, fmt.Errorf("workspace state exceeds %d bytes", w.store.maxStateBytes)
	}
	return w.spool.Write(raw)
}

func (s *SQLiteStore) normalizeMetadataLogicalRootValues(ctx context.Context, workspace string, root *metadataLogicalRootSnapshot,
	fields map[string]metadataCollectionRange, changes map[string]metadataLogicalRootReplacement,
) (_ map[string]metadataLogicalRootReplacement, closeSpools func(), err error) {
	result := make(map[string]metadataLogicalRootReplacement, len(changes))
	for name, value := range changes {
		result[name] = value
	}
	var spools []*RecordSpool
	closeSpools = func() {
		for _, spool := range spools {
			_ = spool.Close()
		}
	}
	cleanup := closeSpools
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	normalizer := metadataLogicalRawNormalizer{}
	for name, part := range fields {
		value, changed := changes[name]
		var source io.ReaderAt = root.read.root
		if changed {
			part = metadataCollectionRange{size: value.size}
			if value.spool != nil {
				source = value.spool
			} else {
				source = bytes.NewReader(value.raw)
			}
		}
		needs, err := normalizer.normalize(ctx, io.NewSectionReader(source, part.offset, part.size), nil)
		if err != nil {
			return nil, nil, err
		}
		if !needs {
			continue
		}
		spool, err := newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
		if err != nil {
			return nil, nil, err
		}
		spools = append(spools, spool)
		if _, err := normalizer.normalize(ctx, io.NewSectionReader(source, part.offset, part.size), metadataLogicalRootNormalizedWriter{s, spool}); err != nil {
			return nil, nil, err
		}
		result[name] = metadataLogicalRootReplacement{spool: spool, size: spool.bytes}
	}
	return result, closeSpools, nil
}
