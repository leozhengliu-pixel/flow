import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(), createAgentSessionFeedback: vi.fn(),
  fetchAgentStatus: vi.fn(), getAgentSession: vi.fn(), markAgentSessionRead: vi.fn(), stopAgentSession: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(), updateAgentMessageFeedback: vi.fn(),
}))
const streams = vi.hoisted(() => ({ streamNewAgentSession: vi.fn(), streamAgentSessionMessage: vi.fn(), streamAgentSessionMessageEdit: vi.fn() }))
vi.mock('@/lib/api', () => api)
vi.mock('@/lib/agent-stream', () => streams)

import { AgentPage } from './agent-page'
import { AgentChatPanel } from './agent-chat-panel'
import { AgentPanel } from './agent-panel'
import { AgentFeedbackDialog, AgentReplyActions } from './agent-message-actions'

const tooltipSurface = async (label: string) => (await screen.findAllByText(label, undefined, { timeout: 3000 }))[0].closest('.flow-tooltip-content')!

beforeEach(() => {
  Object.values(api).forEach(mock => mock.mockReset())
  Object.values(streams).forEach(mock => mock.mockReset())
  api.fetchAgentStatus.mockResolvedValue({ enabled: true, model: 'test' })
  api.getAgentSession.mockRejectedValue(new Error('not stubbed'))
  localStorage.clear()
})

// The reference app shows the lighter tooltip over the agent composer card (Agent page, floating chat, comment replies) and
// a mid-light one over dialogs and floating panels; the palettes themselves are the --tooltip-*-bg tokens.
describe('tooltips over raised agent surfaces', () => {
  it('uses the elevated variant for the Agent page composer buttons', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentPage data={makeBootstrap({ agentSessions: [], agentSkills: [] })} onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onSessionChange={vi.fn()}/></I18nProvider>)
    await user.hover(screen.getByRole('button', { name: 'Attach images, files, or videos' }))
    expect(await tooltipSurface('Attach images, files, or videos')).toHaveClass('flow-tooltip-content--elevated')
    await user.unhover(screen.getByRole('button', { name: 'Attach images, files, or videos' }))
    await user.type(screen.getByRole('textbox', { name: 'Send a message to Flow AI' }), 'Hello')
    await user.hover(screen.getByRole('button', { name: 'Submit comment' }))
    const send = await tooltipSurface('Send message')
    expect(send).toHaveClass('flow-tooltip-content--elevated')
    expect(send.querySelector('kbd')?.textContent).toBe('Enter')
  })

  it('uses the elevated variant for the floating chat composer, inside the raised panel', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentChatPanel issues={[]} onClose={vi.fn()} open/></I18nProvider>)
    const input = screen.getByRole('textbox', { name: 'Send a message to Flow Agent' })
    await waitFor(() => expect(input).toBeEnabled())
    await user.type(input, 'Hello')
    await user.hover(screen.getByRole('button', { name: 'Send message' }))
    expect(await tooltipSurface('Send message')).toHaveClass('flow-tooltip-content--elevated')
  })

  it('uses the raised variant for tooltips in the floating panel, and the base one in the docked sidebar', async () => {
    const user = userEvent.setup()
    const panel = (variant: 'floating' | 'sidebar') => <I18nProvider><AgentPanel open title="Chat" onRequestClose={vi.fn()} variant={variant}><AgentReplyActions content="Answer" message={{ id: 'reply' }}/></AgentPanel></I18nProvider>
    const { unmount } = render(panel('floating'))
    await user.hover(screen.getByRole('button', { name: 'This was helpful' }))
    expect(await tooltipSurface('This was helpful')).toHaveClass('flow-tooltip-content--raised')
    unmount()
    render(panel('sidebar'))
    await user.hover(screen.getByRole('button', { name: 'This was helpful' }))
    const surface = await tooltipSurface('This was helpful')
    expect(surface).not.toHaveClass('flow-tooltip-content--raised')
    expect(surface).not.toHaveClass('flow-tooltip-content--elevated')
  })

  it('uses the raised variant inside the feedback dialog', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentFeedbackDialog open onOpenChange={vi.fn()} onSubmit={vi.fn()}/></I18nProvider>)
    await user.hover(await screen.findByRole('button', { name: 'Attach images, files, or videos' }))
    expect(await tooltipSurface('Attach images, files, or videos')).toHaveClass('flow-tooltip-content--raised')
  })
})

