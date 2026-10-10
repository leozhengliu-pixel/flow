import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { AgentPanel, AgentPanelToggleButton } from './agent-panel'
import { AgentPanelLayout } from './agent-panel-layout'

describe('AgentPanel chrome', () => {
  it('renders sidebar chrome with close and new chat', async () => {
    const onClose = vi.fn()
    const onNew = vi.fn()
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <AgentPanel open title="New chat" onRequestClose={onClose} onNewChat={onNew} variant="sidebar">
          <div>Thread</div>
        </AgentPanel>
      </I18nProvider>,
    )
    expect(screen.getByRole('complementary', { name: 'Flow Agent chat' })).toBeVisible()
    expect(screen.getByText('Thread')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'New chat' }))
    expect(onNew).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Close chat' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('draws the header controls and the Chat toggle with the agent glyphs of Linear\'s chat panel', () => {
    render(
      <I18nProvider>
        <AgentPanel open title="New chat" onRequestClose={vi.fn()} onNewChat={vi.fn()} onOpenFullPage={vi.fn()} variant="floating">
          <div>Thread</div>
        </AgentPanel>
        <AgentPanelToggleButton open={false} onToggle={vi.fn()}/>
      </I18nProvider>,
    )
    const glyph = (name: string) => screen.getByRole('button', { name }).querySelector('svg')
    expect(glyph('Close chat')).toHaveAttribute('data-agent-glyph', 'closeChat')
    expect(glyph('Open full page')).toHaveAttribute('data-agent-glyph', 'openFullPage')
    expect(glyph('Minimize chat')).toHaveAttribute('data-agent-glyph', 'chatMinimize')
    expect(glyph('Open chat')).toHaveAttribute('data-agent-glyph', 'agentPointer')
  })

  it('shows error boundary fallback', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    function Boom(): null {
      throw new Error('boom')
    }
    render(
      <I18nProvider>
        <AgentPanelLayout onRequestClose={vi.fn()}>
          <Boom />
        </AgentPanelLayout>
      </I18nProvider>,
    )
    expect(await screen.findByText('Something went wrong')).toBeVisible()
    expect(screen.getByText('An error occurred while loading the agent panel.')).toBeVisible()
    spy.mockRestore()
  })
})
