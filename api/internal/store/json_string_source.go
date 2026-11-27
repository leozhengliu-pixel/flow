package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

type metadataJSONStringMode uint8

const (
	metadataJSONStringQuoted metadataJSONStringMode = iota
	metadataJSONStringRawToken
	metadataJSONStringBase64
	metadataJSONStringCompactRaw
)

const metadataJSONStringCheckpointBytes = 16 << 10
const metadataJSONStringBufferBytes = 32 << 10

type metadataJSONStringCheckpoint struct {
	input, output   int64
	quoted, escaped bool
}

type metadataJSONStringScope struct {
	mu      sync.Mutex
	closeMu sync.Mutex
	head    *metadataJSONStringSource
	closed  atomic.Bool
	release func()
}

func metadataJSONStringScopeFor(ctx context.Context, owner *ResourceRead) (*metadataJSONStringScope, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return nil, errMetadataLogicalClosed
	}
	if owner.jsonStrings != nil {
		if owner.jsonStrings.closed.Load() {
			return nil, errMetadataLogicalClosed
		}
		return owner.jsonStrings, nil
	}
	cost := 2*int(reflect.TypeFor[metadataJSONStringScope]().Size()) + 4096
	_, release, err := owner.store.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		return nil, err
	}
	scope := &metadataJSONStringScope{release: release}
	owner.jsonStrings = scope
	owner.release = append(owner.release, scope.Close)
	return scope, nil
}

// List links are protected by the scope mutex. An intrusive registry avoids
// retaining either per-leaf closers or emptied map buckets for the response.
func (s *metadataJSONStringScope) add(source *metadataJSONStringSource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return false
	}
	source.next, source.linked = s.head, true
	if s.head != nil {
		s.head.previous = source
	}
	s.head = source
	return true
}

func (s *metadataJSONStringScope) removeLocked(source *metadataJSONStringSource) {
	if !source.linked {
		return
	}
	if source.previous != nil {
		source.previous.next = source.next
	} else {
		s.head = source.next
	}
	if source.next != nil {
		source.next.previous = source.previous
	}
	source.previous, source.next, source.linked = nil, nil, false
}

func (s *metadataJSONStringScope) remove(source *metadataJSONStringSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(source)
}

func (s *metadataJSONStringScope) Close() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	for {
		s.mu.Lock()
		s.closed.Store(true)
		source := s.head
		if source == nil {
			release := s.release
			s.release = nil
			s.mu.Unlock()
			if release != nil {
				release()
			}
			return
		}
		s.removeLocked(source)
		s.mu.Unlock()
		source.Close()
	}
}

// The input stays owned by its caller and must remain immutable. This view owns
// only fixed scratch and a sparse seek index, both released by Close or owner.
type metadataJSONStringSource struct {
	mu          sync.Mutex
	ctx         context.Context
	owner       *ResourceRead
	scope       *metadataJSONStringScope
	previous    *metadataJSONStringSource
	next        *metadataJSONStringSource
	linked      bool
	input       io.ReaderAt
	mode        metadataJSONStringMode
	rawSize     int64
	size        int64
	checkpoints []metadataJSONStringCheckpoint
	buffer      []byte
	encoded     []byte
	bufferAt    int64
	bufferUsed  int
	resume      metadataJSONStringCheckpoint
	resumed     bool
	uploads     bool
	matched     int
	release     func()
	closed      bool
}

