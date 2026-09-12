import { describe, expect, it } from 'vitest'

import { createSettingsSearchIndex, searchTeams, SETTINGS_SEARCH_PAGES } from './settings-search'

describe('settings search index', () => {
  it('matches page sections and individual settings without expanding sibling items', () => {
    const index = createSettingsSearchIndex(SETTINGS_SEARCH_PAGES)
    const results = index.search('theme')

    expect(results.filter(result => result.kind !== 'page').map(result => result.title)).toEqual([
      'Code theme',
      'Interface theme',
      'Interface and theme',
    ])
    expect(results.some(result => result.title === 'Font size')).toBe(false)
  })

  it('reuses cached query results for repeated keystrokes and backspacing', () => {
    const index = createSettingsSearchIndex(SETTINGS_SEARCH_PAGES)
    const first = index.search('notification')
    const second = index.search('notification')

    expect(second).toBe(first)
  })

  it('keeps repeated unique queries bounded', () => {
    const index = createSettingsSearchIndex(SETTINGS_SEARCH_PAGES)
    const start = performance.now()
    for (let query = 0; query < 5_000; query += 1) {
      index.search(`missing-theme-${query}`)
    }
    const elapsed = performance.now() - start

    expect(elapsed).toBeLessThan(2000)
  })
})

describe('team settings search', () => {
  const teams = Array.from({ length: 10_000 }, (_, index) => ({
    key: `TEAM${index}`,
    name: `Team ${index}`,
  }))

  it('keeps large team directories bounded and low cost', () => {
    searchTeams('warmup', teams)
    const start = performance.now()
    let emptyResults: ReturnType<typeof searchTeams> = []
    for (let index = 0; index < 200; index += 1) {
      emptyResults = searchTeams(`missing-query-${index}`, teams)
    }
    const elapsed = performance.now() - start

    expect(emptyResults).toEqual([])
    expect(elapsed).toBeLessThan(2000)

    const matches = searchTeams('cycle duration', teams)
    expect(matches).toHaveLength(30)
    expect(matches.every(result => result.title === 'Cycle duration')).toBe(true)
  })
})
