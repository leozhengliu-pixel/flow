import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Every Pulse string goes through t()/translate(); each one needs a Chinese entry.
const sources = import.meta.glob(
  [
    '../components/pulse/*.tsx',
    '!../components/pulse/*.test.tsx',
    '../components/inbox/pulse-*.tsx',
    '!../components/inbox/*.test.tsx',
    '../components/layout/sidebar.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\b(?:t|translate)\(\s*'((?:[^'\\]|\\.)*)'\s*\)/g,
  /\b(?:t|translate)\(\s*"((?:[^"\\]|\\.)*)"\s*\)/g,
]

describe('Pulse translations', () => {
  it('has a Chinese translation for every t() string in Pulse surfaces', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          const text = match[1].replace(/\\(["'])/g, '$1')
          if (!/[A-Za-z]{2,}/.test(text)) continue
          if (!(text in zhCN) && translateToChinese(text) === text) missing.push(`${path.split('/').pop()}: ${text}`)
        }
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
