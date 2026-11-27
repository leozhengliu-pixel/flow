package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// jsonStringUnitReference is the output of a metadataJSONStringSource as the
// source computed it before runs were copied in bulk: one escape unit or one
// verbatim UTF-8 sequence (quoted) or byte (compact raw) at a time.
func jsonStringUnitReference(raw []byte, mode metadataJSONStringMode) []byte {
	var out []byte
	if mode == metadataJSONStringQuoted {
		out = append(out, '"')
	}
	quoted, escaped := false, false
	for offset := 0; offset < len(raw); {
		unit, width := "", 1
		if mode == metadataJSONStringCompactRaw {
			char := raw[offset]
			if !quoted && metadataJSONStringSpace(char) {
				offset++
				continue
			}
			quoted, escaped = metadataJSONStringState(char, quoted, escaped)
			switch char {
			case '<', '>', '&':
				unit = metadataJSONStringEscapes[char]
			case 0xe2:
				if len(raw)-offset >= 3 && raw[offset+1] == 0x80 && raw[offset+2] == 0xa8 {
					unit, width = string([]byte{'\\', 'u', '2', '0', '2', '8'}), 3
				} else if len(raw)-offset >= 3 && raw[offset+1] == 0x80 && raw[offset+2] == 0xa9 {
					unit, width = string([]byte{'\\', 'u', '2', '0', '2', '9'}), 3
				}
			}
		} else if raw[offset] < utf8.RuneSelf {
			unit = metadataJSONStringEscapes[raw[offset]]
		} else {
			value, size := utf8.DecodeRune(raw[offset:min(len(raw), offset+utf8.UTFMax)])
			width = size
			switch {
			case value == utf8.RuneError && size == 1:
				unit = string([]byte{'\\', 'u', 'f', 'f', 'f', 'd'})
			case value == 0x2028:
				unit = string([]byte{'\\', 'u', '2', '0', '2', '8'})
			case value == 0x2029:
				unit = string([]byte{'\\', 'u', '2', '0', '2', '9'})
			}
		}
		if unit != "" {
			out = append(out, unit...)
		} else {
			out = append(out, raw[offset:offset+width]...)
		}
		offset += width
	}
	if mode == metadataJSONStringQuoted {
		out = append(out, '"')
	}
	return out
}

var (
	jsonStringBulkLineSeparator      = string(rune(0x2028))
	jsonStringBulkParagraphSeparator = string(rune(0x2029))
	jsonStringBulkEmoji              = string(rune(0x1f600))
	jsonStringBulkCJK                = string([]rune{0x4e2d, 0x6587, 0x6587, 0x6863})
)

// jsonStringBulkText is decoded string content: ASCII, escapes, CJK, emoji,
// separators, the upload marker and, unless valid, invalid UTF-8, in pieces
// placed so that some cross window, checkpoint and read boundaries.
func jsonStringBulkText(random *rand.Rand, size int, valid bool) []byte {
	pieces := []string{
		"plain words ", "x", "\n", "\t", "\r\n", "\"", "\\", "<", ">", "&", "\x00", "\x1f", "\b", "\f",
		jsonStringBulkCJK, jsonStringBulkEmoji, jsonStringBulkLineSeparator, jsonStringBulkParagraphSeparator,
		"/uploads/", "/upload", "s/", "/", string(rune(0xe9)), string(rune(0x2027)), string(rune(0x202a)),
	}
	if !valid {
		pieces = append(pieces, "\xff", "\xe2\x80", "\xe2", "\xed\xa0\x80", "\xf0\x9f\x98", "\xc0\xaf")
	}
	var out bytes.Buffer
	for out.Len() < size {
		switch random.Intn(12) {
		case 0:
			// A very long line of ASCII.
			out.WriteString(strings.Repeat("a long line ", 2000+random.Intn(20000)))
		case 1:
			out.WriteString(strings.Repeat(jsonStringBulkCJK, 1000+random.Intn(5000)))
		case 2:
			// Up to a checkpoint or window edge, then something special.
			for _, edge := range []int{metadataJSONStringCheckpointBytes, metadataJSONStringBufferBytes, 4 * metadataJSONStringBufferBytes} {
				if pad := edge - out.Len()%edge - 1 - random.Intn(4); pad > 0 && pad < edge {
					out.WriteString(strings.Repeat("p", pad))
					out.WriteString(pieces[random.Intn(len(pieces))])
					break
				}
			}
		default:
			out.WriteString(pieces[random.Intn(len(pieces))])
		}
	}
	return out.Bytes()
}

