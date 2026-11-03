import { describe, expect, it } from 'vitest'

import { translateToChinese } from './translate'
import { zhCN } from './translations'

// Every static `t('…')` key (and every literal aria-label / placeholder / title attribute, which the DOM walker
// translates) in the app source has to translate: a key without a zh-CN entry reaches the UI in English.
const sources = import.meta.glob(
  ['../**/*.{ts,tsx}', '!../**/*.test.{ts,tsx}', '!../**/translations*.ts', '!../**/__fixtures__/**', '!../**/test-utils/**'],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const T_CALL = /(?<![\w.$])(?:t|translate)\(\s*(?:'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"|`([^`$\\\n]*)`)\s*[,)]/g
const ATTRIBUTE = /\b(?:aria-label|placeholder|title|aria-roledescription)="([^"{}<>\n]+)"/g

// Keys that are data or identifiers rather than copy, so there is nothing to translate. Say why for each one.
const IGNORED_KEYS = new Set<string>([
  // Product and protocol names, written the same in every language.
  'API', 'Atlassian Cloud', 'Jira', 'Slack', 'SLA',
  // Input examples: URLs, addresses, file names, variable names and secrets are literal values.
  'https://', 'https://…', 'https://...', 'https://example.com/webhook', 'https://example.com/webhooks/flow', 'https://example.com/mcp',
  'https://mcp.example.com', 'https://jira.example.com', 'https://gitlab.your-company.com', 'https://bugreporter.com/flow-app',
  'name@company.com', 'alex@acme.com, jordan@acme.com', 'email@gmail.com, email2@gmail.com…', 'customer.com', 'example.com', '.example.com',
  'github-username', 'lin_api_…', 'FLOW_IDP_CLIENT_SECRET', 'VARIABLE_NAME', 'X-API-Key', 'secret', '.npmrc', 'npm ci', 'pdf, docx, xlsx, txt', 'OPS',
  // The word a person has to type to confirm a merge: it is compared with the input, so it must not change.
  'merge',
  // Spoken names of keyboard shortcuts for screen readers, kept as the key names.
  'Command Option P', 'Command Shift O',
])

function collectKeys(files: Record<string, string>, withAttributes = false) {
  const keys = new Map<string, string>()
  const add = (key: string, path: string) => {
    if (!/[A-Za-z]{2}/.test(key) || IGNORED_KEYS.has(key) || keys.has(key)) return
    keys.set(key, path.replace(/^\.\.\//, ''))
  }
  for (const [path, source] of Object.entries(files)) {
    for (const match of source.matchAll(T_CALL)) add((match[1] ?? match[2] ?? match[3] ?? '').replace(/\\(['"])/g, '$1'), path)
    if (!withAttributes) continue
    for (const match of source.matchAll(ATTRIBUTE)) add(match[1].replace(/&quot;/g, '"').replace(/&amp;/g, '&').replace(/&apos;|&#39;/g, "'"), path)
  }
  return keys
}

describe('t() key coverage', () => {
  it('finds t() keys and attribute literals in the source', () => {
    const sample = "t('Hello there') translate(\"Two words\") t(`Back tick`) t(`has ${x}`) set('Nope here') <a aria-label=\"Attr label\" title=\"x\" />"
    expect([...collectKeys({ 'a.tsx': sample }).keys()]).toEqual(['Hello there', 'Two words', 'Back tick'])
    expect([...collectKeys({ 'a.tsx': sample }, true).keys()]).toEqual(['Hello there', 'Two words', 'Back tick', 'Attr label'])
    expect(collectKeys(sources).size).toBeGreaterThan(500)
  })

  it('has a zh-CN entry for every t() key and literal label', () => {
    const missing = [...collectKeys(sources, true)]
      .filter(([key]) => !(key in zhCN) && translateToChinese(key) === key)
      .map(([key, path]) => `${path}: ${key}`)
      .sort()
    expect(missing).toEqual([])
  })
})
