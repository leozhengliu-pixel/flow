import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentMessage, AgentSession } from '@/types/flow'
import type { AgentStreamEvent } from '@/lib/agent-stream'

const api = vi.hoisted(() => ({
  fetchAgentStatus: vi.fn(),
  listAgentSessions: vi.fn(),
  getAgentSession: vi.fn(),
  resolveAgentApproval: vi.fn(),
  stopAgentSession: vi.fn(),
}))
const stream = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/agent-stream', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/agent-stream')>()), ...stream }))

import { LoopAgentPanel } from './loop-agent-panel'
import { splitBuilderReply } from './loop-data'
import { pushAgentSession } from '@/components/agent/use-agent-reply-follow'
import { finishedReplySession, runningReplySession } from '@/test/agent-running-reply'

const SETUP = 'Set up this loop from the Triage agent template'
const question = (id: string, message: string, options: string[]) => ({
  id, sessionId: 'session-1', connectorName: 'Flow Agent', connectorUrl: '', mode: 'form' as const, message,
  schema: { type: 'object', properties: { answer: { type: 'string', enum: options } }, required: ['answer'] },
})
const userMessage: AgentMessage = { id: 'm-user', role: 'user', content: SETUP, createdAt: '2026-09-29T10:00:00Z' }
const reply: AgentMessage = {
  id: 'm-reply', role: 'assistant', durationMs: 12_000, createdAt: '2026-09-29T10:00:12Z',
  content: "I've opened a draft and written the instructions for you.\n\nThe Triage agent loop is live and enabled.",
  parts: [
    { id: 'q1', type: 'elicitation', status: 'completed', text: "Route, but don't close", elicitation: { ...question('q1', 'How much should the triage loop do on its own?', ['Route and close clear duplicates', "Route, but don't close"]), action: 'accept' } },
    { id: 't1', type: 'toolCall', status: 'completed', toolCall: { id: 'c1', name: 'save_loop', title: 'Updated workflow definition draft', status: 'completed', result: { id: 'loop-9', name: 'Triage agent', status: 'draft' } } },
    { id: 'r1_narration_0', type: 'reasoning', status: 'completed', text: 'Checking the team setup' },
    { id: 't2', type: 'toolCall', status: 'completed', toolCall: { id: 'c2', name: 'save_loop', title: 'Created automation', status: 'completed', result: { id: 'loop-9', name: 'Triage agent', status: 'published', published: true, teamId: 'team-1', teamName: 'Test team', runCount30d: 0 } } },
  ],
}
const session = (messages: AgentMessage[], title = 'Triage agent') => ({ id: 'session-1', slugId: 's1', title, loopIds: ['loop-9'], messages, updatedAt: '2026-09-29T10:00:00Z', createdAt: '2026-09-29T10:00:00Z' }) as unknown as AgentSession

function renderPanel(props: Partial<Parameters<typeof LoopAgentPanel>[0]> = {}) {
  const callbacks = { onLoopSaved: vi.fn(), onPublished: vi.fn(), onNavigateLoop: vi.fn() }
  render(
    <I18nProvider>
      <LoopAgentPanel data={makeBootstrap()} loopId="loop-9" title="Triage agent" visual={{ templateId: 'triage-agent', icon: 'Triage' }} open onClose={vi.fn()} {...callbacks} {...props} />
    </I18nProvider>,
  )
  return callbacks
}

