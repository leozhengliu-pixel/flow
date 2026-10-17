import DOMPurify from 'dompurify'
import { embedSourceForUrl, type EmbedSource } from '@/components/editor/embeds/embed-providers'
import { isPlainTextWrapperHtml, isSingleUrl, looksLikeMarkdown } from '@/components/editor/markdown-paste'
import { emptyLine } from './agent-composer-dom'

/**
 * What the Agent composer does with non-file clipboard content, as the reference app does: rich text keeps its links,
 * bold / italic, lists, inline code and code blocks; Markdown is converted the same way; a lone URL becomes a link (or an
 * issue chip). The result is a fragment of the few elements the composer serialises back to Markdown: A, STRONG, EM, S,
 * CODE, PRE > CODE, UL / OL > LI, BR and DIV lines. There are no headings, so a heading becomes a bold line.
 */

const SAFE_HREF = /^(https?:|mailto:)/i
const MAX_PASTE = 200_000

const sanitize = (html: string) => DOMPurify.sanitize(html, {
  ALLOWED_TAGS: ['a', 'b', 'strong', 'i', 'em', 's', 'strike', 'del', 'code', 'pre', 'ul', 'ol', 'li', 'p', 'br', 'div', 'span', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'table', 'thead', 'tbody', 'tr', 'td', 'th', 'img', 'section', 'article', 'font', 'u', 'mark', 'sub', 'sup'],
  ALLOWED_ATTR: ['href', 'src', 'class', 'style', 'data-lang'],
})

const parse = (html: string) => new DOMParser().parseFromString(sanitize(html), 'text/html').body

/** The remote picture when the clipboard html is nothing but one <img> (copying an image on a web page). */
export function remoteImageSource(html: string) {
  if (!html.trim()) return undefined
  const body = parse(html)
  const images = body.querySelectorAll('img')
  if (images.length !== 1 || body.textContent?.trim()) return undefined
  const src = images[0].getAttribute('src') ?? ''
  return /^https?:\/\//i.test(src) ? src : undefined
}

const element = (tag: string, ...children: Node[]) => {
  const el = document.createElement(tag)
  el.append(...children)
  return el
}

const isGap = (node: Node | undefined) => node instanceof HTMLElement && node.tagName === 'DIV' && !node.textContent && node.firstChild instanceof HTMLBRElement