func newMetadataJSONStringSource(ctx context.Context, owner *ResourceRead, input io.ReaderAt, size int64, mode metadataJSONStringMode) (_ *metadataJSONStringSource, err error) {
	if ctx == nil || owner == nil || owner.store == nil || !owner.store.BoundedMetadataEnabled() || input == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size < 0 || mode > metadataJSONStringCompactRaw {
		return nil, ErrMetadataRecordQuery
	}
	if (mode == metadataJSONStringQuoted || mode == metadataJSONStringCompactRaw) && size > (math.MaxInt64-2)/6 {
		return nil, ErrMetadataWorkBudget
	}
	groups := size / 3
	if size%3 != 0 {
		groups++
	}
	if mode == metadataJSONStringBase64 && groups > (math.MaxInt64-2)/4 {
		return nil, ErrMetadataWorkBudget
	}
	points := int64(0)
	if mode == metadataJSONStringQuoted || mode == metadataJSONStringCompactRaw {
		points = size/metadataJSONStringCheckpointBytes + 2
	}
	indexBytes := uint64(points) * uint64(reflect.TypeFor[metadataJSONStringCheckpoint]().Size())
	indexBytes = (indexBytes + (8<<10 - 1)) / (8 << 10) * (8 << 10)
	cost := 2 * (uint64(reflect.TypeFor[metadataJSONStringSource]().Size()) + 2*metadataJSONStringBufferBytes + indexBytes)
	cost += 16 << 10
	if mode == metadataJSONStringCompactRaw {
		cost += logicalJSONStreamWork
	}
	if cost > uint64(metadataWorkBytes()) {
		return nil, ErrMetadataWorkBudget
	}
	owner.mu.Lock()
	closed := owner.closed
	owner.mu.Unlock()
	if closed {
		return nil, errMetadataLogicalClosed
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	_, release, err := owner.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return nil, err
	}
	source := &metadataJSONStringSource{
		ctx: ctx, owner: owner, input: input, rawSize: size, mode: mode, release: release,
		buffer:  make([]byte, metadataJSONStringBufferBytes),
		encoded: make([]byte, metadataJSONStringBufferBytes),
	}
	defer func() {
		if err != nil {
			source.Close()
		}
	}()
	if points != 0 {
		source.checkpoints = make([]metadataJSONStringCheckpoint, 0, int(points))
	}
	if mode == metadataJSONStringCompactRaw {
		if err = validateMetadataCollectionJSON(ctx, io.NewSectionReader(metadataJSONStringRawInput{source}, 0, size)); err != nil {
			return nil, err
		}
	}
	if err = source.index(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	scope, err := metadataJSONStringScopeFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	source.scope, source.owner = scope, nil
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return nil, errMetadataLogicalClosed
	}
	if !scope.add(source) {
		owner.mu.Unlock()
		return nil, errMetadataLogicalClosed
	}
	owner.mu.Unlock()
	return source, nil
}

func (s *metadataJSONStringSource) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	scope := s.scope
	s.scope = nil
	s.input, s.checkpoints, s.buffer, s.encoded = nil, nil, nil, nil
	s.owner = nil
	s.ctx = nil
	s.size, s.rawSize, s.bufferUsed, s.uploads = 0, 0, 0, false
	s.resumed = false
	if s.release != nil {
		s.release()
		s.release = nil
	}
	if scope != nil {
		scope.remove(s)
	}
}

func (s *metadataJSONStringSource) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func (s *metadataJSONStringSource) ContainsUploads() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploads
}

const metadataJSONStringUploadsMarker = "/uploads/"

func (s *metadataJSONStringSource) noteByte(char byte) {
	const marker = metadataJSONStringUploadsMarker
	if s.uploads {
		return
	}
	if char != marker[s.matched] {
		s.matched = 0
		if char != marker[0] {
			return
		}
	}
	s.matched++
	if s.matched == len(marker) {
		s.uploads = true
	}
}

// note feeds value to the upload marker matcher. The marker's only border is
// its single '/', so the match state after a long value depends only on its
// last len(marker)-1 bytes: the head finishes a match carried in from the
// previous value, one search covers the body, and the tail restores the
// state for the next value.
func (s *metadataJSONStringSource) note(value []byte) {
	const marker = metadataJSONStringUploadsMarker
	if s.uploads {
		return
	}
	if len(value) < 4*len(marker) {
		for _, char := range value {
			s.noteByte(char)
		}
		return
	}
	for _, char := range value[:len(marker)-1] {
		s.noteByte(char)
	}
	if s.uploads {
		return
	}
	if bytes.Contains(value, []byte(marker)) {
		s.uploads = true
		return
	}
	s.matched = 0
	for _, char := range value[len(value)-(len(marker)-1):] {
		s.noteByte(char)
	}
}

func (s *metadataJSONStringSource) noteString(value string) {
	for i := 0; i < len(value); i++ {
		s.noteByte(value[i])
	}
}

