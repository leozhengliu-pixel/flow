import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRef } from 'react'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentSession } from '@/types/flow'
import { buildChatHistory, chatHistoryGroup, isChatRelatedTo } from './agent-chat-history-model'
import { AgentChatHistoryPopover } from './agent-chat-history-popover'

const api = vi.hoisted(() => ({ listAgentSessions: vi.fn(), markAgentSessionRead: vi.fn() }))
vi.mock('@/lib/api', () => api)

// Tuesday, 13 October 2026, 09:00 local time.
const NOW = new Date(2026, 9, 13, 9, 0, 0)
const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

function session(id: string, title: string, age: number, extra: Partial<AgentSession> = {}): AgentSession {
  const updatedAt = new Date(NOW.getTime() - age).toISOString()
  return {
    id, slugId: `${id}-slug`, userId: 'user-1', title, favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [],
    createdAt: updatedAt, updatedAt, ...extra,
  }
}

const sessions = [
  session('a', 'QA Pill Measure Options', 27 * MINUTE),
  session('b', 'Choose Next QA Action', 29 * MINUTE),
  session('c', 'Formatting Test with FLO-1', 4 * DAY),
  session('d', '介绍 参照应用的 cycle', 13 * DAY),
  session('e', 'Triage agent', 14 * DAY),
  session('f', 'create team: yes', 15 * DAY, { lastReadAt: new Date(NOW.getTime() - 16 * DAY).toISOString() }),
  session('g', 'Summarize Document 123', 2 * MINUTE, { documentIds: ['doc-1'], location: 'toolbar' }),
]

function renderPopover(props: Partial<Parameters<typeof AgentChatHistoryPopover>[0]> = {}) {
  const onSelect = vi.fn()
  const onClose = vi.fn()
  const anchorRef = createRef<HTMLButtonElement>()
  const view = render(
    <I18nProvider>
      <button aria-label="Chat history" ref={anchorRef} type="button" />
      <AgentChatHistoryPopover anchorRef={anchorRef} onClose={onClose} onSelect={onSelect} open sessions={sessions} {...props} />
    </I18nProvider>,
  )
  return { onSelect, onClose, anchorRef, ...view }
}

describe('chat history grouping', () => {
  it('groups by calendar week, newest first, with the reference labels', () => {
    const { sections } = buildChatHistory(sessions.slice().sort((x, y) => Date.parse(y.updatedAt) - Date.parse(x.updatedAt)), { now: NOW })
    expect(sections.map((section) => [section.label, section.count, section.sessions.map((item) => item.id).join('')])).toEqual([
      ['Today', undefined, 'gab'],
      ['Last week', undefined, 'c'],
      ['{count} weeks ago', 2, 'def'],
    ])
  })

  it('labels yesterday, this week and weeks further back', () => {
    const at = (days: number) => chatHistoryGroup(new Date(NOW.getTime() - days * DAY).toISOString(), NOW)
    expect(at(0).label).toBe('Today')
    expect(at(1).label).toBe('Yesterday')
    // Oct 11 (Sunday) belongs to the previous Monday-first week; Oct 12 is yesterday.
    expect(at(2).label).toBe('Last week')
    expect(at(8).label).toBe('Last week')
    expect(at(9).id).toBe('weeks-2')
    expect(at(16)).toMatchObject({ label: '{count} weeks ago', count: 3 })
    expect(at(120).label).toBe('Older')
    expect(chatHistoryGroup(NOW.toISOString(), new Date(2026, 9, 15, 9)).label).toBe('This week')
  })

  it('lists related chats first and does not repeat them in the time groups', () => {
    const { sections } = buildChatHistory(sessions, { now: NOW, related: { type: 'document', id: 'doc-1' } })
    expect(sections[0]).toMatchObject({ id: 'related', entity: 'document' })
    expect(sections[0].sessions.map((item) => item.id)).toEqual(['g'])
    expect(sections.flatMap((section) => section.sessions).filter((item) => item.id === 'g')).toHaveLength(1)
    expect(isChatRelatedTo(sessions[6], { type: 'document', id: 'doc-1' })).toBe(true)
    expect(isChatRelatedTo(sessions[6], { type: 'project', id: 'doc-1' })).toBe(false)
  })

  it('searches titles into one flat list ranked by where the query appears', () => {
    const result = buildChatHistory(sessions, { query: 'qa', now: NOW })
    expect(result.flat).toBe(true)
    expect(result.rows.map((item) => item.id)).toEqual(['a', 'b'])
    expect(buildChatHistory(sessions, { query: 'zzzq' }).rows).toEqual([])
  })
})

