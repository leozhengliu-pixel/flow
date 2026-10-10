/**
 * A small Markdown -> ProseMirror JSON converter for old document versions that never stored `contentData`.
 * It only has to read what the editors' Markdown projection produces: headings, paragraphs, bullet / numbered / task
 * lists (nested by indentation), block quotes, fenced code, rules, tables, plus bold / italic / strike / code / links.
 * Anything else degrades to a paragraph of plain text. The result feeds the same read-only renderer as `contentData`.
 */
import type { PMMark, PMNode } from './document-history-diff'

const text = (value: string, marks?: PMMark[]): PMNode => ({ type: 'text', text: value, ...(marks?.length ? { marks } : {}) })
const paragraph = (content: PMNode[]): PMNode => (content.length ? { type: 'paragraph', content } : { type: 'paragraph' })

const INLINE = /(\*\*|__)(?=\S)([\s\S]*?\S)\1|(\*|_)(?=\S)([\s\S]*?\S)\3|~~(?=\S)([\s\S]*?\S)~~|`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)/

export function parseInline(source: string, marks: PMMark[] = []): PMNode[] {
  const out: PMNode[] = []
  let rest = source.replace(/\\([\\`*_{}[\]()#+\-.!~|>])/g, '$1')
  while (rest) {
    const match = INLINE.exec(rest)
    if (!match) { out.push(text(rest, marks)); break }
    if (match.index > 0) out.push(text(rest.slice(0, match.index), marks))
    if (match[2] !== undefined) out.push(...parseInline(match[2], [...marks, { type: 'bold' }]))
    else if (match[4] !== undefined) out.push(...parseInline(match[4], [...marks, { type: 'italic' }]))
    else if (match[5] !== undefined) out.push(...parseInline(match[5], [...marks, { type: 'strike' }]))
    else if (match[6] !== undefined) out.push(text(match[6], [...marks, { type: 'code' }]))
    else out.push(...parseInline(match[7], [...marks, { type: 'link', attrs: { href: match[8] } }]))
    rest = rest.slice(match.index + match[0].length)
  }
  return out.filter(node => node.text !== '')
}

function inlineLines(lines: string[]): PMNode[] {
  const out: PMNode[] = []
  lines.forEach((line, index) => {
    if (index) out.push({ type: 'hardBreak' })
    out.push(...parseInline(line.trim()))
  })
  return out
}

