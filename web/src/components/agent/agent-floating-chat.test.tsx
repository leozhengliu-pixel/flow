import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'
import { OPEN_FLOATING_AGENT_EVENT, quoteAgentMessage, requestFloatingAgent, splitAgentQuote } from './agent-floating-request'
import fs from 'node:fs'

const readStyles = (name: string) => fs.readFileSync(decodeURIComponent(new URL(name, import.meta.url).pathname), 'utf8')
const panelCss = readStyles('./agent-panel.module.css')
const threadCss = readStyles('./entity-agent-thread.module.css')

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn(), markAgentSessionRead: vi.fn(), updateAgentMessageFeedback: vi.fn(), createAgentSessionFeedback: vi.fn(), deleteAgentSession: vi.fn(), uploadAgentFile: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentChatPanel } from './agent-chat-panel'
import { AgentPanel } from './agent-panel'
import { clampFloatSize, FLOAT_DEFAULT_SIZE } from './agent-float-size'
import { AGENT_QUOTE_HIGHLIGHT, clearAgentQuoteHighlight, showAgentQuoteHighlight } from './agent-quote-highlight'

/** jsdom has no CSS Custom Highlight API: a registry and Highlight stand-in record what the page would paint. */
function stubHighlights() {
  const registry = new Map<string, { ranges: Range[] }>()
  vi.stubGlobal('Highlight', class { ranges: Range[]; constructor(...ranges: Range[]) { this.ranges = ranges } })
  vi.stubGlobal('CSS', { ...globalThis.CSS, highlights: registry })
  return registry
}
function quotedRange(text: string) {
  const paragraph = document.createElement('p')
  paragraph.textContent = text
  document.body.append(paragraph)
  const range = document.createRange()
  range.selectNodeContents(paragraph)
  return range
}
afterEach(() => { vi.unstubAllGlobals() })

const session = (content: string): AgentSession => ({
  id: 'session-1', slugId: 'chat', userId: 'user-1', title: 'Explain the selection', favorite: false, location: 'toolbar', issueIds: [], skillIds: [],
  messages: [{ id: 'user-message', role: 'user', content, createdAt: '2026-10-13T00:00:00Z' }],
  createdAt: '2026-10-13T00:00:00Z', updatedAt: '2026-10-13T00:00:00Z',
})

describe('quoted selections in agent messages', () => {
  it('sends the selection first as a markdown quote and splits it back out for the chip', () => {
    const message = quoteAgentMessage('line one\nline two', ' What is this? ')
    expect(message).toBe('> ⁠line one\n> line two\n\nWhat is this?')
    expect(splitAgentQuote(message)).toEqual({ quote: 'line one\nline two', text: 'What is this?' })
    expect(quoteAgentMessage('only the quote', '')).toBe('> ⁠only the quote')
    expect(quoteAgentMessage(undefined, 'Plain')).toBe('Plain')
  })

  it('leaves a quote the user typed themselves as plain text', () => {
    expect(splitAgentQuote('> typed quote\n\nhello')).toEqual({ text: '> typed quote\n\nhello' })
  })

  it('opens the floating chat through a window event', () => {
    const listener = vi.fn()
    window.addEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    requestFloatingAgent({ quote: 'Selected' })
    window.removeEventListener(OPEN_FLOATING_AGENT_EVENT, listener)
    expect((listener.mock.calls[0][0] as CustomEvent).detail).toEqual({ quote: 'Selected' })
  })

  it('highlights only the newest quote, and an older chat cannot clear a newer quote\'s highlight', () => {
    const page = stubHighlights()
    const first = showAgentQuoteHighlight('First', quotedRange('First'))
    const second = showAgentQuoteHighlight('Second', quotedRange('Second'))
    expect(page.get(AGENT_QUOTE_HIGHLIGHT)?.ranges[0].toString()).toBe('Second')
    clearAgentQuoteHighlight(first)
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(true)
    clearAgentQuoteHighlight(second)
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(false)
    // Without a range (or the API) nothing is painted, and an earlier highlight goes.
    showAgentQuoteHighlight('Third', quotedRange('Third'))
    requestFloatingAgent({ quote: 'No range' })
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(false)
  })
})

