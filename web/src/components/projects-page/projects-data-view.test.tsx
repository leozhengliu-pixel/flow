import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VirtuosoMockContext } from 'react-virtuoso'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

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
})
