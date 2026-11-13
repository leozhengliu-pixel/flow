package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type hydratedURLSigning struct {
	key     []byte
	base    string
	expires time.Time
	encoded bool
}

type hydratedURLSigningKey struct{}

func hydratedSigningContext(ctx context.Context, w http.ResponseWriter) context.Context {
	for w != nil {
		spool, isSpool := w.(*legacyResponseSpool)
		if isSpool && spool.ctx != nil {
			if admitted, _ := spool.ctx.Value(hydratedLegacyDownloadKey{}).(bool); admitted {
				ctx = context.WithValue(ctx, hydratedLegacyDownloadKey{}, true)
			}
		}
		if isSpool && spool.signing != nil {
			if spool.prepareSigning != nil {
				spool.prepareSigning()
			}
			if spool.signing == nil {
				return ctx
			}
			spool.signing.encoded = true
			return context.WithValue(ctx, hydratedURLSigningKey{}, spool.signing)
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = unwrapper.Unwrap()
	}
	return ctx
}

// Quoted replacements start and end with a quote, which is a delimiter for
// the text pass. Therefore each unchanged range can run through the text pass
// independently; the generated signed path contains no /uploads/ component.
// This composes the legacy regex passes without an intermediate response file.
func rewriteHydratedURLSource(ctx context.Context, source io.ReaderAt, size int64, dst io.Writer, signing *hydratedURLSigning) error {
	reader := bufio.NewReaderSize(nil, 32<<10)
	output := bufio.NewWriterSize(dst, 32<<10)
	scratch := make([]byte, 32<<10)
	text := func(start, end int64) error {
		return rewriteUploadURLSourcePass(ctx, io.NewSectionReader(source, start, end-start), end-start, output, uploadURLText, false, signing.key, signing.base, signing.expires)
	}
	var offset int64
	for offset < size {
		reader.Reset(legacyContextReader{ctx, io.NewSectionReader(source, offset, size-offset)})
		match := uploadURLValue.FindReaderSubmatchIndex(reader)
		if err := ctx.Err(); err != nil {
			return err
		}
		if match == nil {
			break
		}
		start, end := offset+int64(match[0]), offset+int64(match[1])
		if err := text(offset, start); err != nil {
			return err
		}
		output.WriteByte('"')
		if err := writeSignedURLSection(ctx, output, source, offset+int64(match[4]), offset+int64(match[5]), signing.key, signing.base, signing.expires, scratch); err != nil {
			return err
		}
		output.WriteByte('"')
		offset = end
	}
	if err := text(offset, size); err != nil {
		return err
	}
	return output.Flush()
}

func rewriteHydratedJSONSource(ctx context.Context, source interface {
	io.ReaderAt
	Stat() (os.FileInfo, error)
}, dst io.Writer, signing *hydratedURLSigning) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	return rewriteHydratedURLSource(ctx, source, info.Size(), dst, signing)
}

type hydratedStringCheckpoint struct {
	input, output int
}

// A seekable view of an existing string's JSON encoding. Checkpoints let the
// two regex passes revisit ranges without allocating an encoded string or
// writing even a giant URL/filename to temporary storage.
type hydratedStringSource struct {
	value       string
	raw         []byte
	quoted      bool
	checkpoints []hydratedStringCheckpoint
	size        int
}

func newHydratedStringSource(value string) *hydratedStringSource {
	source := &hydratedStringSource{value: value, quoted: true, size: 2}
	source.checkpoints = append(source.checkpoints, hydratedStringCheckpoint{output: 1})
	source.index()
	return source
}

func newHydratedRawStringSource(raw []byte) *hydratedStringSource {
	source := &hydratedStringSource{raw: raw}
	source.checkpoints = append(source.checkpoints, hydratedStringCheckpoint{})
	source.index()
	return source
}

func (source *hydratedStringSource) length() int {
	if source.raw != nil {
		return len(source.raw)
	}
	return len(source.value)
}

func (source *hydratedStringSource) index() {
	last := 0
	for i := 0; i < source.length(); {
		if i-last >= 16<<10 {
			output := source.size
			if source.quoted {
				output--
			}
			source.checkpoints = append(source.checkpoints, hydratedStringCheckpoint{input: i, output: output})
			last = i
		}
		unit, n := source.unit(i)
		if unit == "" {
			source.size += n
		} else {
			source.size += len(unit)
		}
		i += n
	}
}

func (s *hydratedStringSource) unit(i int) (string, int) {
	if s.raw != nil && !s.quoted {
		switch s.raw[i] {
		case '<', '>', '&':
			return hydratedASCIIQuotes[s.raw[i]], 1
		case 0xe2:
			if i+2 < len(s.raw) && s.raw[i+1] == 0x80 {
				if s.raw[i+2] == 0xa8 {
					return `\u2028`, 3
				}
				if s.raw[i+2] == 0xa9 {
					return `\u2029`, 3
				}
			}
		}
		return "", 1
	}
	var c byte
	if s.raw != nil {
		c = s.raw[i]
	} else {
		c = s.value[i]
	}
	if c < utf8.RuneSelf {
		if escaped := hydratedASCIIQuotes[c]; escaped != "" {
			return escaped, 1
		}
		return "", 1
	}
	var r rune
	var n int
	if s.raw != nil {
		r, n = utf8.DecodeRune(s.raw[i:])
	} else {
		r, n = utf8.DecodeRuneInString(s.value[i:])
	}
	switch {
	case r == utf8.RuneError && n == 1:
		return `\ufffd`, 1
	case r == '\u2028':
		return `\u2028`, n
	case r == '\u2029':
		return `\u2029`, n
	}
	return "", n
}

