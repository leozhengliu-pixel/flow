import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AgentMessage, AgentSession } from '@/types/flow'

const api = vi.hoisted(() => ({ getAgentSession: vi.fn(), resolveAgentApproval: vi.fn(), stopAgentSession: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { AGENT_REPLY_FOLLOW_MAX_FAILURES, useAgentReplyFollow } from './use-agent-reply-follow'
import { applyAgentStreamEvent, markAgentSessionFailed, markAgentSessionStopped, markDroppedAgentReply } from './agent-stream-state'
import { agentReplyInProgress } from './agent-reply-progress'

class StatusError extends Error {
  status: number
  constructor(message: string, status: number) { super(message); this.status = status }
}

const now = () => new Date().toISOString()
const question: AgentMessage = { id: 'q', role: 'user', content: 'Write the checklist', createdAt: now() }
const reply = (extra: Partial<AgentMessage> = {}): AgentMessage => ({ id: 'a', role: 'assistant', content: 'Working', createdAt: now(), ...extra })
const chat = (last: AgentMessage, id = 'chat'): AgentSession =>
  ({ id, slugId: id, userId: 'u', title: 'Chat', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [question, last], createdAt: now(), updatedAt: now() })
const running = (id = 'chat') => chat(reply({ status: 'streaming', updatedAt: now() }), id)

beforeEach(() => {
  Object.values(api).forEach(mock => mock.mockReset())
})

describe('the local copy of a streamed reply', () => {
  it('is running from session.started until the stream settles it', () => {
    const started = applyAgentStreamEvent(undefined, { type: 'session.started', session: { ...chat(reply()), messages: [question] }, messageId: 'a' })
    expect(agentReplyInProgress(started)).toBe(true)
    expect(agentReplyInProgress(markAgentSessionFailed(started!, 'provider failed'))).toBe(false)
    expect(agentReplyInProgress(markAgentSessionStopped(started!))).toBe(false)
    expect(agentReplyInProgress(applyAgentStreamEvent(started, { type: 'session.completed', session: chat(reply()) }))).toBe(false)
  })

  it('keeps the server’s updatedAt while streaming (this device’s clock never orders copies)', () => {
    const started = applyAgentStreamEvent(undefined, { type: 'session.started', session: { ...chat(reply()), messages: [question], updatedAt: '2026-11-01T00:00:00.000Z' }, messageId: 'a' })
    expect(applyAgentStreamEvent(started, { type: 'text.delta', messageId: 'a', delta: 'Hi' })?.updatedAt).toBe('2026-11-01T00:00:00.000Z')
  })

  it('a view holding its own messages marks a dropped reply as still running, with its work so far', () => {
    const work = [{ id: 'tool', type: 'toolCall' as const, status: 'completed' as const, toolCall: { id: 'call', name: 'create_document', status: 'completed' as const } }]
    expect(markDroppedAgentReply([question], 'a', work).at(-1)).toMatchObject({ id: 'a', role: 'assistant', status: 'streaming', parts: work })
    expect(markDroppedAgentReply([question, reply()], 'a', work).at(-1)).toMatchObject({ id: 'a', status: 'streaming', parts: work })
  })
})

describe('useAgentReplyFollow', () => {
  const follow = (session: AgentSession, onUpdate = vi.fn()) => renderHook(
    ({ current }) => useAgentReplyFollow({ session: current, streaming: false, onUpdate, intervalMs: 10 }),
    { initialProps: { current: session } },
  )

  it.each([
    ['completed', reply({ content: 'Created the checklist.' })],
    ['failed', reply({ parts: [{ id: 'e', type: 'error', status: 'error', text: 'The model provider is unavailable' }] })],
    ['stopped', reply({ parts: [{ id: 'e', type: 'error', status: 'error', text: 'Generation stopped' }] })],
    // An outcome the server may name on the message (read defensively: anything but "streaming" has ended).
    ['interrupted', reply({ status: 'interrupted' as never })],
  ])('stops following once the reply is %s', async (_name, settled) => {
    const onUpdate = vi.fn()
    api.getAgentSession.mockResolvedValue(chat(settled))
    const hook = follow(running(), onUpdate)
    expect(hook.result.current.following).toBe(true)
    await waitFor(() => expect(onUpdate.mock.calls.at(-1)?.[0].messages.at(-1)).toEqual(settled))
    hook.rerender({ current: chat(settled) })
    expect(hook.result.current.following).toBe(false)
    const reads = api.getAgentSession.mock.calls.length
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(api.getAgentSession.mock.calls.length).toBe(reads)
  })

  it('never lets an older running read replace a reply that already ended', async () => {
    const onUpdate = vi.fn()
    api.getAgentSession.mockResolvedValue(running())
    const hook = follow(chat(reply({ status: 'interrupted' as never })), onUpdate)
    await act(async () => { await hook.result.current.refresh() })
    expect(onUpdate).not.toHaveBeenCalled()
  })

  it('pauses after repeated failed reads and follows again on recover()', async () => {
    const onUpdate = vi.fn()
    api.getAgentSession.mockRejectedValue(new StatusError('Request failed: 502', 502))
    const hook = follow(running(), onUpdate)
    await waitFor(() => expect(hook.result.current.lost).toEqual({ sessionId: 'chat', kind: 'failed' }))
    // Paused, not ended: the reply still shows as running.
    expect(hook.result.current.following).toBe(true)
    expect(hook.result.current.paused).toBe(true)
    expect(api.getAgentSession).toHaveBeenCalledTimes(AGENT_REPLY_FOLLOW_MAX_FAILURES)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(api.getAgentSession).toHaveBeenCalledTimes(AGENT_REPLY_FOLLOW_MAX_FAILURES)
    api.getAgentSession.mockResolvedValue(chat(reply({ content: 'Done.' })))
    await act(async () => { await hook.result.current.recover() })
    expect(hook.result.current.lost).toBeUndefined()
    expect(onUpdate.mock.calls.at(-1)?.[0].messages.at(-1)).toMatchObject({ content: 'Done.' })
  })

  it('stops at once when the chat is gone (deleted, or access revoked)', async () => {
    api.getAgentSession.mockRejectedValue(new StatusError('agent chat not found', 403))
    const hook = follow(running())
    await waitFor(() => expect(hook.result.current.lost).toEqual({ sessionId: 'chat', kind: 'gone' }))
    expect(hook.result.current.following).toBe(false)
    expect(api.getAgentSession).toHaveBeenCalledTimes(1)
  })

  it('drops a read that lands after switching to another chat', async () => {
    const onUpdate = vi.fn()
    let late!: (session: AgentSession) => void
    api.getAgentSession.mockImplementationOnce(() => new Promise(resolve => { late = resolve }))
    const hook = renderHook(({ current }) => useAgentReplyFollow({ session: current, streaming: false, onUpdate, intervalMs: 60_000 }), { initialProps: { current: running() } })
    let pending!: Promise<void>
    act(() => { pending = hook.result.current.recover() })
    hook.rerender({ current: chat(reply({ content: 'Other.' }), 'other') })
    await act(async () => { late(chat(reply({ content: 'Late.' }))); await pending })
    expect(onUpdate).not.toHaveBeenCalled()
  })
})
