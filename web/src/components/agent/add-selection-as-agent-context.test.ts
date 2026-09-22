import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import {
  addSelectionAsAgentContext,
  AGENT_PANEL_OPEN_EVENT,
  AGENT_SELECTION_CONTEXT_MAX_CHARS,
  createSelectedTextAttachment,
  isAgentPanelOpen,
  setAgentPanelOpenState,
  subscribeAgentSelectionContext,
} from './add-selection-as-agent-context'

describe('LS-0686 addSelectionAsAgentContext', () => {
  beforeEach(() => {
    setAgentPanelOpenState(false)
  })
  afterEach(() => {
    setAgentPanelOpenState(false)
  })

  it('creates truncated attachments', () => {
    const attachment = createSelectedTextAttachment('x'.repeat(AGENT_SELECTION_CONTEXT_MAX_CHARS + 10))
    expect(attachment.text).toHaveLength(AGENT_SELECTION_CONTEXT_MAX_CHARS)
    expect(attachment.truncated).toBe(true)
  })

  it('notifies subscribers and opens panel when closed', () => {
    const listener = vi.fn()
    const open = vi.fn()
    const unsubscribe = subscribeAgentSelectionContext(listener)
    window.addEventListener(AGENT_PANEL_OPEN_EVENT, open as EventListener)
    const attachment = addSelectionAsAgentContext('hello world', { issueId: 'issue-1' })
    expect(attachment?.text).toBe('hello world')
    expect(listener).toHaveBeenCalledOnce()
    expect(open).toHaveBeenCalledOnce()
    unsubscribe()
    window.removeEventListener(AGENT_PANEL_OPEN_EVENT, open as EventListener)
  })

  it('skips open signal when panel already open', () => {
    setAgentPanelOpenState(true)
    expect(isAgentPanelOpen()).toBe(true)
    const open = vi.fn()
    window.addEventListener(AGENT_PANEL_OPEN_EVENT, open as EventListener)
    addSelectionAsAgentContext('already open', { openIfClosed: true })
    expect(open).not.toHaveBeenCalled()
    window.removeEventListener(AGENT_PANEL_OPEN_EVENT, open as EventListener)
  })
})
