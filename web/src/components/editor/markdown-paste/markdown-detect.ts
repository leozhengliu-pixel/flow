/**
 * Heuristics that decide whether clipboard text is Markdown source worth converting into blocks, and whether the
 * accompanying text/html is merely a plain-text wrapper (VS Code, terminals, plain editors) rather than a rendered page.
 */

const FENCE = /^\s{0,3}(`{3,}|~{3,})/
const HEADING = /^\s{0,3}#{1,6}[ \t]+\S/
const TASK = /^\s*[-*+][ \t]+\[[ xX]\][ \t]+\S/
const BULLET = /^\s*[-*+][ \t]+\S/
const ORDERED = /^\s*\d{1,9}[.)][ \t]+\S/
const QUOTE = /^\s{0,3}>[ \t]?\S/
const RULE = /^\s{0,3}([-*_])([ \t]*\1){2,}[ \t]*$/
const TABLE_SEPARATOR = /^\s*\|?[ \t]*:?-{1,}:?[ \t]*(\|[ \t]*:?-{1,}:?[ \t]*)+\|?[ \t]*$/
const COLLAPSIBLE_OPEN = /^\+\+\+[ \t]+\S/
const COLLAPSIBLE_CLOSE = /^\+\+\+[ \t]*$/
const HTML_DETAILS = /^\s*<details[\s>]/i
const IMAGE_LINE = /^\s*!\[[^\]\n]*\]\([^)\s]+(?:\s+"[^"]*")?\)\s*$/

const INLINE_PATTERNS = [
  /\*\*[^\s*][^*\n]*\*\*/,
  /(^|[\s(])__[^\s_][^_\n]*__(?=$|[\s).,;:!?])/,
  /(^|[^`])`[^`\n]+`(?!`)/,
  /\[[^\]\n]+\]\((?:https?:\/\/|\/|#|mailto:)[^)\s]*\)/,
  /~~[^\s~][^~\n]*~~/,
  /!\[[^\]\n]*\]\([^)\s]+\)/,
  /(^|[\s(])\*[^\s*][^*\n]*\*(?=$|[\s).,;:!?])/,
]

export function normalizeNewlines(text: string) {
  return text.replace(/\r\n?/g, '\n')
}

/** One non-space token that parses as an http(s) URL: left to the link / embed / mention paste handlers. */
export function isSingleUrl(text: string) {
  const trimmed = text.trim()
  if (!trimmed || /\s/.test(trimmed)) return false
  try {
    const url = new URL(trimmed)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

/** True for a line that on its own is unmistakably a Markdown block (used for single-line pastes too). */
export function isBlockSyntaxLine(line: string) {
  return HEADING.test(line) || TASK.test(line) || BULLET.test(line) || ORDERED.test(line) || QUOTE.test(line) || RULE.test(line) || IMAGE_LINE.test(line)
}

export function looksLikeMarkdown(input: string) {
  const text = normalizeNewlines(input).trim()
  if (!text) return false
  const lines = text.split('\n')
  if (lines.length === 1) return !isSingleUrl(text) && isBlockSyntaxLine(text)

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index]
    if (FENCE.test(line)) {
      // An opening fence only counts when something closes it later; otherwise it is stray backticks.
      const marker = FENCE.exec(line)?.[1] ?? '```'
      if (lines.slice(index + 1).some(next => next.trim().startsWith(marker))) return true
      continue
    }
    if (isBlockSyntaxLine(line)) return true
    if (COLLAPSIBLE_OPEN.test(line) && lines.slice(index + 1).some(next => COLLAPSIBLE_CLOSE.test(next))) return true
    if (HTML_DETAILS.test(line)) return true
    if (index > 0 && line.includes('-') && TABLE_SEPARATOR.test(line) && lines[index - 1].includes('|')) return true
  }
  return INLINE_PATTERNS.some(pattern => lines.some(line => pattern.test(line)))
}

const STRUCTURAL_TAGS = new Set([
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'table', 'thead', 'tbody', 'tr', 'td', 'th', 'blockquote', 'pre', 'img',
  'a', 'strong', 'b', 'em', 'i', 'u', 's', 'del', 'strike', 'code', 'hr', 'details', 'summary', 'input', 'aside', 'figure',
  'video', 'iframe', 'svg', 'mark', 'sub', 'sup', 'label', 'button', 'picture', 'audio',
])

/**
 * Editors and terminals (VS Code, Sublime, iTerm...) put the text on the clipboard twice: once as text/plain and once as
 * a styled `<div>`/`<span>` wrapper. That html carries no real structure, so the plain text is the better source.
 * Rendered pages (GitHub, Notion, a browser selection) contain headings, lists, links, tables... and stay on the HTML path.
 */
export function isPlainTextWrapperHtml(html: string) {
  if (!html.trim()) return true
  if (/data-pm-slice|data-flow-/i.test(html)) return false
  try {
    const document = new DOMParser().parseFromString(html, 'text/html')
    for (const element of document.body.querySelectorAll('*')) {
      if (STRUCTURAL_TAGS.has(element.tagName.toLowerCase())) return false
    }
    return true
  } catch {
    return false
  }
}
