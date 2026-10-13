/**
 * Project and initiative URLs read `<name-slug>-<12 hex>`, as the reference app's do: the hex suffix is stable for the record's
 * life, so a link keeps working after a rename (only the prefix changes) and two records with the same name get
 * distinct URLs. A link is resolved by its suffix; the prefix may be stale. Links made with the legacy name-only slug
 * (before slugs carried a suffix) resolve through `previousSlugIds`. Mirrors `domain.MatchSlugRef` in the API.
 */

export type SlugRecord = { id: string; slugId: string; previousSlugIds?: string[] }

const SUFFIX = /^[0-9a-f]{12}$/

/** The stable 12-hex suffix of a slug (`name-1a2b3c4d5e6f` or a bare `1a2b3c4d5e6f`), or undefined for a legacy slug. */
export function slugSuffix(slugId: string | undefined): string | undefined {
  const value = (slugId ?? '').trim().toLowerCase()
  if (SUFFIX.test(value)) return value
  const tail = value.slice(value.lastIndexOf('-') + 1)
  return value.includes('-') && SUFFIX.test(tail) ? tail : undefined
}

/** How a reference names a record: 3 = id or current slug, 2 = same stable suffix, 1 = an earlier slug, 0 = not at all. */
export function slugRefMatch(record: SlugRecord, ref: string): 0 | 1 | 2 | 3 {
  const value = ref.trim()
  if (!value) return 0
  if (record.id === value || record.slugId === value) return 3
  const suffix = slugSuffix(value)
  if (suffix && suffix === slugSuffix(record.slugId)) return 2
  return record.previousSlugIds?.includes(value) ? 1 : 0
}

/** The record an id or a (current, stale-prefix or legacy) slug names: the best match, the first among equals. */
export function findBySlugRef<T extends SlugRecord>(records: readonly T[] | undefined, ref: string | undefined): T | undefined {
  if (!records || !ref) return undefined
  let best: T | undefined
  let bestMatch = 0
  for (const record of records) {
    const match = slugRefMatch(record, ref)
    if (match > bestMatch) {
      best = record
      bestMatch = match
      if (match === 3) break
    }
  }
  return best
}
