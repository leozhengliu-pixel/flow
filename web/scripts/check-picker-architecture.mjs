import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'

const root = new URL('../src/components/', import.meta.url)
const dedicated = new Set([
  'issue/due-date-picker.tsx',
  'issue/issue-options-menu.tsx',
  'issue/issue-release-picker.tsx',
  'issue/editor/selection-toolbar.tsx',
  'issue/editor/slash-command-menu.tsx',
  // Mentions are an inline editor interaction with caret-relative positioning,
  // so they cannot share the property picker trigger semantics.
  'issue/editor/mention-menu.tsx',
  // Account security's team scope combobox owns filtering and multi-select
  // semantics rather than using an issue property picker.
  'settings/personal-settings.tsx',
])

const files = await collect(root)
const violations = []

for (const file of files) {
  const source = await readFile(file, 'utf8')
  if (!/role=["']listbox["']/.test(source)) continue
  const name = relative(root.pathname, file)
  if (dedicated.has(name)) continue
  if (source.includes('PropertyMenu') || source.includes('usePropertyCommand')) continue
  violations.push(name)
}

if (violations.length) {
  console.error('Picker architecture violations:')
  for (const file of violations) console.error(`- ${file}: reuse PropertyMenu/usePropertyCommand or register a dedicated interaction contract`)
  process.exit(1)
}

async function collect(directory) {
  const output = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory.pathname ?? directory, entry.name)
    if (entry.isDirectory()) output.push(...await collect(path))
    else if (extname(entry.name) === '.tsx') output.push(path)
  }
  return output
}
