import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrap } from '@/test/fixtures'
import { MyIssuesPage } from './my-issues-page'

vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => true }))

it.each([['team-1', 'team'], ['2', 'priority']])('recovers a saved summary value %s without changing valid priority filters', (value, field) => {
  localStorage.clear()
  localStorage.setItem('workspace:my-issues:assigned:filters', JSON.stringify([{ id: 'old-summary', field: 'priority', fieldLabel: 'Priority', operator: 'is', value, valueLabel: 'Old summary' }]))
  const data = makeBootstrap({ issueSlas: [] })
  render(<MemoryRouter><I18nProvider><TooltipProvider><MyIssuesPage data={data} onDeleteIssues={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()}/></TooltipProvider></I18nProvider></MemoryRouter>)
  expect(screen.getByRole('link', { name: /TST-1 Test issue/ })).toBeInTheDocument()
  expect(JSON.parse(localStorage.getItem('workspace:my-issues:assigned:filters')!)[0].field).toBe(field)
})

it('persists a team filter from Teams summary and keeps the matching issue after remount', async () => {
  localStorage.clear()
  const data = makeBootstrap({ issueSlas: [] })
  const persist = vi.fn().mockResolvedValue(undefined)
  const page = <MemoryRouter><I18nProvider><TooltipProvider><MyIssuesPage data={data} onPersistFilters={persist} onDeleteIssues={vi.fn()} onOpenIssue={vi.fn()} onUpdateIssue={vi.fn()} onUpdateIssues={vi.fn()}/></TooltipProvider></I18nProvider></MemoryRouter>
  const user = userEvent.setup()
  const { unmount } = render(page)
  await user.click(screen.getByRole('button', { name: 'Open details' }))
  await user.click(screen.getByRole('tab', { name: 'Teams' }))
  await user.click(within(screen.getByRole('tabpanel', { name: 'Teams summary' })).getByRole('button'))
  await waitFor(() => expect(JSON.parse(localStorage.getItem('workspace:my-issues:assigned:filters') ?? '[]')).toEqual([expect.objectContaining({ field: 'team', fieldLabel: 'Team', value: 'team-1' })]))
  expect(screen.queryByText('No teams used')).not.toBeInTheDocument()
  unmount()
  render(page)
  expect(screen.getByRole('link', { name: /TST-1 Test issue/ })).toBeInTheDocument()
})
