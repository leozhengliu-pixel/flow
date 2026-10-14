/**
 * The faint highlight the reference app keeps on text quoted into the floating agent chat ("Ask agent" on a selection in an
 * editor or a comment composer) while its chip sits in the chat composer. Like the reference app it is a CSS Custom Highlight
 * ("flow-agent-quote", painted with --agent-quote-highlight: the tertiary text colour at 10% in both themes), so the
 * document itself is untouched. It goes once the chip is gone (removed, or the message sent) or the chat is minimized
 * or closed, and does not come back when a minimized chat is reopened; moving the caret or selecting other text in
 * the editor leaves it in place.
 */
import type { Editor } from '@tiptap/core'
import './agent-quote-highlight.css'

export const AGENT_QUOTE_HIGHLIGHT = 'flow-agent-quote'

let current: { token: number; quote: string } | undefined
let counter = 0

function highlightRegistry() {
  return typeof CSS !== 'undefined' && 'highlights' in CSS && typeof Highlight !== 'undefined' ? CSS.highlights : undefined
}

/** The DOM range of `from`–`to` in an editor (undefined when it is empty or the positions are gone). */
export function editorTextRange(editor: Editor | undefined, from: number, to: number): Range | undefined {
  if (!editor || editor.isDestroyed || from >= to || to > editor.state.doc.content.size) return undefined
  try {
    const start = editor.view.domAtPos(from)
    const end = editor.view.domAtPos(to)
    const range = document.createRange()
    range.setStart(start.node, start.offset)
    range.setEnd(end.node, end.offset)
    return range.collapsed ? undefined : range
  } catch {
    return undefined
  }
}

/** Highlights the quoted text (replacing any earlier quote's highlight) and returns its token. */
export function showAgentQuoteHighlight(quote: string, range: Range | undefined): number | undefined {
  const highlights = highlightRegistry()
  current = undefined
  highlights?.delete(AGENT_QUOTE_HIGHLIGHT)
  if (!highlights || !range) return undefined
  highlights.set(AGENT_QUOTE_HIGHLIGHT, new Highlight(range))
  current = { token: ++counter, quote }
  return current.token
}

/** The token of the highlight on the page for `quote`, so the chat that shows its chip can own (and clear) it. */
export function claimAgentQuoteHighlight(quote: string | undefined): number | undefined {
  return quote && current?.quote === quote ? current.token : undefined
}

/** Removes the highlight if it is still the one `token` names (a newer quote's highlight stays). */
export function clearAgentQuoteHighlight(token: number | undefined) {
  if (token === undefined || current?.token !== token) return
  current = undefined
  highlightRegistry()?.delete(AGENT_QUOTE_HIGHLIGHT)
}
