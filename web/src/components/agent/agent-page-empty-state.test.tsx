import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'
import { agentSessionUnread, formatAgentHistoryTime } from './agent-read-state'
import { translateToChinese } from '@/i18n/i18n'

const HOUR = 3_600_000, DAY = 24 * HOUR

function chat(id: string, title: string, ageMs: number, read: 'read' | 'unread' | 'untracked'): AgentSession {
  const updatedAt = new Date(Date.now() - ageMs).toISOString()
  const lastReadAt = read === 'untracked' ? undefined : new Date(Date.now() - ageMs + (read === 'read' ? 1000 : -1000)).toISOString()
  return { id, slugId: `${id}-slug`, userId: 'user-1', title, favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [], createdAt: updatedAt, updatedAt, lastReadAt }
}

function renderPage(sessions: AgentSession[], props: { onNavigate?: (href: string) => void; onSessionChange?: (id: string, session?: AgentSession) => void; chatSlug?: string } = {}) {
  return render(<I18nProvider><AgentPage chatSlug={props.chatSlug} data={makeBootstrap({ agentSessions: sessions, agentSkills: [] })} onNavigate={props.onNavigate ?? vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={props.onSessionChange ?? vi.fn()}/></I18nProvider>)
}

describe('agent page empty state', () => {
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
  })

  it('draws Flow\'s faint mark behind the composer, out of the way of clicks', async () => {
    const { container } = renderPage([])
    expect(await screen.findByRole('textbox', { name: 'Send a message to Flow AI' })).toHaveAttribute('data-placeholder', 'Ask Flow…')
    const logo = container.querySelector('svg[class*="emptyLogo"]')
    expect(logo).not.toBeNull()
    expect(logo).toHaveAttribute('aria-hidden', 'true')
    // The mark and the composer share the composer slot; the mark comes first so the composer paints over it.
    const slot = logo!.parentElement!
    expect(slot.className).toContain('emptyComposerSlot')
    expect(slot.querySelector('[class*="composer"]')).toContainElement(screen.getByRole('textbox', { name: 'Send a message to Flow AI' }))
    expect(container.querySelector('main')?.className).toContain('emptyPage')
  })

  it('shows nothing under the composer when there are no chats, like Linear', () => {
    renderPage([])
    expect(screen.queryByRole('navigation', { name: 'Recent agent chats' })).not.toBeInTheDocument()
  })

  it('lists recent chats newest first with an unread dot only on unread ones', () => {
    renderPage([
      chat('old', 'Write Compare Test project update', 10 * DAY, 'read'),
      chat('new', 'Introduce cycles', 7 * DAY + 3 * HOUR, 'unread'),
      chat('mid', 'Greeting', 8 * DAY, 'untracked'),
    ])
    const history = screen.getByRole('navigation', { name: 'Recent agent chats' })
    const rows = within(history).getAllByRole('link')
    expect(rows.map(row => row.querySelector('[class*="historyTitle"]')?.textContent)).toEqual(['Introduce cycles', 'Greeting', 'Write Compare Test project update'])
    expect(rows.map(row => row.querySelector('time')?.textContent)).toEqual(['1w', '8d', '10d'])

    const [unread, untracked, read] = rows
    expect(unread).toHaveAttribute('data-unread', 'true')
    expect(unread.querySelector('[class*="historyDot"]')).not.toBeNull()
    expect(within(unread).getByText(/Unread/)).toHaveClass('sr-only')
    for (const row of [untracked, read]) {
      expect(row).not.toHaveAttribute('data-unread')
      expect(row.querySelector('[class*="historyDot"]')).toBeNull()
      expect(within(row).queryByText(/Unread/)).not.toBeInTheDocument()
    }
    expect(unread).toHaveAttribute('href', '/workspace/agent/new-slug')
  })

  it('opens a chat in place on click and leaves modified clicks to the browser', async () => {
    const navigate = vi.fn()
    renderPage([chat('one', 'Plan the launch', HOUR, 'unread')], { onNavigate: navigate })
    const row = screen.getByRole('link', { name: /Plan the launch/ })
    // Record whether the page left the modified click alone, then stop jsdom from trying to follow it.
    let prevented: boolean | undefined
    const record = (event: Event) => { prevented = event.defaultPrevented; event.preventDefault() }
    window.addEventListener('click', record)
    fireEvent.click(row, { metaKey: true })
    window.removeEventListener('click', record)
    expect(prevented).toBe(false)
    expect(navigate).not.toHaveBeenCalled()
    await userEvent.setup().click(row)
    expect(navigate).toHaveBeenCalledWith('/workspace/agent/one-slug')
  })

  it('offers Linear\'s row menu and opens a chat in the toolbar', async () => {
    const session = chat('one', 'Plan the launch', HOUR, 'read')
    api.updateAgentSession.mockResolvedValue({ ...session, location: 'toolbar' })
    const sessionChange = vi.fn()
    renderPage([session], { onSessionChange: sessionChange })
    fireEvent.contextMenu(screen.getByRole('link', { name: /Plan the launch/ }))
    const menu = await screen.findByRole('menu', { name: 'Chat options' })
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Copy link', 'Open in new tab', 'Open in toolbar'])
    await userEvent.setup().click(within(menu).getByRole('menuitem', { name: 'Open in toolbar' }))
    expect(api.updateAgentSession).toHaveBeenCalledWith('one', { location: 'toolbar' })
    await waitFor(() => expect(sessionChange).toHaveBeenCalledWith('one', expect.objectContaining({ location: 'toolbar' })))
  })

  it('marks an unread chat read once it is open and keeps the workspace copy in sync', async () => {
    const session = { ...chat('one', 'Plan the launch', HOUR, 'unread'), messages: [{ id: 'm1', role: 'user' as const, content: 'Hi', createdAt: new Date(Date.now() - 2 * HOUR).toISOString() }, { id: 'm2', role: 'assistant' as const, content: 'Hello', createdAt: new Date(Date.now() - HOUR).toISOString() }] }
    api.getAgentSession.mockResolvedValue(session)
    const lastReadAt = new Date().toISOString()
    api.markAgentSessionRead.mockResolvedValue({ ...session, lastReadAt })
    const sessionChange = vi.fn()
    renderPage([session], { chatSlug: 'one-slug', onSessionChange: sessionChange })
    await waitFor(() => expect(api.markAgentSessionRead).toHaveBeenCalledWith('one'))
    await waitFor(() => expect(sessionChange).toHaveBeenCalledWith('one', expect.objectContaining({ id: 'one', lastReadAt })))
    expect(api.markAgentSessionRead).toHaveBeenCalledTimes(1)
  })

  it('does not mark a chat that is already read', async () => {
    const session = { ...chat('one', 'Plan the launch', HOUR, 'read'), messages: [{ id: 'm2', role: 'assistant' as const, content: 'Hello', createdAt: new Date(Date.now() - HOUR).toISOString() }] }
    api.getAgentSession.mockResolvedValue(session)
    renderPage([session], { chatSlug: 'one-slug' })
    expect(await screen.findByText('Hello')).toBeInTheDocument()
    expect(api.markAgentSessionRead).not.toHaveBeenCalled()
  })
})

