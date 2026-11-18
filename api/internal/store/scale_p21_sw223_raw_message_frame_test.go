package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

type scaleP21SW223RawReader func([]byte) (int, error)

func (read scaleP21SW223RawReader) Read(dst []byte) (int, error) { return read(dst) }

func scaleP21SW223Builder(t *testing.T, ctx context.Context) *metadataLogicalBuilder {
	t.Helper()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := builder.storage.file.Name()
	t.Cleanup(func() {
		builder.Close()
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Error("RawMessage frame builder retained its scratch file", err)
		}
		if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
			t.Error("RawMessage frame builder leaked disk or descriptor ownership")
		}
	})
	return builder
}

func TestScaleP21SW223RawMessageFrameRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage{}},
		{"null", json.RawMessage("null")},
		{"object", json.RawMessage(" \n{ \"html\": \"<>&\", \"n\": 9007199254740993 }\t")},
		{"invalid-native", json.RawMessage{0, 255, '{', '\n'}},
		{"large-native", json.RawMessage(strings.Repeat("raw\x00\xff ", 200000))},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := scaleP21SW223Builder(t, t.Context())
			var reader io.Reader
			if test.raw != nil {
				reader = bytes.NewReader(test.raw)
			}
			node, err := builder.writeRawMessageFrame(t.Context(), reader, int64(len(test.raw)))
			if err != nil {
				t.Fatal(err)
			}
			typed, err := builder.writeValue(reflect.ValueOf(test.raw), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			if node.Type() != reflect.TypeFor[json.RawMessage]() || node.cost != typed.cost || node.size != typed.size {
				t.Fatal("streamed RawMessage changed typed byte-frame shape or cost")
			}
			actualFrame, expectedFrame := make([]byte, node.size), make([]byte, typed.size)
			if _, err := builder.storage.file.ReadAt(actualFrame, node.offset); err != nil {
				t.Fatal(err)
			}
			if _, err := builder.storage.file.ReadAt(expectedFrame, typed.offset); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actualFrame, expectedFrame) {
				t.Fatal("streamed frame differs from existing typed RawMessage codec")
			}
			value, err := node.Value(t.Context(), int(node.cost))
			if err != nil || !reflect.DeepEqual(value.Interface(), test.raw) {
				t.Fatal("native RawMessage round trip changed bytes, type or nil/empty presence", err)
			}
			if _, err := node.Value(t.Context(), int(node.cost)-1); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("native materialization bypassed its existing admission check", err)
			}
			var streamed bytes.Buffer
			if err := node.StreamBytes(t.Context(), func(input io.Reader) error {
				_, err := io.Copy(&streamed, input)
				return err
			}); err != nil || !bytes.Equal(streamed.Bytes(), test.raw) {
				t.Fatal("existing StreamBytes changed RawMessage bytes", err)
			}
		})
	}
}

func TestScaleP21SW223RawMessageFrameRangeAndBuffer(t *testing.T) {
	builder := scaleP21SW223Builder(t, t.Context())
	body := strings.Repeat("x", 3*(32<<10)+19)
	input := strings.NewReader(body + "TAIL")
	maxRequest := 0
	reader := scaleP21SW223RawReader(func(dst []byte) (int, error) {
		maxRequest = max(maxRequest, len(dst))
		return input.Read(dst)
	})
	node, err := builder.writeRawMessageFrame(t.Context(), reader, int64(len(body)))
	if err != nil || maxRequest > len(builder.buffer) || input.Len() != len("TAIL") {
		t.Fatal("RawMessage framing exceeded fixed scratch or its declared range", err, maxRequest, input.Len())
	}
	if node.cost != uint64(reflect.TypeFor[json.RawMessage]().Size())*2+128+uint64(len(body))*2 {
		t.Fatal("streamed native cost differs from typed byte-slice cost")
	}
	zero := strings.NewReader("untouched")
	if _, err := builder.writeRawMessageFrame(t.Context(), zero, 0); err != nil || zero.Len() != len("untouched") {
		t.Fatal("present empty frame read beyond its zero-byte range", err)
	}
	finalEOF := scaleP21SW223RawReader(func(dst []byte) (int, error) { return copy(dst, "null"), io.EOF })
	if _, err := builder.writeRawMessageFrame(t.Context(), finalEOF, 4); err != nil {
		t.Fatal("complete final range rejected simultaneous EOF", err)
	}
}

