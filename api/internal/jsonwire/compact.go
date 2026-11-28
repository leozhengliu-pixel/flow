package jsonwire

// Compact counts or appends validated raw JSON. It strips insignificant
// whitespace and applies HTML escaping without rewriting existing escapes or
// numbers. A nil destination only counts.
func Compact(dst, raw []byte) (int, []byte) {
	quoted, escaped := false, false
	size, start := 0, 0
	write := func(end int, replacement string, consumed int) {
		size += end - start + len(replacement)
		if dst != nil {
			dst = append(dst, raw[start:end]...)
			dst = append(dst, replacement...)
		}
		start = end + consumed
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case !quoted && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			write(i, "", 1)
		case escaped:
			escaped = false
		case c == '\\' && quoted:
			escaped = true
		case c == '"':
			quoted = !quoted
		case quoted && (c == '<' || c == '>' || c == '&'):
			write(i, ASCIIEscape(c), 1)
		case quoted && c == 0xe2 && i+2 < len(raw) && raw[i+1] == 0x80:
			switch raw[i+2] {
			case 0xa8:
				write(i, `\u2028`, 3)
				i += 2
			case 0xa9:
				write(i, `\u2029`, 3)
				i += 2
			}
		}
	}
	write(len(raw), "", 0)
	return size, dst
}