describe('agent page composer focus', () => {
  const editorName = { name: 'Send a message to Flow AI' }
  beforeEach(() => {
    localStorage.setItem('flow:locale', 'en-US')
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
  })

  it('focuses the composer when the empty new-chat page opens', async () => {
    renderPage([])
    const editor = await screen.findByRole('textbox', editorName)
    expect(editor).toHaveFocus()
  })

  it('lets typing reach the composer instead of the global shortcuts', async () => {
    // App's shortcut handler ignores keys typed into editable targets (jsdom doesn't implement isContentEditable).
    Object.defineProperty(HTMLElement.prototype, 'isContentEditable', { configurable: true, get() { return this.getAttribute('contenteditable') === 'true' } })
    const shortcut = vi.fn()
    const onKey = (event: KeyboardEvent) => {
      const target = event.target
      const editable = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || (target instanceof HTMLElement && target.isContentEditable)
      if (!editable && event.key === 'c') shortcut()
    }
    window.addEventListener('keydown', onKey)
    try {
      renderPage([])
      const editor = await screen.findByRole('textbox', editorName)
      await userEvent.setup().keyboard('create')
      expect(shortcut).not.toHaveBeenCalled()
      expect(editor).toHaveTextContent('create')
    } finally {
      window.removeEventListener('keydown', onKey)
      delete (HTMLElement.prototype as { isContentEditable?: boolean }).isContentEditable
    }
  })

  it('does not take focus from another text field that already has it', async () => {
    const other = document.createElement('input')
    document.body.append(other)
    other.focus()
    try {
      renderPage([])
      await screen.findByRole('textbox', editorName)
      expect(other).toHaveFocus()
    } finally {
      other.remove()
    }
  })

  it('does not take focus from an open dialog', async () => {
    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    document.body.append(dialog)
    try {
      renderPage([])
      expect(await screen.findByRole('textbox', editorName)).not.toHaveFocus()
    } finally {
      dialog.remove()
    }
  })

  it('leaves an open chat alone', async () => {
    const session = { ...chat('one', 'Plan the launch', HOUR, 'read'), messages: [{ id: 'm1', role: 'user' as const, content: 'Hi', createdAt: new Date().toISOString() }] }
    api.getAgentSession.mockResolvedValue(session)
    renderPage([session], { chatSlug: 'one-slug' })
    expect(await screen.findByRole('textbox', editorName)).not.toHaveFocus()
  })

  it('focuses the editor on the first press anywhere on the composer box', async () => {
    renderPage([])
    const editor = await screen.findByRole('textbox', editorName)
    editor.blur()
    expect(editor).not.toHaveFocus()
    const box = editor.closest('[class*="composer"]') as HTMLElement
    // Empty space around the one-line editor (padding / below the first line).
    expect(fireEvent.mouseDown(box.querySelector('[class*="editorScroll"]')!)).toBe(false)
    expect(editor).toHaveFocus()
  })

  it('keeps the composer buttons working instead of grabbing their press', async () => {
    renderPage([])
    const editor = await screen.findByRole('textbox', editorName)
    editor.blur()
    expect(fireEvent.mouseDown(screen.getByRole('button', { name: 'Attach images, files, or videos' }))).toBe(true)
    expect(editor).not.toHaveFocus()
  })
})

