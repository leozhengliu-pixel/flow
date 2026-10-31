import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { listIssueRecordGroups } from '@/lib/api'
import type { SavedView } from '@/types/flow'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewDetailsPanel } from './saved-view-panels'

vi.mock('@/lib/api', async original => ({ ...await original<object>(), listIssueRecordGroups: vi.fn() }))
const data = makeBootstrap()
const view = { id: 'v', name: 'Review queue', scope: 'personal', ownerId: data.viewer.id } as SavedView
beforeEach(() => { localStorage.clear(); vi.clearAllMocks() })

it('shows real owner and visibility, includes unassigned issues, and drills into the selected assignee', () => {
  const select = vi.fn()
  const rows = [makeIssue({ id: 'a', assignee: undefined }), makeIssue({ id: 'b', assignee: data.viewer })].map(issue => issueToExplorerRow(issue, data.workspace.urlKey))
  render(<I18nProvider><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={select} onToggleFavorite={vi.fn()} rows={rows} users={data.users} view={view} workspace={data.workspace}/></I18nProvider>)
  const panel = screen.getByRole('complementary', { name: 'View sidebar' })
  expect(within(panel).getByText('Visibility')).toBeVisible()
  expect(within(panel).getByText('Personal')).toBeVisible()
  expect(within(panel).getByText('Owner')).toBeVisible()
  fireEvent.click(within(panel).getByRole('button', { name: 'No assignee 1' }))
  expect(select).toHaveBeenCalledWith('assignee', '', 'No assignee', undefined)
  fireEvent.keyDown(within(panel).getByRole('tab', { name: 'Assignees' }), { key: 'ArrowRight' })
  expect(within(panel).getByRole('tab', { name: 'Labels' })).toHaveAttribute('aria-selected', 'true')
})

it('uses complete server counts instead of only the loaded rows and retries a failed summary', async () => {
  vi.mocked(listIssueRecordGroups).mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ groups: [{ value: '', count: 250 }] })
  render(<I18nProvider><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={vi.fn()} onToggleFavorite={vi.fn()} rows={[]} users={data.users} view={view} workspace={data.workspace} summaryQuery={{ teamId: 'team-1', filter: { and: [] }, groupBy: 'status' }}/></I18nProvider>)
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('button', { name: 'No assignee 250' })).toBeVisible()
  await waitFor(() => expect(listIssueRecordGroups).toHaveBeenLastCalledWith(expect.objectContaining({ teamId: 'team-1', groupBy: 'assignee', filter: { and: [] } }), expect.any(AbortSignal)))
})
