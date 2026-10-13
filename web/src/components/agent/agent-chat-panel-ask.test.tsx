import { StrictMode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn(), markAgentSessionRead: vi.fn(), updateAgentMessageFeedback: vi.fn(), createAgentSessionFeedback: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentChatPanel } from './agent-chat-panel'

const session: AgentSession = {
  id: 'session-1', slugId: 'chat', userId: 'user-1', title: 'Chat', favorite: false, location: 'toolbar', issueIds: [], skillIds: [],
  messages: [{ id: 'user-message', role: 'user', content: 'What is the status?', createdAt: '2026-08-31T00:00:00Z' }, { id: 'assistant-message', role: 'assistant', content: 'On track', createdAt: '2026-08-31T00:00:01Z' }],
  createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
}

// ⌘K "Ask Flow" (Tab) opens this panel with the typed question, the page entity as context, and sends it at once.
describe('Agent chat panel opened from the command menu', () => {
  beforeEach(() => {
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    Object.values(streams).forEach(mock => mock.mockReset())
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.started', session: { ...session, messages: session.messages.slice(0, 1) }, messageId: 'assistant-message' })
      onEvent({ type: 'session.completed', session })
      return session
    })
  })

  it('sends the question once with the page entity, even under StrictMode and re-renders', async () => {
    const tree = (label: string) => <StrictMode><I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open autoSubmit initialPrompt="What is the status?" pageContext={{ type: 'project', id: 'project-1', label }}/></I18nProvider></StrictMode>
    const { rerender } = render(tree('Compare Test'))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalledTimes(1))
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ message: 'What is the status?', projectIds: ['project-1'], location: 'toolbar' }), expect.any(Function), expect.any(AbortSignal))
    rerender(tree('Compare Test renamed'))
    await screen.findByText('added to context')
    await new Promise(resolve => setTimeout(resolve, 30))
    expect(streams.streamNewAgentSession).toHaveBeenCalledTimes(1)
    expect(streams.streamAgentSessionMessage).not.toHaveBeenCalled()
  })

  it('shows an initiative as context and sends it as initiativeIds', async () => {
    render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open autoSubmit initialPrompt="What is the status?" pageContext={{ type: 'initiative', id: 'initiative-1', label: 'Q4 push' }}/></I18nProvider>)
    expect(screen.getByText('Q4 push')).toBeVisible()
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalledTimes(1))
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ message: 'What is the status?', initiativeIds: ['initiative-1'], projectIds: [], documentIds: [] }), expect.any(Function), expect.any(AbortSignal))
  })
})
