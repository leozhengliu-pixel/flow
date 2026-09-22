/**
 * LS-0686 addSelectionAsAgentContext — attach selected text as agent composer context.
 * Opens / focuses the toolbar agent panel and fires an attachment signal (vs prompt-only).
 */

export const AGENT_SELECTION_CONTEXT_EVENT = 'flow:agent-selection-context'
export const AGENT_PANEL_FOCUS_EVENT = 'flow:agent-panel-focus'
export const AGENT_PANEL_OPEN_EVENT = 'flow:agent-panel-open'

/** Linear truncates selection context; keep the first N characters. */
export const AGENT_SELECTION_CONTEXT_MAX_CHARS = 8_000

export type AgentSelectedTextAttachment = {
  type: 'selectedText'
  id: string
  text: string
  truncated?: boolean
}

export type AgentSelectionContextDetail = {
  attachment: AgentSelectedTextAttachment
  /** When true, callers should open a new toolbar session if none is open. */
  openIfClosed?: boolean
  issueId?: string
  from?: number
  to?: number
}

type Listener = (detail: AgentSelectionContextDetail) => void

const listeners = new Set<Listener>()
let panelOpen = false

export function setAgentPanelOpenState(open: boolean) {
  panelOpen = open
}

export function isAgentPanelOpen(): boolean {
  return panelOpen
}

export function subscribeAgentSelectionContext(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

function notify(detail: AgentSelectionContextDetail) {
  for (const listener of listeners) listener(detail)
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(AGENT_SELECTION_CONTEXT_EVENT, { detail }))
  }
}

export function createSelectedTextAttachment(text: string): AgentSelectedTextAttachment {
  const trimmed = text.trim()
  const truncated = trimmed.length > AGENT_SELECTION_CONTEXT_MAX_CHARS
  const slice = trimmed.slice(0, AGENT_SELECTION_CONTEXT_MAX_CHARS)
  return {
    type: 'selectedText',
    id: `selection:${Date.now().toString(36)}:${Math.random().toString(36).slice(2, 8)}`,
    text: slice,
    truncated,
  }
}

/**
 * Attach selection as composer context. If the agent panel is closed, ask hosts to open it.
 * Returns the attachment (or undefined when empty).
 */
export function addSelectionAsAgentContext(
  selectedText: string,
  options?: { issueId?: string; from?: number; to?: number; openIfClosed?: boolean },
): AgentSelectedTextAttachment | undefined {
  const attachment = createSelectedTextAttachment(selectedText)
  if (!attachment.text) return undefined

  if (attachment.truncated && typeof window !== 'undefined') {
    window.dispatchEvent(
      new CustomEvent('flow:toast', {
        detail: {
          tone: 'info',
          title: 'Selection truncated',
          description: `Only the first ${AGENT_SELECTION_CONTEXT_MAX_CHARS.toLocaleString()} characters were included.`,
        },
      }),
    )
  }

  const openIfClosed = options?.openIfClosed ?? true
  if (!panelOpen && openIfClosed && typeof window !== 'undefined') {
    window.dispatchEvent(
      new CustomEvent(AGENT_PANEL_OPEN_EVENT, {
        detail: { issueId: options?.issueId, initialPrompt: attachment.text, attachment },
      }),
    )
  }

  notify({
    attachment,
    openIfClosed,
    issueId: options?.issueId,
    from: options?.from,
    to: options?.to,
  })

  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(AGENT_PANEL_FOCUS_EVENT))
    requestAnimationFrame(() => {
      document
        .querySelector<HTMLElement>('[data-agent-panel-popover] [contenteditable="true"], [data-agent-panel-popover] textarea')
        ?.focus()
    })
  }

  return attachment
}
