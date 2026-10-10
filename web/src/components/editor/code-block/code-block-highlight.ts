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

/** Keywords that steer control flow or module structure; themes colour them apart from declarations (`hljs-keyword hljs-control`). */
const CONTROL_KEYWORDS = new Set(['return', 'if', 'else', 'for', 'while', 'do', 'switch', 'case', 'default', 'break', 'continue', 'throw', 'try', 'catch', 'finally', 'yield', 'import', 'export', 'from', 'as', 'package', 'elif', 'except', 'raise'])
/** Words that read as operators and keep the plain text colour. */
const OPERATOR_WORDS = new Set(['in', 'of', 'instanceof', 'typeof', 'new', 'delete', 'void', 'and', 'or', 'not', 'is'])
/** Languages whose plain identifiers are told apart: variables, calls, types and constants (highlight.js leaves them unmarked). */
const IDENTIFIER_LANGUAGES = new Set(['javascript', 'typescript', 'go', 'python', 'java', 'kotlin', 'rust', 'csharp', 'swift', 'c', 'cpp', 'php', 'ruby', 'dart', 'scala', 'objectivec', 'lua', 'perl', 'r'])
/** Ranges that only group other tokens; the plain text they hold is still code and gets coloured. */
const WRAPPER_CLASSES = new Set(['hljs-params', 'hljs-function', 'hljs-subst', 'hljs-class', 'hljs-tag', 'language-javascript', 'language-typescript'])
const DECLARATION_WORDS = /(?:^|[^\w$])(?:function|def|func|fn|fun|sub|proc)\s+$/

