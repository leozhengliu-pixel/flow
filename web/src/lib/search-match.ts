/**
 * Locale-aware search matching. The UI translates system labels at render time (`t(label)`), so a
 * search box must match what the user reads (Chinese) as well as the English source text, keywords
 * and aliases. Every picker, filter menu and command list funnels through these helpers.
 */

export type SearchTranslate = (source: string) => string
export type SearchPart = string | null | undefined | false

/** Lower-cased, width-normalised form used on both the query and the searchable text. */
export function normalizeSearchText(value: string) {
  return value.normalize('NFKC').toLocaleLowerCase().trim()
}

/** Whether every character of `needle` appears in `value` in order (the pickers' existing fuzzy rule). */
export function fuzzyContains(value: string, needle: string) {
  let offset = 0
  for (const character of needle) {
    offset = value.indexOf(character, offset)
    if (offset < 0) return false
    offset += 1
  }
  return true
}

/**
 * The text a row can be found by: the English parts joined, and (when it differs) the same parts
 * as translated. User-entered names pass through `t` unchanged, so they never duplicate.
 */
export function searchVariants(parts: readonly SearchPart[], t?: SearchTranslate): string[] {
  const present = parts.filter((part): part is string => typeof part === 'string' && part.trim().length > 0)
  const english = present.join(' ')
  const variants = [english]
  if (t) {
    const translated = present.map(part => t(part)).join(' ')
    if (translated !== english) variants.push(translated)
  }
  return variants.map(normalizeSearchText)
}

/**
 * True when the query is found in the English or the translated text of the parts. Substring first;
 * `fuzzy` additionally accepts an in-order character subsequence within a single variant.
 */
export function matchesSearch(query: string, parts: readonly SearchPart[], t?: SearchTranslate, { fuzzy = false }: { fuzzy?: boolean } = {}) {
  const needle = normalizeSearchText(query)
  if (!needle) return true
  const variants = searchVariants(parts, t)
  if (variants.some(variant => variant.includes(needle))) return true
  return fuzzy && variants.some(variant => fuzzyContains(variant, needle))
}

/**
 * A cmdk `value`/`keywords` string carrying both languages so cmdk's own scorer finds either.
 * English-only sessions get the plain English text back (ranking unchanged).
 */
export function searchValue(parts: readonly SearchPart[], t?: SearchTranslate) {
  return [...new Set(searchVariantsRaw(parts, t))].join(' ')
}

function searchVariantsRaw(parts: readonly SearchPart[], t?: SearchTranslate) {
  const present = parts.filter((part): part is string => typeof part === 'string' && part.trim().length > 0)
  const english = present.join(' ')
  if (!t) return [english]
  const translated = present.map(part => t(part)).join(' ')
  return translated === english ? [english] : [english, translated]
}

/** Only the translated text of `parts`, for cmdk's `keywords` prop (empty when translation changes nothing). */
export function translatedKeywords(parts: readonly SearchPart[], t?: SearchTranslate): string[] {
  const variants = searchVariantsRaw(parts, t)
  return variants.length > 1 ? [variants[1]] : []
}
