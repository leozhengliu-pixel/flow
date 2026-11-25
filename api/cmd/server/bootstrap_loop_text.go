package main

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
	"unicode"
	"unicode/utf8"
)

const scaleP21LoopTextRunes = 141

type scaleP21LoopDescription struct {
	output       [scaleP21LoopTextRunes * utf8.UTFMax]byte
	spaces       [scaleP21LoopTextRunes * utf8.UTFMax]byte
	outputBytes  int
	outputRunes  int
	spaceBytes   int
	spaceRunes   int
	started      bool
	star         bool
	punctuation  bool
	sentenceDone bool
}

func (d *scaleP21LoopDescription) reset() {
	d.outputBytes, d.outputRunes, d.spaceBytes, d.spaceRunes = 0, 0, 0, 0
	d.started, d.star, d.punctuation, d.sentenceDone = false, false, false, false
}

func (d *scaleP21LoopDescription) emit(raw []byte) {
	if d.outputRunes < scaleP21LoopTextRunes {
		d.outputBytes += copy(d.output[d.outputBytes:], raw)
		d.outputRunes++
	}
}

func (d *scaleP21LoopDescription) replaceStars(r rune, raw []byte) {
	if r == '*' {
		d.star = !d.star
		return
	}
	if d.star {
		d.emit([]byte{'*'})
		d.star = false
	}
	d.emit(raw)
}

func (d *scaleP21LoopDescription) feed(r rune, raw []byte) {
	if d.sentenceDone || d.outputRunes == scaleP21LoopTextRunes {
		return
	}
	if !d.started {
		if unicode.IsSpace(r) {
			return
		}
		d.started = true
	}
	// The legacy helper tests the next byte cast to rune, not the next
	// decoded rune. In particular, invalid 0x85/0xa0 differ from valid NBSP.
	if d.punctuation && unicode.IsSpace(rune(raw[0])) {
		d.sentenceDone = true
		return
	}
	d.punctuation = r == '.' || r == '!' || r == '?'
	if unicode.IsSpace(r) {
		if d.spaceRunes < scaleP21LoopTextRunes {
			d.spaceBytes += copy(d.spaces[d.spaceBytes:], raw)
			d.spaceRunes++
		}
		return
	}
	for pending := d.spaces[:d.spaceBytes]; len(pending) > 0; {
		value, size := utf8.DecodeRune(pending)
		d.replaceStars(value, pending[:size])
		pending = pending[size:]
	}
	d.spaceBytes, d.spaceRunes = 0, 0
	d.replaceStars(r, raw)
}

func (d *scaleP21LoopDescription) text() string {
	if !d.sentenceDone && d.star {
		d.emit([]byte{'*'})
		d.star = false
	}
	if d.outputRunes <= 140 {
		return string(d.output[:d.outputBytes])
	}
	// Rune conversion happens only on the truncating legacy branch; shorter
	// results must retain their original invalid UTF-8 bytes.
	var encoded [137 * utf8.UTFMax]byte
	input, size := d.output[:d.outputBytes], 0
	for range 137 {
		value, width := utf8.DecodeRune(input)
		size += utf8.EncodeRune(encoded[size:], value)
		input = input[width:]
	}
	return strings.TrimSpace(string(encoded[:size])) + "…"
}

type scaleP21LoopTextLine struct {
	description scaleP21LoopDescription
	prefix      bool
	started     bool
	bytes       int
	spaces      int
	colon       bool
}

func (l *scaleP21LoopTextLine) reset() {
	l.description.reset()
	l.prefix, l.started, l.colon = true, false, false
	l.bytes, l.spaces = 0, 0
}

func (l *scaleP21LoopTextLine) feed(r rune, raw []byte) {
	if l.prefix {
		if strings.ContainsRune("#-*> \t0123456789.", r) {
			return
		}
		l.prefix = false
	}
	if unicode.IsSpace(r) {
		if l.started {
			l.spaces = min(40, l.spaces+len(raw))
		}
	} else {
		l.started = true
		l.bytes = min(40, l.bytes+l.spaces+len(raw))
		l.spaces = 0
		l.colon = r == ':'
	}
	l.description.feed(r, raw)
}

type scaleP21LoopTextState struct {
	ctx           context.Context
	hash          hash.Hash
	appendState   encoding.BinaryAppender
	restoreState  encoding.BinaryUnmarshaler
	buffer        [1024]byte
	buffered      int
	checkpoint    [128]byte
	checkpointLen int
	trailingSpace bool
	started       bool
	tail          [utf8.UTFMax]byte
	tailBytes     int
	checked       int
	fallback      scaleP21LoopDescription
	line          scaleP21LoopTextLine
	selected      bool
	description   string
}

