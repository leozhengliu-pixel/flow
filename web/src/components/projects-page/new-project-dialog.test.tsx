import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { NewProjectDialog } from './new-project-dialog'

describe('NewProjectDialog', () => {
  it('keeps a selected icon color when the project name changes', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog
          open
          onClose={vi.fn()}
          onCreate={vi.fn()}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Choose icon' }))
    await user.click(screen.getByRole('button', { name: 'Color #24b4c7' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Cyan project')

    expect(screen.getByRole('button', { name: 'Choose icon' })).toHaveStyle({
      '--view-color': '#24b4c7',
    })
  })

  it('includes milestones and date precision in the create draft', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getAllByRole('button', { name: 'Add' })[0])
    await user.type(screen.getByPlaceholderText('Milestone name'), 'Launch')
    await user.click(screen.getAllByRole('button', { name: 'Add' }).at(-1)!)
    await user.click(screen.getByRole('button', { name: 'Change project target date' }))
    await user.click(screen.getByRole('tab', { name: 'Month' }))
    await user.click(screen.getAllByRole('button', { name: 'Jan' })[0])
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Roadmap')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: ['Launch'],
      targetDateResolution: 'month',
    }))
  })
})
