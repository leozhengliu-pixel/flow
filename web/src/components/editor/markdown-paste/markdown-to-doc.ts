import type { Editor, JSONContent } from '@tiptap/core'
import type { Schema } from '@tiptap/pm/model'
import { normalizeNewlines } from './markdown-detect'

/**
 * Markdown -> ProseMirror JSON for the shared editor. The editor's own Markdown manager (`@tiptap/markdown`) does the
 * CommonMark / GFM parsing (headings, lists, task lists, tables, fenced code, links, quotes, rules, images); this module
 * adds what that parser does not know about (GitHub alerts, `+++ title` and `<details>` collapsibles, inline images
 * inside paragraphs), then rebuilds the result against the live schema so nothing invalid can reach the document.
 */

export const ALERT_CALLOUTS = {
  NOTE: { color: 'cyan', icon: 'ℹ️' },
  TIP: { color: 'green', icon: '💡' },
  IMPORTANT: { color: 'purple', icon: '❗' },
  WARNING: { color: 'orange', icon: '⚠️' },
  CAUTION: { color: 'red', icon: '🛑' },
} as const

type Segment =
  | { kind: 'markdown'; text: string }
  | { kind: 'details'; title: string; open: boolean; children: Segment[] }

type Frame = { kind: 'plus' | 'html'; title: string | null; open: boolean; children: Segment[]; buffer: string[] }

