package jsonwire

import (
	"encoding/json"
	"unicode/utf8"
)

var asciiEscapes = func() [utf8.RuneSelf]string {
	var escapes [utf8.RuneSelf]string
	for i := range escapes {
		encoded, _ := json.Marshal(string(rune(i)))
		if len(encoded) > 3 {
			escapes[i] = string(encoded[1 : len(encoded)-1])
		}
	}
	return escapes
}()

// ASCIIEscape returns the encoding/json escape for an ASCII byte, or an empty
// string when the byte is written as is.
func ASCIIEscape(b byte) string { return asciiEscapes[b] }

func stringEscape(value string, index int) (escape string, width int) {
	if b := value[index]; b < utf8.RuneSelf {
		return asciiEscapes[b], 1
	}
	r, n := utf8.DecodeRuneInString(value[index:])
	switch {
	case r == utf8.RuneError && n == 1:
		return `\ufffd`, n
	case r == '\u2028':
		return `\u2028`, n
	case r == '\u2029':
		return `\u2029`, n
	}
	return "", n
}

// StringBytes includes the quotes and matches encoding/json's HTML escaping.
func StringBytes(value string) int64 {
	size := int64(2)
	for i := 0; i < len(value); {
		escape, width := stringEscape(value, i)
		if escape != "" {
			size += int64(len(escape))
		} else {
			size += int64(width)
		}
		i += width
	}
	return size
}

// AppendString appends the JSON encoding of a string as produced by Marshal.
func AppendString(dst []byte, value string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(value); {
		escape, width := stringEscape(value, i)
		if escape != "" {
			dst = append(dst, value[start:i]...)
			dst = append(dst, escape...)
			start = i + width
		}
		i += width
	}
	dst = append(dst, value[start:]...)
	return append(dst, '"')
}
