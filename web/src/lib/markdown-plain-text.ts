/**
 * Markdown as one line of plain text, for snippets that are not rendered as rich text (inbox rows, search results, list
 * previews, hover-card summaries, toasts, aria-labels). A mention is written as `[label](path)`, so this keeps a link's text
 * and drops its target; agent shortcodes keep their label; images keep their alt text.
 */
export function markdownPlainText(markdown: string | null | undefined): string {
  if (!markdown) return ''
  let text = String(markdown).replace(/\r\n?/g, '\n')
  // Fenced code keeps its content, without the fences or the language tag.
  text = text.replace(/^[ \t]*(```|~~~)[^\n]*\n?([\s\S]*?)(?:\n?[ \t]*\1[ \t]*$|$)/gm, (_match, _fence, body: string) => `${body}\n`)
  // Entity shortcodes: [agentEntity kind="issue" id="..." label="DEV-1"]
  text = text.replace(/\[agentEntity\b[^\]\n]*?\blabel="((?:[^"\\]|\\.)*)"[^\]\n]*\]/g, (_match, label: string) => label.replace(/\\(.)/g, '$1'))
  text = text.replace(/\[agentEntity\b[^\]\n]*\]/g, '')
  // Images, then links (inline, reference-style, autolinks).
  text = text.replace(/!\[((?:[^\]\\]|\\.)*)\]\((?:[^()\s]|\([^()]*\))*(?:\s+"[^"]*")?\)/g, (_match, alt: string) => unescapeText(alt))
  text = text.replace(/\[((?:[^\]\\]|\\.)*)\]\((?:<[^>]*>|(?:[^()\s]|\([^()]*\))*)(?:\s+(?:"[^"]*"|'[^']*'))?\)/g, (_match, label: string) => unescapeText(label))
  text = text.replace(/\[((?:[^\]\\]|\\.)*)\]\[[^\]]*\]/g, (_match, label: string) => unescapeText(label))
  text = text.replace(/^[ \t]*\[[^\]\n]+\]:[ \t]*\S+.*$/gm, '')
  text = text.replace(/<((?:https?|mailto):[^>\s]+)>/g, '$1')
  // Line-level markers: headings, quotes, rules, list and task markers, table pipes.
  text = text.replace(/^[ \t]{0,3}#{1,6}[ \t]+/gm, '')
  text = text.replace(/^[ \t]{0,3}(?:[-*_][ \t]*){3,}$/gm, '')
  text = text.replace(/^[ \t]*(?:>[ \t]?)+/gm, '')
  text = text.replace(/^[ \t]*(?:[-*+]|\d+[.)])[ \t]+(?:\[[ xX]\][ \t]+)?/gm, '')
  text = text.replace(/^[ \t]*\|?[ \t]*:?-{2,}:?(?:[ \t]*\|[ \t]*:?-{2,}:?)*[ \t]*\|?[ \t]*$/gm, '')
  text = text.replace(/^[ \t]*\||\|[ \t]*$/gm, '').replace(/[ \t]*\|[ \t]*/g, ' ')
  // Inline emphasis, strike-through and code spans.
  text = text.replace(/`+([^`\n]+)`+/g, '$1')
  text = text.replace(/(\*\*|__)(?=\S)([\s\S]*?\S)\1/g, '$2')
  text = text.replace(/(^|[^\w*])\*(?=\S)([^*\n]*?\S)\*(?!\w)/g, '$1$2')
  text = text.replace(/(^|[^\w_])_(?=\S)([^_\n]*?\S)_(?![\w])/g, '$1$2')
  text = text.replace(/~~(?=\S)([\s\S]*?\S)~~/g, '$1')
  text = text.replace(/<\/?[a-zA-Z][^>\n]*>/g, '')
  return unescapeText(text).replace(/\s+/g, ' ').trim()
}

function unescapeText(value: string) {
  return value.replace(/\\([\\`*_{}[\]()#+\-.!|>~<])/g, '$1')
}

/** The plain-text snippet, cut at `max` characters with an ellipsis. */
export function markdownSnippet(markdown: string | null | undefined, max = 160): string {
  const text = markdownPlainText(markdown)
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text
}
