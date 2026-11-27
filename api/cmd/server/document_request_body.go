package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	appconfig "flow/api/internal/config"
	"flow/api/internal/store"
)

// Document request bodies (#264 follow-up).
//
// A document's full content travels in the body of its create and update
// requests: Markdown in content and, from the collaborative editor's saves,
// the rich-text tree in contentData and the Yjs state in contentState as
// well, so the body of an editor save is about three times the text. Other
// JSON requests stay under the 16 MiB of requestBodyBudget; these two routes
// accept up to the document limit instead:
//
//   - server.documents.requestBodyMaxBytes in the config file (no
//     environment variable); unset, the largest record the metadata work
//     budget admits (store.MetadataRecordBytesLimit), never less than 16 MiB;
//   - a body over the limit, declared by Content-Length, is answered 413
//     before it is read; a body without Content-Length keeps 16 MiB, since
//     its decode cannot be reserved up front;
//   - a body over 16 MiB reserves documentBodyDecodeWork of the metadata
//     work budget before it is read and releases it once it is decoded;
//     from then on the decoded content is the record the write admits;
//   - the body is decoded once per request, after authentication: the
//     authorization checks that read its ids (peekRequestJSON), the OAuth
//     attribution and the handler share the decoded value. content and
//     contentState are decoded straight from the stream into their strings;
//     only contentData (a JSON tree by nature) and the small members are
//     buffered as JSON before they are decoded.
//
// The 413 message is fixed and nothing of the request is logged.

// documentBodyMaxBytes is server.documents.requestBodyMaxBytes; main sets it
// from the configuration. While it is 0 the limit is derived (documentBodyLimit).
var documentBodyMaxBytes atomic.Int64

const documentBodyTooLargeMessage = "Document request body is larger than this server accepts"

// documentBodyDecodeWork is the metadata work a body of length bytes
// reserves while it is decoded: the decoded strings, the buffered members
// and the tree decoded from them.
func documentBodyDecodeWork(length int64) int64 {
	return min(length, math.MaxInt64/3) * 3
}

var (
	errDocumentBodyTooLarge = errors.New("document request body too large")
	errDocumentBodySyntax   = errors.New("invalid JSON")
)

// documentBodyLimit is the largest body the document create and update
// routes accept.
func (s *server) documentBodyLimit() int64 {
	if limit := documentBodyMaxBytes.Load(); limit > 0 {
		return limit
	}
	limit := jsonRequestLimit()
	if s != nil && s.store != nil {
		limit = max(limit, s.store.MetadataRecordBytesLimit())
	}
	return min(limit, appconfig.MaxDocumentRequestBodyMaxBytes)
}

// documentBodyRoute reports the routes whose body is a document's content:
// POST /api/documents and PATCH /api/documents/{id}, matched on the escaped
// path as the mux matches them.
func documentBodyRoute(r *http.Request) bool {
	path := r.URL.EscapedPath()
	switch r.Method {
	case http.MethodPost:
		return path == "/api/documents"
	case http.MethodPatch:
		id, ok := strings.CutPrefix(path, "/api/documents/")
		return ok && id != "" && !strings.Contains(id, "/")
	}
	return false
}

// documentRequestBody is the body of one document request, decoded at most
// once by the first reader that needs it.
type documentRequestBody struct {
	server *server
	w      http.ResponseWriter
	body   io.ReadCloser
	length int64

	once  sync.Once
	input documentInput
	// fields are the members other than content, contentState and
	// contentData, as a JSON object, for peeks into other types.
	fields []byte
	err    error
}

type documentRequestBodyKey struct{}

func withDocumentRequestBody(s *server, w http.ResponseWriter, r *http.Request) *http.Request {
	body := &documentRequestBody{server: s, w: w, body: r.Body, length: r.ContentLength}
	return r.WithContext(context.WithValue(r.Context(), documentRequestBodyKey{}, body))
}

func requestDocumentBody(r *http.Request) *documentRequestBody {
	body, _ := r.Context().Value(documentRequestBodyKey{}).(*documentRequestBody)
	return body
}

// documentRequestBodies refuses a document body declared over the limit
// before anything reads it and gives the request its shared decoder.
func (s *server) documentRequestBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !documentBodyRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > s.documentBodyLimit() {
			writeError(w, http.StatusRequestEntityTooLarge, documentBodyTooLargeMessage)
			return
		}
		next.ServeHTTP(w, withDocumentRequestBody(s, w, r))
	})
}