func TestScaleP21SW223RawMessageFrameFailuresPreservePriorNodes(t *testing.T) {
	sentinel := errors.New("sw223 reader failed")
	for _, test := range []struct {
		name   string
		reader io.Reader
		size   int64
		want   error
	}{
		{"negative", strings.NewReader(""), -1, ErrMetadataRecordQuery},
		{"nil-positive", nil, 1, ErrMetadataRecordQuery},
		{"size-overflow", strings.NewReader(""), math.MaxInt64, ErrMetadataWorkBudget},
		{"frame-overflow", strings.NewReader(""), metadataLogicalDiskBytes - 24, ErrMetadataWorkBudget},
		{"short", strings.NewReader("abc"), 4, io.ErrUnexpectedEOF},
		{"empty-short", strings.NewReader(""), 1, io.ErrUnexpectedEOF},
		{"reader-error", scaleP21SW223RawReader(func([]byte) (int, error) { return 0, sentinel }), 1, sentinel},
		{"partial-reader-error", io.MultiReader(strings.NewReader("abc"), scaleP21SW223RawReader(func([]byte) (int, error) { return 0, sentinel })), 4, sentinel},
		{"complete-reader-error", scaleP21SW223RawReader(func(dst []byte) (int, error) { return copy(dst, "x"), sentinel }), 1, sentinel},
		{"no-progress", scaleP21SW223RawReader(func([]byte) (int, error) { return 0, nil }), 1, io.ErrNoProgress},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := scaleP21SW223Builder(t, t.Context())
			prior, err := builder.writeValue(reflect.ValueOf("prior"), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			before := builder.offset
			node, err := builder.writeRawMessageFrame(t.Context(), test.reader, test.size)
			if !errors.Is(err, test.want) || node != (metadataLogicalNode{}) {
				t.Fatal("failed frame returned a node or changed its error contract", err)
			}
			if (test.want == ErrMetadataRecordQuery || test.want == ErrMetadataWorkBudget) && builder.offset != before {
				t.Fatal("invalid range wrote before validating its complete bounds")
			}
			value, err := prior.Value(t.Context(), int(prior.cost))
			if err != nil || value.String() != "prior" {
				t.Fatal("failed frame changed an earlier owned node", err)
			}
		})
	}
	t.Run("offset-overflow-before-write", func(t *testing.T) {
		builder := scaleP21SW223Builder(t, t.Context())
		before := builder.offset
		builder.offset = metadataLogicalDiskBytes - 16
		node, err := builder.writeRawMessageFrame(t.Context(), nil, 0)
		builder.offset = before
		if !errors.Is(err, ErrMetadataWorkBudget) || node != (metadataLogicalNode{}) || builder.storage.bytes != 0 {
			t.Fatal("oversized frame wrote before checking its offset bound", err)
		}
	})
	t.Run("closed", func(t *testing.T) {
		builder := scaleP21SW223Builder(t, t.Context())
		builder.Close()
		node, err := builder.writeRawMessageFrame(t.Context(), nil, 0)
		if !errors.Is(err, errMetadataLogicalClosed) || node != (metadataLogicalNode{}) {
			t.Fatal("closed builder admitted another frame", err)
		}
	})
}

func TestScaleP21SW223RawMessageFrameCancellation(t *testing.T) {
	for _, owner := range []string{"request", "builder"} {
		for _, moment := range []string{"before", "during-read"} {
			t.Run(owner+"/"+moment, func(t *testing.T) {
				cancelled, cancel := context.WithCancel(t.Context())
				defer cancel()
				requestCtx, builderCtx := t.Context(), t.Context()
				if owner == "request" {
					requestCtx = cancelled
				} else {
					builderCtx = cancelled
				}
				builder := scaleP21SW223Builder(t, builderCtx)
				prior, err := builder.writeValue(reflect.ValueOf("prior"), 0, false)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				reader := scaleP21SW223RawReader(func(dst []byte) (int, error) {
					calls++
					cancel()
					return copy(dst, "abc"), nil
				})
				before := builder.offset
				if moment == "before" {
					cancel()
				}
				node, err := builder.writeRawMessageFrame(requestCtx, reader, 6)
				if !errors.Is(err, context.Canceled) || node != (metadataLogicalNode{}) ||
					moment == "before" && (calls != 0 || builder.offset != before) ||
					moment == "during-read" && calls != 1 {
					t.Fatal("frame cancellation consumed, exposed or retained the wrong range", err, calls)
				}
				value, err := prior.Value(context.Background(), int(prior.cost))
				if err != nil || value.String() != "prior" {
					t.Fatal("cancelled frame changed the previous node lifetime", err)
				}
			})
		}
	}
}

func TestScaleP21SW223RawMessageFrameJSONOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage{}},
		{"null", json.RawMessage("null")},
		{"html-and-line-separators", json.RawMessage(" \n{\"html\":\"<>&\",\"lines\":\"\u2028\u2029\",\"escaped\":\"\\\\u003c\",\"n\":9007199254740993}\t")},
		{"array", json.RawMessage(" [ null, { \"k\": true }, \"<script>\" ] ")},
		{"invalid-native", json.RawMessage{0, 255, '{'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := scaleP21SW223Builder(t, t.Context())
			var reader io.Reader
			if test.raw != nil {
				reader = bytes.NewReader(test.raw)
			}
			node, err := builder.writeRawMessageFrame(t.Context(), reader, int64(len(test.raw)))
			if err != nil {
				t.Fatal("constructor unexpectedly validated JSON", err)
			}
			var output bytes.Buffer
			stream := &logicalJSONStream{
				ctx: t.Context(), view: &WebhookAggregateRead{},
				out: bufio.NewWriterSize(&output, 64<<10),
			}
			actualErr := stream.write(webhookJSONValue{node: node}, 0)
			if actualErr == nil {
				actualErr = stream.out.Flush()
			}
			expected, expectedErr := json.Marshal(test.raw)
			if (actualErr != nil) != (expectedErr != nil) {
				t.Fatal("existing raw JSON path changed native marshal success/error behavior", actualErr, expectedErr)
			}
			if actualErr == nil && !bytes.Equal(output.Bytes(), expected) {
				t.Fatal("existing raw JSON path changed compaction, HTML escaping or RawMessage encoding")
			}
		})
	}
}
