import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(),
  fetchAgentStatus: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'
import { applyAgentStreamEvent } from './agent-stream-state'

describe('agent page composer', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.values(streams).forEach(mock => mock.mockReset())
    api.fetchAgentStatus.mockResolvedValue({ enabled: false, model: '' })
  })

  it('accepts draft input even when the Agent backend is not configured', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Flow Agent is not configured')).toBeVisible())
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    expect(editor).toHaveAttribute('contenteditable', 'true')
    await user.click(editor)
    await user.type(editor, 'Draft a project plan')
    expect(editor).toHaveTextContent('Draft a project plan')
    expect(screen.getByRole('button', { name: 'Submit comment' })).toBeDisabled()
  })

  it('renders text, reasoning, and tool deltas while a session streams', async () => {
    const user = userEvent.setup()
    const navigate = vi.fn()
    const reload = vi.fn().mockResolvedValue(undefined)
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
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={navigate} onOpenSidebar={vi.fn()} onReload={reload}/></I18nProvider>)
    const editor = screen.getByRole('textbox', { name: 'Send a message to Flow AI' })
    await user.type(editor, 'Hello')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Submit comment' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Submit comment' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/workspace/agent/streamed-chat'))
    expect(streams.streamNewAgentSession).toHaveBeenCalled()
    expect(reload).toHaveBeenCalled()
  })

  it('reduces incremental stream events into one assistant message', () => {
    const session = { id: 'session', slugId: 'chat', userId: 'user', title: 'Chat', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [], createdAt: '', updatedAt: '' } as AgentSession
    const started = applyAgentStreamEvent(undefined, { type: 'session.started', session, messageId: 'message' })!
    const text = applyAgentStreamEvent(started, { type: 'text.delta', messageId: 'message', delta: 'Hello', part: { id: 'text', type: 'text', text: 'Hello', status: 'running' } })!
    const finished = applyAgentStreamEvent(text, { type: 'text.delta', messageId: 'message', delta: ' world', part: { id: 'text', type: 'text', text: 'Hello world', status: 'completed' } })!
    expect(finished.messages[0]).toMatchObject({ content: 'Hello world', parts: [{ id: 'text', text: 'Hello world' }] })
  })

  it('renders persisted reasoning, tool, text, and error parts', async () => {
    const session: AgentSession = {
      id: 'session-parts', slugId: 'parts', userId: 'user-1', title: 'Tool run', favorite: false, location: 'page', issueIds: [], skillIds: [],
      messages: [{ id: 'assistant', role: 'assistant', content: 'Finished', createdAt: '2026-08-31T00:00:00Z', parts: [
        { id: 'reasoning', type: 'reasoning', text: 'Checked workspace state', status: 'completed' },
        { id: 'tool', type: 'toolCall', status: 'completed', toolCall: { id: 'call', name: 'list_issues', arguments: { query: 'bug' }, result: { items: [] }, status: 'completed' } },
        { id: 'text', type: 'text', text: 'Finished', status: 'completed' },
        { id: 'error', type: 'error', text: 'Partial warning', status: 'error' },
      ] }], createdAt: '2026-08-31T00:00:00Z', updatedAt: '2026-08-31T00:00:00Z',
    }
    render(<I18nProvider><AgentPage chatSlug="parts" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    await userEvent.click(screen.getByText('Work completed'))
    expect(screen.getByText('Looked at issues')).toBeVisible()
    expect(screen.getByText('Reasoning')).toBeVisible()
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
    render(<I18nProvider><AgentPage chatSlug="edit" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Edit message' }))
    expect(screen.getByText('Original answer')).toBeVisible()
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
    render(<I18nProvider><AgentPage chatSlug="history" data={makeBootstrap({ agentSessions: [session], agentSkills: [] })} onNavigate={navigate} onOpenSidebar={vi.fn()} onReload={vi.fn().mockResolvedValue(undefined)}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Switch agent chat' }))
    expect(screen.getByRole('group', { name: 'Today' })).toBeVisible()
    await user.hover(screen.getByRole('option', { name: 'New chat' }))
    await user.keyboard('{Enter}')
    expect(navigate).toHaveBeenCalledWith('/workspace/agent')
  })
})
