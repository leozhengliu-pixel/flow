package store

import (
	"bufio"
	"context"
	"errors"
	"io"
	"unicode/utf16"
	"unicode/utf8"
)

const MetadataJSONStringChunkBytes = 4096

var ErrMetadataJSONString = errors.New("invalid metadata JSON string")

// VisitMetadataJSONStringChunks decodes one complete quoted JSON string or
// null without retaining a whole token. Null returns true without callbacks;
// an empty string returns false without callbacks. ASCII JSON whitespace is
// accepted before and after the value; other trailing input is rejected.
//
// Chunks contain complete UTF-8 runes, have at most MetadataJSONStringChunkBytes
// bytes, and are valid only during the callback. Invalid UTF-8 and unpaired
// UTF-16 surrogates become U+FFFD exactly as in encoding/json. Callers own
// admission for retained output and must stage publication: a syntax or reader
// error may follow earlier callbacks. Any callback error aborts. A caller
// retaining only a prefix must ignore later chunks while validation continues.
func VisitMetadataJSONStringChunks(ctx context.Context, input io.Reader, visit func([]byte) error) (bool, error) {
	if ctx == nil || input == nil || visit == nil {
		return false, ErrMetadataJSONString
	}
	d := metadataJSONStringDecoder{
		ctx: ctx, input: bufio.NewReaderSize(input, MetadataJSONStringChunkBytes), visit: visit,
	}
	if err := d.spaces(); err != nil {
		return false, err
	}
	b, err := d.peek()
	if err != nil {
		return false, err
	}
	if b == 'n' {
		for _, want := range []byte("null") {
			if err := d.expect(want); err != nil {
				return false, err
			}
		}
		if err := d.end(); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := d.expect('"'); err != nil {
		return false, err
	}
	if err := d.stringBody(); err != nil {
		return false, err
	}
	if err := d.end(); err != nil {
		return false, err
	}
	return false, d.flush()
}

type metadataJSONStringDecoder struct {
	ctx    context.Context
	input  *bufio.Reader
	visit  func([]byte) error
	output [MetadataJSONStringChunkBytes]byte
	used   int
}

func (d *metadataJSONStringDecoder) peek() (byte, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	raw, err := d.input.Peek(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (d *metadataJSONStringDecoder) byte() (byte, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	b, err := d.input.ReadByte()
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return b, err
}

func (d *metadataJSONStringDecoder) expect(want byte) error {
	b, err := d.byte()
	if err != nil {
		return err
	}
	if b != want {
		return ErrMetadataJSONString
	}
	return nil
}

func (d *metadataJSONStringDecoder) spaces() error {
	for {
		b, err := d.peek()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return nil
		}
		if _, err := d.byte(); err != nil {
			return err
		}
	}
}

func (d *metadataJSONStringDecoder) end() error {
	if err := d.spaces(); err != nil {
		return err
	}
	if _, err := d.peek(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return ErrMetadataJSONString
	}
	return nil
}

func (d *metadataJSONStringDecoder) flush() error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.used == 0 {
		return nil
	}
	if err := d.visit(d.output[:d.used]); err != nil {
		return err
	}
	d.used = 0
	return nil
}

func (d *metadataJSONStringDecoder) appendRune(value rune) error {
	size := utf8.RuneLen(value)
	if d.used+size > len(d.output) {
		if err := d.flush(); err != nil {
			return err
		}
	}
	d.used += utf8.EncodeRune(d.output[d.used:], value)
	return nil
}

func (d *metadataJSONStringDecoder) stringBody() error {
	for {
		b, err := d.peek()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if b >= utf8.RuneSelf {
			value, _, err := d.input.ReadRune()
			if err != nil {
				return err
			}
			if err := d.appendRune(value); err != nil {
				return err
			}
			continue
		}
		b, err = d.byte()
		if err != nil {
			return err
		}
		if b == '"' {
			return nil
		}
		if b < 0x20 {
			return ErrMetadataJSONString
		}
		if b == '\\' {
			if err := d.escape(); err != nil {
				return err
			}
		} else if err := d.appendRune(rune(b)); err != nil {
			return err
		}
	}
}

func (d *metadataJSONStringDecoder) escape() error {
	b, err := d.byte()
	if err != nil {
		return err
	}
	switch b {
	case '"', '\\', '/':
		return d.appendRune(rune(b))
	case 'b':
		return d.appendRune('\b')
	case 'f':
		return d.appendRune('\f')
	case 'n':
		return d.appendRune('\n')
	case 'r':
		return d.appendRune('\r')
	case 't':
		return d.appendRune('\t')
	case 'u':
		value, err := d.unicodeEscape()
		if err != nil {
			return err
		}
		if value < 0xd800 || value > 0xdfff {
			return d.appendRune(value)
		}
		if value <= 0xdbff {
			// Inspect a possible following escape without consuming an
			// invalid/lone surrogate. Its own token must still be decoded.
			raw, err := d.input.Peek(2)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if len(raw) == 2 && raw[0] == '\\' && raw[1] == 'u' {
				raw, err = d.input.Peek(6)
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				if len(raw) == 6 {
					next, valid := metadataJSONStringHexRune(raw[2:])
					if valid && next >= 0xdc00 && next <= 0xdfff {
						if err := d.ctx.Err(); err != nil {
							return err
						}
						if _, err := d.input.Discard(6); err != nil {
							return err
						}
						return d.appendRune(utf16.DecodeRune(value, next))
					}
				}
			}
		}
		return d.appendRune(utf8.RuneError)
	default:
		return ErrMetadataJSONString
	}
}

func (d *metadataJSONStringDecoder) unicodeEscape() (rune, error) {
	var raw [4]byte
	for i := range raw {
		b, err := d.byte()
		if err != nil {
			return 0, err
		}
		raw[i] = b
	}
	value, valid := metadataJSONStringHexRune(raw[:])
	if !valid {
		return 0, ErrMetadataJSONString
	}
	return value, nil
}

func metadataJSONStringHexRune(raw []byte) (rune, bool) {
	if len(raw) != 4 {
		return 0, false
	}
	var value rune
	for _, b := range raw {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += rune(b - '0')
		case b >= 'a' && b <= 'f':
			value += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			value += rune(b-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
