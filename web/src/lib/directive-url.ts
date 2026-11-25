/**
 * The one form an attachment URL takes inside body Markdown — `![label](url)`, `::video[label](url)` and
 * `::file[label](url){…}` — shared with the server (api/cmd/server/attachment_directive_url.go).
 *
 * The editor's tokenizers read the URL as a run without whitespace or `)`, so the attachment URL (which keeps the
 * original file name) is percent-encoded only where it could not stand in that run as is: whitespace (everything
 * `\s` matches, U+3000 included), control characters, `( ) < > \` and backtick, and a `%` that does not start a
 * valid `%XX` escape. Everything else, CJK included, stays; an existing `%XX` is kept, so encoding is idempotent and
 * never double-encodes. The attachment record, its URL and the storage key do not change: the upload route
 * unescapes the path, so the encoded URL downloads the same file.
 */
export function encodeDirectiveUrl(raw: string) {
  if (!raw || !needsEncoding(raw)) return raw
  let out = ''
  for (let index = 0; index < raw.length;) {
    const point = raw.codePointAt(index) ?? 0
    const char = String.fromCodePoint(point)
    if (char === '%') out += validEscapeAt(raw, index) ? '%' : '%25'
    else if (unsafeCodePoint(point)) out += percentBytes(char)
    else out += char
    index += char.length
  }
  return out
}

/** The comparable form of a directive or attachment URL: an encoded directive URL and the raw attachment URL match. */
export function directiveUrlKey(value: string) {
  const encoded = encodeDirectiveUrl(value)
  try { return decodeURIComponent(encoded) }
  catch { return encoded }
}

/** Whether two URLs name the same attachment, whichever of them is encoded for a directive. */
export function sameDirectiveUrl(a: string, b: string) {
  return a === b || directiveUrlKey(a) === directiveUrlKey(b)
}

function needsEncoding(raw: string) {
  for (let index = 0; index < raw.length;) {
    const point = raw.codePointAt(index) ?? 0
    if (point === 0x25 ? !validEscapeAt(raw, index) : unsafeCodePoint(point)) return true
    index += point > 0xffff ? 2 : 1
  }
  return false
}

function validEscapeAt(raw: string, index: number) {
  return /^%[0-9A-Fa-f]{2}/.test(raw.slice(index, index + 3))
}

function unsafeCodePoint(point: number) {
  if (point < 0x20 || (point >= 0x7f && point <= 0x9f)) return true
  // Lone surrogates have no UTF-8 form; they are written as U+FFFD bytes.
  if (point >= 0xd800 && point <= 0xdfff) return true
  return '()<>\\`'.includes(String.fromCodePoint(point)) || /\s/u.test(String.fromCodePoint(point))
}

function percentBytes(char: string) {
  let out = ''
  for (const byte of new TextEncoder().encode(char)) out += `%${byte.toString(16).toUpperCase().padStart(2, '0')}`
  return out
}
