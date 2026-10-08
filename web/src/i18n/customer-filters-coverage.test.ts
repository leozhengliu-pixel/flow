import { describe, expect, it } from 'vitest'

import { translateToChinese } from './i18n'
import { zhCN } from './translations'

// Customer filters, grouping, ordering, display properties, insights and ⌘K entries (Linear parity).
const KEYS = [
  'Customers', 'Customer name', 'Customer count', 'Important customer count', 'Customer owner', 'Customer status', 'Customer tier', 'Customer revenue', 'Customer size',
  'Enter customer count…', 'Enter important customer count…', 'Enter customer revenue…', 'Enter customer size…',
  'greater than or equals', 'less than or equals', 'equals', 'not equals',
  'Unknown customer', 'No owner', 'Current user', 'No customer', 'Customer', 'Important count',
  'customer', 'customers', 'important', 'Monthly revenue', 'Annual revenue', '/mo', '/yr', 'Revenue',
  'Across all customers', 'Hide', 'Display options', 'Grouping', 'Sub-grouping', 'View ordering', 'By customer count', 'By customer revenue',
  'Customers filters', 'No results',
]

const sources = import.meta.glob(
  ['../components/customer/customer-row-properties.tsx', '../components/projects-page/project-customer-filter.tsx', '../components/command/display-commands.ts'],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

describe('customer filter translations', () => {
  it('has Chinese for every customer filter, ordering and display string', () => {
    const missing: string[] = []
    const check = (text: string) => {
      if (!/[A-Za-z]{2,}/.test(text) || text.includes('${')) return
      const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
      if (/[A-Za-z]{3,}/.test(zh) || zh === text && /[A-Za-z]/.test(text)) missing.push(`${text} → ${zh}`)
    }
    KEYS.forEach(check)
    for (const source of Object.values(sources)) for (const match of source.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)+)'/g)) check(match[1])
    expect([...new Set(missing)]).toEqual([])
  })
})
