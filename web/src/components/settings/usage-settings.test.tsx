import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { fetchAICredits } from '@/lib/ai-credits-api'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { SpendLimitSettings, WorkspaceMember } from '@/types/flow'

const api = vi.hoisted(() => ({ updateWorkspacePreferences: vi.fn().mockResolvedValue({}) }))
vi.mock('@/lib/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
vi.mock('@/lib/ai-credits-api', () => ({ fetchAICredits: vi.fn() }))

import { UsageSettings } from './usage-settings'

const members = [viewer, teammate].map((user) => ({ user, role: 'member', status: 'active', joinedAt: '2026-01-01T00:00:00Z' })) as unknown as WorkspaceMember[]

function renderUsage(view?: 'analytics' | 'spend-limits' | 'user-limits' | 'loop-limits', spendLimits?: SpendLimitSettings) {
  const onOpenView = vi.fn()
  const onReload = vi.fn().mockResolvedValue(undefined)
  const data = makeBootstrap({
    members,
    loops: [{ id: 'loop-1', name: 'Weekly digest', status: 'published', runCount30d: 7 }] as never,
    viewerRole: 'owner',
    workspace: { id: 'w', name: 'W', urlKey: 'w', createdAt: '2026-03-17T10:00:00Z' } as never,
    workspaceSettings: { ...makeBootstrap().workspaceSettings, spendLimits } as never,
  })
  render(
    <MemoryRouter><I18nProvider>
      <UsageSettings data={data} view={view} onOpenView={onOpenView} onReload={onReload} />
    </I18nProvider></MemoryRouter>,
  )
  return { onOpenView, onReload }
}

beforeEach(() => {
  api.updateWorkspacePreferences.mockClear()
  vi.mocked(fetchAICredits).mockResolvedValue({ includedMicros: 10_000_000, spentMicros: 510_000, remainingMicros: 9_490_000 })
})

it('lists real credits and spend limits without purchase controls and opens the sub-pages', async () => {
  const { onOpenView } = renderUsage()
  expect(screen.getByRole('heading', { level: 1, name: 'Usage & limits' })).toBeInTheDocument()
  expect(await screen.findByText('$9.49 remaining')).toBeInTheDocument()
  expect(screen.getByText('No limits set')).toBeInTheDocument()
  expect(screen.getByText('7 runs in the last 30 days')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Manage' })).toHaveAttribute('href', '/w/settings/loops')
  expect(screen.queryByRole('button', { name: 'Manage' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Add credits' })).not.toBeInTheDocument()
  expect(screen.queryByText('Automatic reload')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /Usage explorer/ }))
  await userEvent.click(screen.getByRole('button', { name: /Spend limits/ }))
  expect(onOpenView.mock.calls).toEqual([['analytics'], ['spend-limits']])
})

it('saves a workspace limit and a reset frequency', async () => {
  const { onReload } = renderUsage('spend-limits')
  expect(screen.getByText('Daily at 6 AM (', { exact: false })).toHaveTextContent('Monthly on the 17th')
  await userEvent.click(screen.getAllByRole('button', { name: 'Set limit' })[0])
  const dialog = screen.getByRole('dialog')
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Limit' }), '$1,250.5')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(api.updateWorkspacePreferences).toHaveBeenCalledWith({ spendLimits: { workspaceLimit: 1250.5 } }))
  expect(onReload).toHaveBeenCalled()
})

it('rejects text that is not a dollar amount', async () => {
  renderUsage('spend-limits')
  await userEvent.click(screen.getAllByRole('button', { name: 'Set limit' })[1])
  const dialog = screen.getByRole('dialog')
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Limit' }), 'lots')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  expect(within(dialog).getByRole('alert')).toHaveTextContent('Enter a dollar amount')
  expect(api.updateWorkspacePreferences).not.toHaveBeenCalled()
})

it('removes an existing limit by saving zero', async () => {
  renderUsage('spend-limits', { userLimit: 40 })
  expect(screen.getByText('$40 per period')).toBeInTheDocument()
  await userEvent.click(screen.getAllByRole('button', { name: 'Set limit' })[1])
  await userEvent.click(screen.getByRole('button', { name: 'Remove limit' }))
  await waitFor(() => expect(api.updateWorkspacePreferences).toHaveBeenCalledWith({ spendLimits: { userLimit: 0 } }))
})

it('edits the reset schedule', async () => {
  renderUsage('spend-limits', { weeklyResetDay: 1, dailyResetHour: 13, monthlyResetDay: 3 })
  expect(screen.getByText('Daily at 1 PM', { exact: false })).toHaveTextContent('Weekly on Monday · Monthly on the 3rd')
  await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = screen.getByRole('dialog')
  expect(within(dialog).getByRole('combobox', { name: 'Weekly reset day' })).toHaveTextContent('Monday')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(api.updateWorkspacePreferences).toHaveBeenCalledWith({ spendLimits: { dailyResetHour: 13, weeklyResetDay: 1, monthlyResetDay: 3 } }))
})

it('sets a per-member override and filters the member list', async () => {
  renderUsage('user-limits', { userLimit: 100 })
  expect(screen.getByRole('heading', { level: 1, name: 'Usage' })).toBeInTheDocument()
  expect(screen.getByText('Viewer')).toBeInTheDocument()
  expect(screen.getByText('Teammate')).toBeInTheDocument()
  await userEvent.type(screen.getByRole('textbox', { name: 'Search by name…' }), 'team')
  expect(screen.queryByText('Viewer')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Set limit: Teammate' }))
  const dialog = screen.getByRole('dialog')
  expect(within(dialog).getByText('Overrides the default user limit of $100')).toBeInTheDocument()
  await userEvent.type(within(dialog).getByRole('textbox', { name: 'Limit' }), '25')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(api.updateWorkspacePreferences).toHaveBeenCalledWith({ spendLimits: { userOverrides: { 'user-2': 25 } } }))
})

it('lists loops on the per-loop page and the empty explorer', async () => {
  renderUsage('loop-limits')
  expect(screen.getByText('Weekly digest')).toBeInTheDocument()
})

it('shows the empty usage explorer with its filters', () => {
  renderUsage('analytics')
  expect(screen.getByText('0 results')).toBeInTheDocument()
  expect(screen.getByText('total spend')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Export CSV' })).toBeDisabled()
  expect(screen.getByRole('combobox', { name: 'Measure' })).toHaveTextContent('Spend')
  expect(screen.getByRole('combobox', { name: 'Group by' })).toHaveTextContent('Team')
})
