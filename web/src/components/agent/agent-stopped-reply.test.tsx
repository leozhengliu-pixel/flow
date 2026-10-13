import { act, render, screen, waitFor, within } from '@testing-library/react'
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
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'
import { AgentChatPanel } from './agent-chat-panel'
import { isBlankStoppedReply, isStoppedAgentReply, markStoppedAgentReply, settleStoppedAgentReply } from './agent-stopped-reply'

const asked = { id: 'ask', role: 'user' as const, content: 'Create it, but ask me the priority', createdAt: '2026-10-12T00:00:00Z' }
const question: AgentMessage = {
  id: 'reply', role: 'assistant', content: 'Which priority should the issue have?', createdAt: '2026-10-12T00:00:01Z',
  parts: [{ id: 'q1', type: 'question', status: 'pending', text: 'Which priority should the issue have?', question: { allowFreeText: true, options: [{ label: 'Urgent', value: '1' }, { label: 'High', value: '2' }] } }],
}
const answer: AgentMessage = { id: 'done', role: 'assistant', content: 'Created it with High priority.', createdAt: '2026-10-12T00:00:03Z' }
const work: NonNullable<AgentMessage['parts']> = [{ id: 'work-1', type: 'toolCall', status: 'completed', toolCall: { id: 'work-1', name: 'search_issues', arguments: { query: 'export' }, status: 'completed' } }]
const chat = (messages: AgentMessage[]): AgentSession => ({
  id: 'session-q', slugId: 'q', userId: 'user-1', title: 'Priority', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [asked, ...messages], createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:01Z',
})
const renderPage = (session: AgentSession) => render(<I18nProvider><AgentPage chatSlug="q" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
const replyRows = () => document.querySelectorAll('[data-agent-reply-actions]')

beforeEach(() => {
  Object.values(api).forEach(mock => mock.mockReset())
  Object.values(streams).forEach(mock => mock.mockReset())
  api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test' })
  api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
  api.stopAgentSession.mockResolvedValue(undefined)
  localStorage.clear()
})

describe('feedback row under agent messages', () => {
  it('shows no 👍👎 under a question the agent asks while it waits for an answer, on the agent page', async () => {
    renderPage(chat([question]))
    expect(await screen.findByText('Which priority should the issue have?')).toBeVisible()
    expect(replyRows()).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'This was helpful' })).not.toBeInTheDocument()
  })

  it('keeps the row under the answer that follows an answered question, and none under the question', async () => {
    const answered: AgentMessage = { ...question, parts: [{ ...question.parts![0], status: 'completed', question: { ...question.parts![0].question!, selected: '2' } }] }
    renderPage(chat([answered, { id: 'pick', role: 'user', content: 'High', answerTo: 'q1', createdAt: '2026-10-12T00:00:02Z' }, answer]))
    expect(await screen.findByText('Created it with High priority.')).toBeVisible()
    expect(replyRows()).toHaveLength(1)
    expect(document.querySelector('[data-message-id="done"] [data-agent-reply-actions]')).not.toBeNull()
    expect(document.querySelector('[data-message-id="reply"] [data-agent-reply-actions]')).toBeNull()
  })

  it('does the same in the floating chat', async () => {
    const answered: AgentMessage = { ...question, parts: [{ ...question.parts![0], status: 'completed', question: { ...question.parts![0].question!, selected: '2' } }] }
    const toolbar = (messages: AgentMessage[]) => <I18nProvider><AgentChatPanel initialSession={{ ...chat(messages), location: 'toolbar' }} issues={[]} onClose={vi.fn()} open/></I18nProvider>
    const { unmount } = render(toolbar([question]))
    expect(await screen.findByText('Which priority should the issue have?')).toBeVisible()
    expect(replyRows()).toHaveLength(0)
    unmount()
    render(toolbar([answered, { id: 'pick', role: 'user', content: 'High', answerTo: 'q1', createdAt: '2026-10-12T00:00:02Z' }, answer]))
    expect(await screen.findByText('Created it with High priority.')).toBeVisible()
    expect(replyRows()).toHaveLength(1)
  })

  it('also hides the row when the question comes with an introduction', async () => {
    const intro: AgentMessage = { ...question, content: 'Quick check first.\n\nWhich priority should the issue have?', parts: [{ id: 'intro', type: 'text', text: 'Quick check first.' }, ...question.parts!] }
    renderPage(chat([intro]))
    expect(await screen.findByText('Quick check first.')).toBeVisible()
    expect(replyRows()).toHaveLength(0)
  })
})

