import { describe, expect, it } from 'vitest'

import { translateToChinese } from './i18n'
import { zhCN } from './translations'

// The rich-text editor's chrome is rendered inside React (the DOM translator skips ProseMirror), so every
// label passed to t() / listed as a slash command or toolbar item needs a real zh-CN entry.
const sources = import.meta.glob(
  [
    '../components/issue/issue-description-editor.tsx',
    '../components/issue/editor/slash-command-menu.tsx',
    '../components/issue/editor/selection-toolbar.tsx',
    '../components/issue/editor/empty-line-hint.ts',
    '../components/editor/code-block/code-block-view.tsx',
    '../components/editor/table/table-chrome.tsx',
    '../components/editor/table/table-commands.ts',
    '../components/editor/outline/outline-minimap.tsx',
    '../components/editor/callout/callout-node-view.tsx',
    '../components/editor/embeds/embed-node-view.tsx',
    '../components/editor/embeds/embed-extension.ts',
    '../components/editor/embeds/embed-toolbar.tsx',
    '../components/editor/embeds/file-preview-card.tsx',
  ],
  { query: '?raw', import: 'default', eager: true },
) as Record<string, string>

const PATTERNS = [
  /\bt\('((?:[^'\\]|\\.)+)'\)/g,
  /\bt\("((?:[^"\\]|\\.)+)"\)/g,
  /\blabel: '([^']+)'/g,
  /\baria-label=\{t\('([^']+)'\)\}/g,
]

describe('editor chrome translations', () => {
  it('has Chinese for every editor label', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      for (const pattern of PATTERNS) {
        for (const match of source.matchAll(pattern)) {
          const text = match[1]
          if (!/[A-Za-z]{2,}/.test(text)) continue
          const zh = (zhCN as Record<string, string>)[text] ?? translateToChinese(text)
          if (/[A-Za-z]{3,}/.test(zh.replace(/\{[a-zA-Z]+\}/g, '').replace(/\b(?:URL|ID|CSV|Markdown|Slack|Pulse|GitHub|GitLab|Figma|Miro|Tella|Loom|YouTube|Descript)\b/g, ''))) missing.push(`${path.split('/').pop()}: ${text} -> ${zh}`)
        }
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })

  it('translates the slash menu, placeholders and toolbar menu', () => {
    for (const key of ['Heading 4', 'Loading file preview…', 'No access to this file', 'Connect {provider} to preview this file', 'Type / for commands…', 'Start writing…', 'No results found', 'Dismiss', 'Heading 1', 'Collapsible section', 'Insert media…', 'Attach files…', 'Regular text', 'Callout', 'Divider', 'Table']) {
      expect(/[一-鿿]/.test((zhCN as Record<string, string>)[key] ?? translateToChinese(key)), key).toBe(true)
    }
  })
})
