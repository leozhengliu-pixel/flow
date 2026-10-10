import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// The project / initiative row context menus pass English keys through t(); every key needs zh-CN.
const sources = import.meta.glob(
  [
    '../components/ui/row-context-menu.tsx',
    '../components/ui/reminder-options.tsx',
    '../components/ui/reminder-presets.ts',
    '../components/projects-page/project-row-menu.tsx',
    '../components/initiatives/initiative-row-menu.tsx',
    '../components/initiatives/initiative-header-menus.tsx',
    '../components/initiatives/initiative-hierarchy-section.tsx',
    '../components/initiatives/initiative-hierarchy-actions.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\b(?:label|placeholder|emptyLabel)="([^"{}]+)"/g,
  /\bt\('((?:[^'\\]|\\.)+)'\)/g,
  /\b(?:label|group): '([^']+)'/g,
  /\['[A-Za-z]+', '([^']+)'\]/g,
  /\? '([A-Z][a-z]+(?: [a-z]+)*)' : '([A-Z][a-z]+(?: [a-z]+)*)'/g,
]
const PRIORITIES = /const PRIORITIES = \[([^\]]+)\]/

describe('row context menu translations', () => {
  it('has Chinese for every menu string', () => {
    const missing: string[] = []
    const check = (path: string, text: string) => {
      if (!/[A-Za-z]{2,}/.test(text)) return
      const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
      // A partial fallback ("创建new…") is as broken as none; product words stay English.
      if (/[A-Za-z]{3,}/.test(zh.replace(/\b(?:URL|ID|CSV|Markdown|Slack|Pulse)\b/g, ''))) missing.push(`${path.split('/').pop()}: ${text} → ${zh}`)
    }
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) for (const match of source.matchAll(pattern)) for (const text of match.slice(1)) if (text) check(path, text)
      for (const text of source.match(PRIORITIES)?.[1].match(/'([^']+)'/g) ?? []) check(path, text.slice(1, -1))
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
