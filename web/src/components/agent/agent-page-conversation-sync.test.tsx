import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentMessage, AgentSession, BootstrapData } from '@/types/flow'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(), resolveAgentApproval: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'

class StatusError extends Error {
  status: number
  constructor(message: string, status: number) { super(message); this.status = status }
}

const base = Date.now() - 60_000
const at = (seconds: number) => new Date(base + seconds * 1000).toISOString()
const message = (id: string, role: 'user' | 'assistant', content: string, seconds: number, extra: Partial<AgentMessage> = {}): AgentMessage =>
  ({ id, role, content, createdAt: at(seconds), ...(role === 'assistant' ? { parts: [{ id: `${id}_text`, type: 'text', status: 'completed', text: content }] } : {}), ...extra })
const chat = (overrides: Partial<AgentSession> = {}): AgentSession => ({
  id: 'session-chat', slugId: 'chat', userId: 'user-1', title: 'Release plan', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [
    message('q1', 'user', 'Plan the release', 0),
    message('a1', 'assistant', 'Here is the release plan.', 1),
    message('q2', 'user', 'Add the dates', 2),
    message('a2', 'assistant', 'The dates are set.', 3),
  ],
  createdAt: at(0), updatedAt: at(3), ...overrides,
})
const summaryOf = (session: AgentSession, overrides: Partial<AgentSession> = {}): AgentSession => ({ ...session, messages: [], messagesOmitted: true, ...overrides })
const bubbles = () => document.querySelectorAll('[data-message-id]').length
const page = (data: BootstrapData, chatSlug = 'chat') => <I18nProvider><AgentPage chatSlug={chatSlug} data={data} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>

beforeEach(() => {
  Object.values(api).forEach(mock => mock.mockReset())
  Object.values(streams).forEach(mock => mock.mockReset())
  api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'model' })
  api.markAgentSessionRead.mockResolvedValue(undefined)
  api.stopAgentSession.mockResolvedValue(undefined)
})
afterEach(() => vi.useRealTimers())

