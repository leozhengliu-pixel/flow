import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import type { AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchAgentStatus: vi.fn(), markAgentSessionRead: vi.fn() }))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentChatPanel } from './agent-chat-panel'
import { splitAgentDraft } from './agent-draft'

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

  it('hands a finished assistant reply back through onUseResponse', async () => {
    const onUseResponse = vi.fn()
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel initialSession={session} issues={[]} onClose={onClose} onUseResponse={onUseResponse} useResponseLabel="Insert into update" open/></I18nProvider>)
    const buttons = await screen.findAllByRole('button', { name: 'Insert into update' })
    expect(buttons).toHaveLength(1)
    await user.click(buttons[0])
    expect(onUseResponse).toHaveBeenCalledWith('Summary')
    expect(onClose).toHaveBeenCalled()
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
    await user.click(await screen.findByRole('button', { name: 'Stop responding' }))
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

  it('attaches the current page entity to the first message and shows it as added to context', async () => {
    streams.streamNewAgentSession.mockImplementation(async (_input, onEvent) => {
      onEvent({ type: 'session.started', session: { ...session, messages: session.messages.slice(0, 1) }, messageId: 'assistant-message' })
      onEvent({ type: 'session.completed', session })
      return session
    })
    const user = userEvent.setup()
    const { rerender } = render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open pageContext={{ type: 'project', id: 'project-1', label: 'Compare Test' }}/></I18nProvider>)
    expect(screen.getByText('Compare Test')).toBeVisible()
    // Navigating before the conversation starts swaps the attached entity.
    rerender(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open pageContext={{ type: 'document', id: 'document-1', label: 'Launch plan' }}/></I18nProvider>)
    expect(screen.queryByText('Compare Test')).not.toBeInTheDocument()
    expect(screen.getByText('Launch plan')).toBeVisible()
    rerender(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open pageContext={{ type: 'project', id: 'project-1', label: 'Compare Test' }}/></I18nProvider>)
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Summarize')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ projectIds: ['project-1'], documentIds: [] }), expect.any(Function), expect.any(AbortSignal))
    expect(await screen.findByText('added to context')).toBeVisible()
    expect(screen.getByText('Compare Test')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Remove from context' })).not.toBeInTheDocument()
    expect(input).toHaveAttribute('placeholder', 'Reply…')
    expect(screen.queryByText('You')).not.toBeInTheDocument()
    expect(screen.queryByText('Flow Agent')).not.toBeInTheDocument()
  })

  it('does not send the page entity once its chip is removed', async () => {
    streams.streamNewAgentSession.mockResolvedValue(session)
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open pageContext={{ type: 'project', id: 'project-1', label: 'Compare Test' }}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Remove from context' }))
    expect(screen.queryByText('Compare Test')).not.toBeInTheDocument()
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Summarize')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(streams.streamNewAgentSession).toHaveBeenCalled())
    expect(streams.streamNewAgentSession).toHaveBeenCalledWith(expect.objectContaining({ projectIds: [] }), expect.any(Function), expect.any(AbortSignal))
    expect(screen.queryByText('added to context')).not.toBeInTheDocument()
  })

  it('offers feedback and copy actions on finished replies', async () => {
    const user = userEvent.setup()
    // user-event installs its own clipboard stub, so replace it afterwards.
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    render(<I18nProvider><AgentChatPanel initialSession={session} issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    const good = await screen.findByRole('button', { name: 'Good response' })
    expect(good).toHaveAttribute('aria-pressed', 'false')
    await user.click(good)
    expect(good).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: 'Copy message' }))
    expect(writeText).toHaveBeenCalledWith('Summary')
    expect(await screen.findByRole('button', { name: 'Copied to clipboard' })).toBeVisible()
  })
})

describe('splitAgentDraft', () => {
  it('moves a finished update block out of the chat text', () => {
    const reply = 'I drafted an update from the project goals.\n\n```update\nWe matched layout.\n\n- Next: typography\n```'
    expect(splitAgentDraft(reply, 'update')).toEqual({ prose: 'I drafted an update from the project goals.', draft: 'We matched layout.\n\n- Next: typography' })
  })

  it('hides an unfinished block while streaming and ignores other fences', () => {
    expect(splitAgentDraft('Drafting…\n```update\nWe matched', 'update')).toEqual({ prose: 'Drafting…', draft: undefined })
    expect(splitAgentDraft('```ts\nconst a = 1\n```', 'update').prose).toContain('const a = 1')
    expect(splitAgentDraft('plain', undefined)).toEqual({ prose: 'plain' })
  })
})

