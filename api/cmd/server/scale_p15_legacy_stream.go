package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type legacyTransportBudgetKey struct{}

func legacyStreamLease(ctx context.Context, size int64) (func(), error) {
	budget, _ := ctx.Value(legacyTransportBudgetKey{}).(*realtimeBudget)
	return acquireRealtimeWork(ctx, budget, size)
}

// The producer finishes and releases its metadata/SQL snapshot before the
// socket is written. Temporary files are private and removed on every exit.
// Disk holds the compatibility response; a slow reader retains only a chunk.
type legacyResponseSpool struct {
	http.ResponseWriter
	ctx         context.Context
	file        *os.File
	status      int
	err         error
	jsonOnly    bool
	passthrough bool
	reserve     func() error
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
		} else if w.reserve != nil {
			w.err = w.reserve()
		}
	}
}
func (w *legacyResponseSpool) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(p)
	}
	if w.err == nil {
		w.err = w.ctx.Err()
	}
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.file.Write(p)
	w.err = err
	return n, err
}
func (w *legacyResponseSpool) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

func newLegacySpool() (*os.File, error) {
	return os.CreateTemp("", "flow-p15-response-*")
}

func removeLegacySpool(file *os.File) {
	if file == nil {
		return
	}
	_ = file.Close()
	_ = os.Remove(file.Name())
}

func serveDetachedLegacyJSON(w http.ResponseWriter, r *http.Request, next http.Handler) {
	release, err := legacyStreamLease(r.Context(), 256<<10)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
		return
	}
	defer release()
	file, err := newLegacySpool()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not encode workspace")
		return
	}
	defer removeLegacySpool(file)
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), file: file}
	next.ServeHTTP(spool, r)
	if spool.err != nil || r.Context().Err() != nil {
		panic(http.ErrAbortHandler)
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

func sendLegacySpool(ctx context.Context, w http.ResponseWriter, status int, file *os.File) error {
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
	var file *os.File
	defer func() { removeLegacySpool(file) }()
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), jsonOnly: true}
	spool.reserve = func() (err error) {
		release, err = legacyStreamLease(r.Context(), 512<<10)
		if err == nil {
			file, err = newLegacySpool()
			spool.file = file
		}
		return err
	}
	next.ServeHTTP(spool, r)
	if spool.passthrough {
		return
	}
	if spool.err != nil || r.Context().Err() != nil {
		panic(http.ErrAbortHandler)
	}
	if file == nil {
		return
	}
	if (spool.status == 0 || spool.status == http.StatusOK || spool.status == http.StatusCreated) && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		w.Header().Del("Content-Length")
		key, keyErr := s.fileSigningKey(r.Context())
		if keyErr == nil {
			rewritten, err := newLegacySpool()
			if err != nil {
				panic(http.ErrAbortHandler)
			}
			defer removeLegacySpool(rewritten)
			if err := rewriteUploadURLFile(r.Context(), file, rewritten, key, externalBaseURL(r), time.Now().Add(lifetime).Truncate(time.Second)); err != nil {
				panic(http.ErrAbortHandler)
			}
			// Keep the source variable for its cleanup defer.
			if err := sendLegacySpool(r.Context(), w, spool.status, rewritten); err != nil {
				panic(http.ErrAbortHandler)
			}
			return
		} else {
			// The old adapter serves the original bytes on signing failure.
			logSigningFailure(r.Context(), keyErr)
		}
	}
	if err := sendLegacySpool(r.Context(), w, spool.status, file); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// Apply the same two regex passes as rewriteUploadURLs. RE2 scans the file
// through a fixed RuneReader; even one enormous JSON string/filename is never
// accumulated. The intermediate pass also stays on disk.
func rewriteUploadURLFile(ctx context.Context, source *os.File, dst io.Writer, key []byte, base string, expires time.Time) error {
	intermediate, err := newLegacySpool()
	if err != nil {
		return err
	}
	defer removeLegacySpool(intermediate)
	if err := rewriteUploadURLPass(ctx, source, intermediate, uploadURLValue, true, key, base, expires); err != nil {
		return err
	}
	return rewriteUploadURLPass(ctx, intermediate, dst, uploadURLText, false, key, base, expires)
}

func rewriteUploadURLPass(ctx context.Context, source *os.File, dst io.Writer, pattern *regexp.Regexp, quoted bool, key []byte, base string, expires time.Time) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	reader := bufio.NewReaderSize(nil, 32<<10)
	output := bufio.NewWriterSize(dst, 32<<10)
	scratch := make([]byte, 32<<10)
	copyRange := func(start, end int64) error {
		_, err := io.CopyBuffer(output, legacyContextReader{ctx, io.NewSectionReader(source, start, end-start)}, scratch)
		return err
	}
	var offset, written int64
	for offset < info.Size() {
		reader.Reset(legacyContextReader{ctx, io.NewSectionReader(source, offset, info.Size()-offset)})
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
	if err := copyRange(written, info.Size()); err != nil {
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

func writeSignedURLSection(ctx context.Context, output *bufio.Writer, file *os.File, start, end int64, key []byte, base string, expires time.Time, scratch []byte) error {
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