func (s *metadataJSONStringSource) inputAt(raw []byte, offset int64) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.ownerOpen(); err != nil {
		return err
	}
	n, err := s.input.ReadAt(raw, offset)
	if cancelled := s.ctx.Err(); cancelled != nil {
		return cancelled
	}
	if err := s.ownerOpen(); err != nil {
		return err
	}
	if n != len(raw) {
		if err != nil && err != io.EOF {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}

func (s *metadataJSONStringSource) ownerOpen() error {
	if s.scope != nil && s.scope.closed.Load() {
		return errMetadataLogicalClosed
	}
	if s.owner != nil {
		s.owner.mu.Lock()
		closed := s.owner.closed
		s.owner.mu.Unlock()
		if closed {
			return errMetadataLogicalClosed
		}
	}
	return nil
}

type metadataJSONStringRawInput struct{ source *metadataJSONStringSource }

func (r metadataJSONStringRawInput) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if offset >= r.source.rawSize {
		return 0, io.EOF
	}
	length := min(int64(len(p)), r.source.rawSize-offset)
	if err := r.source.inputAt(p[:int(length)], offset); err != nil {
		return 0, err
	}
	if length != int64(len(p)) {
		return int(length), io.EOF
	}
	return len(p), nil
}

// ahead returns every buffered input byte from offset, refilling the window
// at offset when fewer than min(need, rest of input) bytes are buffered.
// complete reports that the returned bytes reach the end of the input.
func (s *metadataJSONStringSource) ahead(offset int64, need int) (raw []byte, complete bool, err error) {
	if err := s.ctx.Err(); err != nil {
		return nil, false, err
	}
	want := min(int64(need), s.rawSize-offset)
	if offset < 0 || want <= 0 {
		return nil, false, io.ErrUnexpectedEOF
	}
	if offset < s.bufferAt || offset+want > s.bufferAt+int64(s.bufferUsed) {
		length := min(int64(len(s.buffer)), s.rawSize-offset)
		if err := s.inputAt(s.buffer[:int(length)], offset); err != nil {
			return nil, false, err
		}
		s.bufferAt, s.bufferUsed = offset, int(length)
	}
	start := int(offset - s.bufferAt)
	return s.buffer[start:s.bufferUsed], s.bufferAt+int64(s.bufferUsed) == s.rawSize, nil
}

var metadataJSONStringEscapes = func() [128]string {
	const hex = "0123456789abcdef"
	var values [128]string
	for char := range 32 {
		values[char] = string([]byte{'\\', 'u', '0', '0', hex[char>>4], hex[char&15]})
	}
	values['\\'], values['"'] = `\\`, `\"`
	values['\n'], values['\r'], values['\t'], values['\b'], values['\f'] = `\n`, `\r`, `\t`, `\b`, `\f`
	values['<'], values['>'], values['&'] = `\u003c`, `\u003e`, `\u0026`
	return values
}()

// metadataJSONStringCompactStops are the bytes that end a verbatim run
// inside a quoted string of compact raw JSON: string state changes and the
// characters json.Marshal escapes.
var metadataJSONStringCompactStops = func() [256]bool {
	var stops [256]bool
	for _, char := range []byte{'"', '\\', '<', '>', '&', 0xe2} {
		stops[char] = true
	}
	return stops
}()

func metadataJSONStringSpace(char byte) bool {
	return char == ' ' || char == '\n' || char == '\r' || char == '\t'
}

func metadataJSONStringState(char byte, quoted, escaped bool) (bool, bool) {
	if escaped {
		return quoted, false
	}
	if quoted && char == '\\' {
		return quoted, true
	}
	if char == '"' {
		return !quoted, false
	}
	return quoted, false
}

// metadataJSONStringSpan is the output of width input bytes: nothing (skip,
// insignificant whitespace), the escape unit, or the input bytes verbatim
// (raw, valid until the next window read).
type metadataJSONStringSpan struct {
	raw   []byte
	unit  string
	width int
	skip  bool
}

func (s metadataJSONStringSpan) length() int64 {
	switch {
	case s.skip:
		return 0
	case s.unit != "":
		return int64(len(s.unit))
	}
	return int64(s.width)
}

