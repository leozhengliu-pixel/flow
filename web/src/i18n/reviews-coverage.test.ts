import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Every Reviews string goes through t(); each one needs a Chinese entry.
const sources = import.meta.glob(['../components/reviews/*.tsx', '!../components/reviews/*.test.tsx'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

const PATTERNS = [
  /\bt\(\s*'((?:[^'\\]|\\.)*)'\s*\)/g,
  /\bt\(\s*"((?:[^"\\]|\\.)*)"\s*\)/g,
  /\bt\(\s*(?:[^()]*\?\s*)"((?:[^"\\]|\\.)*)"\s*:\s*"((?:[^"\\]|\\.)*)"\s*\)/g,
]

describe('Reviews translations', () => {
  it('has a Chinese translation for every t() string in Reviews', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          for (const raw of match.slice(1)) {
            if (!raw) continue
            const text = raw.replace(/\\(["'])/g, '$1')
            if (!/[A-Za-z]{2,}/.test(text)) continue
            if (!(text in zhCN) && translateToChinese(text) === text) missing.push(`${path.split('/').pop()}: ${text}`)
          }
        }
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