// jsonStringBulkRawJSON encodes text as a JSON document with insignificant
// whitespace, leaving <, >, &, U+2028, U+2029 and non-ASCII unescaped and
// spelling some characters as \u escapes and surrogate pairs.
func jsonStringBulkRawJSON(random *rand.Rand, text []byte) []byte {
	var out bytes.Buffer
	out.WriteString(" {\n  \"key\" : [ 1 , true,null, \"")
	for index, char := range string(text) {
		switch {
		case char == '"':
			out.WriteString(`\"`)
		case char == '\\':
			out.WriteString(`\\`)
		case char == '\n':
			out.WriteString(`\n`)
		case char < 0x20:
			out.WriteString(`\u00`)
			out.WriteByte("0123456789abcdef"[char>>4])
			out.WriteByte("0123456789abcdef"[char&15])
		case char == 0x1f600 && index%3 == 0:
			out.WriteString(`\ud83d\ude00`)
		case char == 0xe9 && index%2 == 0:
			out.WriteString(`\u00e9`)
		case char == '/' && index%5 == 0:
			out.WriteString(`\/`)
		default:
			out.WriteRune(char)
		}
		if random.Intn(50000) == 0 {
			out.WriteString("\" ,\n\t\"")
		}
	}
	out.WriteString("\" ] ,\r\n \"after\":\"<&>\" }\n ")
	return out.Bytes()
}

func jsonStringBulkInputs(t *testing.T) map[string][2][]byte {
	t.Helper()
	random := rand.New(rand.NewSource(2026))
	size := 3 * 4 * metadataJSONStringBufferBytes
	if raceDetector {
		size = 4*metadataJSONStringBufferBytes + 4*metadataJSONStringBufferBytes/2
	}
	inputs := map[string][2][]byte{}
	add := func(name string, text []byte) {
		inputs[name] = [2][]byte{text, jsonStringBulkRawJSON(random, text)}
	}
	add("empty", nil)
	add("ascii", []byte(strings.Repeat("ascii only text ", size/16)))
	add("ascii long line", []byte(strings.Repeat("x", size)))
	add("unicode", []byte(strings.Repeat(jsonStringBulkCJK+jsonStringBulkEmoji+string(rune(0xe9)), size/20)))
	add("separators", []byte(strings.Repeat("a"+jsonStringBulkLineSeparator+jsonStringBulkParagraphSeparator, size/7)))
	add("escapes", []byte(strings.Repeat("<a href=\"x\">&amp;</a>\n\t\\", size/24)))
	add("mixed", jsonStringBulkText(random, size, true))
	add("marker at edges", []byte(strings.Repeat("y", metadataJSONStringCheckpointBytes-4)+"/uploads/"+strings.Repeat("z", 4*metadataJSONStringBufferBytes-metadataJSONStringCheckpointBytes-6)+"/uploads/"))
	add("near marker", []byte(strings.Repeat("/upload/uploads", size/15)))
	inputs["invalid utf8"] = [2][]byte{jsonStringBulkText(random, size, false), nil}
	return inputs
}

