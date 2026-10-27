import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchAgentStatus: vi.fn(),
  listAgentSessions: vi.fn(),
  getAgentSession: vi.fn(),
  resolveAgentApproval: vi.fn(),
  markAgentSessionRead: vi.fn(),
  stopAgentSession: vi.fn(),
}))
const streams = vi.hoisted(() => ({
  streamNewAgentSession: vi.fn(),
  streamAgentSessionMessage: vi.fn(),
}))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { EntityAgentPanel } from './entity-agent-panel'
import { pushAgentSession } from './use-agent-reply-follow'
import { finishedReplySession, runningReplySession } from '@/test/agent-running-reply'

const session: AgentSession = {
  id: 'session-1',
  slugId: 'chat',
  userId: 'user-1',
  title: 'Issue chat',
  favorite: false,
  location: 'toolbar',
  issueIds: ['issue-1'],
  skillIds: [],
  messages: [
    { id: 'user-message', role: 'user', content: 'Help', createdAt: '2026-08-31T00:00:00Z' },
    { id: 'assistant-message', role: 'assistant', content: 'Sure', createdAt: '2026-08-31T00:00:01Z' },
  ],
  createdAt: '2026-08-31T00:00:00Z',
  updatedAt: '2026-08-31T00:00:01Z',
}

