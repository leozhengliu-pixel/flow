import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchAgentStatus: vi.fn(),
  listAgentSessions: vi.fn(),
  getAgentSession: vi.fn(),
  resolveAgentApproval: vi.fn(),
}))
const streams = vi.hoisted(() => ({
  streamNewAgentSession: vi.fn(),
  streamAgentSessionMessage: vi.fn(),
}))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { EntityAgentPanel } from './entity-agent-panel'

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
})
