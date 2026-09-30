import { describe, expect, it } from 'vitest'

import { translateToChinese } from './i18n'
import { zhCN } from './translations'

// Settings copy is translated through t()/p() or the DOM translator, both of which read zhCN.
// Scan the settings sources for literal UI strings and make sure each one has a Chinese entry,
// so new settings rows don't ship English-only in zh-CN.
const sources = import.meta.glob(
  [
    '../components/settings/*.tsx',
    '!../components/settings/*.test.tsx',
    '../components/inbox/hosts/priority-inbox-settings*.ts{,x}',
    '../components/automation/automation-trusted-source-editor.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\b(?:t|p)\(\s*"((?:[^"\\]|\\.)*)"\s*[,)]/g,
  /\b(?:t|p)\(\s*'((?:[^'\\]|\\.)*)'\s*[,)]/g,
  /\b(?:title|description)[=:]\s*"([^"{}]*[A-Za-z]{3,}[^"{}]*)"/g,
  /\b(?:title|description):\s*'([^'{}]*[A-Za-z]{3,}[^']*)'/g,
]

/** Product names, identifiers, and example values stay as written. */
const UNTRANSLATED = new Set([
  'Flow', 'Slack', 'Jira', 'API', 'DNS', 'HEX', 'OPS', 'MCPs', 'Claude', 'Codex', 'Claude Sonnet', 'Claude Opus',
  'Atlassian Cloud', 'Promise', 'displayName', 'owner/repo', 'Select', 'members',
])
const isLiteralValue = (value: string) =>
  UNTRANSLATED.has(value) ||
  /^https?:\/\//.test(value) ||
  /^[\w.+-]+@[\w.-]+$/.test(value) ||
  /^[\w-]+(\.[\w-]+)+$/.test(value) ||
  /^[A-Z0-9_]+$/.test(value) ||
  /^[a-z_]+…$/.test(value) ||
  /^[a-z]+(, [a-z]+)+$/.test(value)

function personalOverrides(): string {
  const source = Object.entries(sources).find(([path]) => path.endsWith('/personal-settings.tsx'))?.[1] ?? ''
  return source.slice(source.indexOf('const PERSONAL_ZH'), source.indexOf('export function PersonalSettings'))
}

describe('settings translations', () => {
  it('has a Chinese translation for every literal settings string', () => {
    const personal = personalOverrides()
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          const text = match[1].replace(/\\(["'])/g, '$1').replace(/\s+/g, ' ').trim()
          if (!/[A-Za-z]{2,}/.test(text) || isLiteralValue(text)) continue
          if (personal.includes(`${JSON.stringify(text)}:`) || personal.includes(`  ${text}:`)) continue
          if (!(text in zhCN) && translateToChinese(text) === text) missing.push(`${path.split('/').pop()}: ${text}`)
        }
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