describe('a chat read whole and a refreshed list', () => {
  it('keeps the messages when a metadata refresh sends the chat as a summary again', async () => {
    api.getAgentSession.mockResolvedValue(chat())
    const view = render(page(makeBootstrap({ agentSessions: [summaryOf(chat())] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    expect(bubbles()).toBe(4)
    // The workspace metadata is refreshed (another tab edited a project): the same chat comes back as a summary.
    view.rerender(page(makeBootstrap({ agentSessions: [summaryOf(chat())] })))
    view.rerender(page(makeBootstrap({ agentSessions: [summaryOf(chat(), { title: 'Release plan v2' })] })))
    expect(bubbles()).toBe(4)
    expect(screen.getByText('The dates are set.')).toBeVisible()
    expect(api.getAgentSession).toHaveBeenCalledTimes(1)
  })

  it('reads the chat again when a newer summary says it changed, showing the old messages meanwhile', async () => {
    api.getAgentSession.mockResolvedValueOnce(chat())
    const view = render(page(makeBootstrap({ agentSessions: [summaryOf(chat())] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    let answer!: (session: AgentSession) => void
    api.getAgentSession.mockImplementationOnce(() => new Promise(resolve => { answer = resolve }))
    const edited = chat({ updatedAt: at(9), messages: [...chat().messages.slice(0, 2), message('q2', 'user', 'Add the owners', 8), message('a3', 'assistant', 'The owners are set.', 9)] })
    view.rerender(page(makeBootstrap({ agentSessions: [summaryOf(edited)] })))
    await waitFor(() => expect(api.getAgentSession).toHaveBeenCalledTimes(2))
    expect(bubbles()).toBe(4)
    await act(async () => answer(edited))
    expect(await screen.findByText('The owners are set.')).toBeVisible()
    expect(screen.queryByText('The dates are set.')).not.toBeInTheDocument()
  })

  it('drops a late read made for an older version', async () => {
    let first!: (session: AgentSession) => void
    api.getAgentSession.mockImplementationOnce(() => new Promise(resolve => { first = resolve }))
    const view = render(page(makeBootstrap({ agentSessions: [summaryOf(chat())] })))
    await waitFor(() => expect(api.getAgentSession).toHaveBeenCalledTimes(1))
    const newer = chat({ updatedAt: at(9), messages: [...chat().messages, message('q3', 'user', 'Ship it', 8), message('a3', 'assistant', 'Shipped.', 9)] })
    api.getAgentSession.mockResolvedValueOnce(newer)
    view.rerender(page(makeBootstrap({ agentSessions: [summaryOf(newer)] })))
    expect(await screen.findByText('Shipped.')).toBeVisible()
    await act(async () => first(chat()))
    expect(screen.getByText('Shipped.')).toBeVisible()
    expect(bubbles()).toBe(6)
  })

  it('starts over for another viewer: nothing read for the previous one carries over', async () => {
    api.getAgentSession.mockResolvedValueOnce(chat())
    const data = makeBootstrap({ agentSessions: [summaryOf(chat())] })
    const view = render(page(data))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    api.getAgentSession.mockRejectedValue(new StatusError('agent chat not found', 404))
    view.rerender(page({ ...data, viewer: { ...data.viewer, id: 'someone-else' }, agentSessions: [summaryOf(chat())] }))
    expect(await screen.findByText('This chat is no longer available.')).toBeVisible()
    expect(screen.queryByText('The dates are set.')).not.toBeInTheDocument()
  })

  it('hides a chat that is gone (deleted, or access revoked) and offers nothing to retry', async () => {
    api.getAgentSession.mockRejectedValue(new StatusError('agent chat not found', 404))
    // The list still carried the whole chat from before.
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    expect(await screen.findByText('This chat is no longer available.')).toBeVisible()
    expect(bubbles()).toBe(0)
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(api.getAgentSession).toHaveBeenCalledTimes(1)
  })

  it('keeps showing a chat it already has whole when re-reading it fails, without a notice', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    api.getAgentSession.mockRejectedValue(new TypeError('Failed to fetch'))
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000) })
    expect(api.getAgentSession).toHaveBeenCalledTimes(4)
    expect(screen.getByText('The dates are set.')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('retries a failed read a few times, then says so and reads again on Retry', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    api.getAgentSession.mockRejectedValue(new StatusError('Request failed: 502', 502))
    render(page(makeBootstrap({ agentSessions: [summaryOf(chat())] })))
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000) })
    expect(api.getAgentSession).toHaveBeenCalledTimes(4)
    expect(screen.getByText("Couldn't load this chat.")).toBeVisible()
    // Bounded: no more reads on their own.
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(api.getAgentSession).toHaveBeenCalledTimes(4)
    api.getAgentSession.mockResolvedValue(chat())
    await act(async () => screen.getByRole('button', { name: 'Retry' }).click())
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    expect(screen.queryByText("Couldn't load this chat.")).not.toBeInTheDocument()
    expect(api.getAgentSession).toHaveBeenCalledTimes(5)
  })
})

describe('a reply whose stream breaks', () => {
  const asked = () => chat({ messages: [...chat().messages, message('q3', 'user', 'Write the checklist', 10)], updatedAt: at(10) })
  const finished = () => chat({ messages: [...asked().messages, message('a3', 'assistant', 'Created the checklist.', 12, { durationMs: 2000 })], updatedAt: at(12) })
  const dropAfterStart = () => streams.streamAgentSessionMessage.mockImplementation(async (_id, _message, onEvent) => {
    onEvent({ type: 'session.started', session: asked(), messageId: 'a3' })
    onEvent({ type: 'text.delta', messageId: 'a3', delta: 'Creat', part: { id: 'a3_text', type: 'text', status: 'running', text: 'Creat' } })
    // A proxy closes the connection: no error event, the body just ends.
    throw new Error('Flow Agent stream ended before completion')
  })
  const sendFollowUp = async () => {
    const user = userEvent.setup({ advanceTimers: ms => { if (vi.isFakeTimers()) vi.advanceTimersByTime(ms) } })
    const editor = await screen.findByRole('textbox', { name: 'Send a message to Flow AI' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send message' })).toBeEnabled())
    await user.type(editor, 'Write the checklist')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    return editor
  }

  it('reads the server’s final conversation once, without sending the message again or duplicating it', async () => {
    api.getAgentSession.mockResolvedValueOnce(chat()).mockResolvedValue(finished())
    dropAfterStart()
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    const editor = await sendFollowUp()
    const conversation = screen.getByRole('group', { name: 'Agent conversation' })
    expect(await within(conversation).findByText('Created the checklist.')).toBeVisible()
    expect(within(conversation).getAllByText('Write the checklist')).toHaveLength(1)
    expect(bubbles()).toBe(6)
    expect(editor).toHaveTextContent('')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledTimes(1)
    expect(streams.streamAgentSessionMessageEdit).not.toHaveBeenCalled()
    expect(streams.streamNewAgentSession).not.toHaveBeenCalled()
  })

  it('shows the reply still running on the server, then how it ended', async () => {
    const running = chat({ messages: [...asked().messages, message('a3', 'assistant', 'Creating', 11, { status: 'streaming', parts: [{ id: 'a3_text', type: 'text', status: 'running', text: 'Creating' }] })], updatedAt: at(10) })
    api.getAgentSession.mockResolvedValueOnce(chat()).mockResolvedValueOnce(running).mockResolvedValue(finished())
    dropAfterStart()
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    await sendFollowUp()
    expect(await screen.findByText('Creating')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Stop responding' })).toBeVisible()
    expect(await screen.findByText('Created the checklist.', undefined, { timeout: 4000 })).toBeVisible()
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Stop responding' })).not.toBeInTheDocument())
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledTimes(1)
  })

  it('keeps the reply running while reads fail, pauses after a few, and catches up once back online', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    api.getAgentSession.mockResolvedValueOnce(chat()).mockRejectedValue(new TypeError('Failed to fetch'))
    dropAfterStart()
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    await sendFollowUp()
    // Step the clock a tick at a time so the page renders between reads, as it does in a browser.
    const advance = async (ms: number) => { for (let step = 0; step < ms; step += 500) await act(async () => { await vi.advanceTimersByTimeAsync(500) }) }
    await advance(20_000)
    const reads = api.getAgentSession.mock.calls.length
    expect(reads).toBe(1 + 5)
    // Like the reference app, nothing changes in the message: still working, no error, no Retry.
    expect(screen.getByRole('button', { name: 'Stop responding' })).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    await advance(20_000)
    expect(api.getAgentSession).toHaveBeenCalledTimes(reads)
    api.getAgentSession.mockResolvedValue(finished())
    await act(async () => { window.dispatchEvent(new Event('online')) })
    expect(await screen.findByText('Created the checklist.')).toBeVisible()
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Stop responding' })).not.toBeInTheDocument())
    expect(streams.streamAgentSessionMessage).toHaveBeenCalledTimes(1)
    expect(streams.streamAgentSessionMessageEdit).not.toHaveBeenCalled()
  })

  it('treats Stop as the end of the reply, not as a dropped connection', async () => {
    api.getAgentSession.mockResolvedValueOnce(chat())
    streams.streamAgentSessionMessage.mockImplementation((_id, _message, onEvent, signal: AbortSignal) => {
      onEvent({ type: 'session.started', session: asked(), messageId: 'a3' })
      return new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
    })
    render(page(makeBootstrap({ agentSessions: [chat()] })))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    await sendFollowUp()
    await userEvent.click(await screen.findByRole('button', { name: 'Stop responding' }))
    // A stopped reply leaves the question standing alone, and the page is idle again.
    expect(await screen.findByRole('button', { name: 'Send message' })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Stop responding' })).not.toBeInTheDocument()
    expect(screen.getAllByText('Write the checklist')).toHaveLength(1)
    await new Promise(resolve => setTimeout(resolve, 2500))
    expect(api.stopAgentSession).toHaveBeenCalledWith('session-chat')
    // Only the read that opened the chat: a stopped reply is not followed.
    expect(api.getAgentSession).toHaveBeenCalledTimes(1)
  })

  it('drops a recovery read that lands after switching to another chat', async () => {
    const other = chat({ id: 'session-other', slugId: 'other', title: 'Other', messages: [message('o1', 'user', 'Other question', 0), message('o2', 'assistant', 'Other answer.', 1)] })
    let late!: (session: AgentSession) => void
    api.getAgentSession.mockImplementation((id: string) => id === 'session-other' ? Promise.resolve(other) : Promise.resolve(chat()))
    dropAfterStart()
    const data = makeBootstrap({ agentSessions: [chat(), other] })
    const view = render(page(data))
    expect(await screen.findByText('The dates are set.')).toBeVisible()
    api.getAgentSession.mockImplementation((id: string) => id === 'session-other' ? Promise.resolve(other) : new Promise(resolve => { late = resolve }))
    await sendFollowUp()
    await waitFor(() => expect(late).toBeDefined())
    view.rerender(page(data, 'other'))
    expect(await screen.findByText('Other answer.')).toBeVisible()
    await act(async () => late(finished()))
    expect(screen.getByText('Other answer.')).toBeVisible()
    expect(screen.queryByText('Created the checklist.')).not.toBeInTheDocument()
  })
})