function languageOf(pre: Element) {
  for (const node of [pre, pre.querySelector('code'), pre.parentElement]) {
    const match = /(?:^|\s)(?:language|lang|highlight-source)-([\w+#.-]+)/.exec(node?.className ?? '')
    if (match) return match[1]
  }
  return pre.getAttribute('data-lang') ?? pre.querySelector('code')?.getAttribute('data-lang') ?? ''
}

export function codeBlock(code: string, language = '') {
  const pre = document.createElement('pre')
  if (language) pre.dataset.lang = language
  pre.append(element('code', document.createTextNode(code.replace(/\n$/, ''))))
  return pre
}

type Style = { bold: boolean; italic: boolean }
const styleOf = (el: HTMLElement): Style => {
  const weight = el.style.fontWeight
  return { bold: weight === 'bold' || weight === 'bolder' || Number(weight) >= 600, italic: el.style.fontStyle === 'italic' }
}

const BLOCK = new Set(['P', 'DIV', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'BLOCKQUOTE', 'TABLE', 'THEAD', 'TBODY', 'TR', 'SECTION', 'ARTICLE', 'UL', 'OL', 'LI', 'PRE'])
const PARAGRAPH = /^(P|BLOCKQUOTE|H[1-6])$/

function trimGaps(out: Node[]) {
  while (out.length && isGap(out.at(-1))) out.pop()
  while (out.length && isGap(out[0])) out.shift()
  return out
}

/** A node's children as block-level output: DIV lines, lists and code blocks, with an empty line between paragraphs. */
function blocks(parent: Node): Node[] {
  const out: Node[] = []
  let line: Node[] = []
  const flush = () => {
    while (line[0] instanceof Text && !line[0].data.trim()) line.shift()
    while (line.at(-1) instanceof Text && !(line.at(-1) as Text).data.trim()) line.pop()
    if (line.length) out.push(element('div', ...line))
    line = []
  }
  const gap = () => { if (out.length && !isGap(out.at(-1))) out.push(emptyLine()) }
  for (const child of Array.from(parent.childNodes)) {
    if (!(child instanceof HTMLElement) || !BLOCK.has(child.tagName)) { line.push(...inline(child, { bold: false, italic: false })); continue }
    flush()
    const tag = child.tagName
    if (tag === 'PRE') { out.push(codeBlock(child.textContent ?? '', languageOf(child))); continue }
    if (tag === 'UL' || tag === 'OL') { out.push(list(child)); continue }
    const paragraph = PARAGRAPH.test(tag)
    if (paragraph) gap()
    const inner = blocks(child)
    out.push(...(/^H[1-6]$/.test(tag) ? inner.map(node => node instanceof HTMLElement && node.tagName === 'DIV' ? element('div', element('strong', ...Array.from(node.childNodes))) : node) : inner))
    if (paragraph) gap()
  }
  flush()
  return trimGaps(out)
}

function list(source: HTMLElement) {
  const result = document.createElement(source.tagName === 'OL' ? 'ol' : 'ul')
  for (const item of Array.from(source.children)) {
    if (item.tagName !== 'LI') continue
    const li = document.createElement('li')
    for (const part of blocks(item)) {
      if (isGap(part)) continue
      if (part instanceof HTMLElement && part.tagName === 'DIV') {
        if (li.lastChild && !(li.lastChild instanceof HTMLUListElement || li.lastChild instanceof HTMLOListElement)) li.append(document.createElement('br'))
        li.append(...Array.from(part.childNodes))
      } else li.append(part)
    }
    result.append(li)
  }
  return result
}

function inline(node: Node, style: Style): Node[] {
  if (node instanceof Text) {
    const text = node.data.replace(/[\s ]+/g, ' ')
    return text ? [document.createTextNode(text)] : []
  }
  if (!(node instanceof HTMLElement)) return []
  const tag = node.tagName
  if (tag === 'BR') return [document.createElement('br')]
  if (tag === 'IMG') return []
  const own = styleOf(node)
  const next: Style = { bold: style.bold || own.bold, italic: style.italic || own.italic }
  const children = (mark: Style) => Array.from(node.childNodes).flatMap(child => inline(child, mark))
  // Styled spans (Google Docs wraps everything in <b style="font-weight:normal">, so only a real weight counts).
  const styled = (nodes: Node[]) => {
    let result = nodes
    if (own.bold && !style.bold) result = [element('strong', ...result)]
    if (own.italic && !style.italic) result = [element('em', ...result)]
    return result
  }
  if (tag === 'B' || tag === 'STRONG') {
    if (node.style.fontWeight === 'normal' || node.style.fontWeight === '400') return children(next)
    return style.bold ? children(next) : [element('strong', ...children({ ...next, bold: true }))]
  }
  if (tag === 'I' || tag === 'EM') return style.italic ? children(next) : [element('em', ...children({ ...next, italic: true }))]
  if (tag === 'S' || tag === 'STRIKE' || tag === 'DEL') return [element('s', ...children(next))]
  if (tag === 'CODE') {
    const text = node.textContent ?? ''
    return text ? [element('code', document.createTextNode(text))] : []
  }
  if (tag === 'A') {
    const href = node.getAttribute('href') ?? ''
    const inner = styled(children(next))
    if (!SAFE_HREF.test(href) || !inner.length) return inner
    const a = element('a', ...inner)
    a.setAttribute('href', href)
    return [a]
  }
  return styled(children(next))
}

/** A single line stays inline (a bold word pasted mid-sentence); anything more is a run of lines. */
function finish(parts: Node[]) {
  if (!parts.length) return undefined
  const fragment = document.createDocumentFragment()
  if (parts.length === 1 && parts[0] instanceof HTMLElement && parts[0].tagName === 'DIV') fragment.append(...Array.from(parts[0].childNodes))
  else fragment.append(...parts)
  return fragment.textContent?.trim() ? fragment : undefined
}

/** Pasted rich text (text/html) as composer elements; undefined when nothing is left of it. */
export function htmlToFragment(html: string) {
  if (!html.trim() || html.length > MAX_PASTE) return undefined
  return finish(blocks(parse(html)))
}

const INLINE = /`([^`\n]+)`|\[([^\]\n]+)\]\(((?:https?:\/\/|mailto:)[^)\s]+)\)|\*\*([^\s*](?:[^*\n]*[^\s*])?)\*\*|__([^\s_](?:[^_\n]*[^\s_])?)__|~~([^\s~](?:[^~\n]*[^\s~])?)~~|\*([^\s*](?:[^*\n]*[^\s*])?)\*|(?<![\w])_([^\s_](?:[^_\n]*[^\s_])?)_(?![\w])/g

function inlineMarkdown(text: string): Node[] {
  const out: Node[] = []
  let last = 0
  for (const match of text.matchAll(INLINE)) {
    if (match.index > last) out.push(document.createTextNode(text.slice(last, match.index)))
    const [, code, label, href, strong, strongUnderscore, strike, em, emUnderscore] = match
    if (code !== undefined) out.push(element('code', document.createTextNode(code)))
    else if (label !== undefined) { const a = element('a', ...inlineMarkdown(label)); a.setAttribute('href', href); out.push(a) }
    else if (strong !== undefined || strongUnderscore !== undefined) out.push(element('strong', ...inlineMarkdown(strong ?? strongUnderscore)))
    else if (strike !== undefined) out.push(element('s', ...inlineMarkdown(strike)))
    else out.push(element('em', ...inlineMarkdown(em ?? emUnderscore)))
    last = match.index + match[0].length
  }
  if (last < text.length) out.push(document.createTextNode(text.slice(last)))
  return out
}

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([\w+#.-]*)[^\n]*$/
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/

/**
 * Markdown text as composer elements: lists, bold, italic, strikethrough, links, inline code and fenced code blocks (with their
 * language). Headings, quotes, tables and the like stay literal text, as the composer has no such blocks.
 */
export function markdownToFragment(markdown: string) {
  const lines = markdown.replace(/\r\n?/g, '\n').split('\n')
  const out: Node[] = []
  let stack: { indent: number; list: HTMLElement; item?: HTMLElement }[] = []
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    const fence = FENCE.exec(line)
    if (fence) {
      stack = []
      const code: string[] = []
      const close = new RegExp(`^\\s{0,3}${fence[1][0]}{${fence[1].length},}\\s*$`)
      index++
      while (index < lines.length && !close.test(lines[index])) code.push(lines[index++])
      out.push(codeBlock(code.join('\n'), fence[2]))
      continue
    }
    const item = ITEM.exec(line)
    if (item) {
      const indent = item[1].replace(/\t/g, '  ').length
      const tag = /^\d/.test(item[2]) ? 'ol' : 'ul'
      while (stack.length && stack.at(-1)!.indent > indent) stack.pop()
      let top = stack.at(-1)
      if (top && top.indent === indent && top.list.tagName.toLowerCase() !== tag) { stack.pop(); top = stack.at(-1) }
      if (!top || top.indent < indent) {
        const list = document.createElement(tag)
        const parent = top?.item
        if (parent) parent.append(list); else out.push(list)
        top = { indent, list }
        stack.push(top)
      }
      const li = element('li', ...inlineMarkdown(item[3]))
      top.list.append(li)
      top.item = li
      continue
    }
    stack = []
    // Consecutive blank lines are one paragraph gap.
    if (!line.trim()) { if (!isGap(out.at(-1))) out.push(emptyLine()); continue }
    out.push(element('div', ...inlineMarkdown(line)))
  }
  return finish(trimGaps(out))
}

export type PasteUrl = { kind: 'issue'; identifier: string; href: string } | { kind: 'link'; href: string; embed?: EmbedSource }

/** A lone pasted URL: an issue of this workspace (`/<workspace>/issue/<IDENT>/...`), or a link that may be embeddable. */
export function classifyPastedUrl(text: string, origin: string): PasteUrl | undefined {
  const trimmed = text.trim()
  if (!isSingleUrl(trimmed)) return undefined
  const url = new URL(trimmed)
  const segments = url.pathname.split('/').filter(Boolean)
  if (url.origin === origin && segments[1] === 'issue' && segments[2] && /^[A-Za-z][A-Za-z0-9]*-\d+$/.test(segments[2])) return { kind: 'issue', identifier: segments[2].toUpperCase(), href: trimmed }
  return { kind: 'link', href: trimmed, embed: embedSourceForUrl(trimmed) }
}

export type PastePlan =
  | { kind: 'image'; src: string }
  | { kind: 'url'; url: PasteUrl }
  | { kind: 'fragment'; fragment: DocumentFragment }
  | { kind: 'text' }

/** Decides what a clipboard without files pastes as. */
export function planPaste(data: { getData: (type: string) => string }, origin: string): PastePlan {
  const html = data.getData('text/html')
  const plain = data.getData('text/plain')
  const image = remoteImageSource(html)
  if (image) return { kind: 'image', src: image }
  const url = classifyPastedUrl(plain, origin)
  if (url) return { kind: 'url', url }
  if (plain.length > MAX_PASTE) return { kind: 'text' }
  if (html.trim() && !isPlainTextWrapperHtml(html)) {
    const fragment = htmlToFragment(html)
    if (fragment) return { kind: 'fragment', fragment }
  }
  if (plain.trim() && looksLikeMarkdown(plain)) {
    const fragment = markdownToFragment(plain)
    if (fragment) return { kind: 'fragment', fragment }
  }
  return { kind: 'text' }
}

const mark = (marker: string, text: string) => {
  const match = /^(\s*)([\s\S]*?)(\s*)$/.exec(text)!
  return match[2] ? `${match[1]}${marker}${match[2]}${marker}${match[3]}` : text
}

/** The Markdown of a formatted element the paste put in the composer (undefined for any other element); `walk` serialises its children. */
export function formattingMarkdown(node: HTMLElement, walk: (node: Node) => string): string | undefined {
  const inner = () => Array.from(node.childNodes).map(walk).join('')
  switch (node.tagName) {
    case 'A': {
      const href = node.getAttribute('href')
      // Escape backslashes with brackets so existing escapes cannot terminate the label.
      const target = href?.trim().replace(/[\\()\s<>]/g, char => encodeURIComponent(char).replace(/[()]/g, c => c === '(' ? '%28' : '%29'))
      return target ? `[${inner().replace(/[\\[\]]/g, '\\$&')}](${target})` : inner()
    }
    case 'STRONG': case 'B': return mark('**', inner())
    case 'EM': case 'I': return mark('*', inner())
    case 'S': case 'STRIKE': case 'DEL': return mark('~~', inner())
    case 'CODE': {
      const text = node.textContent ?? ''
      const fence = text.includes('`') ? '``' : '`'
      return text ? `${fence}${text}${fence}` : ''
    }
    case 'PRE': {
      const code = (node.textContent ?? '').replace(/ /g, ' ').replace(/\n$/, '')
      const fence = '`'.repeat(Math.max(3, ...Array.from(code.matchAll(/`+/g), match => match[0].length + 1)))
      return `\n${fence}${node.dataset.lang ?? ''}\n${code}\n${fence}\n`
    }
    case 'UL': case 'OL': {
      let number = 0
      const items = Array.from(node.children).filter(item => item.tagName === 'LI').map(item => {
        const marker = node.tagName === 'OL' ? `${++number}. ` : '- '
        const lines = Array.from(item.childNodes).map(walk).join('').replace(/^\n+|\n+$/g, '').split('\n')
        return marker + lines.map((line, index) => index ? ' '.repeat(marker.length) + line : line).join('\n')
      })
      return `\n${items.join('\n')}\n`
    }
    default: return undefined
  }
}
