import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { translateToChinese } from '@/i18n/translate'
import type { AgentMessage, AgentMessagePart } from '@/types/flow'
import { AGENT_REPLY_INTERRUPTED_TEXT, agentReplyStale, agentSessionStale, isInterruptedAgentReply } from './agent-reply-status'
import { AgentStaleLabel, AgentStaleReplyStatus } from './agent-stale-reply'
import { isBlankStoppedReply, isStoppedAgentReply, settleStoppedAgentReply } from './agent-stopped-reply'
import { EntityAgentThread } from './entity-agent-thread'

const at = '2026-11-11T00:00:00Z'
const reply = (extra: Partial<AgentMessage>): AgentMessage => ({ id: 'm', role: 'assistant', content: 'Half', createdAt: at, ...extra })
const errorPart = (text: string): AgentMessagePart => ({ id: 'm_error', type: 'error', status: 'error', text })
const tool = (name: string, extra: Partial<NonNullable<AgentMessagePart['toolCall']>> = {}): AgentMessagePart => ({ id: `t_${name}`, type: 'toolCall', status: 'error', toolCall: { id: `c_${name}`, name, status: 'error', ...extra } })
const interrupted = (extra: Partial<AgentMessage> = {}) => reply({ outcome: { status: 'interrupted', reason: 'worker_lost' }, parts: [errorPart(AGENT_REPLY_INTERRUPTED_TEXT)], ...extra })
const NO_WORKER = 'No agent worker is available; try again or contact your admin'
const notStarted = () => reply({ content: '', outcome: { status: 'failed', reason: 'worker_unavailable' }, parts: [errorPart(NO_WORKER)] })

afterEach(() => localStorage.removeItem('flow:locale'))

// A reply's outcome (api/cmd/server/agent_reply_outcome.go) decides how it reads; replies saved before outcomes
// existed still read by their error part.
describe('agent reply outcome', () => {
  it('reads a stopped reply from its outcome, and older replies from the error part', () => {
    expect(isStoppedAgentReply(reply({ outcome: { status: 'canceled', reason: 'user_stop' }, parts: [errorPart('Generation stopped')] }))).toBe(true)
    expect(isStoppedAgentReply(reply({ parts: [errorPart('Generation stopped')] }))).toBe(true)
    // Failed replies are not stops: they keep their text, error row and Retry.
    const failed = reply({ outcome: { status: 'failed', reason: 'provider_error' }, parts: [errorPart('Flow Agent provider returned status 500')] })
    expect(isStoppedAgentReply(failed)).toBe(false)
    expect(settleStoppedAgentReply(failed)).toBe(failed)
    expect(isStoppedAgentReply(reply({ content: '', outcome: { status: 'completed', reason: 'no_reply_text' } }))).toBe(false)
  })

  it('shows nothing for an empty turn, as the reference app does, but keeps a reply still being written', () => {
    expect(isBlankStoppedReply(reply({ content: '', outcome: { status: 'completed', reason: 'empty_response' } }))).toBe(true)
    const work = { id: 't', type: 'toolCall' as const, status: 'completed' as const, toolCall: { id: 'c', name: 'list_teams', status: 'completed' as const } }
    expect(isBlankStoppedReply(reply({ content: '', outcome: { status: 'completed', reason: 'no_reply_text' }, parts: [work] }))).toBe(false)
    expect(isBlankStoppedReply(reply({ content: '' }))).toBe(false)
    expect(isBlankStoppedReply(reply({ outcome: { status: 'canceled', reason: 'user_stop' }, parts: [errorPart('Generation stopped')] }))).toBe(true)
  })
})

