import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative } from '@/types/flow'
import { InitiativePageChrome, InitiativePageChromeActions } from './initiative-page-chrome'

const initiative = {
  id: 'init-1',
  name: 'Launch',
  color: '#5E6AD2',
  icon: 'Initiative',
  health: 'onTrack',
  subscribed: false,
} as Initiative

const chrome = (props: { detailsOpen?: boolean; updatesOpen?: boolean; onUpdatesOpenChange?: (open: boolean) => void } = {}) => (
  <InitiativePageChrome
    initiative={initiative}
    initiativeUpdates={[]}
    viewer={{ id: 'u1', displayName: 'Ada', name: 'Ada' } as never}
    tab="overview"
    detailsOpen={props.detailsOpen ?? false}
    updatesOpen={props.updatesOpen ?? false}
    onUpdatesOpenChange={props.onUpdatesOpenChange ?? vi.fn()}
    onOpenActivity={vi.fn()}
    onCreateUpdate={vi.fn(async () => ({}) as never)}
    onUpdate={vi.fn(async () => initiative)}
    detailsSidebar={<aside aria-label="Initiative details">Details</aside>}
  >
    <div>Main</div>
  </InitiativePageChrome>
)

describe('InitiativePageChrome (LS-0311)', () => {
  it('shows the details sidebar while it is open and the floating updates panel', () => {
    const { rerender } = render(<I18nProvider>{chrome()}</I18nProvider>)
    expect(screen.getByText('Main')).toBeInTheDocument()
    expect(screen.queryByLabelText('Initiative details')).not.toBeInTheDocument()
    rerender(<I18nProvider>{chrome({ detailsOpen: true, updatesOpen: true })}</I18nProvider>)
    expect(screen.getByLabelText('Initiative details')).toBeInTheDocument()
    expect(screen.getByLabelText('Initiative updates')).toBeInTheDocument()
    expect(document.querySelector('[data-initiative-page-chrome]')).toHaveClass('has-details')
  })

  it('toggles the updates panel and has no docked agent button (the floating chat is the agent)', async () => {
    const user = userEvent.setup()
    const onUpdates = vi.fn()
    render(<I18nProvider><InitiativePageChromeActions updatesOpen={false} onToggleUpdates={onUpdates}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Open initiative updates' }))
    expect(onUpdates).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /agent/i })).not.toBeInTheDocument()
  })
})
