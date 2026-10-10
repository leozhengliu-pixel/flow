import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// In-app Releases views (directory, pipeline, changelog, release detail, composer) pass English keys through t().
const sources = import.meta.glob(
  [
    '../components/releases/releases-page.tsx',
    '../components/releases/release-*.tsx',
    '../components/releases/use-release-actions.tsx',
    '../components/releases/release-view-model.ts',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\bt\('((?:[^'\\]|\\.)+)'\)/g,
  /\bt\("((?:[^"\\]|\\.)+)"\)/g,
  /\blabel(?:=|: )['"]([^'"]+)['"]/g,
  /\bplaceholder="([^"]+)"/g,
  /\bt\([a-zA-Z.!]+ \? '([^']+)' : '([^']+)'\)/g,
  /\bt\([a-zA-Z.!]+ === '[a-z-]+' \? '([^']+)' : '([^']+)'\)/g,
  /\['[a-zA-Z]+', '([A-Z][^']+)'\]/g,
]

describe('release views translations', () => {
  it('has Chinese for every string', () => {
    const missing: string[] = []
    const seen = new Set<string>()
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) for (const match of source.matchAll(pattern)) for (const raw of match.slice(1)) {
        const text = raw?.replace(/\\'/g, "'")
        if (!text || seen.has(text) || !/[A-Za-z]{2,}/.test(text) || /^(?:https?:|flow-|[a-z]+-[a-z-]+$)/.test(text)) continue
        seen.add(text)
        const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
        if (/[A-Za-z]{3,}/.test(zh.replace(/\b(?:Agent|URL|CI|CD)\b|\{[a-z]+\}/g, ''))) missing.push(`${path.split('/').pop()}: ${text} → ${zh}`)
      }
    }
    expect(seen.size).toBeGreaterThan(80)
    expect(missing).toEqual([])
  })
})