func (b *documentRequestBody) decode(ctx context.Context) error {
	b.once.Do(func() {
		b.fields, b.err = b.server.readDocumentBody(ctx, b.w, b.body, b.length, &b.input)
		b.body = nil
	})
	return b.err
}

// peek reads the decoded body into value as peekRequestJSON does: a
// documentInput gets the whole input, another type the small members.
func (b *documentRequestBody) peek(ctx context.Context, value any) bool {
	if b.decode(ctx) != nil {
		return false
	}
	if input, ok := value.(*documentInput); ok {
		*input = b.input
		return true
	}
	return json.Unmarshal(b.fields, value) == nil
}

// decodeDocumentBody is decodeJSON for the document create and update
// handlers. It reuses the body the request's authorization decoded.
func (s *server) decodeDocumentBody(w http.ResponseWriter, r *http.Request, input *documentInput) bool {
	body := requestDocumentBody(r)
	if body == nil {
		body = &documentRequestBody{server: s, w: w, body: r.Body, length: r.ContentLength}
	}
	if err := body.decode(r.Context()); err != nil {
		writeDocumentBodyError(w, err)
		return false
	}
	*input = body.input
	return true
}

// decodeRequestDocumentBody decodes the request's document body, if it has
// one, and answers its error.
func decodeRequestDocumentBody(w http.ResponseWriter, r *http.Request) bool {
	body := requestDocumentBody(r)
	if body == nil {
		return true
	}
	if err := body.decode(r.Context()); err != nil {
		writeDocumentBodyError(w, err)
		return false
	}
	return true
}

func writeDocumentBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.Is(err, errDocumentBodyTooLarge) || errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, documentBodyTooLargeMessage)
		return
	}
	// Other work holds the budget the decode needs: 503 + Retry-After.
	if writeStoreBusy(w, err) || writeRequestCanceled(w, err) {
		return
	}
	switch {
	case errors.Is(err, store.ErrMetadataWorkBudget):
		// The decode needs more than the whole work budget holds.
		writeError(w, http.StatusRequestEntityTooLarge, documentBodyTooLargeMessage)
	case errors.Is(err, context.DeadlineExceeded):
		setRetryAfter(w, busyDeadlineRetryAfter)
		writeError(w, http.StatusServiceUnavailable, databaseUnavailableMessage)
	default:
		writeError(w, http.StatusBadRequest, "invalid JSON")
	}
}

// readDocumentBody checks the body against the limit, reserves its decode
// when it is over the general JSON limit and decodes it into input.
func (s *server) readDocumentBody(ctx context.Context, w http.ResponseWriter, body io.ReadCloser, length int64, input *documentInput) ([]byte, error) {
	general, limit := jsonRequestLimit(), s.documentBodyLimit()
	if length < 0 {
		limit = min(limit, general)
	}
	if length > limit {
		return nil, errDocumentBodyTooLarge
	}
	if body == nil {
		body = http.NoBody
	}
	if length > general {
		var repo *store.SQLiteStore
		if s != nil {
			repo = s.store
		}
		release, err := repo.AdmitRequestBodyWork(ctx, documentBodyDecodeWork(length))
		if err != nil {
			return nil, err
		}
		defer release()
	}
	return decodeDocumentObject(&jsonNULReader{source: http.MaxBytesReader(w, body, limit)}, input)
}