func (s *scaleP21LoopTextState) flush() {
	_, _ = s.hash.Write(s.buffer[:s.buffered])
	s.buffered = 0
}

func (s *scaleP21LoopTextState) write(raw []byte) {
	for len(raw) > 0 {
		n := copy(s.buffer[s.buffered:], raw)
		s.buffered += n
		raw = raw[n:]
		if s.buffered == len(s.buffer) {
			s.flush()
		}
	}
}

func (s *scaleP21LoopTextState) finishLine() {
	if !s.selected && s.line.started && !(s.line.colon && s.line.bytes < 40) {
		s.description, s.selected = s.line.description.text(), true
	}
	s.line.reset()
}

func (s *scaleP21LoopTextState) rune(r rune, raw []byte) error {
	s.checked += len(raw)
	if s.checked >= 4096 {
		s.checked = 0
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if !s.started {
		if unicode.IsSpace(r) {
			return nil
		}
		s.started = true
	}
	if unicode.IsSpace(r) {
		if !s.trailingSpace {
			s.flush()
			checkpoint, err := s.appendState.AppendBinary(s.checkpoint[:0])
			if err != nil {
				return err
			}
			if len(checkpoint) > len(s.checkpoint) {
				return errors.New("loop text hash checkpoint exceeds its bound")
			}
			s.checkpointLen = len(checkpoint)
			s.trailingSpace = true
		}
	} else {
		s.trailingSpace = false
	}
	s.write(raw)
	if !s.selected {
		s.fallback.feed(r, raw)
		if r == '\n' {
			s.finishLine()
		} else {
			s.line.feed(r, raw)
		}
	}
	return nil
}

func (s *scaleP21LoopTextState) chunk(raw []byte) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	for len(raw) > 0 || s.tailBytes > 0 && utf8.FullRune(s.tail[:s.tailBytes]) {
		if s.tailBytes == 0 && utf8.FullRune(raw) {
			value, size := utf8.DecodeRune(raw)
			if err := s.rune(value, raw[:size]); err != nil {
				return err
			}
			raw = raw[size:]
			continue
		}
		if !utf8.FullRune(s.tail[:s.tailBytes]) {
			s.tail[s.tailBytes] = raw[0]
			s.tailBytes++
			raw = raw[1:]
			continue
		}
		value, size := utf8.DecodeRune(s.tail[:s.tailBytes])
		if err := s.rune(value, s.tail[:size]); err != nil {
			return err
		}
		s.tailBytes = copy(s.tail[:], s.tail[size:s.tailBytes])
	}
	return nil
}

// scaleP21LoopText reproduces the literal contract hash and fallback description
// while retaining only fixed prefixes, a UTF-8 carry and a SHA-256 checkpoint.
func scaleP21LoopText(ctx context.Context, name string, chunks func(func([]byte) error) error) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if chunks == nil {
		return "", "", errors.New("loop instruction reader is nil")
	}
	h := sha256.New()
	appender, appendOK := h.(encoding.BinaryAppender)
	restorer, restoreOK := h.(encoding.BinaryUnmarshaler)
	if !appendOK || !restoreOK {
		return "", "", errors.New("loop text hash does not support bounded checkpoints")
	}
	s := scaleP21LoopTextState{ctx: ctx, hash: h, appendState: appender, restoreState: restorer}
	s.line.reset()
	s.write([]byte(loopContractPromptVersion))
	s.write([]byte{0})
	for value := strings.TrimSpace(name); len(value) > 0; {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		n := min(len(value), len(s.buffer)-s.buffered)
		s.buffered += copy(s.buffer[s.buffered:], value[:n])
		value = value[n:]
		if s.buffered == len(s.buffer) {
			s.flush()
		}
	}
	s.write([]byte{0})
	var observed error
	err := chunks(func(raw []byte) error {
		if observed == nil {
			observed = s.chunk(raw)
		}
		return observed
	})
	if observed != nil {
		return "", "", observed
	}
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	for s.tailBytes > 0 {
		value, size := utf8.DecodeRune(s.tail[:s.tailBytes])
		if err := s.rune(value, s.tail[:size]); err != nil {
			return "", "", err
		}
		s.tailBytes = copy(s.tail[:], s.tail[size:s.tailBytes])
	}
	if s.trailingSpace {
		if err := s.restoreState.UnmarshalBinary(s.checkpoint[:s.checkpointLen]); err != nil {
			return "", "", err
		}
	} else {
		s.flush()
	}
	s.finishLine()
	if !s.selected {
		s.description = s.fallback.text()
	}
	var sum [sha256.Size]byte
	encoded := h.Sum(sum[:0])
	return hex.EncodeToString(encoded[:12]), s.description, nil
}
