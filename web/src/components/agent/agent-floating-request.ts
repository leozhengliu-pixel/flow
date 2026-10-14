/**
 * Opening the app's floating agent chat from anywhere (e.g. a document's selection toolbar), and the way a quoted
 * selection travels inside a message: the reference app shows it as an inline "selected text" chip, while the model reads it as a
 * markdown quote. A word joiner after the first "> " marks the quote as a chip, so a typed "> " stays plain text.
 */
import type { AgentMention } from './agent-mention-input'
import { showAgentQuoteHighlight } from './agent-quote-highlight'

export const OPEN_FLOATING_AGENT_EVENT = 'flow:open-floating-agent'

export type FloatingAgentRequest = {
  /** Text selected on the page, shown as a chip at the start of the composer. */
  quote?: string
  /** Text to send right away (the reference app's "Write with Agent"). */
  prompt?: string
  /** Entities placed in the composer as mention chips (the reference app's "Ask the reference app" on an "Issue created" toast). */
  mentions?: AgentMention[]
  /** The issue the request comes from when it is not the page's issue (e.g. a description in a peek or the inbox). */
  issueId?: string
}

/**
 * Opens the floating chat. With `range` (where `quote` sits in the editor) the quoted text keeps a faint highlight
 * while its chip is in the composer, like the reference app (see agent-quote-highlight).
 */
export function requestFloatingAgent({ range, ...detail }: FloatingAgentRequest & { range?: Range }) {
  if (detail.quote) showAgentQuoteHighlight(detail.quote, range)
  window.dispatchEvent(new CustomEvent<FloatingAgentRequest>(OPEN_FLOATING_AGENT_EVENT, { detail }))
}

const QUOTE_MARK = '⁠'

/** The message the agent receives: the quoted selection first, then what the user wrote. */
export function quoteAgentMessage(quote: string | undefined, text: string) {
  const selection = quote?.replace(/\r\n?/g, '\n').trim()
  if (!selection) return text
  const block = selection.split('\n').map((line, index) => `> ${index === 0 ? QUOTE_MARK : ''}${line}`.trimEnd()).join('\n')
  return text.trim() ? `${block}\n\n${text.trim()}` : block
}

/** Splits a sent message back into its quoted selection (if it starts with one) and the rest of the text. */
export function splitAgentQuote(content: string): { quote?: string; text: string } {
  if (!content.startsWith(`> ${QUOTE_MARK}`)) return { text: content }
  const lines = content.split('\n')
  let end = 0
  while (end < lines.length && lines[end].startsWith('>')) end += 1
  const quote = lines.slice(0, end).map(line => line.replace(/^> ?/, '')).join('\n').replace(QUOTE_MARK, '')
  return { quote, text: lines.slice(end).join('\n').replace(/^\n+/, '') }
}