describe('EntityAgentPanel', () => {
  beforeEach(() => {
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    api.listAgentSessions.mockReset().mockResolvedValue([])
    api.getAgentSession.mockReset()
    Object.values(streams).forEach(mock => mock.mockReset())
  })

  it('hydrates empty and streams a new entity-bound session', async () => {
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.started', session: { ...session, messages: session.messages.slice(0, 1) }, messageId: 'assistant-message' })
      onEvent({ type: 'text.delta', messageId: 'assistant-message', delta: 'Sure' })
      onEvent({ type: 'session.completed', session })
      return session
    })
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <EntityAgentPanel
          open
          onRequestClose={vi.fn()}
          target={{ type: 'issue', id: 'issue-1', title: 'Bug', identifier: 'FLOW-1', issueIds: ['issue-1'] }}
        />
      </I18nProvider>,
    )
    const input = await screen.findByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Help')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Help', issueIds: ['issue-1'], location: 'toolbar' }),
      expect.any(Function),
      expect.any(AbortSignal),
    )
    expect(await screen.findByText('Sure')).toBeVisible()
  })

  it('loads an existing issue-linked session during hydrate', async () => {
    api.listAgentSessions.mockResolvedValue([session])
    render(
      <I18nProvider>
        <EntityAgentPanel
          open
          onRequestClose={vi.fn()}
          target={{ type: 'issue', id: 'issue-1', title: 'Bug', identifier: 'FLOW-1', issueIds: ['issue-1'] }}
        />
      </I18nProvider>,
    )
    expect(await screen.findByText('Issue chat')).toBeVisible()
    await waitFor(() => {
      expect(screen.getByLabelText('AI message')).toHaveTextContent('Sure')
    })
  })

  it('attaches an initiative as context and sends it as initiativeIds', async () => {
    streams.streamNewAgentSession.mockImplementation(async (input, onEvent) => {
      const next = { ...session, issueIds: [], initiativeIds: input.initiativeIds, title: 'Initiative chat' }
      onEvent({ type: 'session.completed', session: next })
      return next
    })
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'initiative', id: 'initiative-1', title: 'Payments Q4' }}/>
      </I18nProvider>,
    )
    const input = await screen.findByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'How is it going?')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'How is it going?', initiativeIds: ['initiative-1'], location: 'toolbar' }),
      expect.any(Function),
      expect.any(AbortSignal),
    )
    // The first message shows the initiative as added to context.
    expect(await screen.findByText('Payments Q4')).toBeVisible()
  })

  it('reopens the latest chat that has the initiative in context', async () => {
    api.listAgentSessions.mockResolvedValue([
      { ...session, id: 'older', title: 'Older initiative chat', issueIds: [], initiativeIds: ['initiative-1'], updatedAt: '2026-08-30T00:00:00Z' },
      { ...session, id: 'other', title: 'Other chat', issueIds: [], initiativeIds: ['initiative-2'], updatedAt: '2026-09-02T00:00:00Z' },
      { ...session, id: 'newer', title: 'Newer initiative chat', issueIds: [], initiativeIds: ['initiative-1'], updatedAt: '2026-09-01T00:00:00Z' },
    ])
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'initiative', id: 'initiative-1', title: 'Payments Q4' }}/>
      </I18nProvider>,
    )
    expect(await screen.findByText('Newer initiative chat')).toBeVisible()
  })

  it('reopens a document’s own chat, never an unrelated one whose slug mentions "document"', async () => {
    api.listAgentSessions.mockResolvedValue([
      { ...session, id: 'unrelated', slugId: 'create-a-document-qa-agent-c4-meeting-notes-abc', title: 'QA Agent C4 Notes', issueIds: [], updatedAt: '2026-09-03T00:00:00Z' },
      { ...session, id: 'own', title: 'Release checklist chat', issueIds: [], documentIds: ['document_1791896373053747000'], updatedAt: '2026-09-01T00:00:00Z' },
    ])
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'document', id: 'document_1791896373053747000', title: 'Release checklist' }}/>
      </I18nProvider>,
    )
    expect(await screen.findByText('Release checklist chat')).toBeVisible()
    expect(screen.queryByText('QA Agent C4 Notes')).not.toBeInTheDocument()
  })

  it('starts a new chat for a document no chat has in context', async () => {
    api.listAgentSessions.mockResolvedValue([
      { ...session, id: 'unrelated', slugId: 'create-a-document-qa-agent-c4-meeting-notes-abc', title: 'QA Agent C4 Notes', issueIds: [], updatedAt: '2026-09-03T00:00:00Z' },
    ])
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'document', id: 'document_1791896373053747000', title: 'Release checklist' }}/>
      </I18nProvider>,
    )
    await waitFor(() => expect(api.listAgentSessions).toHaveBeenCalled())
    expect(await screen.findByText('New chat')).toBeVisible()
    expect(screen.queryByText('QA Agent C4 Notes')).not.toBeInTheDocument()
  })

  it('marks its chat as docked so the app does not also open it as the floating chat', async () => {
    localStorage.removeItem('flow:closed-agent-sessions')
    const docked = vi.fn()
    window.addEventListener('flow:agent-session-docked', docked)
    api.listAgentSessions.mockResolvedValue([{ ...session, id: 'initiative-chat', title: 'Initiative chat', issueIds: [], initiativeIds: ['initiative-1'] }])
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'initiative', id: 'initiative-1', title: 'Payments Q4' }}/>
      </I18nProvider>,
    )
    expect(await screen.findByText('Initiative chat')).toBeVisible()
    await waitFor(() => expect(docked).toHaveBeenCalled())
    expect(JSON.parse(localStorage.getItem('flow:closed-agent-sessions') ?? '[]')).toContain('initiative-chat')
    window.removeEventListener('flow:agent-session-docked', docked)
  })

  it('answers the reply’s question when an option pill is picked', async () => {
    const user = userEvent.setup()
    const asking: AgentSession = {
      ...session, id: 'asking', title: 'Asking chat', issueIds: [], initiativeIds: ['initiative-1'],
      messages: [
        { id: 'u1', role: 'user', content: 'Set the target date', createdAt: '2026-08-31T00:00:00Z' },
        { id: 'a1', role: 'assistant', content: 'Which one?', createdAt: '2026-08-31T00:00:01Z', parts: [
          { id: 'a1_q', type: 'question', status: 'pending', text: 'Which one?', question: { options: [{ label: 'Project', value: 'project' }, { label: 'Both', value: 'both' }], allowFreeText: true } },
        ] },
      ],
    }
    api.listAgentSessions.mockResolvedValue([asking])
    streams.streamAgentSessionMessage.mockResolvedValue(asking)
    render(
      <I18nProvider>
        <EntityAgentPanel open onRequestClose={vi.fn()} target={{ type: 'initiative', id: 'initiative-1', title: 'Payments Q4' }}/>
      </I18nProvider>,
    )
    await user.click(await screen.findByRole('button', { name: 'Both' }))
    await waitFor(() => expect(streams.streamAgentSessionMessage).toHaveBeenCalledWith('asking', 'Both', expect.any(Function), expect.anything(), { answer: { partId: 'a1_q', value: 'both' } }))
  })

  it('does not reload the chat when the host re-renders with a new but equal target', async () => {
    api.listAgentSessions.mockResolvedValue([{ ...session, id: 'initiative-chat', title: 'Initiative chat', issueIds: [], initiativeIds: ['initiative-1'] }])
    const view = (title: string) => (
      <I18nProvider>
        <EntityAgentPanel onSessionChange={vi.fn()} open onRequestClose={vi.fn()} target={{ type: 'initiative', id: 'initiative-1', title }}/>
      </I18nProvider>
    )
    const { rerender } = render(view('Payments Q4'))
    expect(await screen.findByText('Initiative chat')).toBeVisible()
    rerender(view('Payments Q4'))
    rerender(view('Payments Q4 (renamed)'))
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(api.listAgentSessions).toHaveBeenCalledTimes(1)
  })
})

