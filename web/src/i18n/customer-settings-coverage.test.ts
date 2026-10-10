import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Settings › Customer requests passes English keys through t(); every key needs zh-CN.
const sources = import.meta.glob(
  ['../components/settings/customer-requests-settings.tsx', '../lib/customer-settings.ts'],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\bt\(\s*'((?:[^'\\]|\\.)+)'/g,
  /\? '([A-Z][^']+)' : '([A-Z][^']+)'/g,
  /\b(?:title|description)="([^"]+)"/g,
  /\blabel: '([^']+)'/g,
  /return '([A-Z][^']+)'/g,
  /'(The \$\{label\} name cannot be empty\.|Name is too long\.|Description is too long\.)'/g,
]
// Keys built at runtime (countText and the status/tier labels).
const DYNAMIC = [
  '1 customer', '{count} customers', '1 customer status', '{count} customer statuses', '1 customer tier', '{count} customer tiers',
  '1 exclusion', '{count} exclusions', '1 email', '{count} emails', '1 domain', '{count} domains',
  'The customer status name cannot be empty.', 'The customer tier name cannot be empty.',
]

describe('customer requests settings translations', () => {
  it('has Chinese for every string', () => {
    const missing: string[] = []
    const check = (text: string) => {
      if (!/[A-Za-z]{2,}/.test(text) || text.includes('${')) return
      const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
      if (/[A-Za-z]{3,}/.test(zh.replace(/\{(?:count|name|source|value)\}|\b(?:Flow|Gmail|Outlook|Docs)\b/g, ''))) missing.push(`${text} → ${zh}`)
    }
    for (const source of Object.values(sources)) {
      for (const pattern of PATTERNS) for (const match of source.matchAll(pattern)) for (const text of match.slice(1)) if (text) check(text)
    }
    DYNAMIC.forEach(check)
    expect([...new Set(missing)]).toEqual([])
  })
})