describe('LoopAgentPanel', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    for (const mock of [...Object.values(api), ...Object.values(stream)]) mock.mockReset()
    api.fetchAgentStatus.mockResolvedValue({ enabled: true })
  })

  it('polls a turn still running on the server and then shows the whole conversation', async () => {
    // Asked a moment ago (the stream dropped when the page moved on): its reply is still on the way.
    const asked = { ...userMessage, createdAt: new Date().toISOString() }
    api.listAgentSessions.mockResolvedValue([session([asked])])
    api.getAgentSession.mockResolvedValue(session([asked, reply]))
    const { onLoopSaved } = renderPanel({ onPublished: undefined })
    const panel = await screen.findByRole('complementary', { name: 'Loop agent' })
    await waitFor(() => expect(api.getAgentSession).toHaveBeenCalledWith('session-1'), { timeout: 4000 })
    const intro = await within(panel).findByText("I've opened a draft and written the instructions for you.", {}, { timeout: 4000 })
    // The reference app: no bubble for the auto-sent template message; the header carries the loop name.
    expect(within(panel).queryByText(SETUP)).toBeNull()
    expect(within(panel).getAllByText('Triage agent', { selector: 'strong' })[0]).toBeVisible()
    const answer = within(panel).getByRole('blockquote', { name: 'Your answer' })
    expect(answer).toHaveTextContent("How much should the triage loop do on its own?Route, but don't close")
    const updated = within(panel).getByText('Drafted the loop')
    const worked = within(panel).getByText('Worked for 12 seconds')
    expect(worked.closest('details')).not.toHaveAttribute('open')
    const final = within(panel).getByText('The Triage agent loop is live and enabled.')
    const created = within(panel).getByText('Created automation')
    const card = created.closest('.loops-agent-card')!
    expect(card).toHaveTextContent('Triage agent')
    expect(card).toHaveTextContent('Ran 0 times (30d)')
    expect(card.querySelector('.loops-agent-card-team')).toHaveTextContent('Test team')
    expect(card.querySelector('.status-glyph')).not.toBeNull()
    const order = [intro, answer, worked, updated, final, created]
    for (let index = 1; index < order.length; index += 1) expect(order[index - 1].compareDocumentPosition(order[index]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(within(panel).getByRole('button', { name: 'This was helpful' })).toBeVisible()
    expect(within(panel).getByRole('button', { name: 'Leave feedback…' })).toBeVisible()
    expect(within(panel).getAllByRole('button', { name: 'Copy message' }).at(-1)).toBeVisible()
    expect(onLoopSaved).toHaveBeenCalled()
  })

  it('keeps a typed prompt visible and does not poll a finished conversation', async () => {
    const typed: AgentMessage = { ...userMessage, content: 'Build a weekly digest for the team' }
    api.listAgentSessions.mockResolvedValue([session([typed, { ...reply, parts: [], content: 'Drafted the weekly digest.' }], 'Weekly digest')])
    renderPanel({ visual: undefined, title: undefined })
    const panel = await screen.findByRole('complementary', { name: 'Loop agent' })
    await waitFor(() => expect(panel).toHaveTextContent('Build a weekly digest for the team'))
    expect(within(panel).getByText('Weekly digest', { selector: 'strong' })).toBeVisible()
    await new Promise(resolve => setTimeout(resolve, 1700))
    expect(api.getAgentSession).not.toHaveBeenCalled()
  })

  it('streams template questions inline without the setup bubble or a Skip chip', async () => {
    api.listAgentSessions.mockResolvedValue([])
    stream.streamNewAgentSession.mockImplementation((_input: unknown, onEvent: (event: AgentStreamEvent) => void) => {
      onEvent({ type: 'session.started', messageId: 'm-live', session: session([userMessage], SETUP) })
      onEvent({ type: 'text.delta', messageId: 'm-live', delta: "I've opened a draft and written the instructions for you." })
      onEvent({ type: 'elicitation.requested', messageId: 'm-live', part: { id: 'q-live', type: 'elicitation', status: 'pending', elicitation: question('q-live', 'Should the triage loop skip any issues?', ['Review all', 'Skip already assigned']) } })
      return new Promise(() => undefined)
    })
    renderPanel({ autoMessage: SETUP })
    const panel = await screen.findByRole('complementary', { name: 'Loop agent' })
    await waitFor(() => expect(stream.streamNewAgentSession).toHaveBeenCalled())
    expect(await within(panel).findByText('Should the triage loop skip any issues?')).toBeVisible()
    expect(within(panel).getByRole('button', { name: 'Skip already assigned' })).toBeEnabled()
    expect(within(panel).queryByRole('button', { name: 'Skip' })).toBeNull()
    expect(panel.querySelector('.agent-elicitation')).toBeNull()
    expect(within(panel).queryByText(SETUP)).toBeNull()
    // The setup message titles nothing: the header shows the loop name.
    expect(within(panel).getAllByText('Triage agent', { selector: 'strong' })[0]).toBeVisible()
    // Waiting on the answer is not "Working…".
    expect(within(panel).queryByText('Working…')).toBeNull()
    expect(within(panel).queryByText('Thinking…')).toBeNull()
  })

  it('shows a reply saved mid-run after a reload with a live approval card, answered once, and Stop on the server', async () => {
    const running = runningReplySession({ id: 'session-1', loopIds: ['loop-9'] })
    api.listAgentSessions.mockResolvedValue([running])
    api.getAgentSession.mockResolvedValue(running)
    api.resolveAgentApproval.mockResolvedValue({ approvalId: 'approval-1', decision: 'approved' })
    api.stopAgentSession.mockResolvedValue(undefined)
    renderPanel({ visual: undefined, title: undefined })
    const panel = await screen.findByRole('complementary', { name: 'Loop agent' })
    expect(await within(panel).findByText('Checking the branch first.')).toBeVisible()
    expect(within(panel).getByRole('region', { name: 'Local tools requests information' })).toBeVisible()
    fireEvent.click(within(panel).getByRole('button', { name: 'Stop responding' }))
    await waitFor(() => expect(api.stopAgentSession).toHaveBeenCalledWith('session-1'))
    fireEvent.click(within(panel).getByRole('button', { name: 'Approve tool' }))
    expect(api.resolveAgentApproval).toHaveBeenCalledWith('session-1', 'approval-1', 'approve')
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Approve tool' })).toBeDisabled())
    fireEvent.click(within(panel).getByRole('button', { name: 'Reject tool' }))
    expect(api.resolveAgentApproval).toHaveBeenCalledTimes(1)
    // Answered on the Agent page: the realtime push reaches the loop chat at once.
    act(() => pushAgentSession(finishedReplySession(running)))
    expect(await within(panel).findByText('Deleted the branch.')).toBeVisible()
    expect(within(panel).queryByRole('button', { name: 'Approve tool' })).toBeNull()
    expect(within(panel).queryByRole('button', { name: 'Stop responding' })).toBeNull()
  })
})

describe('splitBuilderReply', () => {
  it('splits the intro from the final reply', () => {
    expect(splitBuilderReply('Intro.\n\nDone.', true)).toEqual({ intro: 'Intro.', reply: 'Done.' })
    expect(splitBuilderReply('I can publish it.Done — the loop is live.', true)).toEqual({ intro: 'I can publish it.', reply: 'Done — the loop is live.' })
    expect(splitBuilderReply('Only a reply.', false)).toEqual({ intro: '', reply: 'Only a reply.' })
  })
})
