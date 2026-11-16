package meter

import (
	"encoding/json"
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

var errJSON = errors.New("invalid collaboration JSON")

// jsonStringWork splits the final string from stdlib unquoteBytes' temporary
// buffers. Width calculation neither produces a value nor accepts JSON; the
// scalar decode below remains encoding/json's, including malformed Unicode.
func jsonStringWork(m *Meter, raw []byte) (retained, temporary uint64) {
	length := uint64(len(raw))
	output, scratch := jsonStringWidth(m, raw)
	retained = Add(AllocationSize(output), AllocationSize(16))
	if scratch {
		// unquoteBytes starts with len(body)+2*utf8.UTFMax and doubles
		// (capacity+utf8.UTFMax) on growth. Sum old/new allocation widths
		// until the output fits, including its eight-byte rune margin.
		capacity := Add(length, 2*utf8.UTFMax)
		temporary = AllocationSize(capacity)
		for capacity < Add(output, 2*utf8.UTFMax) {
			capacity = Mul(Add(capacity, utf8.UTFMax), 2)
			temporary = Add(temporary, AllocationSize(capacity))
		}
	}
	return retained, temporary
}

func jsonStringWidth(m *Meter, raw []byte) (output uint64, scratch bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		// Invalid input is still rejected by encoding/json. This bound
		// covers its scalar error path without indexing a missing quote.
		return Mul(uint64(len(raw)), 3), true
	}
	end, nextCheck := len(raw)-1, 1
	for at := 1; at < end; {
		if at >= nextCheck {
			m.Check()
			nextCheck = at + 4096
		}
		if raw[at] == '\\' {
			scratch = true
			if at+1 >= end {
				return Mul(uint64(len(raw)), 3), true
			}
			if raw[at+1] != 'u' {
				output++
				at += 2
				continue
			}
			code, ok := jsonStringHex4(raw[at+2 : end])
			if !ok {
				return Mul(uint64(len(raw)), 3), true
			}
			at += 6
			if utf16.IsSurrogate(code) {
				if at+6 <= end && raw[at] == '\\' && raw[at+1] == 'u' {
					second, ok := jsonStringHex4(raw[at+2 : end])
					if ok && utf16.DecodeRune(code, second) != utf8.RuneError {
						output += 4
						at += 6
						continue
					}
				}
				output += 3
			} else {
				output += uint64(utf8.RuneLen(code))
			}
			continue
		}
		char, size := utf8.DecodeRune(raw[at:end])
		if char == utf8.RuneError && size == 1 {
			output += 3
			scratch = true
		} else {
			output += uint64(size)
		}
		at += size
	}
	return output, scratch
}

