import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'

const sourceRoot = new URL('../src/', import.meta.url)
const roots = [new URL('../src/components/', import.meta.url)]
const baseline = JSON.parse(await readFile(new URL('./design-token-baseline.json', import.meta.url), 'utf8'))
const colorLiteral = /#[\da-f]{3,8}\b|\b(?:rgb|rgba|hsl|hsla|lch|oklch)\([^)]*\)/gi
const violations = []
const sources = []

for (const root of roots) {
  for (const file of await collect(root)) {
    const source = await readFile(file, 'utf8')
    sources.push(source)
    const path = relative(sourceRoot.pathname, file)
    const matches = [...source.matchAll(colorLiteral)]
    const allowance = baseline[path] ?? 0
    if (matches.length > allowance) {
      for (const match of matches.slice(allowance)) {
        const line = source.slice(0, match.index).split('\n').length
        violations.push(`${path}:${line}: ${match[0]}`)
      }
    }
  }
}

const tokenSource = await readFile(new URL('../src/styles/tokens.css', import.meta.url), 'utf8')
const allCSS = `${tokenSource}\n${sources.join('\n')}`
const definitions = new Set([...allCSS.matchAll(/(?:^|[;{])\s*(--[\w-]+)\s*:/gm)].map(match => match[1]))
const dynamicProperties = new Set([
  '--accent', '--capacity', '--customer-columns', '--dashboard-columns', '--dot-index', '--flow-inbox-avatar', '--insight-columns', '--issue-identifier-width', '--li-extra-columns', '--li-month-count', '--li-month-width', '--li-today-left', '--nest-level', '--nested-depth', '--palette-color', '--picker-hue', '--project-status-color', '--status-color', '--status-hue', '--status-progress', '--sub-depth', '--team-columns', '--timeline-drag-offset', '--timeline-start', '--timeline-width', '--view-color', '--views-columns',
])
for (const match of allCSS.matchAll(/var\(\s*(--[\w-]+)/g)) {
  const property = match[1]
  if (!definitions.has(property) && !dynamicProperties.has(property) && !property.startsWith('--radix-')) {
    violations.push(`undefined CSS custom property: ${property}`)
  }
}

if (violations.length) {
  console.error('Design-token regression found. Add a semantic token in styles/tokens.css or reduce the tracked baseline:')
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