describe('agent history helpers', () => {
  it('formats ages in Linear\'s short style', () => {
    const now = Date.parse('2026-10-08T12:00:00Z')
    const ago = (ms: number) => new Date(now - ms).toISOString()
    const t = (source: string) => source
    expect(formatAgentHistoryTime(ago(20_000), t, now)).toBe('now')
    expect(formatAgentHistoryTime(ago(5 * 60_000), t, now)).toBe('5min')
    expect(formatAgentHistoryTime(ago(3 * HOUR), t, now)).toBe('3h')
    expect(formatAgentHistoryTime(ago(2 * DAY), t, now)).toBe('2d')
    // Measured on Linear's agent history: 7d 7h → "1w", 8d 3h → "8d", 8d 17h → "9d", 9d 22h → "10d".
    expect(formatAgentHistoryTime(ago(7 * DAY + 7 * HOUR), t, now)).toBe('1w')
    expect(formatAgentHistoryTime(ago(8 * DAY + 3 * HOUR), t, now)).toBe('8d')
    expect(formatAgentHistoryTime(ago(8 * DAY + 17 * HOUR), t, now)).toBe('9d')
    expect(formatAgentHistoryTime(ago(9 * DAY + 22 * HOUR), t, now)).toBe('10d')
    expect(formatAgentHistoryTime(ago(14 * DAY), t, now)).toBe('2w')
    expect(formatAgentHistoryTime('not a date', t, now)).toBe('')
    expect(formatAgentHistoryTime(ago(8 * DAY), translateToChinese, now)).toBe('8 天')
  })

  it('treats a chat as unread only when it changed after it was last read', () => {
    expect(agentSessionUnread({ updatedAt: '2026-10-08T12:00:01Z', lastReadAt: '2026-10-08T12:00:00Z' })).toBe(true)
    expect(agentSessionUnread({ updatedAt: '2026-10-08T12:00:00Z', lastReadAt: '2026-10-08T12:00:00Z' })).toBe(false)
    expect(agentSessionUnread({ updatedAt: '2026-10-08T12:00:00Z' })).toBe(false)
  })
})
