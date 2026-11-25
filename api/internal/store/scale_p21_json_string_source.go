package store

import (
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
	if cost > metadataWorkBytes {
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

func (s *metadataJSONStringSource) noteByte(char byte) {
	const marker = "/uploads/"
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

func (s *metadataJSONStringSource) note(value []byte) {
	for _, char := range value {
		if s.uploads {
			return
		}
		s.noteByte(char)
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

func (s *metadataJSONStringSource) window(offset int64, size int) ([]byte, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if offset < s.bufferAt || offset+int64(size) > s.bufferAt+int64(s.bufferUsed) {
		length := min(int64(len(s.buffer)), s.rawSize-offset)
		if length < int64(size) || length < 0 {
			return nil, io.ErrUnexpectedEOF
		}
		if err := s.inputAt(s.buffer[:int(length)], offset); err != nil {
			return nil, err
		}
		s.bufferAt, s.bufferUsed = offset, int(length)
	}
	start := int(offset - s.bufferAt)
	return s.buffer[start : start+size], nil
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

func (s *metadataJSONStringSource) unit(offset int64) (string, int, error) {
	raw, err := s.window(offset, 1)
	if err != nil {
		return "", 0, err
	}
	if raw[0] < utf8.RuneSelf {
		return metadataJSONStringEscapes[raw[0]], 1, nil
	}
	raw, err = s.window(offset, int(min(int64(utf8.UTFMax), s.rawSize-offset)))
	if err != nil {
		return "", 0, err
	}
	value, size := utf8.DecodeRune(raw)
	switch {
	case value == utf8.RuneError && size == 1:
		return `\ufffd`, 1, nil
	case value == '\u2028':
		return `\u2028`, size, nil
	case value == '\u2029':
		return `\u2029`, size, nil
	}
	return "", size, nil
}

func (s *metadataJSONStringSource) rawUnit(offset int64, char byte) (string, int, error) {
	switch char {
	case '<', '>', '&':
		return metadataJSONStringEscapes[char], 1, nil
	case 0xe2:
		if s.rawSize-offset >= 3 {
			raw, err := s.window(offset, 3)
			if err != nil {
				return "", 0, err
			}
			if raw[1] == 0x80 {
				if raw[2] == 0xa8 {
					return `\u2028`, 3, nil
				}
				if raw[2] == 0xa9 {
					return `\u2029`, 3, nil
				}
			}
		}
	}
	return "", 1, nil
}

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
			s.checkpoints = append(s.checkpoints, metadataJSONStringCheckpoint{input: offset, output: output, quoted: quoted, escaped: escaped})
		}
		var unit string
		var width int
		var err error
		if s.mode == metadataJSONStringCompactRaw {
			raw, readErr := s.window(offset, 1)
			if readErr != nil {
				return readErr
			}
			char := raw[0]
			if !quoted && metadataJSONStringSpace(char) {
				offset++
				continue
			}
			quoted, escaped = metadataJSONStringState(char, quoted, escaped)
			unit, width, err = s.rawUnit(offset, char)
		} else {
			unit, width, err = s.unit(offset)
		}
		if err != nil {
			return err
		}
		if unit != "" {
			output += int64(len(unit))
			for i := 0; i < len(unit); i++ {
				s.noteByte(unit[i])
			}
		} else {
			raw, err := s.window(offset, width)
			if err != nil {
				return err
			}
			output += int64(width)
			s.note(raw)
		}
		offset += int64(width)
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
	quoted, escaped := point.quoted, point.escaped
	for rawOffset, output := point.input, point.output; rawOffset < s.rawSize && n < len(p) && (s.mode != metadataJSONStringCompactRaw || output < s.size); {
		var unit string
		var width int
		var err error
		if s.mode == metadataJSONStringCompactRaw {
			raw, readErr := s.window(rawOffset, 1)
			if readErr != nil {
				return n, readErr
			}
			char := raw[0]
			if !quoted && metadataJSONStringSpace(char) {
				rawOffset++
				continue
			}
			quoted, escaped = metadataJSONStringState(char, quoted, escaped)
			unit, width, err = s.rawUnit(rawOffset, char)
		} else {
			unit, width, err = s.unit(rawOffset)
		}
		if err != nil {
			return n, err
		}
		length := int64(width)
		if unit != "" {
			length = int64(len(unit))
		}
		if output+length > offset {
			skip := int(max(int64(0), offset-output))
			copied := 0
			if unit != "" {
				copied = copy(p[n:], unit[skip:])
			} else {
				raw, err := s.window(rawOffset, width)
				if err != nil {
					return n, err
				}
				copied = copy(p[n:], raw[skip:])
			}
			n, offset = n+copied, offset+int64(copied)
		}
		rawOffset, output = rawOffset+int64(width), output+length
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