// decodeDocumentObject decodes one JSON document request object from src
// into input, as encoding/json would, and returns
// its small members as a JSON object. content and contentState strings are
// decoded from the stream into their fields; contentData and the other
// members are buffered as JSON and decoded with encoding/json. One of
// content, contentState or contentData named twice is refused.
func decodeDocumentObject(src io.Reader, input *documentInput) ([]byte, error) {
	stream := &jsonValueStream{r: bufio.NewReaderSize(src, 64<<10)}
	first, err := stream.nextByte()
	if err != nil {
		return nil, err
	}
	if first != '{' {
		// Not an object (null, a list...): encoding/json decides.
		var raw bytes.Buffer
		if err := stream.r.UnreadByte(); err != nil {
			return nil, err
		}
		if err := stream.copyValue(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw.Bytes(), input) != nil {
			return nil, errDocumentBodySyntax
		}
		return raw.Bytes(), nil
	}
	streamedStrings := map[string]**string{"content": &input.Content, "contentState": &input.ContentState}
	seen := map[string]bool{}
	var fields, contentData, key bytes.Buffer
	fields.WriteByte('{')
	c, err := stream.nextByte()
	for err == nil && c != '}' {
		if c != '"' {
			return nil, errDocumentBodySyntax
		}
		key.Reset()
		key.WriteByte('"')
		if err := stream.copyString(&key); err != nil {
			return nil, err
		}
		var name string
		if json.Unmarshal(key.Bytes(), &name) != nil {
			return nil, errDocumentBodySyntax
		}
		if c, err = stream.nextByte(); err != nil {
			return nil, err
		}
		if c != ':' {
			return nil, errDocumentBodySyntax
		}
		if c, err = stream.nextByte(); err != nil {
			return nil, err
		}
		field, streamed := streamedStrings[name]
		if streamed || name == "contentData" {
			if seen[name] {
				return nil, errDocumentBodySyntax
			}
			seen[name] = true
		}
		switch {
		case streamed && c == '"':
			value, err := stream.readString()
			if err != nil {
				return nil, err
			}
			*field = &value
		case name == "contentData":
			if err := stream.r.UnreadByte(); err != nil {
				return nil, err
			}
			if err := stream.copyValue(&contentData); err != nil {
				return nil, err
			}
		default:
			if fields.Len() > 1 {
				fields.WriteByte(',')
			}
			fields.Write(key.Bytes())
			fields.WriteByte(':')
			if err := stream.r.UnreadByte(); err != nil {
				return nil, err
			}
			if err := stream.copyValue(&fields); err != nil {
				return nil, err
			}
		}
		if c, err = stream.nextByte(); err != nil {
			return nil, err
		}
		if c == ',' {
			c, err = stream.nextByte()
			if err == nil && c == '}' {
				return nil, errDocumentBodySyntax
			}
		} else if c != '}' {
			return nil, errDocumentBodySyntax
		}
	}
	if err != nil {
		return nil, err
	}
	fields.WriteByte('}')
	if json.Unmarshal(fields.Bytes(), input) != nil {
		return nil, errDocumentBodySyntax
	}
	if seen["contentData"] {
		input.ContentData = nil
		if json.Unmarshal(contentData.Bytes(), &input.ContentData) != nil {
			return nil, errDocumentBodySyntax
		}
	}
	return fields.Bytes(), nil
}

// jsonValueStream reads JSON values from a buffered stream.
type jsonValueStream struct {
	r *bufio.Reader
}

// streamError is a read error as the decoder reports it: the end of the
// stream inside a value is a syntax error, other errors (a body over its
// limit, a broken connection) stay as they are.
func streamError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errDocumentBodySyntax
	}
	return err
}

// nextByte is the next byte that is not JSON whitespace.
func (s *jsonValueStream) nextByte() (byte, error) {
	for {
		c, err := s.r.ReadByte()
		if err != nil {
			return 0, streamError(err)
		}
		if !jsonSpace(c) {
			return c, nil
		}
	}
}

func jsonSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// chunk is the buffered input, filling the buffer when it is empty. At the
// end of the stream it returns the reader's error (io.EOF) unconverted.
func (s *jsonValueStream) chunk() ([]byte, error) {
	if s.r.Buffered() == 0 {
		if _, err := s.r.Peek(1); err != nil {
			return nil, err
		}
	}
	return s.r.Peek(s.r.Buffered())
}

// copyString copies the rest of a string whose opening quote was read, up
// to and including its closing quote.
func (s *jsonValueStream) copyString(dst *bytes.Buffer) error {
	escaped := false
	for {
		chunk, err := s.chunk()
		if err != nil {
			return streamError(err)
		}
		for i, c := range chunk {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				dst.Write(chunk[:i+1])
				_, _ = s.r.Discard(i + 1)
				return nil
			}
		}
		dst.Write(chunk)
		_, _ = s.r.Discard(len(chunk))
	}
}

// copyValue copies the next value's text; whether it is valid JSON is left
// to the decoder that reads the copy.
func (s *jsonValueStream) copyValue(dst *bytes.Buffer) error {
	depth := 0
outer:
	for {
		chunk, err := s.chunk()
		if errors.Is(err, io.EOF) && depth == 0 {
			// A scalar at the end of the stream (a body that is just null).
			return nil
		}
		if err != nil {
			return streamError(err)
		}
		for i, c := range chunk {
			switch {
			case c == '"':
				dst.Write(chunk[:i+1])
				_, _ = s.r.Discard(i + 1)
				if err := s.copyString(dst); err != nil {
					return err
				}
				if depth == 0 {
					return nil
				}
				continue outer
			case c == '{' || c == '[':
				depth++
			case c == '}' || c == ']':
				if depth == 0 {
					// The end of the enclosing object: a scalar ended.
					dst.Write(chunk[:i])
					_, _ = s.r.Discard(i)
					return nil
				}
				if depth--; depth == 0 {
					dst.Write(chunk[:i+1])
					_, _ = s.r.Discard(i + 1)
					return nil
				}
			case depth == 0 && (c == ',' || jsonSpace(c)):
				dst.Write(chunk[:i])
				_, _ = s.r.Discard(i)
				return nil
			}
		}
		dst.Write(chunk)
		_, _ = s.r.Discard(len(chunk))
	}
}

