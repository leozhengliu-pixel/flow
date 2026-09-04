import { render, screen, within } from '@testing-library/react'
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
    await user.keyboard('{Escape}')
  })

  it('uses the shared person picker for the project lead', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog
          leads={[{ id: 'user-1', label: 'Zheng Liu', email: 'zheng@example.com' }]}
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    expect(screen.getByRole('combobox', { name: 'Set project lead' })).toBeVisible()
  })

  it('includes milestones and date precision in the create draft', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Launch')
    await user.type(screen.getByRole('textbox', { name: 'Milestone description' }), 'Launch criteria')
    await user.click(screen.getByRole('button', { name: 'Choose date' }))
    const today = new Date()
    await user.click(screen.getByRole('gridcell', { name: new Intl.DateTimeFormat('en-US', { dateStyle: 'full' }).format(today) }))
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Change project target date' }))
    await user.click(screen.getByRole('tab', { name: 'Month' }))
    await user.click(screen.getAllByRole('button', { name: 'Jan' })[0])
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Roadmap')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: ['Launch'],
      milestoneDetails: [{ name: 'Launch', description: 'Launch criteria', targetDate: `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}` }],
      targetDateResolution: 'month',
    }))
  })

  it('resets a cancelled milestone draft and ignores an empty milestone', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={vi.fn()} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Discard me')
    await user.type(screen.getByRole('textbox', { name: 'Milestone description' }), 'Temporary details')
    await user.click(screen.getByRole('button', { name: 'Discard changes' }))

    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(screen.getByRole('textbox', { name: 'Milestone name' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: 'Milestone description' })).toHaveValue('')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    expect(screen.getByText('Create milestone')).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Remove Discard me' })).not.toBeInTheDocument()
  })

  it('removes an added milestone from both create payload representations', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={vi.fn()} onCreate={onCreate} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.click(screen.getByRole('button', { name: 'Add' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Remove me')
    await user.click(screen.getByRole('button', { name: 'Add milestone' }))
    await user.click(screen.getByRole('button', { name: 'Remove Remove me' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'No milestones')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      milestones: [],
      milestoneDetails: [],
    }))
  })

  it('persists selected members and directional dependencies in the draft', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    render(
      <I18nProvider>
        <NewProjectDialog
          dependencies={[{ id: 'project-1', label: 'Blocked project', color: '#5e6ad2' }]}
          members={[{ id: 'user-1', label: 'Zheng Liu', email: 'zheng@example.com' }]}
          open
          onClose={vi.fn()}
          onCreate={onCreate}
          teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]}
        />
      </I18nProvider>,
    )

    await new Promise(resolve => setTimeout(resolve, 20))
    await user.click(screen.getByRole('combobox', { name: 'Change project members' }))
    await user.click(await screen.findByRole('option', { name: /Zheng Liu/ }))
    await user.click(screen.getByRole('combobox', { name: 'Add dependencies' }))
    await user.click(await screen.findByRole('option', { name: 'Blocked by…' }))
    await user.click(await screen.findByRole('option', { name: 'Blocked project' }))
    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Roadmap')
    await user.click(screen.getByRole('button', { name: 'Create project' }))

    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      memberIds: ['user-1'],
      dependencyIds: ['project-1'],
      dependencyRelations: [{ projectId: 'project-1', type: 'blocked_by' }],
    }))
  })

  it('confirms before discarding a changed project draft', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(
      <I18nProvider>
        <NewProjectDialog open onClose={onClose} onCreate={vi.fn()} teams={[{ id: 'team-1', label: 'Team', color: '#5e6ad2' }]} />
      </I18nProvider>,
    )

    await user.type(screen.getByRole('textbox', { name: 'Project name' }), 'Changed')
    await user.click(screen.getByRole('button', { name: 'Discard project' }))
    expect(screen.getByRole('alertdialog', { name: 'Discard changes?' })).toBeVisible()
    const confirmation = screen.getByRole('alertdialog', { name: 'Discard changes?' })
    await user.click(within(confirmation).getByRole('button', { name: 'Cancel' }))
    expect(onClose).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Discard project' }))
    await user.click(within(screen.getByRole('alertdialog', { name: 'Discard changes?' })).getByRole('button', { name: 'Discard' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
