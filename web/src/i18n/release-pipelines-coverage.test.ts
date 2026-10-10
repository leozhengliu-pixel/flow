import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Settings › Releases (list, new pipeline, pipeline settings, access key, CI setup) passes English keys through t().
const sources = import.meta.glob(
  [
    '../components/releases/pipeline-editor-page.tsx',
    '../components/releases/pipeline-access-key.tsx',
    '../components/releases/pipeline-settings-list.tsx',
    '../components/releases/pipeline-delete-dialog.tsx',
    '../components/releases/pipeline-stage-editor.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\bt\('((?:[^'\\]|\\.)+)'\)/g,
  /\bt\("((?:[^"\\]|\\.)+)"\)/g,
  /\b(?:label|description|hint): '([^']+)'/g,
  /\breturn '([A-Z][^']+\.)'/g,
  /\bheader\('\w+', '([^']+)'\)/g,
  /\bt\([a-z]+ \? '([^']+)' : '([^']+)'\)/g,
  /\bt\([a-z.]+ === '[a-z]+' \? '([^']+)' : '([^']+)'\)/g,
]

describe('release pipeline settings translations', () => {
  it('has Chinese for every string', () => {
    const missing: string[] = []
    const seen = new Set<string>()
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) for (const match of source.matchAll(pattern)) for (const raw of match.slice(1)) {
        const text = raw?.replace(/\\'/g, "'")
        if (!text || seen.has(text) || !/[A-Za-z]{2,}/.test(text)) continue
        seen.add(text)
        const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
        // Product names, placeholders and code stay English.
        if (/[A-Za-z]{3,}/.test(zh.replace(/\b(?:GitHub Actions|GitHub|API|CI|URL|cURL)\b|\{[a-z]+\}|\{\{issues\}\}/g, ''))) missing.push(`${path.split('/').pop()}: ${text} → ${zh}`)
      }
    }
    expect(seen.size).toBeGreaterThan(60)
    expect(missing).toEqual([])
  })

  it('uses one term for pipelines', () => {
    for (const key of ['Pipeline name', 'Filter by pipeline name…', 'Create pipeline', 'Recently deleted pipelines', 'New pipeline', 'Release pipelines']) {
      expect((zhCN as Record<string, string>)[key]).toContain('管线')
      expect((zhCN as Record<string, string>)[key]).not.toContain('流水线')
    }
  })
})
