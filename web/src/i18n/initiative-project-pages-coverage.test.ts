import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Every t() string on the initiative and project pages needs a Chinese entry.
const sources = import.meta.glob(
  [
    '../components/initiatives/initiative-detail-page.tsx',
    '../components/project-detail/*.tsx',
    '!../components/project-detail/*.test.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\b(?:t|translate)\(\s*'((?:[^'\\]|\\.)*)'\s*\)/g,
  /\b(?:t|translate)\(\s*"((?:[^"\\]|\\.)*)"\s*\)/g,
]

describe('initiative and project page translations', () => {
  it('has a Chinese translation for every t() string', () => {
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

  it('translates the strings the zh-CN sweep found in English', () => {
    for (const text of ['Write first initiative update', 'Sub-initiative updates', 'Activity preferences', 'posted an on track update', 'Collapse properties section', 'Expand milestones section']) {
      expect(translateToChinese(text), text).not.toBe(text)
    }
  })
})
