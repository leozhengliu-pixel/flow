package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"flow/api/internal/store"
)

// NUL (U+0000) has no meaning in any text Flow stores, and PostgreSQL
// refuses it in text and varchar values and in the JSON it parses. Input
// loses it at the boundary, so a title, name, key or body sent with NUL is
// stored (and answered) without it on every database:
//
//   - JSON request bodies read by decodeJSON, decodeStrictJSON, decodeSCIM
//     and /mcp go through jsonNULReader, which drops every \u0000 escape
//     inside a JSON string;
//   - Flow tool arguments (MCP clients, the built-in agent, loops) through
//     stripArgumentNUL in callFlowTool;
//   - query string values through stripQueryNUL;
//   - CSV and JSON import cells in parseImportFile.
//
// Anything else (mail, webhook payloads, a decoder not listed) is the
// store's: it drops NUL from text arguments on PostgreSQL (textArgs) and
// \u0000 from the issue, content, metadata and workspace JSON it writes
// (store.StripJSONNUL).

// stripQueryNUL drops NUL from the query string's values (a search, a
// filter), which SQLite full-text search and PostgreSQL refuse.
func stripQueryNUL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "%00") {
			if values, err := url.ParseQuery(r.URL.RawQuery); err == nil {
				for _, list := range values {
					for i := range list {
						list[i] = store.StripNUL(list[i])
					}
				}
				cleaned, location := *r, *r.URL
				location.RawQuery = values.Encode()
				cleaned.URL = &location
				r = &cleaned
			}
		}
		next.ServeHTTP(w, r)
	})
}

// stripRequestJSONNUL reads r.Body through jsonNULReader.
func stripRequestJSONNUL(r *http.Request) {
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = struct {
			io.Reader
			io.Closer
		}{&jsonNULReader{source: r.Body}, r.Body}
	}
}

// stripArgumentNUL removes NUL from every string in decoded tool arguments
// (nested objects and lists included), in place.
func stripArgumentNUL(args map[string]any) {
	for key, value := range args {
		args[key] = stripValueNUL(value)
	}
}

func stripValueNUL(value any) any {
	switch typed := value.(type) {
	case string:
		return store.StripNUL(typed)
	case map[string]any:
		stripArgumentNUL(typed)
	case []any:
		for i, item := range typed {
			typed[i] = stripValueNUL(item)
		}
	case []string:
		for i, item := range typed {
			typed[i] = store.StripNUL(item)
		}
	}
	return value
}

// jsonNULReader passes a JSON document through without the \u0000 escapes
// in its strings. It keeps the JSON's other bytes as they are (an invalid
// document stays invalid for the decoder to refuse) and holds back at most
// the five bytes of an escape it has not finished reading.
type jsonNULReader struct {
	source   io.Reader
	inString bool
	escape   []byte // a backslash escape being read
	out      []byte // filtered bytes; out[read:] are not yet returned
	read     int
	chunk    [8 << 10]byte
	err      error
}

func (r *jsonNULReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.err != nil {
			if len(r.escape) == 0 {
				return 0, r.err
			}
			r.out, r.escape = append(r.out, r.escape...), r.escape[:0]
			break
		}
		n, err := r.source.Read(r.chunk[:])
		r.err = err
		for _, c := range r.chunk[:n] {
			r.feed(c)
		}
	}
	n := copy(p, r.out[r.read:])
	if r.read += n; r.read == len(r.out) {
		r.out, r.read = r.out[:0], 0
	}
	return n, nil
}

func (r *jsonNULReader) feed(c byte) {
	if len(r.escape) > 0 {
		r.escape = append(r.escape, c)
		switch {
		case len(r.escape) == 2 && c != 'u', len(r.escape) > 2 && c != '0':
			// Not \u0000: pass the escape on unchanged.
			r.out, r.escape = append(r.out, r.escape...), r.escape[:0]
		case len(r.escape) == len(`\u0000`):
			r.escape = r.escape[:0]
		}
		return
	}
	switch {
	case !r.inString:
		r.inString = c == '"'
	case c == '"':
		r.inString = false
	case c == '\\':
		r.escape = append(r.escape, c)
		return
	}
	r.out = append(r.out, c)
}