describe('floating agent chat (the reference app toolbar chat)', () => {
  beforeEach(() => {
    localStorage.clear()
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    Object.values(streams).forEach(mock => mock.mockReset())
  })

  const renderChat = (props: Partial<Parameters<typeof AgentChatPanel>[0]> = {}) => {
    const data = makeBootstrap()
    render(<I18nProvider><AgentChatPanel data={data} issues={[]} onClose={vi.fn()} open pageContext={{ type: 'document', id: 'doc-1', label: 'Spec' }} {...props}/></I18nProvider>)
    return data
  }

  it('is blank until you write: no greeting or suggestions, just the context row and composer', async () => {
    renderChat()
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })).toHaveAttribute('contenteditable', 'true'))
    expect(screen.queryByText('Welcome to Flow')).not.toBeInTheDocument()
    expect(screen.queryByText('Ask anything or propose changes')).not.toBeInTheDocument()
    expect(screen.getByText('Spec')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Attach images, files, or videos' })).toBeVisible()
  })

  it('routes every file from the attach button into the message as a block and ignores audio', async () => {
    api.uploadAgentFile.mockImplementation(async (file: File) => ({ name: file.name, contentType: file.type, size: file.size, url: `/uploads/agent_upload_0123456789abcdef_1_${file.name}` }))
    renderChat()
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer).toHaveAttribute('contenteditable', 'true'))
    const input = document.querySelector<HTMLInputElement>('input[type="file"]')!
    const pdf = new File(['pdf'], 'spec.pdf', { type: 'application/pdf' })
    const song = new File(['mp3'], 'song.mp3', { type: 'audio/mpeg' })
    fireEvent.change(input, { target: { files: [pdf, song] } })
    await waitFor(() => expect(composer.querySelectorAll('[data-agent-block]')).toHaveLength(1))
    expect(composer.querySelector('[data-kind="file"]')).not.toBeNull()
    expect(api.uploadAgentFile).toHaveBeenCalledTimes(1)
    expect(api.uploadAgentFile).toHaveBeenCalledWith(pdf)
  })

  it('shows the selection as a chip, enables Send with only the chip, and sends it as a quote with the document as context', async () => {
    streams.streamNewAgentSession.mockImplementation(async (input: { message: string }, onEvent: (event: unknown) => void) => {
      const next = session(input.message)
      onEvent({ type: 'session.completed', session: next })
      return next
    })
    renderChat({ initialQuote: '123123123' })
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer).toHaveAttribute('contenteditable', 'true'))
    const chip = composer.querySelector('[data-agent-quote]')
    expect(chip).toHaveTextContent('123123123')
    expect(chip).toHaveAttribute('contenteditable', 'false')
    const send = screen.getByRole('button', { name: 'Send message' })
    expect(send).toBeEnabled()
    expect(send).toHaveAttribute('data-state', 'ready')
    // Typing after the chip, then sending.
    composer.append(document.createTextNode('What is this?'))
    fireEvent.input(composer)
    fireEvent.click(send)
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(
      expect.objectContaining({ message: '> ⁠123123123\n\nWhat is this?', documentIds: ['doc-1'], location: 'toolbar' }),
      expect.any(Function), expect.any(AbortSignal),
    )
    // The sent message shows the selection as the reference app's inline chip, not as a markdown quote.
    const sent = await screen.findByLabelText('Your message')
    expect(sent).toHaveTextContent('123123123 What is this?')
    expect(sent.querySelector('blockquote')).toBeNull()
  })

  it('keeps the quoted text highlighted while its chip is in the chat, and drops it with the chip', async () => {
    const page = stubHighlights()
    const range = quotedRange('Highlight me')
    requestFloatingAgent({ quote: 'Highlight me', range })
    expect(page.get(AGENT_QUOTE_HIGHLIGHT)?.ranges).toEqual([range])
    renderChat({ initialQuote: 'Highlight me' })
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer.querySelector('[data-agent-quote]')).not.toBeNull())
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(true)
    composer.querySelector('[data-agent-quote]')?.remove()
    fireEvent.input(composer)
    await waitFor(() => expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(false))
  })

  it('drops the highlight when the chat is minimized, and it stays gone when the chat comes back', async () => {
    const user = userEvent.setup()
    const page = stubHighlights()
    requestFloatingAgent({ quote: 'Minimize me', range: quotedRange('Minimize me') })
    renderChat({ initialQuote: 'Minimize me' })
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Send a message to Flow Agent' }).querySelector('[data-agent-quote]')).not.toBeNull())
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(true)
    await user.click(screen.getByRole('button', { name: 'Minimize chat' }))
    expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(false)
  })

  it('drops the highlight when the message is sent', async () => {
    const page = stubHighlights()
    streams.streamNewAgentSession.mockImplementation(async (input: { message: string }) => session(input.message))
    requestFloatingAgent({ quote: 'Send me', range: quotedRange('Send me') })
    renderChat({ initialQuote: 'Send me' })
    const send = screen.getByRole('button', { name: 'Send message' })
    await waitFor(() => expect(send).toBeEnabled())
    fireEvent.click(send)
    await waitFor(() => expect(page.has(AGENT_QUOTE_HIGHLIGHT)).toBe(false))
  })

  it('starts with the mention chips it is handed (an "Issue created" toast\'s "Ask Flow")', async () => {
    const data = makeBootstrap()
    const issue = data.issues[0]
    render(<I18nProvider><AgentChatPanel data={data} initialMentions={[{ type: 'issue', id: issue.id, label: issue.identifier }]} issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer.querySelector(`[data-mention-id="${issue.id}"]`)).not.toBeNull())
    expect(composer.querySelector('[data-mention-id]')).toHaveTextContent(`${issue.identifier} ${issue.title}`)
    expect(screen.getByRole('button', { name: 'Send message' })).toBeEnabled()
  })

  it('asks "Discard draft?" before closing a chat with a draft; Cancel keeps it, Discard closes', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    renderChat({ initialQuote: 'Keep me', onClose })
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Send a message to Flow Agent' }).querySelector('[data-agent-quote]')).not.toBeNull())
    await user.click(screen.getByRole('button', { name: 'Close chat' }))
    const dialog = await screen.findByRole('dialog', { name: 'Discard draft?' })
    expect(dialog).toHaveTextContent('Your draft will be lost if you close this chat.')
    expect(screen.getByRole('button', { name: 'Discard' })).toHaveFocus()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog', { name: 'Discard draft?' })).not.toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: 'Send a message to Flow Agent' }).querySelector('[data-agent-quote]')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Close chat' }))
    await user.click(await screen.findByRole('button', { name: 'Discard' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes at once without a draft (spaces do not count), and Escape hides the chat with its draft instead', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const onHide = vi.fn()
    renderChat({ onClose, onHide })
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer).toHaveAttribute('contenteditable', 'true'))
    composer.append(document.createTextNode('   '))
    fireEvent.input(composer)
    await user.click(screen.getByRole('button', { name: 'Close chat' }))
    expect(screen.queryByRole('dialog', { name: 'Discard draft?' })).not.toBeInTheDocument()
    expect(onClose).toHaveBeenCalledTimes(1)
    composer.append(document.createTextNode('A real draft'))
    fireEvent.input(composer)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onHide).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('dialog', { name: 'Discard draft?' })).not.toBeInTheDocument()
  })

  it('drops the quote when its chip is deleted from the composer', async () => {
    renderChat({ initialQuote: 'Remove me' })
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer.querySelector('[data-agent-quote]')).not.toBeNull())
    composer.querySelector('[data-agent-quote]')?.remove()
    fireEvent.input(composer)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled())
  })

  it('toggles the page context with the "Use document as context" button and with Tab in the composer', async () => {
    const user = userEvent.setup()
    renderChat()
    const toggle = await screen.findByRole('button', { name: 'Use document as context' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByText('Spec')).not.toBeInTheDocument()
    const composer = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(composer).toHaveAttribute('contenteditable', 'true'))
    fireEvent.keyDown(composer, { key: 'Tab' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByText('Spec')).toBeVisible()
    // The context row's × switches it off too.
    await user.click(screen.getByRole('button', { name: 'Remove from context' }))
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
  })

  it('offers the reference app\'s "…" chat menu once the chat exists, and Delete removes it and closes the chat', async () => {
    api.deleteAgentSession.mockResolvedValue(undefined)
    const user = userEvent.setup()
    const onClose = vi.fn()
    renderChat({ initialSession: session('Hello'), onClose })
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    expect(screen.getByRole('menuitem', { name: /Copy as markdown/ })).toBeVisible()
    await user.click(screen.getByRole('menuitem', { name: /Delete/ }))
    await waitFor(() => expect(api.deleteAgentSession).toHaveBeenCalledWith('session-1'))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('has no page-context toggle once the conversation started', async () => {
    renderChat({ initialSession: session('Hello') })
    await screen.findByText('Hello')
    expect(screen.queryByRole('button', { name: 'Use document as context' })).not.toBeInTheDocument()
  })
})

describe('floating agent panel chrome', () => {
  beforeEach(() => localStorage.clear())

  it('is 400×365 by default and resizes within 360–600 wide and 360px–80% of the window tall', () => {
    expect(FLOAT_DEFAULT_SIZE).toEqual({ width: 400, height: 365 })
    expect(clampFloatSize({ width: 100, height: 100 }, 704)).toEqual({ width: 360, height: 360 })
    expect(clampFloatSize({ width: 900, height: 900 }, 704)).toEqual({ width: 600, height: 563 })
  })

  it('sizes itself from the remembered size and grows from its top-left corner', () => {
    localStorage.setItem('flow:agent-float-size', JSON.stringify({ width: 420, height: 380 }))
    render(<I18nProvider><AgentPanel open onRequestClose={vi.fn()} title="New chat" variant="floating"><p>Body</p></AgentPanel></I18nProvider>)
    const panel = screen.getByRole('dialog')
    expect(panel.style.getPropertyValue('--agent-float-width')).toBe('420px')
    expect(panel.style.getPropertyValue('--agent-float-height')).toBe('380px')
    const corner = panel.querySelector('[data-edge="corner"]')!
    fireEvent.pointerDown(corner, { clientX: 500, clientY: 400 })
    act(() => { window.dispatchEvent(new PointerEvent('pointermove', { clientX: 450, clientY: 380 })) })
    act(() => { window.dispatchEvent(new PointerEvent('pointerup')) })
    expect(panel.style.getPropertyValue('--agent-float-width')).toBe('470px')
    expect(panel.style.getPropertyValue('--agent-float-height')).toBe('400px')
    expect(JSON.parse(localStorage.getItem('flow:agent-float-size') ?? '{}')).toEqual({ width: 470, height: 400 })
  })

  it('minimizes to a pill in the bottom bar and comes back from it', async () => {
    const user = userEvent.setup()
    const slot = document.createElement('div')
    slot.setAttribute('data-agent-dock-slot', '')
    document.body.append(slot)
    const onClose = vi.fn()
    render(<I18nProvider><AgentPanel open onRequestClose={onClose} title="Explain the selection" variant="floating" unread><p>Body</p></AgentPanel></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Minimize chat' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(slot).toHaveTextContent('Explain the selection')
    await user.click(screen.getByRole('button', { name: 'Restore chat' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(slot).toBeEmptyDOMElement()
    slot.remove()
  })

  it('pins the measured geometry in CSS: 32/34px offsets, 48px header, hidden scrollbar track, 4px composer frame', () => {
    expect(panelCss).toMatch(/bottom: 34px;\s*right: 32px;/)
    expect(panelCss).toMatch(/height: 48px;\s*flex-basis: 48px;\s*padding: 0 6px 0 18px;/)
    expect(panelCss).toMatch(/\.floatResize\[data-edge='top'\] \{ top: -7px;/)
    expect(threadCss).toMatch(/\.conversation:not\(#_\)\{padding:4px 8px 20px;scrollbar-width:auto;scrollbar-color:transparent transparent\}/)
    expect(threadCss).toMatch(/\.composerArea:not\(#_\)\{margin:0 4px 4px;padding:4px;/)
    expect(threadCss).toMatch(/linear-gradient\(var\(--agent-editing-frame-top\),var\(--agent-editing-frame-bottom\)\)/)
  })
})