// readString decodes the rest of a string whose opening quote was read, as
// encoding/json does: invalid UTF-8 and unpaired surrogates become U+FFFD.
func (s *jsonValueStream) readString() (string, error) {
	var out stringSegments
	for {
		chunk, err := s.chunk()
		if err != nil {
			return "", streamError(err)
		}
		plain := 0
		for plain < len(chunk) {
			if c := chunk[plain]; c == '"' || c == '\\' || c < 0x20 || c >= utf8.RuneSelf {
				break
			}
			plain++
		}
		out.write(chunk[:plain])
		if plain == len(chunk) {
			_, _ = s.r.Discard(plain)
			continue
		}
		c := chunk[plain]
		_, _ = s.r.Discard(plain)
		switch {
		case c == '"':
			_, _ = s.r.Discard(1)
			return out.String(), nil
		case c == '\\':
			_, _ = s.r.Discard(1)
			if err := s.readEscape(&out); err != nil {
				return "", err
			}
		case c < 0x20:
			return "", errDocumentBodySyntax
		default:
			r, _, err := s.r.ReadRune()
			if err != nil {
				return "", streamError(err)
			}
			out.writeRune(r)
		}
	}
}

func (s *jsonValueStream) readEscape(out *stringSegments) error {
	c, err := s.r.ReadByte()
	if err != nil {
		return streamError(err)
	}
	switch c {
	case '"', '\\', '/':
		out.writeByte(c)
	case 'b':
		out.writeByte('\b')
	case 'f':
		out.writeByte('\f')
	case 'n':
		out.writeByte('\n')
	case 'r':
		out.writeByte('\r')
	case 't':
		out.writeByte('\t')
	case 'u':
		hex, err := s.r.Peek(4)
		if err != nil {
			return streamError(err)
		}
		r, ok := hexRune(hex)
		if !ok {
			return errDocumentBodySyntax
		}
		_, _ = s.r.Discard(4)
		if utf16.IsSurrogate(r) {
			// A pair decodes to one rune; an unpaired half is U+FFFD and
			// the escape after it is read on its own.
			if next, err := s.r.Peek(6); err == nil && next[0] == '\\' && next[1] == 'u' {
				if low, ok := hexRune(next[2:]); ok {
					if pair := utf16.DecodeRune(r, low); pair != unicode.ReplacementChar {
						_, _ = s.r.Discard(6)
						out.writeRune(pair)
						return nil
					}
				}
			}
			r = unicode.ReplacementChar
		}
		out.writeRune(r)
	default:
		return errDocumentBodySyntax
	}
	return nil
}

func hexRune(hex []byte) (rune, bool) {
	var r rune
	for _, c := range hex[:4] {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, false
		}
		r = r*16 + rune(c)
	}
	return r, true
}

// stringSegments collects a decoded string in segments of at most 1 MiB and
// joins them once into a string of exactly its length, so a long string
// neither keeps the spare capacity of a doubling buffer nor needs its length
// in advance.
type stringSegments struct {
	done  [][]byte
	cur   []byte
	total int
}

func (s *stringSegments) write(p []byte) {
	for len(p) > 0 {
		if len(s.cur) == cap(s.cur) {
			if s.cur != nil {
				s.done = append(s.done, s.cur)
			}
			s.cur = make([]byte, 0, min(max(2*cap(s.cur), 4<<10), 1<<20))
		}
		n := min(len(p), cap(s.cur)-len(s.cur))
		s.cur = append(s.cur, p[:n]...)
		s.total += n
		p = p[n:]
	}
}

func (s *stringSegments) writeByte(c byte) {
	s.write([]byte{c})
}

func (s *stringSegments) writeRune(r rune) {
	var buf [utf8.UTFMax]byte
	s.write(buf[:utf8.EncodeRune(buf[:], r)])
}

func (s *stringSegments) String() string {
	var out strings.Builder
	out.Grow(s.total)
	for i, segment := range s.done {
		out.Write(segment)
		s.done[i] = nil
	}
	out.Write(s.cur)
	s.done, s.cur = nil, nil
	return out.String()
}
