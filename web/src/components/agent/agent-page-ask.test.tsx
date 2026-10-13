import { act, render, screen, waitFor } from '@testing-library/react'
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
import { AGENT_PAGE_ASK_EVENT } from './agent-ask-request'

// ⌘K "Ask Flow" (Tab) while on the Agent page sends the question in the page chat instead of opening a floating one.
describe('agent page ask request', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
    api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      const session: AgentSession = { id: 'session-1', slugId: 'asked', userId: 'user-1', title: 'Asked', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [{ id: 'user-message', role: 'user', content: 'What is the status?', createdAt: '2026-08-31T00:00:00Z' }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z' }
      onEvent({ type: 'session.started', session, messageId: 'assistant-message' })
      return session
    })
  })

  it('sends the question once in the page chat', async () => {
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await screen.findByRole('textbox', { name: 'Send a message to Flow AI' })
    await waitFor(() => expect(api.fetchAgentStatus).toHaveBeenCalled())
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    act(() => { window.dispatchEvent(new CustomEvent(AGENT_PAGE_ASK_EVENT, { detail: { prompt: 'What is the status?' } })) })
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalledTimes(1))
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ message: 'What is the status?', location: 'page' }), expect.any(Function), expect.any(AbortSignal))
  })
})
