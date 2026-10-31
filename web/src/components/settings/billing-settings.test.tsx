import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { fetchAICredits } from '@/lib/ai-credits-api'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { WorkspaceMember } from '@/types/flow'
import { BillingSettings } from './billing-settings'

vi.mock('@/lib/ai-credits-api', () => ({ fetchAICredits: vi.fn().mockResolvedValue({ includedMicros: 5_000_000, spentMicros: 1_250_000, remainingMicros: 3_750_000 }) }))
const desktopSettings = vi.hoisted(() => ({ value: true }))
vi.mock('@/hooks/use-desktop-settings', () => ({ useDesktopSettings: () => desktopSettings.value }))

const members = [{ user: viewer, role: 'owner', status: 'active', joinedAt: '2026-01-01T00:00:00Z' }] as unknown as WorkspaceMember[]

function renderBilling(onOpenUsage = vi.fn()) {
  render(
    <I18nProvider>
      <BillingSettings data={makeBootstrap({ members, viewerRole: 'owner' })} onOpenUsage={onOpenUsage} />
    </I18nProvider>,
  )
  return onOpenUsage
}

it('shows the free plan, the plan features and an empty invoice list like the reference page', () => {
  renderBilling()
  expect(screen.getByRole('heading', { level: 1, name: 'Billing' })).toBeInTheDocument()
  expect(screen.getByText('Free for all users · 1 user')).toBeInTheDocument()
  const features = within(screen.getByRole('region', { name: 'Business plan' })).getAllByRole('listitem')
  expect(features).toHaveLength(25)
  expect(features[0]).toHaveTextContent('Unlimited teams')
  expect(screen.getByText('No invoices yet')).toBeInTheDocument()
})

it('disables plan changes it cannot perform and says why', () => {
  renderBilling()
  for (const name of ['All plans', 'Switch plan', 'View all plans', 'Upgrade now']) {
    const button = screen.getByRole('button', { name })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('title', 'Billing is not available in Flow')
  }
  expect(screen.getByRole('note')).toHaveTextContent('Billing is not available in Flow')
})

it('shows the remaining AI credits on the usage row, and none when they cannot be read', async () => {
  renderBilling()
  expect(await screen.findByText('$3.75 remaining')).toBeVisible()
  vi.mocked(fetchAICredits).mockRejectedValueOnce(new Error('offline'))
  renderBilling()
  expect(screen.getAllByText(/remaining/)).toHaveLength(1)
})

it('opens Usage & limits from the usage row', async () => {
  const onOpenUsage = renderBilling()
  await userEvent.click(screen.getByRole('link', { name: 'Usage' }))
  expect(onOpenUsage).toHaveBeenCalledTimes(1)
})

it('shows the real empty invoice state on phones when there are no invoices', () => {
  desktopSettings.value = false
  renderBilling()
  expect(screen.getByRole('region', { name: 'Recent invoices' })).toHaveTextContent('No invoices yet')
  expect(screen.queryByText('Credits purchase')).not.toBeInTheDocument()
  desktopSettings.value = true
})
