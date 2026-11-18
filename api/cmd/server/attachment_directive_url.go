package main

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// encodeDirectiveURL is the one form an attachment URL takes inside body
// Markdown: ![label](url), ::video[label](url) and ::file[label](url){…}.
// The editor's tokenizers (web/src/lib/directive-url.ts mirrors this rule)
// read the URL as a run without whitespace or ")", so the stored attachment
// URL — "/uploads/" + the storage key, which keeps the original file name —
// is percent-encoded only where it could not stand in that run as is:
//
//   - whitespace (every character JavaScript's \s matches, so U+3000 too),
//     control characters, and the bytes of invalid UTF-8;
//   - ( ) < > \ and ` (link-destination and code-span syntax);
//   - a % that does not start a valid %XX escape.
//
// Every other character, CJK included, stays as it is, and an existing %XX
// is kept, so the encoding is idempotent and never double-encodes. Attachment
// records, their URL and the storage key are unchanged; GET /uploads/{name}
// unescapes the path, so the encoded URL resolves to the same object.
func encodeDirectiveURL(raw string) string {
	if !directiveURLNeedsEncoding(raw) {
		return raw
	}
	var out strings.Builder
	out.Grow(len(raw) + 8)
	for index := 0; index < len(raw); {
		r, width := utf8.DecodeRuneInString(raw[index:])
		switch {
		case r == '%' && directiveEscapeAt(raw, index):
			out.WriteByte('%')
		case r == '%':
			out.WriteString("%25")
		case r == utf8.RuneError && width == 1:
			writePercentBytes(&out, raw[index:index+1])
		case directiveURLUnsafeRune(r):
			writePercentBytes(&out, raw[index:index+width])
		default:
			out.WriteString(raw[index : index+width])
		}
		index += width
	}
	return out.String()
}

// directiveURLKey is the comparable form of a URL written in a directive or
// stored on an attachment: canonical encoding, then unescaped. An encoded
// directive URL and the raw attachment URL it came from have the same key.
func directiveURLKey(value string) string {
	encoded := encodeDirectiveURL(value)
	if decoded, err := url.PathUnescape(encoded); err == nil {
		return decoded
	}
	return encoded
}

// containsAttachmentURL reports whether body text already carries the
// attachment's URL, raw (older bodies) or in its directive encoding.
func containsAttachmentURL(body, attachmentURL string) bool {
	if attachmentURL == "" {
		return false
	}
	return strings.Contains(body, attachmentURL) || strings.Contains(body, encodeDirectiveURL(attachmentURL))
}

func directiveURLNeedsEncoding(raw string) bool {
	for index := 0; index < len(raw); {
		r, width := utf8.DecodeRuneInString(raw[index:])
		if r == '%' {
			if !directiveEscapeAt(raw, index) {
				return true
			}
		} else if (r == utf8.RuneError && width == 1) || directiveURLUnsafeRune(r) {
			return true
		}
		index += width
	}
	return false
}

func directiveURLUnsafeRune(r rune) bool {
	switch r {
	case '(', ')', '<', '>', '\\', '`', 0xFEFF:
		return true
	}
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.IsSpace(r)
}

func writePercentBytes(out *strings.Builder, value string) {
	const hex = "0123456789ABCDEF"
	for index := 0; index < len(value); index++ {
		out.WriteByte('%')
		out.WriteByte(hex[value[index]>>4])
		out.WriteByte(hex[value[index]&0x0f])
	}
}

// directiveEscapeAt reports whether raw[index] starts a valid %XX escape.
func directiveEscapeAt(raw string, index int) bool {
	return index+2 < len(raw) && directiveHexDigit(raw[index+1]) && directiveHexDigit(raw[index+2])
}

func directiveHexDigit(value byte) bool {
	return '0' <= value && value <= '9' || 'a' <= value && value <= 'f' || 'A' <= value && value <= 'F'
}
