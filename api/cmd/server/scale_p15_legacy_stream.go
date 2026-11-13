package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/store"
)

type legacyTransportBudgetKey struct{}

func legacyStreamLease(ctx context.Context, size int64) (func(), error) {
	budget, _ := ctx.Value(legacyTransportBudgetKey{}).(*realtimeBudget)
	return acquireRealtimeWork(ctx, budget, size)
}

// Detach the producer before socket writes while the spool budget allows.
// At the byte cap, drain the prefix and continue through the bounded encoder;
// the producer then lives until encoding finishes.
type legacyResponseSpool struct {
	http.ResponseWriter
	ctx            context.Context
	file           *store.RecordSpool
	status         int
	err            error
	jsonOnly       bool
	passthrough    bool
	reserve        func() error
	signing        *hydratedURLSigning
	prepareSigning func()
	bodyStarted    bool
}

func (w *legacyResponseSpool) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *legacyResponseSpool) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 && status >= 200 {
		w.status = status
		if w.jsonOnly && ((status != http.StatusOK && status != http.StatusCreated) || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")) {
			w.passthrough = true
			removeLegacySpool(w.file)
			w.ResponseWriter.WriteHeader(status)
		} else {
			if w.jsonOnly {
				w.Header().Del("Content-Length")
			}
			if w.prepareSigning != nil {
				w.prepareSigning()
			}
			if w.reserve != nil {
				w.err = w.reserve()
			}
			if w.file == nil && w.err == nil {
				w.passthrough = true
				w.ResponseWriter.WriteHeader(status)
			}
		}
	}
}
func (w *legacyResponseSpool) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.err == nil {
		w.err = w.ctx.Err()
	}
	if w.err != nil {
		return 0, w.err
	}
	if !w.bodyStarted && len(p) > 0 {
		w.bodyStarted = true
		// json.Encoder and cached JSON handlers hand us an already complete
		// value. Rewrite that resident source directly, with the same spool
		// fallback as the structured P15 encoder and no response-sized copy.
		if w.signing != nil && w.signing.key != nil && !w.signing.encoded && json.Valid(p) {
			w.signing.encoded = true
			ctx := context.WithValue(w.ctx, hydratedURLSigningKey{}, (*hydratedURLSigning)(nil))
			if err := rewriteHydratedURLSource(ctx, bytes.NewReader(p), int64(len(p)), w, w.signing); err != nil {
				w.err = err
				return 0, err
			}
			return len(p), nil
		}
	}
	if w.passthrough {
		n, err := w.ResponseWriter.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		w.err = err
		return n, err
	}
	if w.file != nil {
		n, err := w.file.Write(p)
		if err == nil {
			return n, nil
		}
		// A failed/short disk write is recoverable: the successful prefix
		// remains in the file and the producer still owns the rest of p.
		if err := sendLegacySpool(w.ctx, w.ResponseWriter, w.status, w.file); err != nil {
			w.err = err
			return n, err
		}
		removeLegacySpool(w.file)
		w.file = nil
		w.passthrough = true
		written, err := w.ResponseWriter.Write(p[n:])
		if err == nil && written != len(p)-n {
			err = io.ErrShortWrite
		}
		w.err = err
		return n + written, err
	}
	w.passthrough = true
	w.ResponseWriter.WriteHeader(w.status)
	return w.ResponseWriter.Write(p)
}
func (w *legacyResponseSpool) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

func newLegacySpool() (*store.RecordSpool, error) {
	return store.NewRecordSpool()
}

func removeLegacySpool(file *store.RecordSpool) {
	_ = file.Close()
}

func serveDetachedLegacyJSON(w http.ResponseWriter, r *http.Request, next http.Handler) {
	release, err := legacyStreamLease(r.Context(), 256<<10)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
		return
	}
	defer release()
	// Creation failure and a zero/full budget both use the same bounded
	// encoder, with cancellation/error handling still on the writer.
	file, _ := newLegacySpool()
	defer removeLegacySpool(file)
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), file: file}
	next.ServeHTTP(spool, r)
	releaseHydratedProducer(r.Context())
	if spool.err != nil || r.Context().Err() != nil {
		panic(http.ErrAbortHandler)
	}
	if spool.passthrough || file == nil {
		return
	}
	if err := sendLegacySpool(r.Context(), w, spool.status, file); err != nil {
		panic(http.ErrAbortHandler)
	}
}

type legacyContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r legacyContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func sendLegacySpool(ctx context.Context, w http.ResponseWriter, status int, file *store.RecordSpool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	buffer := make([]byte, 64<<10)
	_, err := io.CopyBuffer(w, legacyContextReader{ctx, file}, buffer)
	return err
}

