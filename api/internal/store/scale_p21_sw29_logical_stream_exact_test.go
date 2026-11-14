package store

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type sw29StreamTruncatedFile struct {
	metadataLogicalFile
	end int64
}

func (f sw29StreamTruncatedFile) ReadAtContext(ctx context.Context, p []byte, offset int64) (int, error) {
	if offset >= f.end {
		return 0, io.EOF
	}
	truncated := int64(len(p)) > f.end-offset
	if truncated {
		p = p[:f.end-offset]
	}
	count, err := metadataLogicalReadAt(ctx, f.metadataLogicalFile, p, offset)
	if truncated && err == nil {
		err = io.EOF
	}
	return count, err
}

func sw29StreamExactNode(t *testing.T, packed bool, length int) (metadataLogicalNode, func()) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		Workspace: domain.Workspace{ID: "stream-exact"},
		Settings:  map[string]any{"credential": strings.Repeat("a", length)},
	}, copyStamp{"stream-exact", -1})
	if err != nil {
		t.Fatal(err)
	}
	if packed {
		next, err := metadataLogicalUnitsImport(t.Context(), source)
		source.Release()
		if err != nil {
			t.Fatal(err)
		}
		source = next
	}
	var result metadataLogicalNode
	err = source.VisitFields(t.Context(), func(name string, field metadataLogicalNode) error {
		if name != "settings" {
			return nil
		}
		return field.MapEntries(t.Context(), func(key, value metadataLogicalNode) error {
			match, err := key.StringEqual(t.Context(), "credential")
			if err != nil || !match {
				return err
			}
			unwrapped, err := metadataAggregateUnwrap(t.Context(), metadataAggregateStoredNode{value}, false)
			if err != nil {
				return err
			}
			result = unwrapped.(metadataAggregateStoredNode).value
			return nil
		})
	})
	if err != nil || result.typ != reflect.TypeFor[string]() {
		source.Release()
		t.Fatal("missing exact framed credential", err)
	}
	return result, source.Release
}

func TestScaleP21SW29LogicalStreamExactTruncation(t *testing.T) {
	for _, packed := range []bool{false, true} {
		name := "FILE"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			node, close := sw29StreamExactNode(t, packed, 70)
			defer close()
			original := node.source.file
			node.source.file = sw29StreamTruncatedFile{original, node.offset + 24 + 64}
			defer func() { node.source.file = original }()
			err := node.StreamBytes(t.Context(), func(reader io.Reader) error {
				var prefix [64]byte
				if _, err := io.ReadFull(reader, prefix[:]); err != nil {
					return err
				}
				var extra [1]byte
				_, err := io.ReadFull(reader, extra[:])
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("truncated declared70-byte credential ended as %v; would be accepted as64", err)
				}
				return err
			})
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("premature payload EOF escaped the framing boundary", err)
			}
			err = node.StreamBytes(t.Context(), func(reader io.Reader) error {
				_, err := io.ReadAll(io.LimitReader(reader, 65))
				return err
			})
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("bounded ReadAll accepted a truncated declared payload", err)
			}
			// Partial callback reads are permitted; the storage adapter must
			// not force a drain after the consumer has obtained its prefix.
			if err := node.StreamBytes(t.Context(), func(reader io.Reader) error {
				var prefix [64]byte
				_, err := io.ReadFull(reader, prefix[:])
				return err
			}); err != nil {
				t.Fatal("intentional prefix consumption was forced to drain", err)
			}
		})
	}
}

func TestScaleP21SW29LogicalStreamExactBoundariesCancellationClose(t *testing.T) {
	for _, packed := range []bool{false, true} {
		for _, length := range []int{64, 70} {
			node, close := sw29StreamExactNode(t, packed, length)
			err := node.StreamBytes(t.Context(), func(reader io.Reader) error {
				var prefix [64]byte
				if _, err := io.ReadFull(reader, prefix[:]); err != nil {
					return err
				}
				var extra [1]byte
				n, err := io.ReadFull(reader, extra[:])
				if length == 64 && (n != 0 || err != io.EOF) || length == 70 && (n != 1 || err != nil) {
					t.Fatalf("packed=%v length=%d boundary=%d,%v", packed, length, n, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			short := node
			short.size -= 6
			if err := short.StreamBytes(t.Context(), func(io.Reader) error {
				t.Fatal("declared payload escaped its enclosing frame")
				return nil
			}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("short enclosing frame did not constrain its payload", err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := node.StreamBytes(cancelled, func(io.Reader) error {
				t.Fatal("cancelled header invoked the consumer")
				return nil
			}); !errors.Is(err, context.Canceled) {
				t.Fatal("header cancellation was not preserved", err)
			}
			request, cancel := context.WithCancel(t.Context())
			err = node.StreamBytes(request, func(reader io.Reader) error {
				cancel()
				_, err := io.ReadAll(reader)
				return err
			})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("payload cancellation was not preserved", err)
			}
			close()
			if err := node.StreamBytes(t.Context(), func(io.Reader) error {
				t.Fatal("closed generation invoked the consumer")
				return nil
			}); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("closed generation revived its framed payload", err)
			}
		}
	}
}

func TestScaleP21SW29LogicalStreamExactCloseDuringCallback(t *testing.T) {
	for _, packed := range []bool{false, true} {
		node, close := sw29StreamExactNode(t, packed, 70)
		defer close()
		if err := node.StreamBytes(t.Context(), func(reader io.Reader) error {
			close()
			got, err := io.ReadAll(reader)
			if err != nil || len(got) != 70 {
				t.Fatal("closing the caller removed an active callback pin", packed, len(got), err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := node.StreamBytes(t.Context(), func(io.Reader) error {
			t.Fatal("callback pin survived completion after its caller closed")
			return nil
		}); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("completed callback leaked source ownership", packed, err)
		}
	}
}