func (s *hydratedStringSource) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if offset >= int64(s.size) {
		return 0, io.EOF
	}
	start := int(offset)
	n := 0
	if s.quoted && start == 0 && len(p) > 0 {
		p[0], n, start = '"', 1, 1
	}
	index := sort.Search(len(s.checkpoints), func(i int) bool { return s.checkpoints[i].output > start }) - 1
	point := s.checkpoints[max(0, index)]
	for i, output := point.input, point.output; i < s.length() && n < len(p); {
		unit, width := s.unit(i)
		length := len(unit)
		if unit == "" {
			length = width
		}
		if end := output + length; end > start {
			var copied int
			if unit != "" {
				copied = copy(p[n:], unit[max(0, start-output):])
			} else if s.raw != nil {
				copied = copy(p[n:], s.raw[i+max(0, start-output):i+width])
			} else {
				copied = copy(p[n:], s.value[i+max(0, start-output):i+width])
			}
			n += copied
			start += copied
		}
		output += length
		i += width
	}
	if s.quoted && n < len(p) && start == s.size-1 {
		p[n] = '"'
		n++
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func encodeHydratedSignedString(ctx context.Context, buffer *bufio.Writer, value string) (bool, error) {
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	if signing == nil || signing.key == nil || !strings.Contains(value, "/uploads/") {
		return false, nil
	}
	source := newHydratedStringSource(value)
	// Avoid applying signing again to the generated URL prefix and query.
	ctx = context.WithValue(ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
	return true, rewriteHydratedURLSource(ctx, source, int64(source.size), buffer, signing)
}

func encodeHydratedSignedByteString(ctx context.Context, buffer *bufio.Writer, value []byte) (bool, error) {
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	if signing == nil || signing.key == nil || !bytes.Contains(value, []byte("/uploads/")) {
		return false, nil
	}
	source := &hydratedStringSource{raw: value, quoted: true, size: 2, checkpoints: []hydratedStringCheckpoint{{output: 1}}}
	source.index()
	ctx = context.WithValue(ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
	return true, rewriteHydratedURLSource(ctx, source, int64(source.size), buffer, signing)
}

type hydratedBase64Source struct{ raw []byte }

func (s hydratedBase64Source) ReadAt(p []byte, offset int64) (int, error) {
	size := base64.StdEncoding.EncodedLen(len(s.raw))
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if offset >= int64(size+2) {
		return 0, io.EOF
	}
	n := 0
	if offset == 0 && len(p) > 0 {
		p[0], n, offset = '"', 1, 1
	}
	var encoded [32 << 10]byte
	for n < len(p) && offset <= int64(size) {
		index := int(offset) - 1
		start := index / 4 * 3
		end := min(start+len(encoded)/4*3, len(s.raw))
		length := base64.StdEncoding.EncodedLen(end - start)
		base64.StdEncoding.Encode(encoded[:length], s.raw[start:end])
		copied := copy(p[n:], encoded[index%4:length])
		n += copied
		offset += int64(copied)
	}
	if n < len(p) && offset == int64(size+1) {
		p[n] = '"'
		n++
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func encodeHydratedSignedBase64(ctx context.Context, buffer *bufio.Writer, raw []byte) (bool, error) {
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	if signing == nil || signing.key == nil {
		return false, nil
	}
	ctx = context.WithValue(ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
	return true, rewriteHydratedURLSource(ctx, hydratedBase64Source{raw}, int64(2+base64.StdEncoding.EncodedLen(len(raw))), buffer, signing)
}

func encodeHydratedSignedRaw(ctx context.Context, buffer *bufio.Writer, raw []byte) (bool, error) {
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	if signing == nil || signing.key == nil || !bytes.Contains(raw, []byte("/uploads/")) {
		return false, nil
	}
	// Preserve RawMessage's escaping and number spelling. Each token is a
	// seekable view of resident bytes, including arbitrarily large strings.
	for i := 0; i < len(raw); {
		if i%(16<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return true, err
			}
		}
		if raw[i] != '"' {
			if raw[i] != ' ' && raw[i] != '\n' && raw[i] != '\r' && raw[i] != '\t' {
				buffer.WriteByte(raw[i])
			}
			i++
			continue
		}
		end := i + 1
		for end < len(raw) {
			if end%(16<<10) == 0 {
				if err := ctx.Err(); err != nil {
					return true, err
				}
			}
			if raw[end] == '\\' {
				end += 2
				continue
			}
			if raw[end] == '"' {
				end++
				break
			}
			end++
		}
		source := newHydratedRawStringSource(raw[i:end])
		unsigned := context.WithValue(ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
		if err := rewriteHydratedURLSource(unsigned, source, int64(source.size), buffer, signing); err != nil {
			return true, err
		}
		i = end
	}
	return true, nil
}

func writeHydratedEncodedJSON(ctx context.Context, buffer *bufio.Writer, raw []byte) error {
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	if signing == nil || signing.key == nil {
		_, err := buffer.Write(raw)
		return err
	}
	ctx = context.WithValue(ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
	return rewriteHydratedURLSource(ctx, bytes.NewReader(raw), int64(len(raw)), buffer, signing)
}

func writeHydratedSignedJSON(w http.ResponseWriter, status int, value any) bool {
	if status != http.StatusOK && status != http.StatusCreated {
		return false
	}
	ctx := hydratedSigningContext(context.Background(), w)
	if ctx.Value(hydratedURLSigningKey{}) == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buffer := bufio.NewWriterSize(w, 64<<10)
	if err := encodeHydratedJSON(ctx, buffer, reflect.ValueOf(value), 0); err != nil {
		panic(http.ErrAbortHandler)
	}
	buffer.WriteByte('\n')
	if err := buffer.Flush(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return true
}