// The reference app has no chat copy for an interrupted reply: no error row, the work it did shown as on a completed
// reply, and the chat's status reads as a stale session.
describe('interrupted reply', () => {
  it('drops only the error row, and reads older replies by their error text', () => {
    const work = tool('list_teams', { status: 'completed', result: '[]' })
    const message = interrupted({ parts: [work, { id: 'm_text', type: 'text', status: 'completed', text: 'Half' }, errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] })
    const settled = settleStoppedAgentReply(message)
    expect(settled.content).toBe('Half')
    expect(settled.parts?.map(part => part.type)).toEqual(['toolCall', 'text'])
    expect(isBlankStoppedReply(message)).toBe(false)
    expect(isInterruptedAgentReply(reply({ parts: [errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))).toBe(true)
    expect(isInterruptedAgentReply(reply({ status: 'streaming', parts: [errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))).toBe(false)
    // Nothing came back: nothing is left in the chat.
    expect(isBlankStoppedReply(interrupted({ content: '' }))).toBe(true)
  })

  it('reads "Stopped responding" once acknowledged and "Didn\'t start" when it never reached a worker', () => {
    expect(agentReplyStale(interrupted())).toEqual({ started: true, label: 'Stopped responding', retrySafe: true })
    // An interrupted reply was accepted and handed to the model or a worker, even when nothing came back yet.
    expect(agentReplyStale(interrupted({ content: '' }))).toEqual({ started: true, label: 'Stopped responding', retrySafe: true })
    expect(agentReplyStale(notStarted())).toEqual({ started: false, label: 'Didn’t start', retrySafe: true })
    expect(agentReplyStale(reply({ content: '', parts: [errorPart(NO_WORKER)] }))?.label).toBe('Didn’t start')
    expect(agentReplyStale(reply({ outcome: { status: 'failed', reason: 'provider_error' } }))).toBeUndefined()
    expect(agentReplyStale(reply({ outcome: { status: 'completed' } }))).toBeUndefined()
    // Only the chat's latest message counts.
    const later = { id: 'u2', role: 'user' as const, content: 'Again', createdAt: at }
    expect(agentSessionStale({ messages: [interrupted(), later] })).toBeUndefined()
    expect(agentSessionStale({ messages: [later, notStarted()] })?.label).toBe('Didn’t start')
    expect(translateToChinese('Stopped responding')).toBe('已停止响应')
    expect(translateToChinese('Didn’t start')).toBe('未启动')
  })

  it('never offers a retry that could repeat a write', () => {
    expect(agentReplyStale(interrupted({ parts: [tool('list_issues'), tool('get_issue'), errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))?.retrySafe).toBe(true)
    expect(agentReplyStale(interrupted({ parts: [tool('save_issue'), errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))?.retrySafe).toBe(false)
    expect(agentReplyStale(interrupted({ parts: [tool('external_code__delete_branch'), errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))?.retrySafe).toBe(false)
    expect(agentReplyStale(interrupted({ parts: [tool('list_issues', { change: { kind: 'issue' } as never }), errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] }))?.retrySafe).toBe(false)
  })

  it('panel: "Agent didn\'t start" with Retry only when safe; "Stopped responding" without Retry', async () => {
    const retry = vi.fn()
    const { rerender } = render(<I18nProvider><AgentStaleReplyStatus stale={{ started: false, label: 'Didn’t start', retrySafe: true }} onRetry={retry}/></I18nProvider>)
    expect(screen.getByText('Agent didn’t start')).toBeVisible()
    expect(screen.getByText('The request may not have reached the agent')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(retry).toHaveBeenCalledOnce()
    rerender(<I18nProvider><AgentStaleReplyStatus stale={{ started: false, label: 'Didn’t start', retrySafe: false }} onRetry={retry}/></I18nProvider>)
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    rerender(<I18nProvider><AgentStaleReplyStatus stale={{ started: true, label: 'Stopped responding', retrySafe: true }} onRetry={retry}/></I18nProvider>)
    expect(screen.getByText('Stopped responding')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    rerender(<I18nProvider><AgentStaleLabel stale={{ started: false, label: 'Didn’t start', retrySafe: true }}/></I18nProvider>)
    expect(screen.getByText('Didn’t start')).toBeVisible()
  })

  it('in the chat: no error row, the work stays, and the panel status in Chinese', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const ask = { id: 'u1', role: 'user' as const, content: 'Close DEV-1', createdAt: at }
    const props = { conversationDraftKey: 'test', input: '', onInputChange: vi.fn(), onSubmit: vi.fn(), onRetry: vi.fn() }
    const { rerender } = render(<I18nProvider><EntityAgentThread {...props} messages={[ask, interrupted({ content: 'Looking at DEV-1', parts: [{ id: 'm_text', type: 'text', status: 'completed', text: 'Looking at DEV-1' }, errorPart(AGENT_REPLY_INTERRUPTED_TEXT)] })]}/></I18nProvider>)
    expect(screen.getByText('Looking at DEV-1')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(AGENT_REPLY_INTERRUPTED_TEXT)).toBeNull()
    expect(screen.getByText('已停止响应')).toBeInTheDocument()
    // Interrupted before anything came back: nothing in the chat, the status still says it stopped responding.
    rerender(<I18nProvider><EntityAgentThread {...props} messages={[ask, interrupted({ content: '' })]}/></I18nProvider>)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByText('已停止响应')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
    rerender(<I18nProvider><EntityAgentThread {...props} messages={[ask, notStarted()]} error={NO_WORKER}/></I18nProvider>)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(NO_WORKER)).toBeNull()
    expect(screen.getByText('Agent 未启动')).toBeInTheDocument()
    expect(screen.getByText('请求可能未送达 Agent')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
