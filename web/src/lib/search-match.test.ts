import { describe, expect, it } from 'vitest'
import { fuzzyContains, matchesSearch, normalizeSearchText, searchValue, searchVariants, translatedKeywords } from './search-match'

const zh: Record<string, string> = { Status: '状态', Priority: '优先级', 'Change status…': '更改状态…' }
const t = (source: string) => zh[source] ?? source

describe('search matching', () => {
  it('normalizes case and width', () => {
    expect(normalizeSearchText('  ＡBc ')).toBe('abc')
  })

  it('matches English and translated text, never the other way around', () => {
    expect(matchesSearch('status', ['Status'], t)).toBe(true)
    expect(matchesSearch('状态', ['Status'], t)).toBe(true)
    expect(matchesSearch('优先', ['Status'], t)).toBe(false)
    expect(matchesSearch('状态', ['Status'])).toBe(false)
  })

  it('matches keywords and joins parts without crossing languages', () => {
    expect(matchesSearch('tags', ['Labels', 'tags change labels'], t)).toBe(true)
    expect(matchesSearch('更改', ['Change status…', 'edit'], t)).toBe(true)
    expect(matchesSearch('状态a', ['Status', 'alias'], t)).toBe(false)
  })

  it('leaves user-entered names alone and treats an empty query as a match', () => {
    expect(matchesSearch('', ['Anything'], t)).toBe(true)
    expect(matchesSearch('  ', ['Anything'], t)).toBe(true)
    expect(matchesSearch('bug', ['Bug'], t)).toBe(true)
    expect(searchVariants(['Bug'], t)).toEqual(['bug'])
  })

  it('supports ordered-character fuzzy matching on request', () => {
    expect(fuzzyContains('change status', 'chst')).toBe(true)
    expect(fuzzyContains('change status', 'sc')).toBe(false)
    expect(matchesSearch('chst', ['Change status…'], t)).toBe(false)
    expect(matchesSearch('chst', ['Change status…'], t, { fuzzy: true })).toBe(true)
    expect(matchesSearch('更状', ['Change status…'], t, { fuzzy: true })).toBe(true)
  })

  it('builds cmdk values and keywords carrying both languages', () => {
    expect(searchValue(['Status'], t)).toBe('Status 状态')
    expect(searchValue(['Bug'], t)).toBe('Bug')
    expect(searchValue(['Status'])).toBe('Status')
    expect(translatedKeywords(['Priority'], t)).toEqual(['优先级'])
    expect(translatedKeywords(['Priority', 'Status'], t)).toEqual(['优先级 状态'])
    expect(translatedKeywords(['Bug'], t)).toEqual([])
    expect(translatedKeywords(['Bug'])).toEqual([])
  })
})
