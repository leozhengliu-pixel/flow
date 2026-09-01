import type { ComponentProps, ReactNode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, project } from '@/test/fixtures'

vi.mock('@/components/property/property-menu', () => ({
  PropertyMenu: ({ label, onChange, options, selectedId, value }: { label: string; value: string; selectedId?: string; options: Array<{ id: string }>; onChange: (id: string) => void }) => <button aria-label={`Change ${label}`} onClick={() => onChange(options.find(option => option.id !== selectedId)?.id ?? '')}>{value}</button>,
}))
vi.mock('@/components/views/view-icon-picker', () => ({ ViewIconPicker: ({ onChange }: { onChange: (value: { icon: string; color: string }) => void }) => <button aria-label="Change project icon" onClick={() => onChange({ icon: 'Cube', color: '#123456' })}>Icon</button> }))
vi.mock('@/components/issue/issue-description-editor', () => ({ IssueDescriptionEditor: ({ ariaLabel, onBlur, onChange, value }: { ariaLabel: string; value: string; onBlur: () => void; onChange: (value: { markdown: string }) => void }) => <textarea aria-label={ariaLabel} defaultValue={value} onBlur={onBlur} onChange={event => onChange({ markdown: event.target.value })}/> }))
vi.mock('@/components/projects-page/project-target-date-picker', () => ({ ProjectDatePicker: ({ children, label, onChange }: { children: ReactNode; label: string; onChange: (value: string) => void }) => <button aria-label={label} onClick={() => onChange('2026-09-01')}>{children}</button> }))

import { ProjectOverview } from './project-overview'

describe('project overview workflow', () => {
  it('edits project fields, navigates updates, and creates scoped resources', async () => {
    const user = userEvent.setup()
    const data = makeBootstrap()
    const save = vi.fn().mockResolvedValue(undefined)
    const onTabChange = vi.fn()
    const onCreateResource = vi.fn().mockResolvedValue({ id: 'resource-1' })
    const onCreateMilestone = vi.fn().mockResolvedValue({ id: 'milestone-1', name: 'Launch' })
    const props = {
      project: { ...project, milestones: [], resources: [], customers: [] }, projects: [project], initiatives: [],
      documents: data.documents, projectStatuses: [project.status], projectUpdates: [], users: data.users, teams: data.teams,
      labels: [], labelGroups: [], projectIssues: [], save, onTabChange,
      onCreateResource, onUpdateResource: vi.fn(), onDeleteResource: vi.fn(),
      onCreateMilestone, onUpdateMilestone: vi.fn(), onDeleteMilestone: vi.fn(),
    } as unknown as ComponentProps<typeof ProjectOverview>
    render(<I18nProvider><ProjectOverview {...props}/></I18nProvider>)

    const name = screen.getByRole('textbox', { name: 'Project name' })
    await user.clear(name)
    await user.type(name, 'Renamed project')
    await user.tab()
    expect(save).toHaveBeenCalledWith({ name: 'Renamed project' })
    await user.click(screen.getByRole('button', { name: 'Change project icon' }))
    expect(save).toHaveBeenCalledWith({ icon: 'Cube', color: '#123456' })
    await user.click(screen.getByRole('button', { name: /Write first project update/ }))
    expect(onTabChange).toHaveBeenCalledWith('activity')
    expect(screen.getByText('No issues in scope')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Milestone' }))
    await user.type(screen.getByRole('textbox', { name: 'Milestone name' }), 'Launch')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(onCreateMilestone).toHaveBeenCalledWith(project.id, expect.objectContaining({ name: 'Launch' })))

    await user.click(screen.getByRole('button', { name: /Add customer request/ }))
    const customer = await screen.findByRole('textbox', { name: 'Add customer request' })
    await user.type(customer, 'Acme request')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(save).toHaveBeenCalledWith({ customers: ['Acme request'] })

    await user.click(screen.getByRole('button', { name: /Add document or link/ }))
    await user.click(await screen.findByText('Create new document…'))
    expect(onCreateResource).toHaveBeenCalledWith(project.id, { type: 'document', title: 'Untitled document' })
  })
})
