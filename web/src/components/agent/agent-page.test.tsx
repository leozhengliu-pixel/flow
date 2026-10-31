import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(), resolveAgentApproval: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'
import { applyAgentStreamEvent } from './agent-stream-state'
import { clearLiveAgentSession, setLiveAgentSession } from './agent-live-sessions'
import { pushAgentSession } from './use-agent-reply-follow'
import { finishedReplySession, runningReplySession } from '@/test/agent-running-reply'

describe('agent page composer', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: false, model: '' })
    api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
    api.stopAgentSession.mockResolvedValue(undefined)
  })

  it('accepts draft input even when the Agent backend is not configured', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    const editor = await screen.findByRole('textbox', { name: 'Send a message to Flow AI' })
    expect(editor).toHaveAttribute('contenteditable', 'true')
    expect(screen.queryByText('Flow Agent is not configured')).not.toBeInTheDocument()
    expect(screen.queryByText('Get started with some examples')).not.toBeInTheDocument()
    await user.click(editor)
    await user.type(editor, 'Draft a project plan')
    expect(editor).toHaveTextContent('Draft a project plan')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Submit comment' })).toBeDisabled())
  })

  it('shows Flow example cards in an empty Agent state', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    expect(await screen.findByRole('textbox', { name: 'Send a message to Flow AI' })).toHaveAttribute('data-placeholder', 'Ask Flow…')
    expect(screen.getByRole('button', { name: 'Skills' })).toBeVisible()
    expect(screen.getByText('Get started with some examples')).toBeVisible()
    expect(screen.getByText('Summarize the latest project updates')).toBeVisible()
    expect(document.querySelector('svg.emptyGraphic, [class*="emptyGraphic"]')).toBeNull()
  })

  it('renders text, reasoning, and tool deltas while a session streams', async () => {
    const user = userEvent.setup()
    const navigate = vi.fn()
    const sessionChange = vi.fn()
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      const session: AgentSession = { id: 'session-1', slugId: 'streamed-chat', userId: 'user-1', title: 'Streamed chat', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [{ id: 'user-message', role: 'user', content: 'Hello', createdAt: '2026-08-31T00:00:00Z' }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      onEvent({ type: 'reasoning.delta', messageId: 'assistant-message', delta: 'Checking workspace', part: { id: 'reasoning', type: 'reasoning', text: 'Checking workspace', status: 'running' } })
      onEvent({ type: 'tool.started', messageId: 'assistant-message', part: { id: 'tool', type: 'toolCall', status: 'running', toolCall: { id: 'call-1', name: 'list_issues', arguments: { query: 'bug' }, status: 'running' } } })
      onEvent({ type: 'tool.completed', messageId: 'assistant-message', part: { id: 'tool', type: 'toolCall', status: 'completed', toolCall: { id: 'call-1', name: 'list_issues', arguments: { query: 'bug' }, result: { items: [] }, status: 'completed' } } })
      onEvent({ type: 'text.delta', messageId: 'assistant-message', delta: 'No bugs found.', part: { id: 'text', type: 'text', text: 'No bugs found.', status: 'completed' } })
      const completed: AgentSession = { ...session, messages: [...session.messages, { id: 'assistant-message', role: 'assistant', content: 'No bugs found.', parts: [], createdAt: '2026-08-31T00:00:01Z' }] }
      onEvent({ type: 'session.completed', session: completed })
      return completed
    })
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={navigate} onOpenSidebar={vi.fn()} onSessionChange={sessionChange}/></I18nProvider>)
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    await user.type(editor, 'Hello')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Submit comment' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/workspace/agent/streamed-chat'))
    expect(streams.streamNewAgentSession).toHaveBeenCalled()
    // The finished chat is saved into the workspace data directly: no full workspace reload.
    expect(sessionChange).toHaveBeenCalledWith('session-1', expect.objectContaining({ id: 'session-1', messages: expect.arrayContaining([expect.objectContaining({ content: 'No bugs found.' })]) }))
  })

  it('shows the sent message and a working state before the server answers', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    let release!: (event: unknown) => void
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => new Promise((resolve, reject) => {
      release = onEvent
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
      void resolve
    }))
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    await user.type(editor, 'Plan the launch')
    expect(screen.getByRole('button', { name: 'Submit comment' })).toHaveAttribute('data-state', 'ready')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    // Nothing has come back from the server yet.
    const conversation = await screen.findByRole('group', { name: 'Agent conversation' })
    expect(within(conversation).getByText('Plan the launch')).toBeVisible()
    expect(within(conversation).getByText('Thinking…')).toBeVisible()
    expect(editor).toHaveTextContent('')
    expect(screen.getByRole('button', { name: 'Stop responding' })).toHaveAttribute('data-state', 'working')
    // When the stream starts, the server's copy replaces the optimistic one without duplicating it.
    const session: AgentSession = { id: 'session-2', slugId: 'launch', userId: 'user-1', title: 'Plan the launch', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [{ id: 'user-message', role: 'user', content: 'Plan the launch', createdAt: '2026-08-31T00:00:00Z' }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
    act(() => release({ type: 'session.started', session, messageId: 'assistant-message' }))
    await waitFor(() => expect(document.querySelector('[data-message-id="user-message"]')).not.toBeNull())
    expect(within(screen.getByRole('group', { name: 'Agent conversation' })).getAllByText('Plan the launch')).toHaveLength(1)
    expect(screen.getByText('Thinking…')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
  })

  it('keeps the accent send button for an empty composer', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Submit comment' })).toHaveAttribute('data-state', 'ready'))
  })

  it('shows a thinking state before the provider sends its first delta', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => {
      const session: AgentSession = { id: 'session-waiting', slugId: 'waiting', userId: 'user-1', title: 'Waiting', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [{ id: 'user-message', role: 'user', content: 'Think first', createdAt: '2026-08-31T00:00:00Z' }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    await user.type(editor, 'Think first')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(screen.getByText('Thinking…')).toBeVisible())
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
  })

  it('keeps the working state after the new chat opens and the server copy still ends with the question', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    const session: AgentSession = { id: 'session-opened', slugId: 'opened', userId: 'user-1', title: 'Opened', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [{ id: 'user-message', role: 'user', content: 'Think first', createdAt: new Date().toISOString() }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
    // The server has not stored an assistant message yet when the opened chat is re-read.
    api.getAgentSession.mockResolvedValue(session)
    let rerenderWithSlug: () => void = () => undefined
    const onNavigate = vi.fn(() => rerenderWithSlug())
    streams.streamNewAgentSession.mockImplementation((_input, onEvent, signal: AbortSignal) => {
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    const data = makeBootstrap({ agentSessions: [session], agentSkills: [] })
    const page = (chatSlug?: string) => <I18nProvider><AgentPage chatSlug={chatSlug} data={data} onNavigate={onNavigate} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>
    const view = render(page())
    rerenderWithSlug = () => view.rerender(page('opened'))
    const user = userEvent.setup()
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow AI' }), 'Think first')
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(api.getAgentSession).toHaveBeenCalledWith('session-opened'))
    await waitFor(() => expect(screen.getByText('Thinking…')).toBeVisible())
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
  })

  it('reduces incremental stream events into one assistant message', () => {
    const session = { id: 'session', slugId: 'chat', userId: 'user', title: 'Chat', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [], createdAt: '', updatedAt: '' } as AgentSession
    const started = applyAgentStreamEvent(undefined, { type: 'session.started', session, messageId: 'message' })!
    const text = applyAgentStreamEvent(started, { type: 'text.delta', messageId: 'message', delta: 'Hello', part: { id: 'text', type: 'text', text: 'Hello', status: 'running' } })!
    const finished = applyAgentStreamEvent(text, { type: 'text.delta', messageId: 'message', delta: ' world', part: { id: 'text', type: 'text', text: 'Hello world', status: 'completed' } })!
    expect(finished.messages[0]).toMatchObject({ content: 'Hello world', parts: [{ id: 'text', text: 'Hello world' }] })
  })

  it('shows a reply that finished while the page was away when the chat is reopened', async () => {
    const asked = { id: 'q', role: 'user' as const, content: 'Summarize the project', createdAt: '2026-08-31T00:00:00Z' }
    const stale: AgentSession = { id: 'session-away', slugId: 'away', userId: 'user-1', title: 'Summary', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [asked], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
    api.getAgentSession.mockResolvedValue({ ...stale, messages: [asked, { id: 'a', role: 'assistant', content: 'The project is on track.', createdAt: '2026-08-31T00:01:00Z' }] })
    render(<I18nProvider><AgentPage chatSlug="away" data={makeBootstrap({ agentSessions: [stale], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(screen.getByText('The project is on track.')).toBeVisible())
    expect(api.getAgentSession).toHaveBeenCalledWith('session-away')
  })

  it('keeps showing a reply that is still streaming when the chat is reopened, and stops it on the server', async () => {
    api.stopAgentSession.mockResolvedValue(undefined)
    const liveSession: AgentSession = { id: 'session-live', slugId: 'live', userId: 'user-1', title: 'Live', favorite: false, location: 'page', issueIds: [], skillIds: [], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z',
      messages: [{ id: 'q', role: 'user', content: 'Explain cycles', createdAt: '2026-08-31T00:00:00Z' }, { id: 'a', role: 'assistant', content: '', createdAt: '2026-08-31T00:00:01Z', parts: [{ id: 't', type: 'text', text: 'Cycles are time boxes', status: 'running' }] }] }
    setLiveAgentSession(liveSession)
    try {
      // The workspace copy predates the reply.
      render(<I18nProvider><AgentPage chatSlug="live" data={makeBootstrap({ agentSessions: [{ ...liveSession, messages: liveSession.messages.slice(0, 1) }], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      expect(await screen.findByText(/Cycles are time boxes/)).toBeVisible()
      await userEvent.click(screen.getByRole('button', { name: 'Stop responding' }))
      expect(api.stopAgentSession).toHaveBeenCalledWith('session-live')
    } finally {
      clearLiveAgentSession('session-live')
    }
  })

  it('keeps the work group running between steps of a reply that is still streaming', async () => {
    const liveSession: AgentSession = { id: 'session-between', slugId: 'between', userId: 'user-1', title: 'Between', favorite: false, location: 'page', issueIds: [], skillIds: [], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z',
      messages: [{ id: 'q', role: 'user', content: 'Check the project', createdAt: '2026-08-31T00:00:00Z' }, { id: 'a', role: 'assistant', content: '', createdAt: '2026-08-31T00:00:01Z', parts: [{ id: 's', type: 'step', title: 'Checking the project', text: 'Reading it first', status: 'completed' }, { id: 't', type: 'toolCall', status: 'completed', toolCall: { id: 'call', name: 'get_project', status: 'completed', arguments: {} } }] }] }
    setLiveAgentSession(liveSession)
    try {
      render(<I18nProvider><AgentPage chatSlug="between" data={makeBootstrap({ agentSessions: [{ ...liveSession, messages: liveSession.messages.slice(0, 1) }], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      // Like the reference app, the header says Thinking… while the model works out its next step after the tools finished.
      expect(await screen.findByText('Thinking…', { selector: 'summary span' })).toBeVisible()
      expect(screen.queryByText('Work completed')).not.toBeInTheDocument()
    } finally {
      clearLiveAgentSession('session-between')
    }
  })

  it('shows a working state for a question still waiting on the server and picks up the reply', async () => {
    const asked = { id: 'q', role: 'user' as const, content: 'Any blockers?', createdAt: new Date().toISOString() }
    const waiting: AgentSession = { id: 'session-wait', slugId: 'wait', userId: 'user-1', title: 'Blockers', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [asked], createdAt: asked.createdAt, updatedAt: asked.createdAt }
    api.getAgentSession.mockResolvedValue(waiting)
    const onSessionChange = vi.fn()
    render(<I18nProvider><AgentPage chatSlug="wait" data={makeBootstrap({ agentSessions: [waiting], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={onSessionChange}/></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Thinking…')).toBeVisible())
    api.getAgentSession.mockResolvedValue({ ...waiting, messages: [asked, { id: 'a', role: 'assistant', content: 'No blockers.', createdAt: new Date().toISOString() }] })
    await waitFor(() => expect(screen.getByText('No blockers.')).toBeVisible(), { timeout: 4000 })
    expect(onSessionChange).toHaveBeenCalledWith('session-wait', expect.objectContaining({ id: 'session-wait' }))
  })

  it('shows a reply still running on the server after a reload, with its partial text and an answerable approval', async () => {
    const user = userEvent.setup()
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    const asked = { id: 'q', role: 'user' as const, content: 'Delete the branch', createdAt: '2026-08-31T00:00:00Z' }
    const call = { id: 'call', name: 'external_code__delete_branch', title: 'Delete branch', arguments: {}, status: 'pending' as const, approvalId: 'approval-1' }
    const streaming = { id: 'a', role: 'assistant' as const, content: 'Checking the branch first.', status: 'streaming' as const, createdAt: '2026-08-31T00:00:01Z', updatedAt: '2026-08-31T00:00:02Z', parts: [
      { id: 'text', type: 'text' as const, text: 'Checking the branch first.', status: 'running' as const },
      { id: 'tool', type: 'toolCall' as const, status: 'pending' as const, toolCall: call },
    ] }
    const running: AgentSession = { id: 'session-live', slugId: 'live', userId: 'user-1', title: 'Branch', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [asked, streaming], createdAt: asked.createdAt, updatedAt: asked.createdAt }
    api.getAgentSession.mockResolvedValue(running)
    api.resolveAgentApproval.mockResolvedValue({ approvalId: 'approval-1', decision: 'approved' })
    render(<I18nProvider><AgentPage chatSlug="live" data={makeBootstrap({ agentSessions: [running], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    expect(await screen.findByText('Checking the branch first.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Stop responding' })).toBeVisible()
    const approve = await screen.findByRole('button', { name: 'Approve tool' })
    await user.click(approve)
    expect(api.resolveAgentApproval).toHaveBeenCalledWith('session-live', 'approval-1', 'approve')
    // Answered: the card stays off until the saved reply shows the approval resolved.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Approve tool' })).toBeDisabled())
    api.getAgentSession.mockResolvedValue({ ...running, messages: [asked, { id: 'a', role: 'assistant', content: 'Deleted the branch.', createdAt: '2026-08-31T00:00:05Z', parts: [
      { id: 'tool', type: 'toolCall', status: 'completed', toolCall: { ...call, status: 'completed', result: { ok: true } } },
      { id: 'text', type: 'text', text: 'Deleted the branch.', status: 'completed' },
    ] }] })
    await waitFor(() => expect(screen.getByText('Deleted the branch.')).toBeVisible(), { timeout: 4000 })
    expect(screen.queryByRole('button', { name: 'Approve tool' })).not.toBeInTheDocument()
  })

  it('follows a running reply answered from another chat surface, and Stop ends it on the server', async () => {
    const user = userEvent.setup()
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    api.stopAgentSession.mockResolvedValue(undefined)
    const running = runningReplySession({ location: 'page' })
    api.getAgentSession.mockResolvedValue(running)
    const onSessionChange = vi.fn()
    render(<I18nProvider><AgentPage chatSlug="live" data={makeBootstrap({ agentSessions: [running], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={onSessionChange}/></I18nProvider>)
    expect(await screen.findByRole('button', { name: 'Approve tool' })).toBeEnabled()
    expect(screen.getByRole('region', { name: 'Local tools requests information' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
    expect(api.stopAgentSession).toHaveBeenCalledWith('session-live')
    // The floating chat answered the approval and the question; the realtime push shows it here at once.
    const finished = finishedReplySession(running)
    act(() => pushAgentSession(finished))
    expect(await screen.findByText('Deleted the branch.')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Approve tool' })).not.toBeInTheDocument()
    expect(screen.getByText('Response sent')).toBeVisible()
    expect(onSessionChange).toHaveBeenCalledWith('session-live', finished)
  })

  it('renders persisted reasoning, tool, text, and error parts', async () => {
    const session: AgentSession = {
      id: 'session-parts', slugId: 'parts', userId: 'user-1', title: 'Tool run', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [{ id: 'assistant', role: 'assistant', content: 'Finished', createdAt: '2026-08-31T00:00:00Z', parts: [
        { id: 'msg_narration_0', type: 'reasoning', text: 'Checked workspace state', status: 'completed' },
        { id: 'tool', type: 'toolCall', status: 'completed', toolCall: { id: 'call', name: 'list_issues', arguments: { query: 'bug' }, result: { items: [] }, status: 'completed' } },
        { id: 'text', type: 'text', text: 'Finished', status: 'completed' },
        { id: 'error', type: 'error', text: 'Partial warning', status: 'error' },
      ] }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z',
    }
    render(<I18nProvider><AgentPage chatSlug="parts" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await userEvent.click(screen.getByText('Work completed'))
    expect(screen.getByText('Looked at issues')).toBeVisible()
    expect(screen.getByText('Checked workspace state')).toBeVisible()
    expect(screen.getByText('Finished')).toBeVisible()
    expect(screen.getByRole('alert')).toHaveTextContent('Partial warning')
  })

  it('keeps the conversation visible while editing a user message', async () => {
    const session: AgentSession = {
      id: 'session-edit', slugId: 'edit', userId: 'user-1', title: 'Edit chat', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [
        { id: 'user', role: 'user', content: 'Original question', createdAt: '2026-08-31T00:00:00Z' },
        { id: 'assistant', role: 'assistant', content: 'Original answer', createdAt: '2026-08-31T00:00:01Z' },
      ], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
    }
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage chatSlug="edit" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Edit message' }))
    // Like the reference app, the edited message and everything after it leave the conversation while the composer holds it.
    expect(screen.queryByText('Original answer')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Send a message to Flow AI' })).toHaveTextContent('Original question')
    expect(screen.getByText('Editing message')).toBeVisible()
  })

  it('groups chat history and supports keyboard navigation to a new chat', async () => {
    const session: AgentSession = {
      id: 'session-history', slugId: 'history', userId: 'user-1', title: 'Workspace review', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [],
      createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
    }
    const navigate = vi.fn()
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage chatSlug="history" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={navigate} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Switch agent chat' }))
    expect(screen.getByRole('group', { name: 'Today' })).toBeVisible()
    await user.hover(screen.getByRole('option', { name: 'New chat' }))
    await user.keyboard('{Enter}')
    expect(navigate).toHaveBeenCalledWith('/workspace/agent')
  })

  it('shows phone history with calendar-week groups and a searchable chat list', async () => {
    vi.setSystemTime(new Date('2026-10-30T12:00:00'))
    const media = vi.spyOn(window, 'matchMedia').mockImplementation(query => ({
      matches: query === '(max-width: 767px)', media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: () => false,
    }))
    try {
      const sessions: AgentSession[] = [1, 3, 10, 17, 90].map((days, index) => ({
        id: `phone-${index}`, slugId: `phone-${index}`, userId: 'user-1', title: `[mobile] Chat ${index}`, favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [],
        createdAt: new Date(Date.now() - days * 86400000).toISOString(), updatedAt: new Date(Date.now() - days * 86400000).toISOString(),
      }))
      const navigate = vi.fn()
      const user = userEvent.setup()
      render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: sessions, agentSkills: [] })} onNavigate={navigate} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      await user.click(screen.getByRole('button', { name: 'Switch agent chat' }))
      for (const name of ['Yesterday', 'This week', 'Last week', '2 weeks ago', 'Older']) expect(screen.getByRole('group', { name })).toBeVisible()
      expect(screen.queryByRole('option', { name: 'New chat' })).not.toBeInTheDocument()
      expect(screen.getAllByRole('option').every(option => option.getAttribute('aria-selected') === 'false')).toBe(true)
      await user.type(screen.getByPlaceholderText('Chat history'), 'Chat 3')
      expect(screen.getAllByRole('option')).toHaveLength(1)
      expect(screen.queryByRole('group', { name: '2 weeks ago' })).not.toBeInTheDocument()
      await user.keyboard('{Enter}')
      expect(navigate).toHaveBeenCalledWith('/workspace/agent/phone-3')
    } finally {
      media.mockRestore()
      vi.useRealTimers()
    }
  })

  it('renders assistant markdown as rich content', async () => {
    const session: AgentSession = {
      id: 'session-markdown', slugId: 'markdown', userId: 'user-1', title: 'Markdown', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [{ id: 'assistant', role: 'assistant', content: '## Plan\n\n1. **Build** the API\n2. `Verify` the UI', createdAt: '2026-08-31T00:00:00Z' }],
      createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z',
    }
    render(<I18nProvider><AgentPage chatSlug="markdown" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    expect(await screen.findByRole('heading', { name: 'Plan' })).toBeVisible()
    expect(screen.getByRole('list')).toBeVisible()
    expect(screen.getByText('Build').tagName).toBe('STRONG')
    expect(screen.getByText('Verify').tagName).toBe('CODE')
  })
  it('renders the reference app answer chrome and sends a suggestion chip as the next message', async () => {
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    streams.streamAgentSessionMessage.mockResolvedValue(undefined)
    const session: AgentSession = {
      id: 'session-chrome', slugId: 'chrome', userId: 'user-1', title: 'Chrome', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [
        { id: 'user', role: 'user', content: 'TST-1?', createdAt: '2026-08-31T00:00:00Z' },
        { id: 'assistant', role: 'assistant', content: 'Look at TST-1 and TST-2.\n\n```suggestions\nCompare TST-1 and TST-2\n```', createdAt: '2026-08-31T00:00:01Z' },
      ],
      createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
    }
    const data = makeBootstrap({ agentSessions: [session], agentSkills: [], issues: [makeIssue(), makeIssue({ id: 'issue-2', identifier: 'TST-2', number: 2, title: 'Second issue' })] })
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage chatSlug="chrome" data={data} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await waitFor(() => expect(document.querySelectorAll('a[data-agent-entity="issue"]')).toHaveLength(2))
    expect(screen.getByText('TST-1?')).toBeVisible()
    expect(within(screen.getByRole('list', { name: 'Referenced issues' })).getAllByRole('link')).toHaveLength(2)
    const chip = await screen.findByRole('button', { name: 'Compare TST-1 and TST-2' })
    await user.click(chip)
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledWith('session-chrome', 'Compare TST-1 and TST-2', expect.any(Function), expect.any(AbortSignal), expect.anything())
  })

  describe('retrying a failed reply', () => {
    const failedChat = (): AgentSession => ({
      id: 'session-failed', slugId: 'failed', userId: 'user-1', title: 'Failed chat', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [
        { id: 'user-message', role: 'user', content: 'Summarise the launch', createdAt: '2026-08-31T00:00:00Z' },
        { id: 'assistant-message', role: 'assistant', content: '', createdAt: '2026-08-31T00:00:01Z', parts: [{ id: 'assistant-message_error', type: 'error', status: 'error', text: 'Provider timed out' }] },
      ],
      createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
    })
    const conversation = () => screen.getByRole('group', { name: 'Agent conversation' })
    // Streams left hanging by a test keep a live copy of their chat; don't let it leak into the next one.
    afterEach(() => ['session-failed', 'session-live'].forEach(clearLiveAgentSession))

    it('reuses the saved question and replaces the error row instead of adding a second copy', async () => {
      api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
      const failed = failedChat()
      api.getAgentSession.mockResolvedValue(failed)
      let emit!: (event: unknown) => void
      streams.streamAgentSessionMessageEdit.mockImplementation((_id, _messageId, _message, onEvent) => new Promise(() => { emit = onEvent }))
      const user = userEvent.setup()
      render(<I18nProvider><AgentPage chatSlug="failed" data={makeBootstrap({ agentSessions: [failed], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      expect(await screen.findByText('The model provider took too long to respond. Try again.')).toBeVisible()
      await user.click(await screen.findByRole('button', { name: 'Retry' }))
      // The edit endpoint keeps the stored question (same id) and drops the failed reply; no new message is appended.
      expect(streams.streamAgentSessionMessageEdit).toHaveBeenCalledWith('session-failed', 'user-message', 'Summarise the launch', expect.any(Function), expect.any(AbortSignal))
      expect(streams.streamAgentSessionMessage).not.toHaveBeenCalled()
      await waitFor(() => expect(within(conversation()).getByText('Thinking…')).toBeVisible())
      expect(within(conversation()).getAllByText('Summarise the launch')).toHaveLength(1)
      expect(screen.queryByText('Provider timed out')).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
      // The server's copy of the turn replaces the optimistic one: still a single question.
      act(() => emit({ type: 'session.started', session: { ...failed, messages: [failed.messages[0]] }, messageId: 'assistant-retry' }))
      await waitFor(() => expect(document.querySelector('[data-message-id="user-message"]')).not.toBeNull())
      expect(within(conversation()).getAllByText('Summarise the launch')).toHaveLength(1)
      expect(screen.queryByText('Provider timed out')).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    })

    it('shows a turn that fails while streaming as an error row with Retry, without restoring the composer', async () => {
      api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
      const started: AgentSession = { ...failedChat(), id: 'session-live', slugId: 'live', messages: [{ id: 'user-message', role: 'user', content: 'Summarise the launch', createdAt: '2026-08-31T00:00:00Z' }] }
      streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
        onEvent({ type: 'session.started', session: started, messageId: 'assistant-message' })
        onEvent({ type: 'error', error: 'Provider timed out' })
        throw new Error('Provider timed out')
      })
      streams.streamAgentSessionMessageEdit.mockImplementation(() => new Promise(() => undefined))
      const user = userEvent.setup()
      render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
      await user.type(editor, 'Summarise the launch')
      await user.click(screen.getByRole('button', { name: 'Submit comment' }))
      expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
      // One question, one error row (no extra banner), and the sent text is not put back for a second send.
      expect(within(conversation()).getAllByText('Summarise the launch')).toHaveLength(1)
      expect(screen.getAllByText('Provider timed out')).toHaveLength(1)
      expect(editor).toHaveTextContent('')
      await user.click(screen.getByRole('button', { name: 'Retry' }))
      expect(streams.streamAgentSessionMessageEdit).toHaveBeenCalledWith('session-live', 'user-message', 'Summarise the launch', expect.any(Function), expect.any(AbortSignal))
      await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument())
      expect(within(conversation()).getAllByText('Summarise the launch')).toHaveLength(1)
      expect(screen.queryByText('Provider timed out')).not.toBeInTheDocument()
      expect(streams.streamNewAgentSession).toHaveBeenCalledTimes(1)
      expect(streams.streamAgentSessionMessage).not.toHaveBeenCalled()
    })

    it('leaves the composer draft alone when retrying', async () => {
      api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
      const failed = failedChat()
      api.getAgentSession.mockResolvedValue(failed)
      streams.streamAgentSessionMessageEdit.mockImplementation(() => new Promise(() => undefined))
      const user = userEvent.setup()
      render(<I18nProvider><AgentPage chatSlug="failed" data={makeBootstrap({ agentSessions: [failed], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
      await user.type(editor, 'Something else I was typing')
      await user.click(await screen.findByRole('button', { name: 'Retry' }))
      await waitFor(() => expect(streams.streamAgentSessionMessageEdit).toHaveBeenCalled())
      expect(editor).toHaveTextContent('Something else I was typing')
    })

    it('offers Retry only on the newest reply, not on older errors in the history', async () => {
      api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
      const failed = failedChat()
      const older: AgentSession = { ...failed, messages: [
        ...failed.messages,
        { id: 'user-2', role: 'user', content: 'Try again later', createdAt: '2026-08-31T00:01:00Z' },
        { id: 'assistant-2', role: 'assistant', content: 'Here you go.', createdAt: '2026-08-31T00:01:01Z', parts: [{ id: 'text', type: 'text', text: 'Here you go.', status: 'completed' }] },
      ] }
      api.getAgentSession.mockResolvedValue(older)
      render(<I18nProvider><AgentPage chatSlug="failed" data={makeBootstrap({ agentSessions: [older], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
      expect(await screen.findByText('The model provider took too long to respond. Try again.')).toBeVisible()
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    })
  })
})
