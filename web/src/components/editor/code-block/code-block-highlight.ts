import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { detectLanguage, HIGHLIGHT_LIMIT, hljs, resolveLanguage } from './languages'

export interface TokenRange { from: number; to: number; className: string }

const ENTITIES: Record<string, string> = { '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#x27;': "'", '&#39;': "'" }

/** Turns highlight.js's HTML output into character ranges of the plain text, one per token span. */
export function tokenRanges(html: string): TokenRange[] {
  const ranges: TokenRange[] = []
  const open: { from: number; className: string }[] = []
  let offset = 0
  const pattern = /<span class="([^"]*)">|<\/span>|&(?:amp|lt|gt|quot|#x27|#39);|[^<&]+|[<&]/g
  for (const match of html.matchAll(pattern)) {
    const [text, className] = match
    if (className !== undefined) open.push({ from: offset, className })
    else if (text === '</span>') {
      const token = open.pop()
      if (token && offset > token.from) ranges.push({ from: token.from, to: offset, className: token.className })
    } else offset += ENTITIES[text] ? 1 : text.length
  }
  return ranges
}

/** Keywords that steer control flow; themes may colour them apart from declarations (`hljs-keyword hljs-control`). */
const CONTROL_KEYWORDS = new Set(['return', 'if', 'else', 'for', 'while', 'do', 'switch', 'case', 'break', 'continue', 'throw', 'try', 'catch', 'finally', 'await', 'yield'])

/** The highlight.js token ranges for a code block's text; empty for plain text, unknown languages and huge blocks. */
export function highlightCode(code: string, language: string | null | undefined): TokenRange[] {
  if (!code || code.length > HIGHLIGHT_LIMIT) return []
  const raw = String(language ?? '').trim().toLowerCase()
  try {
    const id = !raw || raw === 'auto' ? detectLanguage(code) ?? undefined : resolveLanguage(raw)
    if (!id || id === 'plaintext') return []
    return tokenRanges(hljs.highlight(code, { language: id, ignoreIllegals: true }).value)
      .map(range => range.className === 'hljs-keyword' && CONTROL_KEYWORDS.has(code.slice(range.from, range.to)) ? { ...range, className: 'hljs-keyword hljs-control' } : range)
  } catch {
    return []
  }
}

const cache = new WeakMap<ProseMirrorNode, TokenRange[]>()

function rangesFor(node: ProseMirrorNode) {
  let ranges = cache.get(node)
  if (!ranges) {
    ranges = highlightCode(node.textContent, node.attrs.language as string | null)
    cache.set(node, ranges)
  }
  return ranges
}

function build(doc: ProseMirrorNode, name: string) {
  const decorations: Decoration[] = []
  doc.descendants((node, pos) => {
    if (node.type.name !== name) return !node.isTextblock
    for (const range of rangesFor(node)) decorations.push(Decoration.inline(pos + 1 + range.from, pos + 1 + range.to, { class: range.className }))
    return false
  })
  return DecorationSet.create(doc, decorations)
}

const key = new PluginKey<DecorationSet>('flowCodeBlockHighlight')

/** Highlights every code block of the document with highlight.js token classes (`hljs-keyword`, ...) as inline decorations. */
export function codeBlockHighlightPlugin(name: string) {
  return new Plugin<DecorationSet>({
    key,
    state: {
      init: (_, state) => build(state.doc, name),
      // Nodes are immutable, so untouched blocks come straight from the cache; only edited blocks are re-highlighted.
      apply: (transaction, decorations, _old, state) => transaction.docChanged ? build(state.doc, name) : decorations,
    },
    props: { decorations: state => key.getState(state) },
  })
}