const BULLET = /^(\s*)([-*+])\s+(.*)$/
const ORDERED = /^(\s*)(\d+)[.)]\s+(.*)$/
const TASK = /^\[( |x|X)\]\s+(.*)$/
const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([\w+#-]*)/
const TABLE_DIVIDER = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/

function splitRow(line: string) {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map(cell => cell.trim())
}

export function markdownToDoc(markdown: string): PMNode {
  const lines = (markdown ?? '').replace(/\r\n?/g, '\n').split('\n')
  const blocks = parseBlocks(lines)
  return { type: 'doc', content: blocks.length ? blocks : [{ type: 'paragraph' }] }
}

function parseBlocks(lines: string[]): PMNode[] {
  const blocks: PMNode[] = []
  let index = 0
  while (index < lines.length) {
    const line = lines[index]
    if (!line.trim()) { index++; continue }
    const fence = FENCE.exec(line)
    if (fence) {
      const body: string[] = []
      index++
      while (index < lines.length && !(lines[index].trim().startsWith(fence[1]) && lines[index].trim() === fence[1])) body.push(lines[index++])
      index++
      blocks.push({ type: 'codeBlock', attrs: { language: fence[2] || null }, ...(body.length ? { content: [text(body.join('\n'))] } : {}) })
      continue
    }
    const heading = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line)
    if (heading) {
      blocks.push({ type: 'heading', attrs: { level: heading[1].length }, content: parseInline(heading[2]) })
      index++
      continue
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) { blocks.push({ type: 'horizontalRule' }); index++; continue }
    if (line.includes('|') && index + 1 < lines.length && TABLE_DIVIDER.test(lines[index + 1]) && lines[index + 1].includes('-')) {
      const head = splitRow(line)
      const rows: string[][] = []
      index += 2
      while (index < lines.length && lines[index].includes('|') && lines[index].trim()) rows.push(splitRow(lines[index++]))
      const cell = (type: string, value: string): PMNode => ({ type, content: [paragraph(parseInline(value))] })
      blocks.push({
        type: 'table',
        content: [
          { type: 'tableRow', content: head.map(value => cell('tableHeader', value)) },
          ...rows.map((row): PMNode => ({ type: 'tableRow', content: head.map((_, column) => cell('tableCell', row[column] ?? '')) })),
        ],
      })
      continue
    }
    if (/^\s{0,3}>/.test(line)) {
      const quote: string[] = []
      while (index < lines.length && /^\s{0,3}>/.test(lines[index])) quote.push(lines[index++].replace(/^\s{0,3}>\s?/, ''))
      blocks.push({ type: 'blockquote', content: parseBlocks(quote) })
      continue
    }
    if (BULLET.test(line) || ORDERED.test(line)) {
      const [list, next] = parseList(lines, index)
      blocks.push(list)
      index = next
      continue
    }
    const paragraphLines: string[] = []
    while (index < lines.length && lines[index].trim() && !FENCE.test(lines[index]) && !/^(#{1,6})\s/.test(lines[index]) && !/^\s{0,3}>/.test(lines[index]) && !BULLET.test(lines[index]) && !ORDERED.test(lines[index])) paragraphLines.push(lines[index++])
    blocks.push(paragraph(inlineLines(paragraphLines)))
  }
  return blocks
}

function indentOf(line: string) {
  return line.length - line.trimStart().length
}

function parseList(lines: string[], start: number): [PMNode, number] {
  const first = BULLET.exec(lines[start]) ?? ORDERED.exec(lines[start])!
  const baseIndent = first[1].length
  const ordered = ORDERED.test(lines[start]) && !BULLET.test(lines[start])
  const items: PMNode[] = []
  let task: boolean | undefined
  let index = start
  while (index < lines.length) {
    const line = lines[index]
    if (!line.trim()) {
      // A blank line only continues the list when more of it follows.
      let ahead = index + 1
      while (ahead < lines.length && !lines[ahead].trim()) ahead++
      if (ahead < lines.length && (BULLET.test(lines[ahead]) || ORDERED.test(lines[ahead])) && indentOf(lines[ahead]) >= baseIndent) { index = ahead; continue }
      break
    }
    const match = BULLET.exec(line) ?? ORDERED.exec(line)
    if (!match || indentOf(line) < baseIndent) break
    if (indentOf(line) > baseIndent) break // nested items are consumed with their parent below
    const isOrderedItem = ORDERED.test(line) && !BULLET.test(line)
    if (isOrderedItem !== ordered) break
    let body = match[3]
    const taskMatch = TASK.exec(body)
    const isTask = Boolean(taskMatch)
    if (items.length === 0) task = isTask
    if (isTask !== task) break
    const checked = taskMatch ? taskMatch[1].toLowerCase() === 'x' : false
    if (taskMatch) body = taskMatch[2]
    index++
    const children: PMNode[] = [paragraph(parseInline(body))]
    const nested: string[] = []
    while (index < lines.length) {
      const next = lines[index]
      if (!next.trim()) {
        let ahead = index + 1
        while (ahead < lines.length && !lines[ahead].trim()) ahead++
        if (ahead < lines.length && indentOf(lines[ahead]) > baseIndent) { nested.push(''); index++; continue }
        break
      }
      if (indentOf(next) <= baseIndent) break
      nested.push(next)
      index++
    }
    if (nested.some(row => row.trim())) children.push(...parseBlocks(nested.map(row => row.slice(Math.min(indentOf(row), baseIndent + 2)))))
    items.push({ type: task ? 'taskItem' : 'listItem', ...(task ? { attrs: { checked } } : {}), content: children })
  }
  const type = task ? 'taskList' : ordered ? 'orderedList' : 'bulletList'
  return [{ type, ...(ordered ? { attrs: { start: Number(first[2]) || 1 } } : {}), content: items }, index]
}