func jsonStringBulkCompare(t *testing.T, name string, raw []byte, mode metadataJSONStringMode) {
	t.Helper()
	want := jsonStringUnitReference(raw, mode)
	if mode == metadataJSONStringCompactRaw || utf8.Valid(raw) {
		if oracle := scaleP21SW144Expected(t, raw, mode); !bytes.Equal(oracle, want) {
			t.Fatalf("%s: the unit reference differs from encoding/json", name)
		}
	}
	_, owner := scaleP21SW144Owner(t)
	defer owner.Close()
	source, err := newMetadataJSONStringSource(t.Context(), owner, bytes.NewReader(raw), int64(len(raw)), mode)
	if err != nil {
		t.Fatal(name, err)
	}
	defer source.Close()
	if source.Size() != int64(len(want)) || source.ContainsUploads() != bytes.Contains(want, []byte("/uploads/")) {
		t.Fatalf("%s mode %d: size %d uploads %v, want %d %v", name, mode, source.Size(), source.ContainsUploads(), len(want), bytes.Contains(want, []byte("/uploads/")))
	}
	if len(source.checkpoints) > len(raw)/metadataJSONStringCheckpointBytes+2 {
		t.Fatalf("%s: %d checkpoints for %d bytes", name, len(source.checkpoints), len(raw))
	}
	read := func(chunk int, reverse bool) {
		got := make([]byte, len(want))
		var offsets []int
		for offset := 0; offset < len(want); offset += chunk {
			offsets = append(offsets, offset)
		}
		if reverse {
			for i, j := 0, len(offsets)-1; i < j; i, j = i+1, j-1 {
				offsets[i], offsets[j] = offsets[j], offsets[i]
			}
		}
		for _, offset := range offsets {
			end := min(len(want), offset+chunk)
			n, err := source.ReadAt(got[offset:end], int64(offset))
			if n != end-offset || (err != nil && !(errors.Is(err, io.EOF) && end == len(want))) {
				t.Fatalf("%s mode %d chunk %d: ReadAt(%d) = %d, %v", name, mode, chunk, offset, n, err)
			}
		}
		if !bytes.Equal(got, want) {
			index := 0
			for index < len(got) && got[index] == want[index] {
				index++
			}
			t.Fatalf("%s mode %d chunk %d reverse %v: output differs at %d of %d", name, mode, chunk, reverse, index, len(want))
		}
	}
	for _, chunk := range []int{metadataRecordChunkBytes, 4093, 100003} {
		read(chunk, false)
	}
	read(metadataRecordChunkBytes, true)
	if len(want) < 4096 {
		read(1, false)
		read(7, true)
	}
	random := rand.New(rand.NewSource(int64(len(raw))))
	expected := bytes.NewReader(want)
	for range 200 {
		scaleP21SW144CompareAt(t, source, expected, random.Int63n(int64(len(want))+2), random.Intn(70000))
	}
}

// The bulk source writes the same bytes, size and upload marker as the unit
// by unit source, for every read pattern.
func TestJSONStringSourceBulkMatchesUnitReference(t *testing.T) {
	for name, input := range jsonStringBulkInputs(t) {
		jsonStringBulkCompare(t, name, input[0], metadataJSONStringQuoted)
		if input[1] != nil {
			jsonStringBulkCompare(t, name, input[1], metadataJSONStringCompactRaw)
		}
	}
	// Small values at every split of special sequences.
	for _, text := range []string{
		"a" + jsonStringBulkLineSeparator, jsonStringBulkParagraphSeparator + "b", "\xe2\x80", "\xe2", "x\xe2\x80\xa8\xe2\x80\xa9y",
		jsonStringBulkEmoji, "\xf0\x9f\x98", "<>&\"\\", "/uploads/", "",
	} {
		jsonStringBulkCompare(t, "small", []byte(text), metadataJSONStringQuoted)
	}
	for _, raw := range []string{
		`"` + jsonStringBulkLineSeparator + `"`, `["\"<", "\\", "\ud83d\ude00", "a\/b"]`, " \"\xe2\x80\xa9\" ", `{"a":"\u2028"}`,
	} {
		jsonStringBulkCompare(t, "small raw", []byte(raw), metadataJSONStringCompactRaw)
	}
}

// legacyProjectCopyStoredJSONReference is the byte by byte NUL escape strip.
func legacyProjectCopyStoredJSONReference(input []byte) ([]byte, bool) {
	reader := bufio.NewReader(bytes.NewReader(input))
	var out bytes.Buffer
	inString := false
	for {
		char, err := reader.ReadByte()
		if err != nil {
			return out.Bytes(), true
		}
		switch {
		case !inString:
			inString = char == '"'
		case char == '"':
			inString = false
		case char == '\\':
			next, err := reader.ReadByte()
			if err != nil {
				// A string that ends inside an escape is an error.
				return nil, false
			}
			if next == 'u' {
				if suffix, _ := reader.Peek(4); bytes.Equal(suffix, []byte("0000")) {
					_, _ = reader.Discard(4)
					continue
				}
			}
			out.WriteByte(char)
			out.WriteByte(next)
			continue
		}
		out.WriteByte(char)
	}
}