describe('EntityAgentPanel following a reply that runs on the server', () => {
  const issueTarget = { type: 'issue' as const, id: 'issue-1', title: 'Bug', identifier: 'FLOW-1', issueIds: ['issue-1'] }
  beforeEach(() => {
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    api.getAgentSession.mockReset()
    api.resolveAgentApproval.mockReset()
    api.stopAgentSession.mockReset().mockResolvedValue(undefined)
    Object.values(streams).forEach(mock => mock.mockReset())
  })

  it('shows the partial reply after a reload with a live approval card and answers it once', async () => {
    const user = userEvent.setup()
    const running = runningReplySession({ issueIds: ['issue-1'] })
    api.listAgentSessions.mockReset().mockResolvedValue([running])
    api.getAgentSession.mockResolvedValue(running)
    api.resolveAgentApproval.mockResolvedValue({ approvalId: 'approval-1', decision: 'approved' })
    render(<I18nProvider><EntityAgentPanel open onRequestClose={vi.fn()} target={issueTarget}/></I18nProvider>)
    expect(await screen.findByText('Checking the branch first.')).toBeVisible()
    expect(screen.getByRole('region', { name: 'Local tools requests information' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Stop responding' }))
    expect(api.stopAgentSession).toHaveBeenCalledWith('session-live')
    await user.click(screen.getByRole('button', { name: 'Approve tool' }))
    expect(api.resolveAgentApproval).toHaveBeenCalledWith('session-live', 'approval-1', 'approve')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Approve tool' })).toBeDisabled())
    api.getAgentSession.mockResolvedValue(finishedReplySession(running))
    await waitFor(() => expect(screen.getByText('Deleted the branch.')).toBeVisible(), { timeout: 4000 })
    expect(screen.queryByRole('button', { name: 'Approve tool' })).not.toBeInTheDocument()
  })

  it('updates when another surface answers, and re-reads instead of failing when the approval is already answered', async () => {
    const user = userEvent.setup()
    const running = runningReplySession({ issueIds: ['issue-1'] })
    api.listAgentSessions.mockReset().mockResolvedValue([running])
    api.getAgentSession.mockResolvedValue(running)
    api.resolveAgentApproval.mockRejectedValue(Object.assign(new Error('agent approval not found'), { status: 404 }))
    render(<I18nProvider><EntityAgentPanel open onRequestClose={vi.fn()} target={issueTarget}/></I18nProvider>)
    await user.click(await screen.findByRole('button', { name: 'Approve tool' }))
    await waitFor(() => expect(api.getAgentSession).toHaveBeenCalledWith('session-live'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    act(() => pushAgentSession(finishedReplySession(running)))
    expect(await screen.findByText('Deleted the branch.')).toBeVisible()
    expect(screen.getByText('Response sent')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Stop responding' })).not.toBeInTheDocument()
  })
})