func jsonStringHex4(raw []byte) (rune, bool) {
	if len(raw) < 4 {
		return 0, false
	}
	var result rune
	for _, char := range raw[:4] {
		result *= 16
		switch {
		case '0' <= char && char <= '9':
			result += rune(char - '0')
		case 'a' <= char && char <= 'f':
			result += rune(char-'a') + 10
		case 'A' <= char && char <= 'F':
			result += rune(char-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}

type jsonParser struct {
	meter     *Meter
	traversal *Meter
	raw       []byte
	at        int
}

func (p *jsonParser) space() {
	nextCheck := p.at
	for p.at < len(p.raw) {
		if p.at >= nextCheck {
			p.meter.Check()
			nextCheck = p.at + 4096
		}
		switch p.raw[p.at] {
		case ' ', '\n', '\r', '\t':
			p.at++
		default:
			return
		}
	}
}

func (p *jsonParser) scalar() (any, error) {
	start, nextCheck := p.at, p.at
	if p.raw[p.at] == '"' {
		p.at++
		for p.at < len(p.raw) {
			if p.at >= nextCheck {
				p.meter.Check()
				nextCheck = p.at + 4096
			}
			c := p.raw[p.at]
			p.at++
			if c == '\\' {
				p.at++
			} else if c == '"' {
				break
			}
		}
	} else {
		for p.at < len(p.raw) {
			if p.at >= nextCheck {
				p.meter.Check()
				nextCheck = p.at + 4096
			}
			switch p.raw[p.at] {
			case ',', ':', ']', '}', ' ', '\t', '\r', '\n':
				goto complete
			default:
				p.at++
			}
		}
	}
complete:
	if p.at > len(p.raw) || p.at == start {
		return nil, errJSON
	}
	token := p.raw[start:p.at]
	temporary := uint64(4096) // stdlib scalar decoder/scanner and fixed stack.
	if token[0] == '"' {
		retained, scratch := jsonStringWork(p.meter, token)
		p.meter.Reserve(retained)
		temporary = Add(temporary, scratch)
	} else {
		// A float may box eight bytes. Numeric conversion can concurrently
		// own its input string, strconv's error string, and the JSON type
		// error's "number ..." string. All expire inside this scalar call.
		p.meter.Reserve(AllocationSize(8))
		if token[0] == '-' || token[0] >= '0' && token[0] <= '9' {
			temporary = Add(temporary, Add(Mul(AllocationSize(uint64(len(token))), 2), AllocationSize(Add(uint64(len(token)), 8))))
		}
	}
	releaseTemporary := p.meter.Temporary(temporary)
	defer releaseTemporary()
	// Every byte walk above advances inside this finite token. Check its
	// cancellation/progress without retaining one work token per input byte.
	// Container and CRDT iteration Step quotas remain unchanged.
	p.meter.Check()
	var value any
	if err := json.Unmarshal(token, &value); err != nil {
		// Do not retain stdlib's potentially large numeric error text after
		// releasing temporary ownership. DecodeJSON exposes errJSON's
		// existing invalid-input boundary, not individual scalar errors.
		return nil, errJSON
	}
	p.meter.Check()
	return value, nil
}

func (p *jsonParser) value(depth int) (any, error) {
	p.traversal.Enter()
	defer p.traversal.Leave()
	p.space()
	if p.at == len(p.raw) {
		return nil, errJSON
	}
	switch p.raw[p.at] {
	case '{':
		if depth == 10000 {
			return nil, errJSON
		}
		p.at++
		out := MakeMap[map[string]any, string, any](p.meter, 0)
		p.space()
		if p.at < len(p.raw) && p.raw[p.at] == '}' {
			p.at++
			return out, nil
		}
		for {
			p.traversal.Step()
			p.space()
			if p.at == len(p.raw) || p.raw[p.at] != '"' {
				return nil, errJSON
			}
			key, err := p.scalar()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.at == len(p.raw) || p.raw[p.at] != ':' {
				return nil, errJSON
			}
			p.at++
			value, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			Set(p.meter, out, key.(string), value)
			p.space()
			if p.at == len(p.raw) {
				return nil, errJSON
			}
			if p.raw[p.at] == '}' {
				p.at++
				return out, nil
			}
			if p.raw[p.at] != ',' {
				return nil, errJSON
			}
			p.at++
		}
	case '[':
		if depth == 10000 {
			return nil, errJSON
		}
		p.at++
		out := MakeSlice[[]any, any](p.meter, 0, 0)
		p.space()
		if p.at < len(p.raw) && p.raw[p.at] == ']' {
			p.at++
			return out, nil
		}
		for {
			p.traversal.Step()
			value, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			out = Append[[]any, any](p.meter, out, value)
			p.space()
			if p.at == len(p.raw) {
				return nil, errJSON
			}
			if p.raw[p.at] == ']' {
				p.at++
				return out, nil
			}
			if p.raw[p.at] != ',' {
				return nil, errJSON
			}
			p.at++
		}
	default:
		return p.scalar()
	}
}

func unmarshalJSON(m *Meter, raw []byte, value any) error {
	m.Check()
	// Parser stack/high-water and container traversal expire at decode
	// completion. The original meter retains only native result storage.
	var traversal *Meter
	if m != nil {
		var err error
		traversal, err = NewMeter(m.ctx, m.admit)
		if err != nil {
			m.fail(err)
		}
		defer func() {
			failure := recover()
			var allocationErr error
			if abort, ok := failure.(*Abort); ok {
				allocationErr = abort.Err
			}
			traversal.Close()
			if allocationErr != nil {
				// Close clears its reusable abort object. Propagate the
				// captured failure through the containing graph owner.
				m.fail(allocationErr)
			}
			if failure != nil {
				panic(failure)
			}
		}()
	}
	p := jsonParser{meter: m, traversal: traversal, raw: raw}
	result, err := p.value(0)
	if err != nil {
		return err
	}
	p.space()
	if p.at != len(raw) {
		return errJSON
	}
	*(value.(*any)) = result
	return nil
}
