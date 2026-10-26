import { describe, expect, it } from 'vitest'

import { zhCN } from './translations'

// The new issue dialog passes its messages through t(); every key needs zh-CN with the same placeholders.
const source = Object.values(import.meta.glob('../components/create-issue/create-issue-dialog.tsx', { query: '?raw', import: 'default', eager: true }) as Record<string, string>)[0]

const PATTERNS = [
  /\bt\('((?:[^'\\]|\\.)+)'\)/g,
  /\bt\([^()']*\? '([^']+)' : '([^']+)'\)/g,
]

describe('new issue dialog translations', () => {
  it('has Chinese for every message, keeping its placeholders', () => {
    const keys = new Set<string>()
    for (const pattern of PATTERNS) for (const match of source.matchAll(pattern)) for (const text of match.slice(1)) if (text) keys.add(text)
    for (const key of ['Statuses are still loading. Try again in a moment.', 'Choose a status from the {team} workflow', '{team} has no status for new issues']) expect(keys).toContain(key)
    const missing = [...keys].filter(key => {
      const zh = (zhCN as Record<string, string>)[key]
      const placeholders = key.match(/\{\w+\}/g) ?? []
      return !zh || /[A-Za-z]{3,}/.test(zh.replace(/\{\w+\}/g, '')) || placeholders.some(placeholder => !zh.includes(placeholder))
    })
    expect(missing).toEqual([])
  })

  it('never shows an untranslated literal error', () => {
    expect(source.match(/setError\((?:'|`|")/g) ?? []).toEqual([])
  })
})
