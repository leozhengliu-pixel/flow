import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentMessage, AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(), createAgentSessionFeedback: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(), updateAgentMessageFeedback: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
const toasts = vi.hoisted(() => ({ toast: { info: vi.fn(), success: vi.fn(), error: vi.fn() } }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)
vi.mock('@/lib/toast', () => toasts)

import { AgentPage } from './agent-page'
import { AgentReplyActions, AgentUserMessageActions } from './agent-message-actions'

const question: AgentMessage = {
  id: 'reply', role: 'assistant', content: 'Which priority should the issue have?', createdAt: '2026-10-12T00:00:01Z',
  parts: [{ id: 'q1', type: 'question', status: 'pending', text: 'Which priority should the issue have?', question: { allowFreeText: true, options: [{ label: 'Urgent', value: '1' }, { label: 'High', value: '2' }, { label: 'Medium', value: '3' }] } }],
}
const chat = (messages: AgentMessage[]): AgentSession => ({
  id: 'session-q', slugId: 'q', userId: 'user-1', title: 'Priority', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [{ id: 'ask', role: 'user', content: 'Create it, but ask me the priority', createdAt: '2026-10-12T00:00:00Z' }, ...messages],
  createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:01Z',
})
const renderPage = (session: AgentSession) => render(<I18nProvider><AgentPage chatSlug="q" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)

describe('agent reply and message actions', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    Object.values(toasts.toast).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test' })
    api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
  })

  it('rates a reply: helpful toggles to a solid thumb labelled Remove feedback, and is saved', async () => {
    const user = userEvent.setup()
    api.updateAgentMessageFeedback.mockImplementation(async () => chat([]))
    const onSessionChange = vi.fn()
    render(<I18nProvider><AgentReplyActions content="Answer" message={{ id: 'reply' }} sessionId="session-q" onSessionChange={onSessionChange}/></I18nProvider>)
    expect(screen.getAllByRole('button').map(button => button.getAttribute('aria-label'))).toEqual(['This was helpful', 'Leave feedback…', 'Copy message'])
    await user.click(screen.getByRole('button', { name: 'This was helpful' }))
    const pressed = screen.getByRole('button', { name: 'Remove feedback' })
    expect(pressed).toHaveAttribute('aria-pressed', 'true')
    expect(pressed.querySelector('[data-flow-glyph="thumbsUpFilled"]')).not.toBeNull()
    await waitFor(() => expect(onSessionChange).toHaveBeenCalled())
    expect(api.updateAgentMessageFeedback).toHaveBeenCalledWith('session-q', 'reply', { rating: 'positive' })
    await user.click(pressed)
    expect(api.updateAgentMessageFeedback).toHaveBeenLastCalledWith('session-q', 'reply', { rating: '' })
    expect(screen.getByRole('button', { name: 'This was helpful' })).toHaveAttribute('aria-pressed', 'false')
  })

  it('keeps the previous rating when saving fails', async () => {
    const user = userEvent.setup()
    api.updateAgentMessageFeedback.mockRejectedValue(new Error('Offline'))
    render(<I18nProvider><AgentReplyActions content="Answer" message={{ id: 'reply' }} sessionId="session-q"/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'This was helpful' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'This was helpful' })).toHaveAttribute('aria-pressed', 'false'))
    expect(toasts.toast.error).toHaveBeenCalledWith('Offline')
  })

  it('copies a message with the reference app\'s "Copied message" notice and offers Edit on your own messages', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const onEdit = vi.fn()
    render(<I18nProvider><AgentUserMessageActions content="My question" onEdit={onEdit}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Copy message' }))
    expect(writeText).toHaveBeenCalledWith('My question')
    await waitFor(() => expect(toasts.toast.info).toHaveBeenCalledWith('Copied message', { description: 'Message copied to clipboard' }))
    await user.click(screen.getByRole('button', { name: 'Edit message' }))
    expect(onEdit).toHaveBeenCalled()
  })

  it('shows a question\'s options as pills, switches the composer to "Something else…" and sends the pick as the answer', async () => {
    const user = userEvent.setup()
    streams.streamAgentSessionMessage.mockImplementation(() => new Promise(() => undefined))
    renderPage(chat([question]))
    expect(await screen.findByText('Which priority should the issue have?')).toBeVisible()
    const group = screen.getByRole('group', { name: 'Choose an option' })
    expect(within(group).getAllByRole('button').map(button => button.textContent)).toEqual(['Urgent', 'High', 'Medium'])
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Send a message to Flow AI' })).toHaveAttribute('data-placeholder', 'Something else…'))
    await user.click(within(group).getByRole('button', { name: 'High' }))
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledWith('session-q', 'High', expect.any(Function), expect.any(AbortSignal), expect.objectContaining({ answer: { partId: 'q1', value: '2' } }))
    // The options collapse into the chosen one (with its check) straight away; the pick is not shown as a message bubble.
    const chosen = screen.getByRole('group', { name: 'Choose an option' })
    expect(within(chosen).getAllByRole('button').map(button => button.textContent)).toEqual(['High'])
    expect(within(chosen).getByRole('button', { name: 'High' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByText('High', { selector: '[data-message-id] p' })).not.toBeInTheDocument()
  })

  it('shows only the chosen option for an answered question and hides the answer message', async () => {
    const answered: AgentMessage = { ...question, parts: [{ ...question.parts![0], status: 'completed', question: { ...question.parts![0].question!, selected: '2' } }] }
    renderPage(chat([answered, { id: 'pick', role: 'user', content: 'High', answerTo: 'q1', createdAt: '2026-10-12T00:00:02Z' }, { id: 'done', role: 'assistant', content: 'Created it with High priority.', createdAt: '2026-10-12T00:00:03Z' }]))
    const group = await screen.findByRole('group', { name: 'Choose an option' })
    expect(within(group).getAllByRole('button').map(button => button.textContent)).toEqual(['High'])
    expect(within(group).getByRole('button', { name: 'High' })).toBeDisabled()
    expect(document.querySelector('[data-message-id="pick"]')).toBeNull()
  })

  it('hides the edited message and the rest of the chat while editing, under the reference app\'s "Editing message" banner', async () => {
    const user = userEvent.setup()
    renderPage(chat([{ id: 'answer', role: 'assistant', content: 'An answer', createdAt: '2026-10-12T00:00:01Z' }]))
    await user.click(await screen.findByRole('button', { name: 'Edit message' }))
    expect(screen.getByText('Editing message')).toBeVisible()
    expect(screen.queryByText('An answer')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel editing' }))
    expect(await screen.findByText('An answer')).toBeVisible()
  })

  it('lists Rename…, Copy as markdown, Leave feedback… and Delete in the chat menu and sends chat feedback', async () => {
    const user = userEvent.setup()
    api.createAgentSessionFeedback.mockResolvedValue(chat([]))
    renderPage(chat([{ id: 'answer', role: 'assistant', content: 'An answer', createdAt: '2026-10-12T00:00:01Z' }]))
    await user.click(await screen.findByRole('button', { name: 'Chat options' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map(item => item.textContent)).toEqual(['Rename…ShiftR', 'Copy as markdown', 'Leave feedback…', 'DeleteCtrlDelete'])
    await user.click(within(menu).getByRole('menuitem', { name: 'Leave feedback…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Leave feedback' })
    await user.type(within(dialog).getByRole('textbox', { name: 'Tell us more' }), 'Great chat')
    await user.click(within(dialog).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.createAgentSessionFeedback).toHaveBeenCalledWith('session-q', 'Great chat'))
  })
})
