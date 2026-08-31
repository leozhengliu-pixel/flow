import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'

const api = vi.hoisted(() => ({
  createAgentSession: vi.fn(), createAgentSessionMessage: vi.fn(), deleteAgentSession: vi.fn(),
  fetchAgentStatus: vi.fn(), updateAgentSession: vi.fn(), updateAgentSessionMessage: vi.fn(),
}))
vi.mock('@/lib/api', () => api)

import { AgentPage } from './agent-page'

describe('agent page composer', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
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
})
