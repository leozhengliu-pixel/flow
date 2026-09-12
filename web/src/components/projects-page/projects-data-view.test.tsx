import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtuosoMockContext } from 'react-virtuoso'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { MILESTONE_PATH_LENGTH, milestoneProgressLength } from '@/components/issue/milestone-progress'

import { ProjectsDataView, type ProjectPageItem } from './projects-data-view'

const project: ProjectPageItem = {
  id: 'project-1', name: 'Project one', health: 'on-track', priority: 'high', issueCount: 2, progress: 40, status: 'In Progress',
}

describe('ProjectsDataView project menu', () => {
  it('reuses grouped labels in the context menu and replaces a peer before saving', async () => {
    const user = userEvent.setup()
    const onPropertyChange = vi.fn()
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [{ ...project, labelIds: ['alpha', 'plain'] }] }]} propertyOptions={{ labels: [{ value: 'alpha', label: 'Alpha', group: 'Delivery', groupId: 'delivery' }, { value: 'beta', label: 'Beta', group: 'Delivery', groupId: 'delivery' }, { value: 'plain', label: 'Public' }] }} onPropertyChange={onPropertyChange}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 40, clientY: 40 })
    await user.hover(screen.getByRole('menuitem', { name: /Labels/ }))
    await user.click(screen.getByRole('option', { name: 'Delivery Alpha' }))
    await user.click(screen.getByRole('option', { name: 'Beta' }))
    expect(onPropertyChange).toHaveBeenCalledWith(expect.objectContaining({ id: project.id }), 'labels', 'plain,beta')
  })
  it('supports the same project in multiple virtualized groups without duplicate keys', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      render(<I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 36 }}><ProjectsDataView groups={Array.from({ length: 50 }, (_, index) => ({ id: `group-${index}`, name: `Group ${index}`, projects: [project] }))}/></VirtuosoMockContext.Provider></I18nProvider>)
      await waitFor(() => expect(screen.getAllByRole('row', { name: 'Project one' }).length).toBeGreaterThan(1))
      expect(errors.mock.calls.flat().join(' ')).not.toContain('same key')
    } finally { errors.mockRestore() }
  })
  it('opens nested context menus to the left when the row menu is against the right edge', async () => {
    const user = userEvent.setup()
    const box = (x: number, y: number, w: number, h: number) => ({ x, y, left: x, top: y, right: x + w, bottom: y + h, width: w, height: h, toJSON() { return this } })
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 900, clientY: 80 })
    const menu = document.querySelector('.lp-project-context') as HTMLElement
    vi.spyOn(menu, 'getBoundingClientRect').mockReturnValue(box(window.innerWidth - 240, 80, 232, 420))
    const status = screen.getByRole('menuitem', { name: /Status/ })
    vi.spyOn(status, 'getBoundingClientRect').mockReturnValue(box(window.innerWidth - 228, 124, 220, 30))
    await user.hover(status)
    expect(document.querySelector('.lp-project-context__nested')).toHaveClass('is-start')
  })

  it('keeps hidden table columns in the subgrid so later columns do not shift', () => {
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]}/></I18nProvider>)

    const initiativeColumn = document.querySelector('.lp-project-table__header [role="columnheader"][data-column-hidden="true"]')
    const targetDateColumn = [...document.querySelectorAll('.lp-project-table__header [role="columnheader"]')].find(column => column.textContent?.includes('Target date'))
    expect(initiativeColumn).toBeTruthy()
    expect(initiativeColumn).not.toHaveAttribute('hidden')
    expect(targetDateColumn).toBeTruthy()
    expect(targetDateColumn).not.toHaveAttribute('data-column-hidden')
  })

  it('persists favorite, subscription, and reminder actions through the API integration', async () => {
    const user = userEvent.setup()
    const onFavoriteChange = vi.fn().mockResolvedValue(undefined)
    const onSubscriptionEventsChange = vi.fn().mockResolvedValue(undefined)
    const onCreateReminder = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><ProjectsDataView
      groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]}
      projectMenu={{ isFavorite: () => false, subscriptionEvents: () => [], onFavoriteChange, onSubscriptionEventsChange, onCreateReminder }}
    /></I18nProvider>)

    const row = screen.getByRole('row', { name: 'Project one' })
    fireEvent.contextMenu(row, { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /Favorite/ }))
    expect(onFavoriteChange).toHaveBeenCalledWith('project-1', true)

    fireEvent.contextMenu(row, { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /Subscribe/ }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: /An issue is added/ }))
    expect(onSubscriptionEventsChange).toHaveBeenCalledWith('project-1', ['issueAdded'])

    fireEvent.contextMenu(row, { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /Remind me/ }))
    await user.click(screen.getByRole('menuitem', { name: /Tomorrow/ }))
    await waitFor(() => expect(onCreateReminder).toHaveBeenCalledWith('project-1', expect.any(String)))
  })

  it('renders board cards with wrapping titles, singular issue counts, and a plus-only add control', () => {
    render(<I18nProvider><ProjectsDataView
      groups={[{
        id: 'status-progress',
        name: 'In Progress',
        projects: [{
          ...project,
          name: 'Favorite Perf Project',
          issueCount: 1,
          summary: 'A long summary that should sit above the issue count.',
          targetDate: 'Aug 2026',
          rawTargetDate: '2026-08-01',
          milestone: '车商城316迭代',
          milestoneDate: 'Aug 15',
          milestoneProgress: 81,
          rawMilestoneDate: '2020-08-15',
        }],
      }]}
      grouping="Status"
      layout="board"
      visibleProperties={['Summary', 'Priority', 'Status', 'Health', 'Lead', 'Target date', 'Issues']}
    /></I18nProvider>)

    const card = screen.getByRole('button', { name: 'Favorite Perf Project' })
    expect(screen.queryByRole('combobox', { name: 'In Progress' })).not.toBeInTheDocument()
    expect(card).toHaveTextContent('A long summary that should sit above the issue count.')
    expect(card).toHaveTextContent('Aug 2026')
    expect(card).toHaveTextContent('车商城316迭代')
    expect(card).toHaveTextContent('Aug 15')
    const milestoneIcon = card.querySelector('.milestone-progress-icon')
    expect(milestoneIcon).toHaveClass('is-progress')
    expect(milestoneIcon).toHaveClass('is-overdue')
    expect(milestoneIcon).toHaveAttribute('aria-label', 'Milestone 车商城316迭代. Progress: 81%.')
    const length = milestoneProgressLength(81)
    expect(milestoneIcon?.querySelector('.is-value')).toHaveAttribute('stroke-dasharray', `${length} ${MILESTONE_PATH_LENGTH - length}`)
    expect(screen.getByRole('button', { name: '1 issue' })).toBeInTheDocument()
    expect(screen.queryByText('1 issues')).not.toBeInTheDocument()
    const add = screen.getByRole('button', { name: 'Add new project', hidden: true })
    expect(add).toHaveClass('lp-project-board__add')
    expect(add).toHaveTextContent('')
    expect(screen.getByRole('button', { name: 'Open menu' })).toBeInTheDocument()
  })

  it('hides the board summary when the Summary display property is disabled', () => {
    render(<I18nProvider><ProjectsDataView
      groups={[{ id: 'status-progress', name: 'In Progress', projects: [{ ...project, summary: 'Hidden board summary' }] }]}
      layout="board"
      visibleProperties={['Priority', 'Health', 'Lead', 'Target date', 'Issues']}
    /></I18nProvider>)

    expect(screen.queryByText('Hidden board summary')).not.toBeInTheDocument()
  })

  it('keeps Status on board cards when the board is grouped by another property', () => {
    render(<I18nProvider><ProjectsDataView
      groups={[{ id: 'team-int', name: 'Integration Workspace', projects: [project] }]}
      grouping="Team"
      layout="board"
      visibleProperties={['Status']}
    /></I18nProvider>)

    expect(screen.getByRole('combobox', { name: 'In Progress' })).toBeInTheDocument()
  })

  it('keeps empty board columns instead of the list empty state', () => {
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-backlog', name: 'Backlog', projects: [] }, { id: 'status-progress', name: 'In Progress', projects: [] }]} layout="board"/></I18nProvider>)
    expect(screen.getByRole('list', { name: undefined })).toBeTruthy()
    expect(screen.getByLabelText('Backlog')).toBeInTheDocument()
    expect(screen.getByLabelText('In Progress')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'No projects' })).not.toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Add new project', hidden: true })).toHaveLength(2)
  })

  it('hides a board column and restores it from Hidden columns', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-backlog', name: 'Backlog', projects: [project] }, { id: 'status-progress', name: 'In Progress', projects: [] }]} layout="board"/></I18nProvider>)
    await user.click(screen.getAllByRole('button', { name: 'Open menu' })[0])
    await user.click(screen.getByRole('menuitem', { name: 'Hide column' }))
    expect(screen.queryByLabelText('Backlog')).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Hidden columns' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Backlog/ }))
    expect(screen.getByLabelText('Backlog')).toBeInTheDocument()
  })

  it('selects every project in a board column from the column menu', async () => {
    const user = userEvent.setup()
    const onSelectionChange = vi.fn()
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project, { ...project, id: 'project-2', name: 'Project two' }] }]} layout="board" onSelectionChange={onSelectionChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Select all in column' }))
    expect(onSelectionChange).toHaveBeenCalledWith(['project-1', 'project-2'])
  })

  it('renders board loading skeletons and a retryable error state', () => {
    const { rerender } = render(<I18nProvider><ProjectsDataView groups={[]} layout="board" loading/></I18nProvider>)
    expect(screen.getByLabelText('Loading projects')).toBeInTheDocument()
    expect(document.querySelectorAll('.lp-project-state__skel-card').length).toBeGreaterThan(0)
    const onRetry = vi.fn()
    rerender(<I18nProvider><ProjectsDataView error="Timed out" groups={[]} layout="board" onRetry={onRetry}/></I18nProvider>)
    expect(screen.getByRole('alert')).toHaveTextContent("Projects couldn't load")
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })
})
