import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative } from '@/types/flow'
import { InitiativeAgentSidebarContainer } from './initiative-agent-sidebar-container'
import { InitiativePageChrome, InitiativePageChromeActions } from './initiative-page-chrome'

const initiative = {
  id: 'init-1',
  name: 'Launch',
  color: '#5E6AD2',
  icon: 'Initiative',
  health: 'onTrack',
  subscribed: false,
} as Initiative

describe('InitiativeAgentSidebarContainer (LS-0299)', () => {
  it('docks EntityAgentPanel slot when open', () => {
    render(
      <I18nProvider>
        <InitiativeAgentSidebarContainer initiative={initiative} open onOpenChange={vi.fn()}>
          <div>Wave3 body</div>
        </InitiativeAgentSidebarContainer>
      </I18nProvider>,
    )
    expect(screen.getByLabelText('Initiative agent sidebar')).toBeInTheDocument()
    expect(screen.getByText('Wave3 body')).toBeInTheDocument()
  })

  it('shows the entity agent panel without children', () => {
    render(
      <I18nProvider>
        <InitiativeAgentSidebarContainer initiative={initiative} open onOpenChange={vi.fn()} />
      </I18nProvider>,
    )
    expect(screen.getByLabelText('Entity agent panel')).toBeInTheDocument()
  })
})

describe('InitiativePageChrome (LS-0311)', () => {
  it('wires agent sidebar and updates floating panel', async () => {
    const user = userEvent.setup()
    const onAgent = vi.fn()
    const onUpdates = vi.fn()
    render(
      <I18nProvider>
        <div>
          <InitiativePageChromeActions
            agentOpen={false}
            updatesOpen={false}
            onToggleAgent={() => onAgent(true)}
            onToggleUpdates={() => onUpdates(true)}
          />
          <InitiativePageChrome
            initiative={initiative}
            initiativeUpdates={[]}
            viewer={{ id: 'u1', displayName: 'Ada', name: 'Ada' } as never}
            tab="overview"
            detailsOpen={false}
            agentOpen
            onAgentOpenChange={onAgent}
            updatesOpen
            onUpdatesOpenChange={onUpdates}
            onOpenActivity={vi.fn()}
            onCreateUpdate={vi.fn(async () => ({}) as never)}
            onUpdate={vi.fn(async () => initiative)}
          >
            <div>Main</div>
          </InitiativePageChrome>
        </div>
      </I18nProvider>,
    )
    expect(screen.getByText('Main')).toBeInTheDocument()
    expect(screen.getByLabelText('Initiative agent sidebar')).toBeInTheDocument()
    expect(screen.getByLabelText('Initiative updates')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open agent sidebar' }))
    expect(onAgent).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Open initiative updates' }))
    expect(onUpdates).toHaveBeenCalled()
  })
})
