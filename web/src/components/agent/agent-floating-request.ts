/**
 * Opening the app's floating agent chat from anywhere (e.g. a document's selection toolbar), and the way a quoted
 * selection travels inside a message: the reference app shows it as an inline "selected text" chip, while the model reads it as a
 * markdown quote. A word joiner after the first "> " marks the quote as a chip, so a typed "> " stays plain text.
 */
export const OPEN_FLOATING_AGENT_EVENT = 'flow:open-floating-agent'

export type FloatingAgentRequest = {
  /** Text selected on the page, shown as a chip at the start of the composer. */
  quote?: string
  /** Text to send right away (the reference app's "Write with Agent"). */
  prompt?: string
}

export function requestFloatingAgent(detail: FloatingAgentRequest) {
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
