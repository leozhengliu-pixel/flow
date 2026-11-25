package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// scaleP21SW228Chunks returns its bytes in reads of cycling small sizes, so
// buffered runs end inside escapes and multi-byte sequences.
type scaleP21SW228Chunks struct {
	data  []byte
	sizes []int
	next  int
}

func (r *scaleP21SW228Chunks) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	size := min(len(p), len(r.data), r.sizes[r.next%len(r.sizes)])
	r.next++
	n := copy(p, r.data[:size])
	r.data = r.data[n:]
	return n, nil
}

// TestScaleP21SW228JSONRuns pins the run-based string scanner and raw
// compactor to their byte-at-a-time contracts: the scanner accepts exactly
// json.Valid's strings (offsets included) and the compactor writes exactly
// json.Marshal's bytes for a RawMessage, however the input is chunked.
func TestScaleP21SW228JSONRuns(t *testing.T) {
	valid := []string{
		`""`, `"plain"`, `"<>&"`, `"\"\\\/\b\f\n\r\t"`, `"ꯍꯍ\u0000"`,
		`"` + strings.Repeat(`<`, 5000) + `"`, `"` + strings.Repeat(`x\"`, 4000) + `"`,
		` { "a" : [ 1 , "<>&" , "` + "  " + `" , true , null ] , "b\\" : "é` + "\xe2\x80" + `x" } `,
		`{"s":"` + strings.Repeat(" <", 3000) + `","t":"\\ "}`,
		`["\\\\","\\\"<",` + "\n\t\r" + `"` + "\xff\xe2" + `"]`,
		`"` + strings.Repeat("\xe2\x80\xa9", 7000) + `"`,
	}
	invalid := []string{
		`"\x"`, `"\u12G4"`, `"\u12"`, `"` + "\x01" + `"`, `"unterminated`, `"\`, `"\u`,
		`"` + strings.Repeat(`<`, 3000) + `\q"`, `"` + strings.Repeat("ab", 3000) + "\n" + `"`,
	}
	chunkings := [][]int{{1 << 20}, {1}, {2, 3, 5, 7}, {4093, 6, 1, 11}, {16383, 5}}
	for _, input := range append(valid, invalid...) {
		for _, sizes := range chunkings {
			reader := &scaleP21SW228Chunks{data: []byte(input), sizes: sizes}
			scanner := metadataCollectionScanner{ctx: t.Context(), input: bufio.NewReaderSize(reader, metadataRecordChunkBytes)}
			_, err := scanner.value(0)
			if err == nil {
				err = scanner.end()
			}
			if (err == nil) != json.Valid([]byte(input)) {
				t.Fatalf("scanner acceptance differs from json.Valid for %.40q (chunks %v): %v", input, sizes, err)
			}
			if err == nil && scanner.offset != int64(len(input)) {
				t.Fatalf("scanner offset %d, want %d", scanner.offset, len(input))
			}
			if !json.Valid([]byte(input)) {
				continue
			}
			want, err := json.Marshal(json.RawMessage(input))
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			stream := &logicalJSONStream{ctx: t.Context(), out: bufio.NewWriterSize(&got, 64)}
			if err := stream.compactRaw(&scaleP21SW228Chunks{data: []byte(input), sizes: sizes}); err != nil {
				t.Fatal(err)
			}
			if err := stream.out.Flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("compactor differs from json.Marshal for %.40q (chunks %v)", input, sizes)
			}
		}
	}
	reader := &scaleP21SW228Chunks{data: []byte(`"` + strings.Repeat("x", 1<<20) + `"`), sizes: []int{4096}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := &logicalJSONStream{ctx: &scaleP21SW205CancelContext{Context: ctx, cancel: cancel, at: 2}, out: bufio.NewWriter(io.Discard)}
	if err := stream.compactRaw(reader); !errors.Is(err, context.Canceled) {
		t.Fatal("compactor stopped observing cancellation", err)
	}
}
