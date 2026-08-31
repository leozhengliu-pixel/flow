import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentChatPanel } from './agent-chat-panel'

const session: AgentSession = {
  id: 'session-1', slugId: 'chat', userId: 'user-1', title: 'Chat', favorite: false, location: 'toolbar', issueIds: ['issue-1'], skillIds: [],
  messages: [{ id: 'user-message', role: 'user', content: 'Summarize', createdAt: '2026-08-31T00:00:00Z' }, { id: 'assistant-message', role: 'assistant', content: 'Summary', createdAt: '2026-08-31T00:00:01Z' }],
  createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:01Z',
}

describe('Agent chat panel streaming', () => {
  beforeEach(() => {
    api.fetchAgentStatus.mockReset().mockResolvedValue({ enabled: true, model: 'model' })
    Object.values(streams).forEach(mock => mock.mockReset())
  })

  it('streams a new toolbar conversation and keeps selected issue context', async () => {
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.started', session: { ...session, messages: session.messages.slice(0, 1) }, messageId: 'assistant-message' })
      onEvent({ type: 'text.delta', messageId: 'assistant-message', delta: 'Summary' })
      onEvent({ type: 'session.completed', session })
      return session
    })
    const user = userEvent.setup()
    const data = makeBootstrap()
    const onSessionChange = vi.fn()
    render(<I18nProvider><AgentChatPanel issues={[issueToExplorerRow(data.issues[0], 'workspace')]} onClose={vi.fn()} onSessionChange={onSessionChange} open/></I18nProvider>)
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Summarize')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(onSessionChange).toHaveBeenCalledWith(session))
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ message: 'Summarize', issueIds: [data.issues[0].id], location: 'toolbar' }), expect.any(Function), expect.any(AbortSignal))
    expect(screen.getByText('Summary')).toBeVisible()
  })

  it('aborts an in-flight stream from the stop control', async () => {
    let signal: AbortSignal | undefined
    streams.streamNewAgentSession.mockImplementation((_input, _onEvent, nextSignal) => {
      signal = nextSignal
      return new Promise((_resolve, reject) => nextSignal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Long task')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await user.click(await screen.findByRole('button', { name: 'Stop generating' }))
    expect(signal?.aborted).toBe(true)
  })

  it('renders persisted reasoning and markdown in toolbar conversations', async () => {
    const richSession: AgentSession = {
      ...session,
      messages: [{
        id: 'assistant-rich', role: 'assistant', content: '## Result\n\n- **Passed** checks', createdAt: '2026-08-31T00:00:01Z',
        parts: [{ id: 'reasoning', type: 'reasoning', text: 'Inspected the workspace', status: 'completed' }],
      }],
    }
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel initialSession={richSession} issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    expect(await screen.findByRole('heading', { name: 'Result' })).toBeVisible()
    expect(screen.getByRole('list')).toBeVisible()
    await user.click(screen.getByText('Work completed'))
    expect(screen.getByText('Inspected the workspace')).toBeVisible()
  })
})
