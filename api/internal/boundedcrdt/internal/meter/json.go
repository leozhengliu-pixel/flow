package meter

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var errJSON = errors.New("invalid collaboration JSON")

// jsonStringCost follows encoding/json.unquoteBytes: ordinary strings alias
// the input until their final string copy; escapes allocate one scratch buffer;
// malformed UTF-8 can triple output and geometrically grow that buffer.
func jsonStringCost(raw []byte) uint64 {
	length := uint64(len(raw))
	invalidUTF8 := !utf8.Valid(raw)
	escaped := bytes.IndexByte(raw, '\\') >= 0
	cost := uint64(128)
	output := length
	if invalidUTF8 {
		output = Mul(length, 3)
	}
	if escaped || invalidUTF8 {
		capacity := Add(length, 8)
		cost = Add(cost, AllocationSize(capacity))
		for capacity < Add(output, 8) {
			capacity = Mul(Add(capacity, 4), 2)
			cost = Add(cost, AllocationSize(capacity))
		}
	}
	return Add(cost, AllocationSize(output))
}

type jsonParser struct {
	meter *Meter
	raw   []byte
	at    int
}

func (p *jsonParser) space() {
	for p.at < len(p.raw) {
		p.meter.Step()
		switch p.raw[p.at] {
		case ' ', '\n', '\r', '\t':
			p.at++
		default:
			return
		}
	}
}

func (p *jsonParser) scalar() (any, error) {
	start := p.at
	if p.raw[p.at] == '"' {
		p.at++
		for p.at < len(p.raw) {
			p.meter.Step()
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
			p.meter.Step()
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
	// String escape/Unicode replacement, float64 conversion, and literal
	// acceptance remain the standard library's; it sees no containers.
	p.meter.Reserve(1024)
	var value any
	if err := json.Unmarshal(p.raw[start:p.at], &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (p *jsonParser) value(depth int) (any, error) {
	p.meter.Enter()
	defer p.meter.Leave()
	p.space()
	if p.at == len(p.raw) {
		return nil, errJSON
	}
	switch p.raw[p.at] {
	case '{':
		if depth == 10000 {
			// encoding/json's scanner maxNestingDepth, preserved.
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
			p.meter.Step()
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
			p.meter.Step()
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
	JSON(m, raw)
	p := jsonParser{meter: m, raw: raw}
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
