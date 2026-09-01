import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { ProjectsDataView, type ProjectPageItem } from './projects-data-view'

const project: ProjectPageItem = {
  id: 'project-1', name: 'Project one', health: 'on-track', priority: 'high', issueCount: 2, progress: 40, status: 'In Progress',
}

describe('ProjectsDataView project menu', () => {
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