func (s *server) signedFileURLsStream(w http.ResponseWriter, r *http.Request, next http.Handler, lifetime time.Duration) {
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	var file *store.RecordSpool
	defer func() { removeLegacySpool(file) }()
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), jsonOnly: true}
	spool.signing = &hydratedURLSigning{base: externalBaseURL(r), expires: time.Now().Add(lifetime).Truncate(time.Second)}
	r = r.WithContext(context.WithValue(r.Context(), hydratedURLSigningKey{}, spool.signing))
	spool.prepareSigning = func() {
		spool.prepareSigning = nil
		if key, err := s.fileSigningKey(r.Context()); err == nil {
			spool.signing.key = key
		} else {
			logSigningFailure(r.Context(), err)
			spool.signing = nil
		}
	}
	spool.reserve = func() (err error) {
		release, err = legacyStreamLease(r.Context(), 512<<10)
		if err == nil {
			file, err = newLegacySpool()
			spool.file = file
			// An unavailable temporary volume is also a direct-stream fallback.
			err = nil
		}
		return err
	}
	next.ServeHTTP(spool, r)
	releaseHydratedProducer(r.Context())
	if spool.err != nil || r.Context().Err() != nil {
		panic(http.ErrAbortHandler)
	}
	if spool.passthrough {
		return
	}
	if file == nil {
		return
	}
	if (spool.status == 0 || spool.status == http.StatusOK || spool.status == http.StatusCreated) && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		w.Header().Del("Content-Length")
		if spool.signing != nil && !spool.signing.encoded {
			w.WriteHeader(spool.status)
			if err := rewriteHydratedJSONSource(r.Context(), file, w, spool.signing); err != nil {
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
	if err := sendLegacySpool(r.Context(), w, spool.status, file); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// Apply the same two regex passes as rewriteUploadURLs without an intermediate
// response file. RE2 and copying both use fixed-size buffers.
func rewriteUploadURLFile(ctx context.Context, source *store.RecordSpool, dst io.Writer, key []byte, base string, expires time.Time) error {
	return rewriteHydratedJSONSource(ctx, source, dst, &hydratedURLSigning{key: key, base: base, expires: expires})
}

func rewriteUploadURLPass(ctx context.Context, source *store.RecordSpool, dst io.Writer, pattern *regexp.Regexp, quoted bool, key []byte, base string, expires time.Time) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	return rewriteUploadURLSourcePass(ctx, source, info.Size(), dst, pattern, quoted, key, base, expires)
}

func rewriteUploadURLSourcePass(ctx context.Context, source io.ReaderAt, size int64, dst io.Writer, pattern *regexp.Regexp, quoted bool, key []byte, base string, expires time.Time) error {
	reader := bufio.NewReaderSize(nil, 32<<10)
	output := bufio.NewWriterSize(dst, 32<<10)
	scratch := make([]byte, 32<<10)
	copyRange := func(start, end int64) error {
		_, err := io.CopyBuffer(output, legacyContextReader{ctx, io.NewSectionReader(source, start, end-start)}, scratch)
		return err
	}
	var offset, written int64
	for offset < size {
		reader.Reset(legacyContextReader{ctx, io.NewSectionReader(source, offset, size-offset)})
		match := pattern.FindReaderSubmatchIndex(reader)
		if err := ctx.Err(); err != nil {
			return err
		}
		if match == nil {
			break
		}
		start, end := offset+int64(match[0]), offset+int64(match[1])
		nameGroup := 2
		if quoted {
			nameGroup = 4
		} else {
			var prefix [10]byte
			n := min(start, int64(len(prefix)))
			_, _ = source.ReadAt(prefix[:n], start-n)
			if strings.HasSuffix(string(prefix[:n]), "/api/files") || strings.HasSuffix(string(prefix[:n]), "/api/mcp") {
				offset = end
				continue
			}
		}
		if err := copyRange(written, start); err != nil {
			return err
		}
		nameStart, nameEnd := offset+int64(match[nameGroup]), offset+int64(match[nameGroup+1])
		if quoted {
			output.WriteByte('"')
		}
		if err := writeSignedURLSection(ctx, output, source, nameStart, nameEnd, key, base, expires, scratch); err != nil {
			return err
		}
		if quoted {
			output.WriteByte('"')
		}
		offset, written = end, end
	}
	if err := copyRange(written, size); err != nil {
		return err
	}
	return output.Flush()
}

type unescapePathReader struct{ reader *bufio.Reader }

func pathHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (r unescapePathReader) Read(p []byte) (int, error) {
	for i := range p {
		c, err := r.reader.ReadByte()
		if err != nil {
			return i, err
		}
		if c == '%' {
			a, errA := r.reader.ReadByte()
			b, errB := r.reader.ReadByte()
			x, okA := pathHex(a)
			y, okB := pathHex(b)
			if errA != nil || errB != nil || !okA || !okB {
				return i, errors.New("invalid path escape")
			}
			c = x<<4 | y
		}
		p[i] = c
	}
	return len(p), nil
}

func writeSignedURLSection(ctx context.Context, output *bufio.Writer, file io.ReaderAt, start, end int64, key []byte, base string, expires time.Time, scratch []byte) error {
	reader := bufio.NewReaderSize(nil, 4096)
	reset := func() io.Reader {
		reader.Reset(legacyContextReader{ctx, io.NewSectionReader(file, start, end-start)})
		return unescapePathReader{reader}
	}
	// PathUnescape preserves the entire original filename on any bad escape.
	_, err := io.CopyBuffer(io.Discard, reset(), scratch)
	decode := err == nil
	if ctx.Err() != nil {
		return ctx.Err()
	}
	nameReader := func() io.Reader {
		if decode {
			return reset()
		}
		return legacyContextReader{ctx, io.NewSectionReader(file, start, end-start)}
	}
	mac := hmac.New(sha256.New, key)
	if _, err := io.CopyBuffer(mac, nameReader(), scratch); err != nil {
		return err
	}
	unix := strconv.FormatInt(expires.Unix(), 10)
	_, _ = fmt.Fprintf(mac, "\n%s", unix)
	if err := encodeHydratedStringContent(ctx, output, strings.TrimRight(base, "/")+"/api/files/signed/"); err != nil {
		return err
	}
	input := nameReader()
	for {
		n, err := input.Read(scratch)
		if n > 0 {
			// PathEscape operates on bytes, so chunk boundaries are harmless.
			if e := encodeHydratedStringContent(ctx, output, url.PathEscape(string(scratch[:n]))); e != nil {
				return e
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return encodeHydratedStringContent(ctx, output, "?expires="+unix+"&signature="+hex.EncodeToString(mac.Sum(nil)))
}
