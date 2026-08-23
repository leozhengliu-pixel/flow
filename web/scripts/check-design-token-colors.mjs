import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'

const sourceRoot = new URL('../src/', import.meta.url)
const roots = [
  new URL('../src/components/my-issues/', import.meta.url),
  new URL('../src/components/issue-explorer/', import.meta.url),
  new URL('../src/components/property/', import.meta.url),
]
const colorLiteral = /#[\da-f]{3,8}\b|\b(?:rgb|rgba|hsl|hsla|lch|oklch)\([^)]*\)/gi
const violations = []

for (const root of roots) {
  for (const file of await collect(root)) {
    const source = await readFile(file, 'utf8')
    for (const match of source.matchAll(colorLiteral)) {
      const line = source.slice(0, match.index).split('\n').length
      violations.push(`${relative(sourceRoot.pathname, file)}:${line}: ${match[0]}`)
    }
  }
}

if (violations.length) {
  console.error('Hardcoded UI colors found. Add a semantic token in styles/tokens.css instead:')
  for (const violation of violations) console.error(`- ${violation}`)
  process.exit(1)
}

async function collect(directory) {
  const output = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory.pathname ?? directory, entry.name)
    if (entry.isDirectory()) output.push(...await collect(path))
    else if (extname(entry.name) === '.css') output.push(path)
  }
  return output
}
