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
    await user.click(screen.getByRole('menuitem', { name: /Labels/ }))
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
  it('shows Linear\'s project row menu: order, left-aligned rows, key hints and ▶ markers', async () => {
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [{ ...project, startDate: 'Sep 28', rawStartDate: '2026-09-28' }] }]} projectMenu={{ isFavorite: () => true, subscriptionEvents: () => [], onFavoriteChange: vi.fn(), onSubscriptionEventsChange: vi.fn(), onCreateReminder: vi.fn() }}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 40, clientY: 40 })
    const menu = screen.getByRole('menu', { name: 'Project actions' })
    expect(menu).toHaveClass('linear-menu')
    expect(menu.querySelector('input')).toBeNull()
    const rows = [...menu.querySelectorAll<HTMLElement>(':scope > [role=menuitem], :scope > [role=separator]')]
    expect(rows.map(row => row.getAttribute('role') === 'separator' ? '—' : row.dataset.menuItem)).toEqual([
      'Status', 'Priority', 'Project lead', 'Members', 'Start date…', 'Target date…', 'Labels', 'More properties', '—',
      'Copy', '—', 'Unfavorite', 'Subscribe', 'Remind me', '—', 'New comment…', '—', 'Delete',
    ])
    for (const row of rows.filter(row => row.getAttribute('role') === 'menuitem')) {
      expect(row).toHaveClass('linear-menu__row')
      expect(row.firstElementChild).toHaveClass('linear-menu__icon')
    }
    const hint = (name: string) => rows.find(row => row.dataset.menuItem === name)?.querySelector('[data-shortcut]')?.getAttribute('data-shortcut')
    expect(hint('Status')).toBe('P then S')
    expect(hint('Start date…')).toBe('Ctrl ⌥ S')
    expect(hint('Unfavorite')).toBe('⌥ F')
    expect(hint('Remind me')).toBe('⇧ H')
    expect(hint('Delete')).toBeUndefined()
    expect(rows.find(row => row.dataset.menuItem === 'Status')?.textContent).toContain('then')
    expect(rows.find(row => row.dataset.menuItem === 'Start date…')?.querySelector('.linear-menu__detail')?.textContent).toBe('Sep 28')
    const markers = rows.filter(row => row.querySelector('.linear-menu__marker'))
    expect(markers.map(row => row.dataset.menuItem)).toEqual(['Status', 'Priority', 'Project lead', 'Members', 'Labels', 'More properties', 'Copy', 'Subscribe', 'Remind me'])
    expect(markers[0].querySelector('.linear-menu__marker')?.textContent).toBe('▶')
    expect(rows.find(row => row.dataset.menuItem === 'Delete')?.className).not.toMatch(/danger/)
  })

  it('uses the shared filter field for Labels, Linear glyphs in More properties and links Slack to its settings', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]} propertyOptions={{ labels: [{ value: 'plain', label: 'Public' }] }}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /Labels/ }))
    const labels = screen.getByRole('menu', { name: 'Labels' })
    expect(labels.querySelector('.project-label-search input')).toHaveAttribute('placeholder', 'Add labels…')
    expect(labels.querySelector('.property-command-search-shortcut, kbd')).toBeNull()
    await user.keyboard('{Escape}')
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /More properties/ }))
    const more = screen.getByRole('menu', { name: 'More properties' })
    expect(more.querySelector('[data-menu-item="Dependencies"] [data-linear-glyph]')).toHaveAttribute('data-linear-glyph', 'dependencies')
    expect(more.querySelector('[data-menu-item="Configure Slack notifications…"]')).toHaveAttribute('href', expect.stringContaining('/settings/integrations/slack'))
  })

  it('opens Move only for manually ordered lists and runs every move action', async () => {
    const user = userEvent.setup()
    const onProjectAction = vi.fn()
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]} manualOrdering onProjectAction={onProjectAction}/></I18nProvider>)
    fireEvent.contextMenu(screen.getByRole('row', { name: 'Project one' }), { clientX: 40, clientY: 40 })
    await user.click(screen.getByRole('menuitem', { name: /^Move/ }))
    expect(screen.getAllByRole('menuitem').map(item => item.getAttribute('data-menu-item'))).toEqual(expect.arrayContaining(['Move to top', 'Move up', 'Move down', 'Move to bottom']))
    await user.click(screen.getByRole('menuitem', { name: /Move to top/ }))
    expect(onProjectAction).toHaveBeenCalledWith(expect.objectContaining({ id: project.id }), 'moveTop')
  })

  it('applies a status from its submenu and runs key hints inside the menu', async () => {
    const user = userEvent.setup()
    const onPropertyChange = vi.fn()
    const onFavoriteChange = vi.fn().mockResolvedValue(undefined)
    render(<I18nProvider><ProjectsDataView groups={[{ id: 'status-progress', name: 'In Progress', projects: [project] }]} onPropertyChange={onPropertyChange} projectMenu={{ isFavorite: () => false, subscriptionEvents: () => [], onFavoriteChange, onSubscriptionEventsChange: vi.fn(), onCreateReminder: vi.fn() }}/></I18nProvider>)
    const row = screen.getByRole('row', { name: 'Project one' })
    fireEvent.contextMenu(row, { clientX: 40, clientY: 40 })
    const menu = screen.getByRole('menu', { name: 'Project actions' })
    fireEvent.keyDown(menu, { key: 'p', code: 'KeyP' })
    fireEvent.keyDown(menu, { key: 's', code: 'KeyS' })
    const status = await screen.findByRole('menu', { name: 'Status' })
    expect(status.querySelector('input')).toHaveAttribute('placeholder', 'Change status…')
    expect(screen.getByRole('menuitem', { name: /In Progress/ })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('menuitem', { name: /Completed/ }))
    expect(onPropertyChange).toHaveBeenCalledWith(expect.objectContaining({ id: project.id }), 'status', 'Completed')

    fireEvent.contextMenu(row, { clientX: 40, clientY: 40 })
    fireEvent.keyDown(screen.getByRole('menu', { name: 'Project actions' }), { key: 'ƒ', code: 'KeyF', altKey: true })
    expect(onFavoriteChange).toHaveBeenCalledWith('project-1', true)
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

  it('creates into a custom workspace status from its board group', async () => {
    const user = userEvent.setup()
    const onCreateProject = vi.fn()
    render(<I18nProvider><ProjectsDataView
      groups={[{ id: 'status-building', name: '建设中', color: '#f2c94c', projects: [] }]}
      grouping="Status"
      layout="board"
      onCreateProject={onCreateProject}
      propertyOptions={{ status: [
        { value: '待规划', label: '待规划', color: '#8a8d93', statusType: 'backlog' },
        { value: '建设中', label: '建设中', color: '#f2c94c', statusType: 'started' },
      ] }}
    /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Create new project' }))
    expect(onCreateProject).toHaveBeenCalledWith('建设中')
  })

  it('uses the workspace backlog status outside status grouping and in an empty view', async () => {
    const user = userEvent.setup()
    const onCreateProject = vi.fn()
    const status = [
      { value: '待规划', label: '待规划', color: '#8a8d93', statusType: 'backlog' },
      { value: '建设中', label: '建设中', color: '#f2c94c', statusType: 'started' },
    ]
    const { rerender } = render(<I18nProvider><ProjectsDataView
      groups={[{ id: 'team-building', name: '建设中', projects: [] }]}
      grouping="Team"
      layout="board"
      onCreateProject={onCreateProject}
      propertyOptions={{ status }}
    /></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Create new project' }))
    expect(onCreateProject).toHaveBeenLastCalledWith('待规划')

    rerender(<I18nProvider><ProjectsDataView groups={[]} grouping="Status" layout="list" onCreateProject={onCreateProject} propertyOptions={{ status }}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'New project' }))
    expect(onCreateProject).toHaveBeenLastCalledWith('待规划')
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
