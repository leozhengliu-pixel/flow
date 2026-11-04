import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'

/**
 * `text.replace('{name}', value)` expands `$&`, `$'`, `$$` and `` $` `` inside `value`, so a person named "A$&B" turns into
 * "A{name}B". A replacer function (or `asIs(value)` from lib/as-is) inserts the value as written.
 */
const SRC = join(process.cwd(), 'src')

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap(name => {
    const path = join(directory, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : []
  })
}

function unsafePlaceholderReplacements(file: string) {
  const text = readFileSync(file, 'utf8')
  const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const found: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && ['replace', 'replaceAll'].includes(node.expression.name.text) && node.arguments.length === 2) {
      const [pattern, value] = node.arguments
      const placeholder = ts.isStringLiteralLike(pattern) ? /^\{[\w.]+\}$/.test(pattern.text) : ts.isRegularExpressionLiteral(pattern) && /^\/\\?\{[\w.]+\\?\}\/g?$/.test(pattern.text)
      const literal = (ts.isStringLiteralLike(value) && !value.text.includes('$')) || ts.isNumericLiteral(value) || (ts.isCallExpression(value) && ts.isIdentifier(value.expression) && value.expression.text === 'asIs')
      if (placeholder && !ts.isArrowFunction(value) && !ts.isFunctionExpression(value) && !literal) found.push(`${file}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1}`)
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return found
}

describe('placeholder replacement', () => {
  it('never lets a replacement value expand $ patterns', () => {
    expect(sources(SRC).flatMap(unsafePlaceholderReplacements)).toEqual([])
  })

  it('keeps `$&` in a name when a replacer function fills the placeholder', () => {
    expect('Change start date for {name}'.replace('{name}', () => 'A$&B')).toBe('Change start date for A$&B')
  })
})
