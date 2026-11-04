// A letter or digit with its combining marks, so a decomposed accent ("e" + U+0301) or a Devanagari vowel sign stays attached.
const initial = /[\p{L}\p{N}]\p{M}*/u

/**
 * Up to two initials for a person's name or email, one per word: the first letter or digit of each word, whole (never half a
 * surrogate pair). Emoji and punctuation are skipped, so "🚀 Rocket Team" gives "RT" and an emoji-only name gives `fallback`.
 */
export function avatarInitials(name: string, fallback = '?') {
  return name.split(/[\s@]+/)
    .map(part => part.match(initial)?.[0])
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