const FENCE = /^\s{0,3}(`{3,}|~{3,})/
const PLUS_OPEN = /^\+\+\+[ \t]+(\S.*?)[ \t]*$/
const PLUS_CLOSE = /^\+\+\+[ \t]*$/
const HTML_OPEN = /^\s*<details((?:\s[^>]*)?)>\s*(?:<summary[^>]*>(.*?)<\/summary>)?\s*$/i
const HTML_SUMMARY = /^\s*<summary[^>]*>(.*?)<\/summary>\s*$/i
const HTML_CLOSE = /^\s*<\/details>\s*$/i
const ALERT_MARKER = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n?/i

/** Splits the source into plain Markdown runs and collapsible sections (nested ones included), leaving fenced code alone. */
export function splitCollapsibles(source: string): Segment[] {
  const root: Frame = { kind: 'plus', title: '', open: false, children: [], buffer: [] }
  const stack: Frame[] = [root]
  let fence: string | null = null
  const flush = (frame: Frame) => {
    if (!frame.buffer.length) return
    frame.children.push({ kind: 'markdown', text: frame.buffer.join('\n') })
    frame.buffer = []
  }
  const close = () => {
    const frame = stack.pop()!
    flush(frame)
    stack[stack.length - 1].children.push({ kind: 'details', title: frame.title ?? 'Details', open: frame.open, children: frame.children })
  }
  for (const line of source.split('\n')) {
    const top = stack[stack.length - 1]
    const fenceMatch = FENCE.exec(line)
    if (fence) {
      if (fenceMatch && fenceMatch[1][0] === fence[0] && fenceMatch[1].length >= fence.length && line.trim() === fenceMatch[1]) fence = null
      top.buffer.push(line)
      continue
    }
    if (fenceMatch) {
      fence = fenceMatch[1]
      top.buffer.push(line)
      continue
    }
    const plusOpen = PLUS_OPEN.exec(line)
    if (plusOpen) {
      flush(top)
      stack.push({ kind: 'plus', title: plusOpen[1], open: false, children: [], buffer: [] })
      continue
    }
    if (PLUS_CLOSE.test(line) && stack.length > 1 && top.kind === 'plus') {
      close()
      continue
    }
    const htmlOpen = HTML_OPEN.exec(line)
    if (htmlOpen) {
      flush(top)
      stack.push({ kind: 'html', title: htmlOpen[2] === undefined ? null : plainTitle(htmlOpen[2]), open: /\bopen\b/i.test(htmlOpen[1]), children: [], buffer: [] })
      continue
    }
    if (top.kind === 'html' && top.title === null && !top.buffer.length && !top.children.length) {
      const summary = HTML_SUMMARY.exec(line)
      if (summary) {
        top.title = plainTitle(summary[1])
        continue
      }
      if (!line.trim()) continue
    }
    if (HTML_CLOSE.test(line) && stack.length > 1 && top.kind === 'html') {
      close()
      continue
    }
    top.buffer.push(line)
  }
  while (stack.length > 1) close()
  flush(root)
  return root.children
}

function plainTitle(value: string) {
  return value.replace(/<[^>]+>/g, '').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&').replace(/\s+/g, ' ').trim()
}

type Context = { schema: Schema; headingLevels: number[] }

function headingLevels(editor: Editor): number[] {
  const levels = (editor.extensionManager.extensions.find(extension => extension.name === 'heading')?.options as { levels?: number[] } | undefined)?.levels
  return levels?.length ? levels : [1, 2, 3, 4, 5, 6]
}

function isEmptyParagraph(node: JSONContent | undefined) {
  return !!node && node.type === 'paragraph' && !node.content?.length
}

function trimEmptyEdges(blocks: JSONContent[]) {
  let start = 0
  let end = blocks.length
  while (start < end && isEmptyParagraph(blocks[start])) start++
  while (end > start && isEmptyParagraph(blocks[end - 1])) end--
  return blocks.slice(start, end)
}

/** Soft line breaks inside a paragraph stay visible as hard breaks, so pasted text keeps its lines. */
function withHardBreaks(content: JSONContent[] | undefined, schema: Schema): JSONContent[] | undefined {
  if (!content) return content
  const out: JSONContent[] = []
  for (const node of content) {
    if (node.type !== 'text' || !node.text?.includes('\n')) {
      out.push(node)
      continue
    }
    node.text.split('\n').forEach((part, index) => {
      if (index > 0) out.push(schema.nodes.hardBreak ? { type: 'hardBreak' } : { type: 'text', text: ' ', ...(node.marks ? { marks: node.marks } : {}) })
      if (part) out.push({ ...node, text: part })
    })
  }
  return out
}

function imageAsLink(node: JSONContent): JSONContent {
  const src = String(node.attrs?.src ?? '')
  return { type: 'text', text: String(node.attrs?.alt || src), marks: [{ type: 'link', attrs: { href: src } }] }
}

function alertOf(quote: JSONContent): { kind: keyof typeof ALERT_CALLOUTS; rest: JSONContent[] } | null {
  const first = quote.content?.[0]
  const text = first?.type === 'paragraph' ? first.content?.[0] : undefined
  if (!first || !text || text.type !== 'text' || text.marks?.length || !text.text) return null
  const match = ALERT_MARKER.exec(text.text)
  if (!match) return null
  const remaining = text.text.slice(match[0].length)
  const inline = [...(remaining ? [{ ...text, text: remaining }] : []), ...(first.content?.slice(1) ?? [])]
  if (inline[0]?.type === 'hardBreak') inline.shift()
  const rest = [...(inline.length ? [{ ...first, content: inline }] : []), ...(quote.content?.slice(1) ?? [])]
  return { kind: match[1].toUpperCase() as keyof typeof ALERT_CALLOUTS, rest }
}

function fixBlocks(nodes: JSONContent[] | undefined, ctx: Context): JSONContent[] {
  return (nodes ?? []).flatMap(node => fixBlock(node, ctx))
}

function fixBlock(node: JSONContent, ctx: Context): JSONContent[] {
  switch (node.type) {
    case 'codeBlock':
    case 'horizontalRule':
    case 'image':
      return [node]
    case 'blockquote': {
      const alert = alertOf(node)
      if (!alert) return [{ ...node, content: fixBlocks(node.content, ctx) }]
      const { color, icon } = ALERT_CALLOUTS[alert.kind]
      const content = fixBlocks(alert.rest, ctx)
      return [{ type: 'callout', attrs: { color, icon }, content: content.length ? content : [{ type: 'paragraph' }] }]
    }
    case 'paragraph':
      return liftImages(node, ctx)
    case 'heading': {
      const requested = Number(node.attrs?.level ?? 1)
      const level = ctx.headingLevels.reduce((best, candidate) => Math.abs(candidate - requested) < Math.abs(best - requested) ? candidate : best, ctx.headingLevels[0])
      const content = (node.content ?? []).map(child => child.type === 'image' ? imageAsLink(child) : child)
      return [{ ...node, attrs: { ...node.attrs, level }, content: withHardBreaks(content, ctx.schema) }]
    }
    case 'listItem':
    case 'taskItem': {
      const content = fixBlocks(node.content, ctx)
      if (content[0]?.type !== 'paragraph') content.unshift({ type: 'paragraph' })
      return [{ ...node, content }]
    }
    default:
      return [node.content ? { ...node, content: fixBlocks(node.content, ctx) } : node]
  }
}

/** The `image` node is a block atom, so an image inside a sentence ends the paragraph and sits on its own line. */
function liftImages(paragraph: JSONContent, ctx: Context): JSONContent[] {
  const out: JSONContent[] = []
  let run: JSONContent[] = []
  const flushRun = () => {
    if (run.length) out.push({ type: 'paragraph', content: withHardBreaks(run, ctx.schema) })
    run = []
  }
  for (const child of paragraph.content ?? []) {
    if (child.type === 'image' && ctx.schema.nodes.image) {
      flushRun()
      out.push(child)
    } else if (child.type === 'image') {
      run.push(imageAsLink(child))
    } else {
      run.push(child)
    }
  }
  flushRun()
  return out.length ? out : [paragraph]
}

function isSafeUrl(url: unknown, allowDataImage = false) {
  if (typeof url !== 'string') return false
  const value = url.trim()
  if (!value) return false
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(value)?.[1]?.toLowerCase()
  if (!scheme) return true
  if (['http', 'https', 'mailto', 'tel', 'ftp', 'sms'].includes(scheme)) return true
  return allowDataImage && /^data:image\/(png|jpe?g|gif|webp|avif);/i.test(value)
}

function allowedAttrs(spec: { attrs?: Record<string, unknown> } | undefined, attrs: Record<string, unknown> | undefined) {
  if (!attrs || !spec?.attrs) return undefined
  const result: Record<string, unknown> = {}
  for (const key of Object.keys(spec.attrs)) if (key in attrs) result[key] = attrs[key]
  return Object.keys(result).length ? result : undefined
}

/** Rebuilds JSON against the schema: unknown nodes dissolve into their children, unknown marks / attrs / unsafe URLs drop. */
export function sanitizeJSON(node: JSONContent, schema: Schema): JSONContent[] {
  if (node.type === 'text') {
    if (!node.text) return []
    const marks = (node.marks ?? []).flatMap(mark => {
      const type = mark.type ? schema.marks[mark.type] : undefined
      if (!type) return []
      if (mark.type === 'link' && !isSafeUrl(mark.attrs?.href)) return []
      const attrs = allowedAttrs(type.spec, mark.attrs)
      return [attrs ? { type: type.name, attrs } : { type: type.name }]
    })
    return [{ type: 'text', text: node.text, ...(marks.length ? { marks } : {}) }]
  }
  const type = node.type ? schema.nodes[node.type] : undefined
  const children = (node.content ?? []).flatMap(child => sanitizeJSON(child, schema))
  if (!type) return children
  if (node.type === 'image' && !isSafeUrl(node.attrs?.src, true)) return []
  const attrs = allowedAttrs(type.spec, node.attrs)
  return [{ type: type.name, ...(attrs ? { attrs } : {}), ...(children.length ? { content: children } : {}) }]
}

function plainBlock(node: JSONContent): JSONContent {
  const text = (function collect(value: JSONContent): string {
    if (value.type === 'text') return value.text ?? ''
    if (value.type === 'hardBreak') return '\n'
    return (value.content ?? []).map(collect).join(value.content?.some(child => child.type !== 'text' && child.type !== 'hardBreak') ? '\n' : '')
  })(node)
  return { type: 'paragraph', content: text ? text.split('\n').flatMap((line, index) => [...(index ? [{ type: 'hardBreak' }] : []), ...(line ? [{ type: 'text', text: line }] : [])]) : [] }
}

/** Keeps every block that is valid for the schema, and degrades the ones that are not to plain paragraphs. */
function validBlocks(blocks: JSONContent[], schema: Schema): JSONContent[] {
  return blocks.map(block => {
    try {
      schema.nodeFromJSON(block).check()
      return block
    } catch {
      try {
        const fallback = plainBlock(block)
        schema.nodeFromJSON(fallback).check()
        return fallback
      } catch {
        return { type: 'paragraph' }
      }
    }
  })
}

function parseSegments(segments: Segment[], editor: Editor, ctx: Context): JSONContent[] {
  return segments.flatMap(segment => {
    if (segment.kind === 'markdown') {
      if (!segment.text.trim()) return []
      const parsed = editor.markdown?.parse(segment.text)
      if (!parsed) throw new Error('markdown manager unavailable')
      return trimEmptyEdges(fixBlocks(parsed.content, ctx))
    }
    const body = parseSegments(segment.children, editor, ctx)
    return [{
      type: 'details',
      attrs: { open: segment.open },
      content: [
        { type: 'detailsSummary', content: segment.title ? [{ type: 'text', text: segment.title }] : [] },
        { type: 'detailsContent', content: body.length ? body : [{ type: 'paragraph' }] },
      ],
    }]
  })
}

/** Plain paragraphs split on blank lines: the fallback when Markdown cannot be parsed and the import of non-Markdown text. */
export function plainTextToBlocks(text: string): JSONContent[] {
  return normalizeNewlines(text).split(/\n{2,}/).map(chunk => chunk.replace(/^\n+|\n+$/g, '')).filter(Boolean).map(chunk => {
    const lines = chunk.split('\n')
    return { type: 'paragraph', content: lines.flatMap((line, index) => [...(index ? [{ type: 'hardBreak' }] : []), ...(line ? [{ type: 'text', text: line }] : [])]) }
  })
}

/** Like `markdownToDocJSON` but returns null instead of degrading, so a paste handler can fall back to the default paste. */
export function tryMarkdownToDocJSON(editor: Editor, markdown: string): JSONContent | null {
  try {
    const { schema } = editor
    const ctx: Context = { schema, headingLevels: headingLevels(editor) }
    const blocks = parseSegments(splitCollapsibles(normalizeNewlines(markdown)), editor, ctx)
    const content = validBlocks(blocks.flatMap(block => sanitizeJSON(block, schema)), schema)
    return content.length ? { type: 'doc', content } : null
  } catch {
    return null
  }
}

/** Converts Markdown into a `doc` JSON for the editor's schema. Never throws: unparseable input becomes plain paragraphs. */
export function markdownToDocJSON(editor: Editor, markdown: string): JSONContent {
  const parsed = tryMarkdownToDocJSON(editor, markdown)
  if (parsed) return parsed
  const fallback = plainTextToBlocks(markdown)
  return { type: 'doc', content: fallback.length ? fallback : [{ type: 'paragraph' }] }
}