// span returns the output of the input at offset, which starts a unit, with
// the string state before it; it advances the state past the span. A
// verbatim run stops at a byte that needs an escape, at the end of the
// window and after limit input bytes, but never inside a UTF-8 sequence, so
// every span ends on a unit boundary. Runs are found with table lookups over
// the whole window instead of one window read per character.
func (s *metadataJSONStringSource) span(offset, limit int64, quoted, escaped *bool) (metadataJSONStringSpan, error) {
	buf, complete, err := s.ahead(offset, utf8.UTFMax)
	if err != nil {
		return metadataJSONStringSpan{}, err
	}
	end := len(buf)
	if limit < int64(end) {
		end = int(max(limit, 1))
	}
	if s.mode == metadataJSONStringCompactRaw {
		q, e := *quoted, *escaped
		if !q && metadataJSONStringSpace(buf[0]) {
			n := 1
			for n < end && metadataJSONStringSpace(buf[n]) {
				n++
			}
			return metadataJSONStringSpan{width: n, skip: true}, nil
		}
		i := 0
		for i < end {
			char := buf[i]
			if q && !e && !metadataJSONStringCompactStops[char] {
				i++
				continue
			}
			if !q && metadataJSONStringSpace(char) {
				break
			}
			if char == 0xe2 && i+3 > len(buf) && !complete {
				// Re-examine this byte with its successors in view.
				break
			}
			unit, width := "", 1
			switch {
			case char == '<' || char == '>' || char == '&':
				unit = metadataJSONStringEscapes[char]
			case char == 0xe2 && i+3 <= len(buf) && buf[i+1] == 0x80 && (buf[i+2] == 0xa8 || buf[i+2] == 0xa9):
				unit, width = `\u2028`, 3
				if buf[i+2] == 0xa9 {
					unit = `\u2029`
				}
			}
			if unit != "" {
				if i > 0 {
					break
				}
				*quoted, *escaped = metadataJSONStringState(char, q, e)
				return metadataJSONStringSpan{unit: unit, width: width}, nil
			}
			q, e = metadataJSONStringState(char, q, e)
			i++
		}
		*quoted, *escaped = q, e
		return metadataJSONStringSpan{raw: buf[:i], width: i}, nil
	}
	i := 0
	for i < end {
		char := buf[i]
		if char < utf8.RuneSelf {
			if unit := metadataJSONStringEscapes[char]; unit != "" {
				if i > 0 {
					break
				}
				return metadataJSONStringSpan{unit: unit, width: 1}, nil
			}
			i++
			continue
		}
		if !complete && !utf8.FullRune(buf[i:]) {
			break
		}
		value, size := utf8.DecodeRune(buf[i:])
		unit := ""
		switch {
		case value == utf8.RuneError && size == 1:
			unit = `\ufffd`
		case value == '\u2028':
			unit = `\u2028`
		case value == '\u2029':
			unit = `\u2029`
		}
		if unit != "" {
			if i > 0 {
				break
			}
			return metadataJSONStringSpan{unit: unit, width: size}, nil
		}
		if i > 0 && i+size > end {
			break
		}
		i += size
	}
	return metadataJSONStringSpan{raw: buf[:i], width: i}, nil
}

func (s *metadataJSONStringSource) index() error {
	if s.mode == metadataJSONStringRawToken || s.mode == metadataJSONStringBase64 {
		s.size = s.rawSize
		if s.mode == metadataJSONStringBase64 {
			groups := s.rawSize / 3
			if s.rawSize%3 != 0 {
				groups++
			}
			s.size = groups*4 + 2
		}
		for offset := int64(0); offset < s.rawSize; {
			size := min(int64(len(s.encoded)/4*3), s.rawSize-offset)
			raw := s.buffer[:int(size)]
			if err := s.inputAt(raw, offset); err != nil {
				return err
			}
			if s.mode == metadataJSONStringBase64 {
				n := base64.StdEncoding.EncodedLen(len(raw))
				base64.StdEncoding.Encode(s.encoded[:n], raw)
				s.note(s.encoded[:n])
			} else {
				s.note(raw)
			}
			offset += size
		}
		return s.ctx.Err()
	}
	output := int64(0)
	if s.mode == metadataJSONStringQuoted {
		output = 1
	}
	s.checkpoints = append(s.checkpoints, metadataJSONStringCheckpoint{output: output})
	quoted, escaped := false, false
	for offset := int64(0); offset < s.rawSize; {
		last := s.checkpoints[len(s.checkpoints)-1]
		if offset-last.input >= metadataJSONStringCheckpointBytes {
			last = metadataJSONStringCheckpoint{input: offset, output: output, quoted: quoted, escaped: escaped}
			s.checkpoints = append(s.checkpoints, last)
		}
		span, err := s.span(offset, last.input+metadataJSONStringCheckpointBytes-offset, &quoted, &escaped)
		if err != nil {
			return err
		}
		if span.unit != "" {
			s.noteString(span.unit)
		} else if !span.skip {
			s.note(span.raw)
		}
		output += span.length()
		offset += int64(span.width)
	}
	if s.mode == metadataJSONStringQuoted {
		output++
	}
	s.size = output
	return s.ctx.Err()
}

