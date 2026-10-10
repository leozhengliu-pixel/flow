import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Inline comments and document notifications: every t() string and every
// menu label has Chinese copy.
const sources = import.meta.glob(
  [
    '../components/documents/inline-comments/*.tsx',
    '!../components/documents/inline-comments/*.test.tsx',
    '../components/inbox/hosts/document-inbox-view.tsx',
    '../components/inbox/hosts/document-inbox-model.ts',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\b(?:t|translate)\(\s*'((?:[^'\\]|\\.)*)'\s*\)/g,
  /\b(?:t|translate)\(\s*"((?:[^"\\]|\\.)*)"\s*\)/g,
  /\blabel="([^"]+)"/g,
  /\blabel=\{[^}]*\?\s*'([^']+)'\s*:\s*'([^']+)'\s*\}/g,
  /^\s+document\w+: '([^']+)',$/gm,
]

describe('Document comments translations', () => {
  it('has a Chinese translation for every string', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          for (const text of match.slice(1).filter(Boolean).map(value => value.replace(/\\(["'])/g, '$1'))) {
            if (!/[A-Za-z]{2,}/.test(text)) continue
            if (!(text in zhCN) && translateToChinese(text) === text) missing.push(`${path.split('/').pop()}: ${text}`)
          }
        }
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
