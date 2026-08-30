import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'

const sourceRoot = new URL('../src/', import.meta.url)
const noOpHandler = /on[A-Z][A-Za-z0-9]*\s*=\s*\{\s*\(\s*\)\s*=>\s*(?:undefined|null|\{\s*\})\s*\}/g
const inertHref = /href\s*=\s*(?:\{\s*)?["'](?:#|javascript:[^"']*)["']\s*\}?/gi
const violations = []

for (const file of await collect(sourceRoot)) {
  if (/\.(?:story|stories)\.tsx$|(?:^|[-.])fixture\.tsx$/i.test(file)) continue
  const source = await readFile(file, 'utf8')
  for (const match of source.matchAll(noOpHandler)) {
    const line = source.slice(0, match.index).split('\n').length
    violations.push(`${relative(sourceRoot.pathname, file)}:${line}: ${match[0]}`)
  }
  for (const match of source.matchAll(inertHref)) {
    const line = source.slice(0, match.index).split('\n').length
    violations.push(`${relative(sourceRoot.pathname, file)}:${line}: ${match[0]}`)
  }
}

if (violations.length) {
  console.error('No-op UI handlers found. Wire the control to behavior, mark a supported disabled state, or render non-interactive content:')
  for (const violation of violations) console.error(`- ${violation}`)
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