const CONST_DECLARATION = /\bconst\s+([A-Za-z_$][\w$]*)\s*(?::[^=\n]+)?=(?!=)/g
const LITERAL_WORDS = new Set(['true', 'false', 'null', 'undefined', 'nil', 'None', 'True', 'False', 'NaN'])
const IDENTIFIER = /[A-Za-z_$][\w$]*/g
const BASH_ASSIGNMENT = /(?:^|[\s;|&({])([A-Za-z_]\w*)=/gm
const BASH_COMMAND = /(^|[|;&({]|\b(?:then|do|else|elif)\b|\$\()\s*(?!(?:then|do|else|elif|if|while|for|case|in)\b)([A-Za-z_./~][\w./~-]*)/gm

const hasClass = (range: TokenRange, name: string) => range.className.split(' ').includes(name)

/** Re-classifies highlight.js scopes that Linear colours differently, per language. */
function refine(code: string, language: string, ranges: TokenRange[]): TokenRange[] {
  const literals = new Set(ranges.filter(range => hasClass(range, 'hljs-literal')).map(range => `${range.from}:${range.to}`))
  const dropped = new Set<TokenRange>()
  const result: TokenRange[] = []
  for (const range of ranges) {
    const text = code.slice(range.from, range.to)
    let className = range.className
    if (hasClass(range, 'hljs-keyword')) {
      // `true` and `null` are marked as a literal wrapping a keyword; only the literal colour applies.
      if (literals.has(`${range.from}:${range.to}`)) continue
      if (CONTROL_KEYWORDS.has(text)) className = 'hljs-keyword hljs-control'
      else if (OPERATOR_WORDS.has(text)) className = 'hljs-plain'
    } else if (hasClass(range, 'hljs-meta')) {
      if (text.startsWith('@')) className = 'hljs-call'
      else if (text.startsWith('#!')) className = 'hljs-comment'
      else if (text.startsWith('<!')) { className = 'hljs-plain'; for (const inner of ranges) if (inner !== range && inner.from >= range.from && inner.to <= range.to) dropped.add(inner) }
    } else if (hasClass(range, 'hljs-name') && (language === 'javascript' || language === 'typescript')) {
      // JSX: components (`<Layout>`) read as types, intrinsic elements (`<div>`) as variables.
      className = /^[A-Z]/.test(text) ? 'hljs-ident-type' : 'hljs-ident'
    } else if (hasClass(range, 'hljs-string') && (language === 'javascript' || language === 'typescript') && text.startsWith('{')) {
      // A JSX attribute expression (`onClick={() => go()}`) is code, not a string.
      continue
    } else if (hasClass(range, 'hljs-attr') && language === 'json') className = 'hljs-string'
    else if (hasClass(range, 'hljs-string') && language === 'xml' && /^(["']).*\1$/s.test(text)) {
      // Attribute values keep the string colour; their quotes read as punctuation.
      result.push({ ...range, from: range.from + 1, to: range.to - 1 })
      continue
    } else if (hasClass(range, 'hljs-regexp') && /^\/.+\/[a-z]*$/s.test(text)) {
      // The delimiters read as punctuation; the pattern and its flags keep the regexp colour.
      const close = range.from + text.lastIndexOf('/')
      result.push({ ...range, from: range.from + 1, to: close })
      if (close + 1 < range.to) result.push({ ...range, from: close + 1 })
      continue
    } else if (hasClass(range, 'hljs-variable') && language !== 'css' && text.startsWith('$') && text.length > 1) {
      // `$NAME`: the sigil is an operator, the name a variable.
      result.push({ ...range, from: range.from + 1 })
      continue
    } else if (hasClass(range, 'hljs-string') && (language === 'css' || language === 'scss' || language === 'less') && !/^["']/.test(text)) className = 'hljs-plain'
    else if (hasClass(range, 'hljs-built_in')) {
      const callable = /^\s*\(/.test(code.slice(range.to, range.to + 8))
      if (language === 'bash' || language === 'shell') className = text === 'export' ? 'hljs-keyword hljs-control' : 'hljs-built_in hljs-call'
      else if (callable || language === 'css' || language === 'scss' || language === 'less') className = 'hljs-built_in hljs-call'
    }
    result.push(className === range.className ? range : { ...range, className })
  }
  return result.filter(range => !dropped.has(range))
}

/** Whether `at` sits inside an unclosed `{` counted from `from` (a JSX expression). */
function insideBraces(code: string, from: number, at: number) {
  let depth = 0
  for (let index = from; index < at; index++) {
    const char = code[index]
    if (char === '{') depth++
    else if (char === '}') depth = Math.max(0, depth - 1)
  }
  return depth > 0
}

const GO_QUALIFIED = /(?<![\w.])([a-z_]\w*)\.([A-Z]\w*)(?![\w(])(?!\s*\()/g
const GO_EXPRESSION_WORDS = new Set(['return', 'case', 'go', 'defer', 'range', 'else', 'if', 'for', 'switch', 'in', 'var', 'const'])

/** `context.Context` / `*http.Request` in a type position: the package is plain text and the name a type (`time.Second` stays a variable). */
function qualifiedGoTypes(code: string) {
  const classes = new Map<number, string>()
  for (const match of code.matchAll(GO_QUALIFIED)) {
    const before = code.slice(0, match.index).replace(/[ \t]+$/, '')
    const previous = before.slice(-1)
    if (!/[\w*\])]/.test(previous)) continue
    const word = before.match(/\w+$/)?.[0]
    if (word && GO_EXPRESSION_WORDS.has(word)) continue
    classes.set(match.index, 'hljs-plain')
    classes.set(match.index + match[1].length + 1, 'hljs-ident-type')
  }
  return classes
}

/** Adds the identifiers highlight.js leaves unmarked, so variables, calls, types and constants get their own colours. */
function addIdentifiers(code: string, language: string, ranges: TokenRange[]): TokenRange[] {
  const owner = new Int32Array(code.length).fill(-1)
  const byLength = ranges.map((range, index) => ({ range, index })).sort((a, b) => (b.range.to - b.range.from) - (a.range.to - a.range.from))
  for (const { range, index } of byLength) owner.fill(index, range.from, range.to)
  const extra: TokenRange[] = []
  const jsx = language === 'javascript' || language === 'typescript'
  const covered = (at: number) => {
    const index = owner[at]
    if (index < 0) return false
    const range = ranges[index]
    // Text between JSX tags is plain, but an expression in braces is code.
    if (jsx && hasClass(range, 'language-xml')) return !insideBraces(code, range.from, at)
    return !isWrapper(range)
  }
  const qualified = language === 'go' ? qualifiedGoTypes(code) : new Map<number, string>()
  if (IDENTIFIER_LANGUAGES.has(language)) {
    const constants = language === 'javascript' || language === 'typescript' ? new Set([...code.matchAll(CONST_DECLARATION)].map(match => match[1])) : null
    for (const match of code.matchAll(IDENTIFIER)) {
      const from = match.index
      const to = from + match[0].length
      if (covered(from)) continue
      const word = match[0]
      const callable = /^[ \t]*\(/.test(code.slice(to, to + 16)) || DECLARATION_WORDS.test(code.slice(Math.max(0, from - 16), from))
      const member = code[from - 1] === '.'
      let className = 'hljs-ident'
      if (qualified.has(from)) className = qualified.get(from)!
      else if (callable) className = 'hljs-call'
      else if (language === 'go' && /\bpackage\s+$/.test(code.slice(Math.max(0, from - 12), from))) className = 'hljs-plain'
      else if (LITERAL_WORDS.has(word)) className = 'hljs-constant'
      else if (constants?.has(word)) className = 'hljs-constant'
      else if (/^[A-Z][A-Z0-9_]+$/.test(word)) className = 'hljs-constant'
      else if (/^[A-Z]/.test(word) && !member) className = 'hljs-ident-type'
      extra.push({ from, to, className })
    }
  } else if (language === 'bash' || language === 'shell') {
    for (const match of code.matchAll(BASH_COMMAND)) {
      const word = match[2]
      const from = match.index + match[0].length - word.length
      if (owner[from] >= 0) continue
      extra.push({ from, to: from + word.length, className: code[from + word.length] === '=' ? 'hljs-ident' : 'hljs-call' })
    }
    for (const match of code.matchAll(BASH_ASSIGNMENT)) {
      const from = match.index + match[0].length - match[1].length - 1
      if (owner[from] < 0 && !extra.some(range => range.from === from)) extra.push({ from, to: from + match[1].length, className: 'hljs-ident' })
    }
  }
  return extra.length ? [...ranges, ...extra] : ranges
}

function isWrapper(range: TokenRange) {
  return range.className.split(' ').some(name => WRAPPER_CLASSES.has(name))
}

/** The highlight.js token ranges for a code block's text; empty for plain text, unknown languages and huge blocks. */
export function highlightCode(code: string, language: string | null | undefined): TokenRange[] {
  if (!code || code.length > HIGHLIGHT_LIMIT) return []
  const raw = String(language ?? '').trim().toLowerCase()
  try {
    const id = !raw || raw === 'auto' ? detectLanguage(code) ?? undefined : resolveLanguage(raw)
    if (!id || id === 'plaintext') return []
    const tokens = refine(code, id, tokenRanges(hljs.highlight(code, { language: id, ignoreIllegals: true }).value))
    return addIdentifiers(code, id, tokens)
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
