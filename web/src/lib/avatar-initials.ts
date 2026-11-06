// The word-start test, a whole letter with its combining marks, so a decomposed accent ("e" + U+0301) or a Devanagari vowel sign stays attached.
const wordLetter = /^\p{L}\p{M}*/u
// Letters the avatar font can draw: ASCII, Latin-1 and digits. A name with none of them in its initials is not drawn by the reference.
const drawable = /[A-Za-z\u00C0-\u00ff0-9]/
const anyLetterOrDigit = /[\p{L}\p{N}]/u
const letterOrDigit = /[\p{L}\p{N}]\p{M}*/u

/** The first `count` UTF-16 units of `text`, without a trailing half of a surrogate pair. */
function leadingUnits(text: string, count: number) {
  const head = text.slice(0, count)
  return /[\ud800-\udbff]$/.test(head) ? head.slice(0, -1) : head
}

/**
 * The initials of a person's name or email, chosen like the reference app's avatar:
 * - an email uses its local part, with the first "." read as a space ("skyler.anderson@x" is "Skyler Anderson");
 * - only words that start with a letter count: "[mobile] Skyler Anderson" is "SA";
 * - two words give the first letter of each ("SA"); three or more give the first word's letter and the last two words' ("Flow QA OAuth app" is "FOA");
 * - one word (or none that start with a letter) gives its first two characters, punctuation included ("Hui" is "HU", "O'Neil" is "O'");
 * - the result is upper-cased and is drawn only when it holds an ASCII, Latin-1 or digit character, otherwise `fallback`.
 * Names the reference leaves blank because the font has no glyph (Chinese, Cyrillic, Greek, Arabic, ...) keep one letter per word, up to
 * two words, so a Chinese name still shows "张" instead of an empty circle. Characters are whole code points, never half a surrogate pair.
 */
export function avatarInitials(name: string, fallback = '?') {
  const text = name.includes('@') ? name.split('@')[0].replace('.', ' ') : name
  const lead = (text.match(/\S+/g) ?? []).map(word => word.match(wordLetter)?.[0]).filter((letter): letter is string => Boolean(letter))
  const value = lead.length > 2 ? lead[0] + lead.slice(-2).join('') : lead.length === 2 ? lead.join('') : leadingUnits(text, 2)
  if (drawable.test(value)) return value.toUpperCase()
  if (!anyLetterOrDigit.test(value)) return fallback
  return text.split(/\s+/)
    .map(part => part.match(letterOrDigit)?.[0])
    .filter(Boolean)
    .slice(0, 2)
    .join('')
    .toUpperCase() || fallback
}

const graphemes = new Intl.Segmenter(undefined, { granularity: 'grapheme' })

/** The first `count` user-perceived characters of a label, never half an emoji or surrogate pair (for team, customer and workspace marks). */
export function leadingCharacters(value: string, count = 1) {
  let result = ''
  let taken = 0
  for (const { segment } of graphemes.segment(value)) {
    if (taken++ >= count) break
    result += segment
  }
  return result
}
