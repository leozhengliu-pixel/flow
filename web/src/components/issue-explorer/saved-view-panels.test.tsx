import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { listIssueRecordGroups } from '@/lib/api'
import type { SavedView } from '@/types/flow'
import { issueToExplorerRow } from './issue-explorer-model'
import { SavedViewDetailsPanel } from './saved-view-panels'
import { IssueRowActionContext } from '@/components/my-issues/use-issue-row-actions'
import type { BootstrapData } from '@/types/flow'

vi.mock('@/lib/api', async original => ({ ...await original<object>(), listIssueRecordGroups: vi.fn() }))
const data = makeBootstrap()
const view = { id: 'v', name: 'Review queue', scope: 'personal', ownerId: data.viewer.id } as SavedView
beforeEach(() => { localStorage.clear(); vi.clearAllMocks() })
afterEach(() => vi.restoreAllMocks())

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

it.each([false, true])('shows Teams even for a single team and drills into it (phone: %s)', phone => {
  const matchMedia = window.matchMedia.bind(window)
  vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ ...matchMedia(query), matches: phone && query === '(max-width: 767px)' }))
  const select = vi.fn()
  const issue = makeIssue({ id: 'one' })
  render(<I18nProvider><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={select} onToggleFavorite={vi.fn()} rows={[issueToExplorerRow(issue, data.workspace.urlKey)]} teams={[issue.team]} users={data.users} view={view} workspace={data.workspace}/></I18nProvider>)
  fireEvent.click(screen.getByRole('tab', { name: 'Teams' }))
  fireEvent.click(screen.getByRole('button', { name: `${issue.team.name} 1` }))
  expect(select).toHaveBeenCalledWith('team', issue.team.id, issue.team.name, undefined)
})

it('uses complete server estimate buckets, including unestimated issues, for breakdown totals', async () => {
  const team = data.teams[0]
  const withEstimates = { ...data, teamSettings: { ...data.teamSettings, [team.id]: { teamId: team.id, estimateType: 'fibonacci', estimateCountUnestimated: true } } } as BootstrapData
  vi.mocked(listIssueRecordGroups).mockImplementation(async query => ({ groups: query.groupBy === 'estimate' ? [{ value: '5', count: 20 }, { value: '', count: 3 }] : [{ value: '', count: 23 }] }))
  render(<I18nProvider><IssueRowActionContext.Provider value={{ data: withEstimates, onUpdateIssue: vi.fn() }}><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={vi.fn()} onToggleFavorite={vi.fn()} rows={[]} teams={data.teams} users={data.users} view={view} workspace={data.workspace} summaryQuery={{ teamId: team.id, filter: { and: [] } }}/></IssueRowActionContext.Provider></I18nProvider>)
  const item = await screen.findByRole('button', { name: 'No assignee 23' })
  await waitFor(() => expect(item.querySelector('b')).toHaveTextContent('103'))
  expect(item.querySelector('b .estimate-icon')).not.toBeNull()
  expect(listIssueRecordGroups).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'estimate', filter: { and: [{ and: [] }, { field: 'assignee', operator: 'isEmpty', values: [''] }, { field: 'teamId', operator: 'in', values: [team.id] }] } }), expect.any(AbortSignal))
})

it('keeps a plain count when the summary bucket contains no estimate-enabled team issues', async () => {
  const team = data.teams[0]
  const withEstimates = { ...data, teamSettings: { ...data.teamSettings, [team.id]: { teamId: team.id, estimateType: 'fibonacci' } } } as BootstrapData
  vi.mocked(listIssueRecordGroups).mockImplementation(async query => ({ groups: query.groupBy === 'estimate' ? [] : [{ value: '', count: 7 }] }))
  render(<I18nProvider><IssueRowActionContext.Provider value={{ data: withEstimates, onUpdateIssue: vi.fn() }}><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={vi.fn()} onToggleFavorite={vi.fn()} rows={[]} teams={data.teams} users={data.users} view={view} workspace={data.workspace} summaryQuery={{ filter: { and: [] } }}/></IssueRowActionContext.Provider></I18nProvider>)
  const item = await screen.findByRole('button', { name: 'No assignee 7' })
  await waitFor(() => expect(listIssueRecordGroups).toHaveBeenCalledWith(expect.objectContaining({ groupBy: 'estimate' }), expect.any(AbortSignal)))
  expect(item.querySelector('b')).toHaveTextContent('7')
  expect(item.querySelector('b .estimate-icon')).toBeNull()
})

it('names deactivated, SCIM-provisioned and unknown assignees instead of showing their IDs', async () => {
  const suspended = { ...data.users[1], id: 'usr_1789976810279757001', displayName: 'Dana Former', name: 'Dana Former', active: false, avatarUrl: 'https://img.example.test/dana.png' }
  const scim = { ...data.users[1], id: 'usr_1789976810279757002', displayName: '', name: '', email: 'sam@example.test', username: undefined }
  const users = [...data.users, suspended, scim]
  vi.mocked(listIssueRecordGroups).mockResolvedValue({ groups: [{ value: suspended.id, count: 5 }, { value: scim.id, count: 4 }, { value: 'usr_1789976810279757008', count: 3 }] })
  render(<I18nProvider><SavedViewDetailsPanel inline favorite={false} menu={null} onClose={vi.fn()} onSummaryItemSelect={vi.fn()} onToggleFavorite={vi.fn()} rows={[]} summaryOptions={{ assignee: [] } as never} users={users} view={view} workspace={data.workspace} summaryQuery={{ filter: { and: [] } }}/></I18nProvider>)
  const panel = screen.getByRole('complementary', { name: 'View sidebar' })
  const dana = await within(panel).findByRole('button', { name: 'Dana Former 5' })
  expect(dana.querySelector('img')).toHaveAttribute('src', 'https://img.example.test/dana.png')
  expect(within(panel).getByRole('button', { name: 'sam@example.test 4' })).toBeVisible()
  expect(within(panel).getByRole('button', { name: 'Unknown user 3' })).toBeVisible()
  expect(panel.textContent).not.toContain('usr_')
})