func (s *metadataJSONStringSource) ReadAt(p []byte, offset int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errMetadataLogicalClosed
	}
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if err := s.ownerOpen(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if offset >= s.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.mode == metadataJSONStringRawToken {
		length := min(int64(len(p)), s.size-offset)
		n, err := s.input.ReadAt(p[:int(length)], offset)
		if cancelled := s.ctx.Err(); cancelled != nil {
			return n, cancelled
		}
		if n != int(length) {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		if err != nil && err != io.EOF {
			return n, err
		}
		if n != len(p) {
			return n, io.EOF
		}
		return n, nil
	}
	if s.mode == metadataJSONStringBase64 {
		return s.base64At(p, offset)
	}
	n := 0
	if s.mode == metadataJSONStringQuoted && offset == 0 {
		p[0], n, offset = '"', 1, 1
	}
	index := sort.Search(len(s.checkpoints), func(i int) bool { return s.checkpoints[i].output > offset }) - 1
	point := s.checkpoints[max(0, index)]
	// A sequential reader continues from where the previous read stopped
	// instead of re-walking from the checkpoint.
	if s.resumed && s.resume.output <= offset && s.resume.output > point.output {
		point = s.resume
	}
	quoted, escaped := point.quoted, point.escaped
	rawOffset, output := point.input, point.output
	s.resumed = false
	for rawOffset < s.rawSize && n < len(p) && (s.mode != metadataJSONStringCompactRaw || output < s.size) {
		// Verbatim runs stop exactly at the requested offset and at the end
		// of p, so only an escape unit or a UTF-8 sequence is split.
		limit := int64(len(p) - n)
		if output < offset {
			limit = offset - output
		}
		before := metadataJSONStringCheckpoint{input: rawOffset, output: output, quoted: quoted, escaped: escaped}
		span, err := s.span(rawOffset, limit, &quoted, &escaped)
		if err != nil {
			return n, err
		}
		length := span.length()
		if output+length > offset {
			skip := int(max(int64(0), offset-output))
			copied := 0
			if span.unit != "" {
				copied = copy(p[n:], span.unit[skip:])
			} else {
				copied = copy(p[n:], span.raw[skip:])
			}
			n, offset = n+copied, offset+int64(copied)
			if int64(skip+copied) < length {
				s.resume, s.resumed = before, true
				break
			}
		}
		rawOffset, output = rawOffset+int64(span.width), output+length
	}
	if !s.resumed {
		s.resume, s.resumed = metadataJSONStringCheckpoint{input: rawOffset, output: output, quoted: quoted, escaped: escaped}, true
	}
	if s.mode == metadataJSONStringQuoted && n < len(p) && offset == s.size-1 {
		p[n], n = '"', n+1
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, s.ctx.Err()
}

func (s *metadataJSONStringSource) base64At(p []byte, offset int64) (int, error) {
	n := 0
	if offset == 0 {
		p[0], n, offset = '"', 1, 1
	}
	for n < len(p) && offset < s.size-1 {
		encodedOffset := offset - 1
		rawOffset := encodedOffset / 4 * 3
		size := min(int64(len(s.encoded)/4*3), s.rawSize-rawOffset)
		raw := s.buffer[:int(size)]
		if err := s.inputAt(raw, rawOffset); err != nil {
			return n, err
		}
		length := base64.StdEncoding.EncodedLen(len(raw))
		base64.StdEncoding.Encode(s.encoded[:length], raw)
		copied := copy(p[n:], s.encoded[int(encodedOffset%4):length])
		n, offset = n+copied, offset+int64(copied)
	}
	if n < len(p) && offset == s.size-1 {
		p[n], n = '"', n+1
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, s.ctx.Err()
}