describe('AgentChatHistoryPopover', () => {
  beforeEach(() => {
    api.listAgentSessions.mockReset()
    api.listAgentSessions.mockResolvedValue([])
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(NOW)
  })
  afterEach(() => vi.useRealTimers())

  it('renders nothing while closed', () => {
    renderPopover({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('shows group headers, titles, short ages and the unread dot', () => {
    renderPopover()
    const dialog = screen.getByRole('dialog', { name: 'Chat history' })
    expect(screen.getByRole('combobox', { name: 'Chat history' })).toHaveFocus()
    expect(screen.getByPlaceholderText('Chat history')).toBeVisible()
    for (const heading of ['Today', 'Last week', '2 weeks ago']) expect(within(dialog).getByText(heading)).toBeInTheDocument()
    const row = (title: string) => screen.getByText(title).closest('[role=option]') as HTMLElement
    expect(within(row('QA Pill Measure Options')).getByText('27min')).toBeInTheDocument()
    expect(within(row('Summarize Document 123')).getByText('2min')).toBeInTheDocument()
    expect(within(row('Formatting Test with FLO-1')).getByText('4d')).toBeInTheDocument()
    expect(within(row('介绍 参照应用的 cycle')).getByText('13d')).toBeInTheDocument()
    expect(within(row('Triage agent')).getByText('2w')).toBeInTheDocument()
    // Only the chat updated after it was last read is unread.
    expect(row('create team: yes').querySelector('[class*="dot"]')).not.toBeNull()
    expect(row('Triage agent').querySelector('[class*="dot"]')).toBeNull()
    expect(row('QA Pill Measure Options').querySelector('[class*="dot"]')).toBeNull()
  })

  it('puts the page entity\'s chats under "Related to this …" and labels toolbar chats', () => {
    renderPopover({ related: { type: 'document', id: 'doc-1' }, toolbarSessionIds: ['g'] })
    const headings = screen.getAllByRole('group').map((group) => group.getAttribute('aria-label'))
    expect(headings[0]).toBe('Related to this document')
    expect(headings.filter((label) => label === 'Today')).toHaveLength(1)
    const row = screen.getByText('Summarize Document 123').closest('[role=option]') as HTMLElement
    expect(within(row).getByText('Toolbar')).toBeInTheDocument()
    expect(screen.getByText('QA Pill Measure Options').closest('[role=option]')).not.toHaveTextContent('Toolbar')
  })

  it('filters while typing, hides the headings and highlights the best match', async () => {
    const user = userEvent.setup()
    renderPopover()
    await user.type(screen.getByRole('combobox'), 'qa')
    expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual([expect.stringContaining('QA Pill'), expect.stringContaining('Choose Next QA')])
    expect(screen.queryByText('Today')).not.toBeInTheDocument()
    expect(screen.getAllByRole('option')[0]).toHaveAttribute('data-focused', 'true')
    expect(screen.getByRole('status')).toHaveTextContent('Showing 2 items')
    await user.clear(screen.getByRole('combobox'))
    expect(screen.getAllByRole('option').some((option) => option.getAttribute('data-focused') === 'true')).toBe(false)
  })

  it('shows an empty state when nothing matches', async () => {
    const user = userEvent.setup()
    renderPopover()
    await user.type(screen.getByRole('combobox'), 'zzzq')
    expect(screen.getByText('No matching history')).toBeInTheDocument()
    expect(screen.queryByRole('option')).not.toBeInTheDocument()
  })

  it('moves with the arrow keys (wrapping), Tab and End, tracking the active option', async () => {
    const user = userEvent.setup()
    renderPopover()
    const input = screen.getByRole('combobox')
    const focused = () => screen.getAllByRole('option').findIndex((option) => option.getAttribute('data-focused') === 'true')
    expect(focused()).toBe(-1)
    await user.keyboard('{ArrowDown}')
    expect(focused()).toBe(0)
    expect(input).toHaveAttribute('aria-activedescendant', screen.getAllByRole('option')[0].id)
    await user.keyboard('{ArrowDown}{Tab}')
    expect(focused()).toBe(2)
    await user.keyboard('{ArrowUp}{ArrowUp}{ArrowUp}')
    expect(focused()).toBe(sessions.length - 1)
    await user.keyboard('{Home}')
    expect(focused()).toBe(0)
    await user.keyboard('{End}')
    expect(focused()).toBe(sessions.length - 1)
    expect(input).toHaveFocus()
  })

  it('follows the mouse', async () => {
    const user = userEvent.setup()
    renderPopover()
    await user.hover(screen.getByText('Choose Next QA Action'))
    expect(screen.getByText('Choose Next QA Action').closest('[role=option]')).toHaveAttribute('data-focused', 'true')
  })

  it('Enter opens the highlighted chat without leaving the page', async () => {
    const user = userEvent.setup()
    const location = window.location.href
    const { onSelect, onClose } = renderPopover()
    await user.keyboard('{Enter}')
    expect(onSelect).not.toHaveBeenCalled()
    await user.keyboard('{ArrowDown}{ArrowDown}{Enter}')
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect.mock.calls[0][0].id).toBe('a')
    expect(onClose).toHaveBeenCalledWith()
    expect(window.location.href).toBe(location)
  })

  it('clicking a row selects that chat and closes', async () => {
    const user = userEvent.setup()
    const location = window.location.href
    const { onSelect, onClose } = renderPopover()
    await user.click(screen.getByText('Triage agent'))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'e' }))
    expect(onClose).toHaveBeenCalled()
    expect(window.location.href).toBe(location)
  })

  it('Escape inside the popover closes it and asks to restore focus; clicks outside close it, the button does not', async () => {
    const user = userEvent.setup()
    const { onClose, anchorRef } = renderPopover()
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledWith({ restoreFocus: true })
    onClose.mockClear()
    await user.click(anchorRef.current!)
    expect(onClose).not.toHaveBeenCalled()
    await user.click(document.body)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not list chats without a slug or loop builder chats', () => {
    renderPopover({ sessions: [session('x', 'Unsaved chat', MINUTE, { slugId: '' }), session('y', 'Loop builder', MINUTE, { loopIds: ['loop-1'] }), session('z', 'Saved chat', MINUTE)] })
    expect(screen.getAllByRole('option')).toHaveLength(1)
    expect(screen.getByText('Saved chat')).toBeInTheDocument()
  })

  it('asks the server for chats when the page loaded without them', async () => {
    api.listAgentSessions.mockResolvedValue([session('late', 'Loaded on open', 3 * HOUR)])
    renderPopover({ sessions: [] })
    expect(await screen.findByText('Loaded on open')).toBeInTheDocument()
    expect(screen.getByText('Loaded on open').closest('[role=option]')).toHaveTextContent('3h')
    expect(api.listAgentSessions).toHaveBeenCalledTimes(1)
  })
})