func TestLegacyProjectCopyStoredJSONBulkMatchesReference(t *testing.T) {
	random := rand.New(rand.NewSource(197))
	inputs := [][]byte{nil, []byte(`"`), []byte(`"\`), []byte(`"\u00`), []byte(`{"a":"\u0000"}`)}
	for _, input := range jsonStringBulkInputs(t) {
		if input[1] != nil {
			inputs = append(inputs, input[1])
		}
	}
	nul := []byte(`{"d":"` + strings.Repeat(`x\u0000\\u0000\"\u00001`, 3000) + `","e":"\u0000"}`)
	for _, pad := range []int{0, 1, metadataRecordChunkBytes - 7, metadataRecordChunkBytes - 3, metadataRecordChunkBytes - 1} {
		inputs = append(inputs, append([]byte(`{"p":"`+strings.Repeat("p", pad)), nul...))
	}
	for range 20 {
		var raw bytes.Buffer
		for raw.Len() < 3*metadataRecordChunkBytes {
			raw.WriteString([]string{`"`, `\`, `\u0000`, `\u00`, `u0000`, "abc", " ", `\"`, `\\`}[random.Intn(9)])
		}
		inputs = append(inputs, raw.Bytes())
	}
	for _, input := range inputs {
		var output bytes.Buffer
		err := legacyProjectCopyStoredJSON(t.Context(), &output, bytes.NewReader(input))
		want, ok := legacyProjectCopyStoredJSONReference(input)
		if (err == nil) != ok || (ok && !bytes.Equal(output.Bytes(), want)) {
			t.Fatalf("stored JSON copy differs for %d input bytes: %v", len(input), err)
		}
	}
	if err := legacyProjectCopyStoredJSON(canceledContext(), io.Discard, strings.NewReader(`{"a":"b"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("a canceled copy ran", err)
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// A bounded save compares, compacts, copies and measures large records
// through these paths; they once walked the record one character at a time
// (the source through a window read each), 15 times slower than the legacy
// save's encoding/json (26 times under the race detector). Their throughput
// must stay within a small factor of encoding/json on the same bytes, scaled
// for the instrumentation of the build (jsonStringBulkThroughputFactor).
func TestJSONStringSourceBulkThroughput(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	text := jsonStringBulkText(random, 4<<20, true)
	raw := jsonStringBulkRawJSON(random, text)
	measure := func(run func()) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			started := time.Now()
			run()
			best = min(best, time.Since(started))
		}
		return best
	}
	baseline := measure(func() {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatal(err)
		}
		var escaped bytes.Buffer
		json.HTMLEscape(&escaped, compact.Bytes())
		if _, err := json.Marshal(string(text)); err != nil {
			t.Fatal(err)
		}
	})
	bounded := measure(func() {
		_, owner := scaleP21SW144Owner(t)
		defer owner.Close()
		for _, input := range []struct {
			raw  []byte
			mode metadataJSONStringMode
		}{{raw, metadataJSONStringCompactRaw}, {text, metadataJSONStringQuoted}} {
			source, err := newMetadataJSONStringSource(t.Context(), owner, bytes.NewReader(input.raw), int64(len(input.raw)), input.mode)
			if err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, metadataRecordChunkBytes)
			for offset := int64(0); offset < source.Size(); offset += int64(len(buffer)) {
				if _, err := source.ReadAt(buffer, offset); err != nil && !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
			}
			source.Close()
		}
		if err := legacyProjectCopyStoredJSON(t.Context(), io.Discard, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := metadataAggregateStringEncodingWork(t.Context(), string(text)); err != nil {
			t.Fatal(err)
		}
	})
	factor := jsonStringBulkThroughputFactor(raceDetector, testing.CoverMode() != "")
	t.Logf("encoding/json %s, bounded string paths %s (%.1fx, bound %dx)", baseline, bounded, float64(bounded)/float64(baseline), factor)
	if bounded > time.Duration(factor)*baseline+50*time.Millisecond {
		t.Fatalf("bounded string paths took %s, more than %dx encoding/json's %s", bounded, factor, baseline)
	}
}

// jsonStringBulkThroughputFactor is the allowed slowdown against
// encoding/json. Coverage counts every basic block of this package's code,
// one counter per byte in the scanning loops, but not encoding/json's; CI
// tests with atomic coverage, whose counters the race detector intercepts as
// synchronised atomics. Measured on the same machine (bulk paths / the per
// character paths they replaced):
//
//	plain 1.2x / 15.7x, -race 1.7x / 27.7x,
//	atomic coverage 3.2x / 31.5x, -race with atomic coverage 12x / 164x
//	(CI: 11.7x).
//
// Each bound sits about a factor of three from both sides.
func jsonStringBulkThroughputFactor(race, cover bool) int {
	switch {
	case race && cover:
		return 40
	case cover:
		return 10
	}
	return 6
}

var (
	jsonStringBulkEmojiEscape    = []byte("\\" + "ud83d" + "\\" + "ude00")
	jsonStringBulkLoneSurrogate  = []byte("\\" + "ud800")
	jsonStringBulkHex            = "0123456789abcdef"
	jsonStringBulkTokenChunkings = [][]int{{1 << 20}, {3, 1, 2}, {4093, 6, 1, 11}, {16383, 5}}
)

// jsonStringBulkToken encodes text as one JSON string token, keeping invalid
// UTF-8 bytes as they are and adding surrogate pair and lone surrogate
// escapes.
func jsonStringBulkToken(text []byte) []byte {
	out := []byte{'"'}
	for index := 0; index < len(text); {
		char, size := utf8.DecodeRune(text[index:])
		switch {
		case char == utf8.RuneError && size == 1:
			out = append(out, text[index])
		case char == '"' || char == '\\':
			out = append(out, '\\', byte(char))
		case char < 0x20:
			out = append(out, '\\', 'u', '0', '0', jsonStringBulkHex[char>>4], jsonStringBulkHex[char&15])
		case char == 0x1f600 && index%3 == 0:
			out = append(out, jsonStringBulkEmojiEscape...)
		case char == 'x' && index%7 == 0:
			out = append(out, jsonStringBulkLoneSurrogate...)
		default:
			out = append(out, text[index:index+size]...)
		}
		index += size
	}
	return append(out, '"')
}

// The run-based string encoder and decoder write what the rune by rune paths
// wrote, at the same chunk boundaries, however their input is split.
func TestJSONStringRunsMatchRunePaths(t *testing.T) {
	for name, input := range jsonStringBulkInputs(t) {
		text := input[0]
		quoted := jsonStringUnitReference(text, metadataJSONStringQuoted)
		token := jsonStringBulkToken(text)
		var decoded string
		if err := json.Unmarshal(token, &decoded); err != nil {
			t.Fatal(name, err)
		}
		for _, sizes := range jsonStringBulkTokenChunkings {
			var got bytes.Buffer
			stream := &logicalJSONStream{ctx: t.Context(), out: bufio.NewWriterSize(&got, 64)}
			if err := stream.stringContent(&scaleP21SW228Chunks{data: text, sizes: sizes}); err != nil {
				t.Fatal(name, err)
			}
			if err := stream.out.Flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), quoted[1:len(quoted)-1]) {
				t.Fatalf("%s chunks %v: string content differs from encoding/json", name, sizes)
			}
			var chunks [][]byte
			null, err := VisitMetadataJSONStringChunks(t.Context(), &scaleP21SW228Chunks{data: token, sizes: sizes}, func(chunk []byte) error {
				chunks = append(chunks, bytes.Clone(chunk))
				return nil
			})
			if err != nil || null {
				t.Fatal(name, null, err)
			}
			if joined := bytes.Join(chunks, nil); string(joined) != decoded {
				t.Fatalf("%s chunks %v: decoded string differs from encoding/json", name, sizes)
			}
			for index, chunk := range chunks {
				if len(chunk) > MetadataJSONStringChunkBytes || !utf8.Valid(chunk) {
					t.Fatalf("%s: chunk %d of %d bytes is oversized or splits a rune", name, index, len(chunk))
				}
				// Chunks are filled up to their last whole rune.
				if index+1 < len(chunks) {
					_, next := utf8.DecodeRune(chunks[index+1])
					if len(chunk)+next <= MetadataJSONStringChunkBytes {
						t.Fatalf("%s chunks %v: chunk %d of %d bytes was flushed early", name, sizes, index, len(chunk))
					}
				}
			}
		}
	}
}
