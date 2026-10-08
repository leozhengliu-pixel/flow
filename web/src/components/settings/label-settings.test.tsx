import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { mergeLabels, updateWorkspaceLabel } from '@/lib/api'
import type { BootstrapData, LabelResourceType } from '@/types/flow'
import { DomainLabelsSettings } from './domain-settings'
import { canMergeLabels, matchesLabelFilters, mergeTarget, visibleLabelColumns } from './label-settings-model'

vi.mock('@/lib/api', async original => ({ ...await original<typeof import('@/lib/api')>(), mergeLabels: vi.fn(), updateWorkspaceLabel: vi.fn() }))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
beforeEach(() => { vi.clearAllMocks(); localStorage.clear(); localStorage.setItem('flow:locale', 'en-US') })

function page(resourceType: LabelResourceType, overrides: Partial<BootstrapData> = {}, onReload = vi.fn().mockResolvedValue(undefined)) {
  return render(<I18nProvider><DomainLabelsSettings resourceType={resourceType} onReload={onReload} data={makeBootstrap({ labels: [], labelGroups: [], initiatives: [], ...overrides } as never)}/></I18nProvider>)
}

describe('label settings structure', () => {
  it('shows Linear\'s toolbar and empty state on initiative labels without a scope selector', () => {
    page('initiative')
    expect(screen.getByRole('heading', { name: 'Initiative labels' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'New group' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'New label' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Filter by name…' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add filter' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Display options' })).toBeVisible()
    expect(screen.queryByRole('combobox', { name: /workspace/i })).not.toBeInTheDocument()
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('No labels found')
    expect(screen.queryByRole('button', { name: /^Order by/ })).not.toBeInTheDocument()
  })

  it('offers only ordering and archived options for initiative labels', async () => {
    page('initiative', { labels: [{ id: 'l1', name: 'Strategy', color: '#123456', resourceType: 'initiative' }] } as never)
    await userEvent.setup().click(screen.getByRole('button', { name: 'Display options' }))
    expect(await screen.findByRole('combobox', { name: 'Ordering' })).toHaveTextContent('Name')
    expect(screen.queryByRole('combobox', { name: 'Grouping' })).not.toBeInTheDocument()
    expect(screen.queryByRole('switch', { name: 'Show team labels' })).not.toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Show archived' })).toHaveAttribute('aria-checked', 'false')
  })

  it('offers grouping and team labels for project labels', async () => {
    page('project', { labels: [{ id: 'p1', name: 'Platform', color: '#123456', resourceType: 'project' }] } as never)
    expect(screen.getByRole('button', { name: 'Order by Name, sorted ascending' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Order by Projects' })).toBeVisible()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Display options' }))
    expect(await screen.findByRole('combobox', { name: 'Grouping' })).toHaveTextContent('No grouping')
    expect(screen.getByRole('switch', { name: 'Show team labels' })).toBeVisible()
  })

  it('shows No matching labels when the name filter hides every label', async () => {
    page('issue', { labels: [{ id: 'b', name: 'Bug', color: '#ff0000', resourceType: 'issue' }] } as never)
    await userEvent.setup().type(screen.getByRole('textbox', { name: 'Filter by name…' }), 'zzz')
    expect(screen.getByRole('status')).toHaveTextContent('No matching labels')
  })

  it('shows archived labels with an Archived column when Show archived is on', async () => {
    const user = userEvent.setup()
    page('project', { labels: [
      { id: 'a', name: 'Active', color: '#123456', resourceType: 'project' },
      { id: 'r', name: 'Retired', color: '#654321', resourceType: 'project', archivedAt: '2026-09-01T00:00:00.000Z' },
    ] } as never)
    expect(screen.queryByDisplayValue('Retired')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(await screen.findByRole('switch', { name: 'Show archived' }))
    expect(screen.getByDisplayValue('Retired')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Order by Archived' })).toBeInTheDocument()
  })

  it('orders by usage from the column header, most used first', async () => {
    page('issue', { labels: [
      { id: 'a', name: 'Alpha', color: '#111111', resourceType: 'issue', issueCount: 1 },
      { id: 'b', name: 'Beta', color: '#222222', resourceType: 'issue', issueCount: 5 },
    ] } as never)
    const names = () => screen.getAllByRole('textbox').map(input => (input as HTMLInputElement).value).filter(value => value === 'Alpha' || value === 'Beta')
    expect(names()).toEqual(['Alpha', 'Beta'])
    await userEvent.setup().click(screen.getByRole('button', { name: 'Order by Issues' }))
    expect(names()).toEqual(['Beta', 'Alpha'])
    expect(screen.getByRole('button', { name: 'Order by Issues, sorted descending' })).toBeVisible()
  })

  it('shows the Rules column with SLA and triage rule counts', () => {
    page('issue', {
      settings: { sla: { enabled: true } },
      labels: [{ id: 'b', name: 'Bug', color: '#ff0000', resourceType: 'issue' }],
      slaRules: [{ id: 'sla', name: 'Bugs', teamIds: [], filters: { label: 'b' }, targetMinutes: 60, pauseStatuses: [], businessHours: false, enabled: true }],
      triageRoutingRules: [{ id: 'tr', teamId: 'team-1', name: 'Route bugs', position: 0, enabled: true, conditions: { labelId: 'b' }, responsibilityId: 'x', labelIds: [] }],
    } as never)
    expect(screen.getByRole('button', { name: 'Order by Rules' })).toBeVisible()
    expect(screen.getByText('2')).toBeVisible()
  })
})

describe('label row menus', () => {
  it('offers Convert to label group for an unused label and hides View labeled', async () => {
    page('initiative', { labels: [{ id: 'l1', name: 'Strategy', color: '#123456', resourceType: 'initiative', issueCount: 0 }] } as never)
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open Strategy menu' }))
    expect(await screen.findByRole('menuitem', { name: 'Convert to label group' })).toBeVisible()
    expect(screen.queryByRole('menuitem', { name: 'View labeled initiatives' })).not.toBeInTheDocument()
  })

  it('links a used initiative label to its initiative label page', async () => {
    page('initiative', { labels: [{ id: 'l1', name: 'Strategy', color: '#123456', resourceType: 'initiative', issueCount: 2 }] } as never)
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open Strategy menu' }))
    const item = await screen.findByRole('menuitem', { name: 'View labeled initiatives' })
    expect(item).toHaveAttribute('href', '/workspace/initiative-label/Strategy')
    expect(screen.queryByRole('menuitem', { name: 'Convert to label group' })).not.toBeInTheDocument()
  })

  it('archives an unused label without a confirmation dialog', async () => {
    vi.mocked(updateWorkspaceLabel).mockResolvedValue({ id: 'l1', name: 'Strategy', color: '#123456' })
    page('project', { labels: [{ id: 'l1', name: 'Strategy', color: '#123456', resourceType: 'project', issueCount: 0 }] } as never)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Open Strategy menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Archive…' }))
    await waitFor(() => expect(updateWorkspaceLabel).toHaveBeenCalledWith('l1', expect.objectContaining({ archivedAt: expect.any(String) })))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('merges selected labels into the most used one', async () => {
    vi.mocked(mergeLabels).mockResolvedValue({ id: 'b', name: 'Bug', color: '#ff0000' })
    page('issue', { labels: [
      { id: 'b', name: 'Bug', color: '#ff0000', resourceType: 'issue', issueCount: 4 },
      { id: 'd', name: 'Defect', color: '#00ff00', resourceType: 'issue', issueCount: 1 },
    ] } as never)
    const user = userEvent.setup()
    for (const checkbox of screen.getAllByRole('checkbox', { name: 'Select label' })) await user.click(checkbox)
    await user.click(screen.getByRole('button', { name: /Actions/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Merge labels…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('heading', { name: 'Merge 2 labels?' })).toBeVisible()
    expect(dialog).toHaveTextContent('These labels will be merged into Bug')
    await user.click(within(dialog).getByRole('button', { name: 'Merge' }))
    await waitFor(() => expect(mergeLabels).toHaveBeenCalledWith('b', ['d']))
  })
})

describe('label settings model', () => {
  it('drops columns like Linear on narrow screens', () => {
    expect(visibleLabelColumns({ resourceType: 'issue', rulesVisible: true, showTeamColumn: true, showArchived: true, width: 1440 })).toEqual(['title', 'description', 'team', 'rules', 'usage', 'lastAppliedAt', 'createdAt', 'archivedAt'])
    expect(visibleLabelColumns({ resourceType: 'initiative', rulesVisible: true, showTeamColumn: false, showArchived: false, width: 1440 })).toEqual(['title', 'description', 'usage', 'lastAppliedAt', 'createdAt'])
    expect(visibleLabelColumns({ resourceType: 'issue', rulesVisible: true, showTeamColumn: false, showArchived: false, width: 600 })).toEqual(['title'])
  })

  it('filters by Never applied and relative last-applied dates', () => {
    const now = new Date('2026-10-07T12:00:00Z')
    const recent = { id: 'r', name: 'Recent', color: '#000', lastAppliedAt: '2026-10-05T12:00:00Z' }
    const unused = { id: 'u', name: 'Unused', color: '#000' }
    const base = { teams: [], teamOperator: 'is' as const, lastAppliedOperator: 'after' as const }
    const context = (usage: number) => ({ usage, teamUsage: () => new Map<string, number>(), now })
    expect(matchesLabelFilters(unused, { ...base, lastApplied: 'never' }, context(0))).toBe(true)
    expect(matchesLabelFilters(recent, { ...base, lastApplied: 'never' }, context(1))).toBe(false)
    expect(matchesLabelFilters(recent, { ...base, lastApplied: '7' }, context(1))).toBe(true)
    expect(matchesLabelFilters(recent, { ...base, lastApplied: '1' }, context(1))).toBe(false)
    expect(matchesLabelFilters(recent, { ...base, lastApplied: '1', lastAppliedOperator: 'before' }, context(1))).toBe(true)
  })

  it('merges into the workspace label, then the most used one, and only within one group', () => {
    const team = { id: 't', name: 'Team copy', color: '#000', scope: 'team-1', issueCount: 9 }
    const workspace = { id: 'w', name: 'Workspace', color: '#000', issueCount: 1 }
    expect(mergeTarget([team, workspace], label => label.issueCount ?? 0).id).toBe('w')
    expect(canMergeLabels([team, workspace])).toBe(true)
    expect(canMergeLabels([workspace, { ...team, groupId: 'g' }])).toBe(false)
    expect(canMergeLabels([workspace])).toBe(false)
  })
})