describe('a stopped reply', () => {
  const stoppedMarker = { id: 'reply-stopped', type: 'error' as const, status: 'error' as const, text: 'Generation stopped' }
  const partial: AgentMessage = { id: 'reply', role: 'assistant', content: 'Backlog: Work is', createdAt: '2026-10-12T00:00:01Z', parts: [...work, { id: 'reply-text', type: 'text', text: 'Backlog: Work is' }, stoppedMarker] }

  it('keeps only the work: no partial answer, no "Generation stopped" row, no Retry, no rating', async () => {
    renderPage(chat([partial]))
    const conversation = await screen.findByRole('group', { name: 'Agent conversation' })
    expect(within(conversation).getByText('Create it, but ask me the priority')).toBeVisible()
    expect(within(conversation).queryByText(/Backlog/)).not.toBeInTheDocument()
    expect(within(conversation).queryByText('Generation stopped')).not.toBeInTheDocument()
    expect(within(conversation).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(replyRows()).toHaveLength(0)
    // The work the agent had done stays, folded.
    expect(conversation.querySelector('details')).not.toBeNull()
  })

  it('leaves only your message when the reply was stopped before it did anything', async () => {
    renderPage(chat([{ id: 'reply', role: 'assistant', content: '', createdAt: '2026-10-12T00:00:01Z', parts: [stoppedMarker] }]))
    const conversation = await screen.findByRole('group', { name: 'Agent conversation' })
    expect(within(conversation).getByText('Create it, but ask me the priority')).toBeVisible()
    expect(document.querySelector('[data-message-id="reply"]')).toBeNull()
    expect(within(conversation).queryByText('Generation stopped')).not.toBeInTheDocument()
    expect(within(conversation).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('stops a streaming reply on the page: the message stays, the partial answer goes, the composer stays empty', async () => {
    const user = userEvent.setup()
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => {
      const session: AgentSession = { ...chat([]), id: 'session-live', messages: [{ id: 'user-message', role: 'user', content: 'Explain two plus two', createdAt: '2026-10-12T00:00:00Z' }] }
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      onEvent({ type: 'text.delta', messageId: 'assistant-message', delta: 'Two plus', part: { id: 'text', type: 'text', text: 'Two plus', status: 'running' } })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    await user.type(editor, 'Explain two plus two')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    expect(await screen.findByText('Two plus')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
    await waitFor(() => expect(screen.queryByText('Two plus')).not.toBeInTheDocument())
    const conversation = screen.getByRole('group', { name: 'Agent conversation' })
    expect(within(conversation).getByText('Explain two plus two')).toBeVisible()
    expect(within(conversation).queryByText('Generation stopped')).not.toBeInTheDocument()
    expect(within(conversation).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(replyRows()).toHaveLength(0)
    // The prompt is not put back in the composer.
    expect(editor).toHaveTextContent('')
  })

  it('stops a reply in the floating chat the same way', async () => {
    const user = userEvent.setup()
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => {
      const session: AgentSession = { ...chat([]), id: 'session-live', location: 'toolbar', messages: [{ id: 'user-message', role: 'user', content: 'Explain two plus two', createdAt: '2026-10-12T00:00:00Z' }] }
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      onEvent({ type: 'text.delta', messageId: 'assistant-message', delta: 'Two plus' })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Explain two plus two')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(await screen.findByText('Two plus')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
    await waitFor(() => expect(screen.queryByText('Two plus')).not.toBeInTheDocument())
    expect(screen.getByText('Explain two plus two')).toBeVisible()
    expect(screen.queryByText('Generation stopped')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(replyRows()).toHaveLength(0)
    expect(input).toHaveValue('')
  })

  it('settles stopped replies and marks the trailing one when a view ends the stream itself', () => {
    expect(isStoppedAgentReply(partial)).toBe(true)
    expect(isStoppedAgentReply(answer)).toBe(false)
    expect(settleStoppedAgentReply(partial)).toMatchObject({ content: '', parts: work })
    expect(settleStoppedAgentReply(answer)).toBe(answer)
    expect(isBlankStoppedReply({ ...partial, parts: [stoppedMarker] })).toBe(true)
    expect(isBlankStoppedReply(partial)).toBe(false)
    const marked = markStoppedAgentReply([asked, { id: 'streamed', role: 'assistant', content: 'Two plus', createdAt: '' }], [{ id: 'live', type: 'toolCall', status: 'running' }])
    expect(marked.at(-1)).toMatchObject({ id: 'streamed', content: 'Two plus', parts: [{ id: 'live', status: 'error' }, { type: 'error', text: 'Generation stopped' }] })
    expect(isStoppedAgentReply(marked.at(-1))).toBe(true)
    // Stopped before any text: the marker is added as its own reply after your message.
    const bare = markStoppedAgentReply([asked])
    expect(bare).toHaveLength(2)
    expect(isBlankStoppedReply(bare[1])).toBe(true)
    // Marking twice does not duplicate the marker.
    expect(markStoppedAgentReply(marked).at(-1)?.parts?.filter(part => part.type === 'error')).toHaveLength(1)
  })

  it('still renders the thinking state while a reply is running', async () => {
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => {
      onEvent({ type: 'session.started', session: { ...chat([]), id: 'session-live', messages: [{ id: 'user-message', role: 'user', content: 'Think', createdAt: '2026-10-12T00:00:00Z' }] }, messageId: 'assistant-message' })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow AI' }), 'Think')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(screen.getByText('Thinking…')).toBeVisible())
    await act(async () => { await user.click(screen.getByRole('button', { name: 'Stop responding' })) })
    await waitFor(() => expect(screen.queryByText('Thinking…')).not.toBeInTheDocument())
    expect(document.querySelector('[data-message-id="user-message"]')).not.toBeNull()
  })
})
